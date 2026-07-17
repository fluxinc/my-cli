package access

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQuarantineConfirmedAtomicallyMovesCheckoutAndWorktreeWithoutByteLoss(t *testing.T) {
	fixture := setupQuarantineFixture(t, true)
	writeRecoveryFile(t, filepath.Join(fixture.repo, "tracked.txt"), "dirty main\n")
	writeRecoveryFile(t, filepath.Join(fixture.repo, "untracked.txt"), "main untracked\n")
	writeRecoveryFile(t, filepath.Join(fixture.repo, "ignored.env"), "main ignored\n")
	writeRecoveryFile(t, filepath.Join(fixture.worktree, "tracked.txt"), "dirty worktree\n")
	writeRecoveryFile(t, filepath.Join(fixture.worktree, "worktree-untracked.txt"), "worktree untracked\n")
	personal := filepath.Join(fixture.root, "personal", "keep.txt")
	unmanaged := filepath.Join(fixture.root, "repos", "unmanaged", "keep.txt")
	writeRecoveryFile(t, personal, "personal\n")
	writeRecoveryFile(t, unmanaged, "unmanaged\n")
	mainBefore := snapshotRecoveryTree(t, fixture.repo)
	worktreeBefore := snapshotRecoveryTree(t, fixture.worktree)
	confirmFixtureRevocation(t, fixture)

	result, err := QuarantineConfirmed(QuarantineInput{
		Home: fixture.home, Path: fixture.repo, ActorID: fixture.allowed.Actor.ID,
		Now: time.Date(2026, 7, 16, 23, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Journal.Phase != "complete" || len(result.Journal.Sources) != 2 {
		t.Fatalf("journal = %#v", result.Journal)
	}
	if pathExistsNoFollow(fixture.repo) || pathExistsNoFollow(fixture.worktree) {
		t.Fatal("active checkout or linked worktree still exists after confirmed quarantine")
	}
	for _, source := range result.Journal.Sources {
		if source.State != "verified" || !pathExistsNoFollow(source.QuarantinedPath) {
			t.Fatalf("source = %#v", source)
		}
		var want map[string]string
		switch source.OriginalPath {
		case fixture.repo:
			want = mainBefore
		case fixture.worktree:
			want = worktreeBefore
		default:
			t.Fatalf("unexpected moved source %s", source.OriginalPath)
		}
		if got := snapshotRecoveryTree(t, source.QuarantinedPath); !reflect.DeepEqual(want, got) {
			t.Fatalf("moved checkout bytes differ for %s", source.OriginalPath)
		}
	}
	if data, err := os.ReadFile(personal); err != nil || string(data) != "personal\n" {
		t.Fatalf("personal content changed: %q err=%v", data, err)
	}
	if data, err := os.ReadFile(unmanaged); err != nil || string(data) != "unmanaged\n" {
		t.Fatalf("unmanaged content changed: %q err=%v", data, err)
	}
	if runtime.GOOS != "windows" {
		transactionInfo, err := os.Stat(filepath.Dir(result.JournalPath))
		if err != nil {
			t.Fatal(err)
		}
		if transactionInfo.Mode().Perm() != 0o700 {
			t.Fatalf("quarantine transaction mode = %o, want 700", transactionInfo.Mode().Perm())
		}
		journalInfo, err := os.Stat(result.JournalPath)
		if err != nil {
			t.Fatal(err)
		}
		if journalInfo.Mode().Perm() != 0o600 {
			t.Fatalf("journal mode = %o, want 600", journalInfo.Mode().Perm())
		}
	}
	inventory, err := LoadInventory(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Repositories) != 0 || len(inventory.Quarantines) != 1 {
		t.Fatalf("inventory after quarantine = %#v", inventory)
	}
	if inventory.Quarantines[0].PurgeEligible || !containsRecoveryReason(inventory.Quarantines[0].RetentionReasons, "remote-retention-not-authoritatively-proven") {
		t.Fatalf("quarantine retention verdict = %#v", inventory.Quarantines[0])
	}
	eventPath := filepath.Join(fixture.home, ".local", "state", "my-cli", "access", "events.jsonl")
	events, err := os.ReadFile(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	if eventCount := strings.Count(strings.TrimSpace(string(events)), "\n") + 1; eventCount != 1 {
		t.Fatalf("security event count = %d, want 1", eventCount)
	}
	if strings.Contains(string(events), "main untracked") || strings.Contains(string(events), "main ignored") {
		t.Fatalf("security event leaked repository content: %s", events)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(eventPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("security event mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestQuarantineResumeAfterIntentAndAfterUnjournaledRename(t *testing.T) {
	for _, phase := range []string{"after-intent", "after-rename-before-journal:0"} {
		t.Run(phase, func(t *testing.T) {
			fixture := setupQuarantineFixture(t, false)
			writeRecoveryFile(t, filepath.Join(fixture.repo, "tracked.txt"), "dirty\n")
			before := snapshotRecoveryTree(t, fixture.repo)
			confirmFixtureRevocation(t, fixture)
			result, err := QuarantineConfirmed(QuarantineInput{
				Home: fixture.home, Path: fixture.repo, ActorID: fixture.allowed.Actor.ID,
				Now: time.Date(2026, 7, 16, 23, 0, 0, 0, time.UTC),
				Failpoint: func(current string) error {
					if current == phase {
						return errors.New("simulated crash")
					}
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), "simulated crash") {
				t.Fatalf("QuarantineConfirmed error = %v", err)
			}
			if result.JournalPath == "" {
				t.Fatal("interrupted quarantine did not return a journal path")
			}
			lockPath := filepath.Join(fixture.root, ".my-cli", "access-quarantine.lock")
			if _, err := os.Stat(lockPath); err != nil {
				t.Fatalf("persistent advisory lock file missing after interruption: %v", err)
			}
			resumed, err := ResumeQuarantine(fixture.home, result.JournalPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			if resumed.Journal.Phase != "complete" || pathExistsNoFollow(fixture.repo) {
				t.Fatalf("resumed journal = %#v", resumed.Journal)
			}
			if got := snapshotRecoveryTree(t, resumed.Journal.Sources[0].QuarantinedPath); !reflect.DeepEqual(before, got) {
				t.Fatal("resumed quarantine changed checkout bytes")
			}
			again, err := ResumeQuarantine(fixture.home, result.JournalPath, nil)
			if err != nil || again.Journal.Phase != "complete" {
				t.Fatalf("idempotent resume = %#v err=%v", again, err)
			}
		})
	}
}

func TestQuarantineRefusesTamperedOutsideInventoryPath(t *testing.T) {
	fixture := setupQuarantineFixture(t, false)
	confirmFixtureRevocation(t, fixture)
	inventory, err := LoadInventory(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(fixture.home, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	outside, err = filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	inventory.Repositories[0].CanonicalPath = outside
	if err := saveInventory(fixture.home, inventory); err != nil {
		t.Fatal(err)
	}
	if _, err := QuarantineConfirmed(QuarantineInput{
		Home: fixture.home, Path: outside, ActorID: fixture.allowed.Actor.ID, Now: time.Now(),
	}); err == nil || !strings.Contains(err.Error(), "outside allowed root") {
		t.Fatalf("tampered inventory error = %v", err)
	}
	if !pathExistsNoFollow(outside) || !pathExistsNoFollow(fixture.repo) {
		t.Fatal("tampered inventory moved an authorized or unauthorized path")
	}
}

func TestQuarantineCapsuleFailureLeavesCheckoutBlockedInPlace(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "umbrella")
	repo := filepath.Join(root, "repos", "unborn")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runRecoveryGit(t, repo, "init", "-q")
	allowed := positiveDecision("R_unborn")
	entry, err := RecordPositive(RecordInput{
		Home: home, Path: repo, AllowedRoot: root, Organization: "acme", Manifest: "acme",
		Umbrella: root, SourceRef: "manifest:acme:repo:unborn", Kind: "repo",
		Decision: allowed, CheckedAt: time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := quarantineFixture{home: home, root: entry.AllowedRoot, repo: entry.CanonicalPath, allowed: allowed}
	before := snapshotRecoveryTree(t, fixture.repo)
	confirmFixtureRevocation(t, fixture)
	result, err := QuarantineConfirmed(QuarantineInput{
		Home: home, Path: fixture.repo, ActorID: allowed.Actor.ID, Now: time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "rev-parse HEAD") {
		t.Fatalf("capsule failure error = %v", err)
	}
	if !pathExistsNoFollow(fixture.repo) || !reflect.DeepEqual(before, snapshotRecoveryTree(t, fixture.repo)) {
		t.Fatal("capsule failure moved or changed source checkout")
	}
	if result.JournalPath == "" || !pathExistsNoFollow(result.JournalPath) || result.Journal.Sources[0].State != "preparing" {
		t.Fatalf("capsule failure journal = %#v", result)
	}
	if _, err := ResumeQuarantine(home, result.JournalPath, nil); err == nil || !strings.Contains(err.Error(), "not prepared") {
		t.Fatalf("resume incomplete capsule error = %v", err)
	}
	if !pathExistsNoFollow(fixture.repo) {
		t.Fatal("resume moved checkout despite incomplete capsule")
	}
	inventory, err := LoadInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Repositories) != 1 || len(inventory.Quarantines) != 0 {
		t.Fatalf("capsule failure changed managed inventory: %#v", inventory)
	}
}

func TestQuarantineRenameFailureHasNoCopyOrDeleteFallback(t *testing.T) {
	fixture := setupQuarantineFixture(t, false)
	writeRecoveryFile(t, filepath.Join(fixture.repo, "tracked.txt"), "dirty\n")
	before := snapshotRecoveryTree(t, fixture.repo)
	confirmFixtureRevocation(t, fixture)
	result, err := QuarantineConfirmed(QuarantineInput{
		Home: fixture.home, Path: fixture.repo, ActorID: fixture.allowed.Actor.ID, Now: time.Now(),
		Rename: func(string, string) error { return errors.New("invalid cross-device link") },
	})
	if err == nil || !strings.Contains(err.Error(), "invalid cross-device link") {
		t.Fatalf("rename failure error = %v", err)
	}
	if !pathExistsNoFollow(fixture.repo) || !reflect.DeepEqual(before, snapshotRecoveryTree(t, fixture.repo)) {
		t.Fatal("rename failure copied, deleted, or changed source checkout")
	}
	if len(result.Journal.Sources) != 1 || pathExistsNoFollow(result.Journal.Sources[0].QuarantinedPath) {
		t.Fatalf("rename failure created a checkout fallback: %#v", result.Journal.Sources)
	}
}

func TestConcurrentQuarantineHasSingleMover(t *testing.T) {
	fixture := setupQuarantineFixture(t, false)
	confirmFixtureRevocation(t, fixture)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	firstDone := make(chan error, 1)
	go func() {
		_, err := QuarantineConfirmed(QuarantineInput{
			Home: fixture.home, Path: fixture.repo, ActorID: fixture.allowed.Actor.ID, Now: time.Now(),
			Failpoint: func(phase string) error {
				if phase == "after-intent" {
					once.Do(func() { close(entered) })
					<-release
				}
				return nil
			},
		})
		firstDone <- err
	}()
	<-entered
	_, secondErr := QuarantineConfirmed(QuarantineInput{
		Home: fixture.home, Path: fixture.repo, ActorID: fixture.allowed.Actor.ID, Now: time.Now(),
	})
	if secondErr == nil || !strings.Contains(secondErr.Error(), "another quarantine operation is active") {
		t.Fatalf("second quarantine error = %v", secondErr)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	inventory, err := LoadInventory(fixture.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Quarantines) != 1 {
		t.Fatalf("quarantine records = %d, want 1", len(inventory.Quarantines))
	}
}

func TestQuarantineWithOpenFileDescriptorOnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows open-handle sharing behavior requires Windows CI")
	}
	fixture := setupQuarantineFixture(t, false)
	openFile, err := os.Open(filepath.Join(fixture.repo, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer openFile.Close()
	confirmFixtureRevocation(t, fixture)
	result, err := QuarantineConfirmed(QuarantineInput{
		Home: fixture.home, Path: fixture.repo, ActorID: fixture.allowed.Actor.ID, Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if pathExistsNoFollow(fixture.repo) || result.Journal.Phase != "complete" {
		t.Fatalf("open descriptor blocked POSIX quarantine: %#v", result.Journal)
	}
	data := make([]byte, 4)
	if _, err := openFile.Read(data); err != nil || string(data) != "base" {
		t.Fatalf("open descriptor no longer reads moved bytes: %q err=%v", data, err)
	}
}

func TestQuarantineAdvisoryLockIsReleasedByProcessCrash(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestQuarantineLockCrashHelper")
	cmd.Env = append(os.Environ(), "MYCLI_QUARANTINE_LOCK_HELPER="+root)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("lock helper did not acquire lock: line=%q err=%v", line, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed lock helper exited successfully")
	}
	_, release, err := acquireQuarantineLock(root)
	if err != nil {
		t.Fatalf("kernel did not release advisory lock after process crash: %v", err)
	}
	release()
}

func TestQuarantineLockCrashHelper(t *testing.T) {
	root := os.Getenv("MYCLI_QUARANTINE_LOCK_HELPER")
	if root == "" {
		return
	}
	if _, _, err := acquireQuarantineLock(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "locked")
	for {
		time.Sleep(time.Hour)
	}
}

func TestQuarantineMoverHasNoRecursiveDeleteFallback(t *testing.T) {
	data, err := os.ReadFile("quarantine.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"os.RemoveAll(", "safefs.RemoveAll("} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("quarantine mover contains forbidden recursive delete path %q", forbidden)
		}
	}
}

type quarantineFixture struct {
	home     string
	root     string
	repo     string
	worktree string
	allowed  Decision
}

func setupQuarantineFixture(t *testing.T, withWorktree bool) quarantineFixture {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "umbrella")
	repo := filepath.Join(root, "repos", "sample")
	remote := filepath.Join(home, "remote.git")
	runRecoveryGit(t, home, "init", "--bare", "-q", remote)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runRecoveryGit(t, repo, "init", "-q")
	runRecoveryGit(t, repo, "config", "user.name", "Quarantine Test")
	runRecoveryGit(t, repo, "config", "user.email", "quarantine@example.invalid")
	writeRecoveryFile(t, filepath.Join(repo, ".gitignore"), "ignored.env\n")
	writeRecoveryFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runRecoveryGit(t, repo, "add", ".")
	runRecoveryGit(t, repo, "commit", "-q", "-m", "base")
	runRecoveryGit(t, repo, "branch", "-M", "main")
	runRecoveryGit(t, repo, "remote", "add", "origin", remote)
	runRecoveryGit(t, repo, "push", "-q", "-u", "origin", "main")
	worktree := ""
	if withWorktree {
		worktree = filepath.Join(root, "sessions", "session-1", "repos", "sample")
		if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
			t.Fatal(err)
		}
		runRecoveryGit(t, repo, "worktree", "add", "-q", "-b", "session-work", worktree)
	}
	allowed := positiveDecision("R_repo")
	entry, err := RecordPositive(RecordInput{
		Home: home, Path: repo, AllowedRoot: root, Organization: "acme", Manifest: "acme",
		Umbrella: root, SourceRef: "manifest:acme:repo:sample", Kind: "repo",
		Decision: allowed, CheckedAt: time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	root = entry.AllowedRoot
	repo = entry.CanonicalPath
	if worktree != "" {
		worktree, err = filepath.EvalSymlinks(worktree)
		if err != nil {
			t.Fatal(err)
		}
	}
	return quarantineFixture{home: home, root: root, repo: repo, worktree: worktree, allowed: allowed}
}

func confirmFixtureRevocation(t *testing.T, fixture quarantineFixture) {
	t.Helper()
	denied := Decision{State: StateDenied, ReasonCode: "repository_not_found", Actor: fixture.allowed.Actor}
	for _, at := range []time.Time{
		time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 16, 21, 15, 0, 0, time.UTC),
	} {
		result, err := RecordObservation(ObservationInput{
			Home: fixture.home, Path: fixture.repo, Decision: denied, CheckedAt: at,
			RequiredConfirmations: 2, ConfirmationInterval: 15 * time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		if at.Minute() == 15 && !result.CleanupEligible {
			t.Fatalf("revocation was not confirmed: %#v", result)
		}
	}
}
