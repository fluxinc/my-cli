//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/attempt"
)

func attemptTestApp(stdout, stderr *bytes.Buffer, exec func(path string, args []string, dir string) error) app {
	return app{
		stdout:      stdout,
		stderr:      stderr,
		lookPath:    func(string) (string, error) { return "/test/bin/codex", nil },
		execHarness: exec,
	}
}

func attemptLaunchArgs(home string, extra ...string) []string {
	args := []string{"my", "ai", "--manifest", "acme", "--home", home, "--no-session", "--no-refresh", "--no-update-check"}
	return append(append(args, extra...), "codex")
}

func attemptEntries(t *testing.T, root string) []attempt.Entry {
	t.Helper()
	entries, err := attempt.List(root, attempt.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func oneAttempt(t *testing.T, root string) attempt.Entry {
	t.Helper()
	entries := attemptEntries(t, root)
	if len(entries) != 1 {
		t.Fatalf("want 1 launch record, got %d: %+v", len(entries), entries)
	}
	if entries[0].Record == nil {
		t.Fatalf("launch record unreadable: %+v", entries[0])
	}
	return entries[0]
}

func attemptGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func attemptMountRepo(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	attemptGit(t, dir, "init", "-q", "-b", "main")
	writeCLITestFile(t, filepath.Join(dir, "a.md"), "a\n")
	attemptGit(t, dir, "add", "a.md")
	attemptGit(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func TestLaunchRecordsObservedExitAndGoal(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return nil })
	if err := a.run(attemptLaunchArgs(home, "--goal", "desk:4821")); err != nil {
		t.Fatal(err)
	}
	entry := oneAttempt(t, root)
	rec := entry.Record
	if entry.State != attempt.StateClosed || rec.ClosureReason != attempt.ClosureExited {
		t.Fatalf("state=%s closure=%q", entry.State, rec.ClosureReason)
	}
	if rec.ObservedExit == nil || rec.ObservedExit.Code != 0 || rec.ObservedExit.At == "" {
		t.Fatalf("observed_exit = %+v", rec.ObservedExit)
	}
	if rec.Harness != "codex" || rec.Goal != "desk:4821" || rec.WorkDir != root || rec.Umbrella != root || rec.SessionID != "" {
		t.Fatalf("record = %+v", rec)
	}
	if rec.Before == nil || rec.After == nil || rec.After.Kind != attempt.KindAfter {
		t.Fatalf("observations missing: before=%v after=%v", rec.Before, rec.After)
	}
	if rec.ReconciledAt != "" || rec.ChildState != "" {
		t.Fatalf("a normally closed launch carries reconcile fields: %+v", rec)
	}
	if strings.Contains(stderr.String(), "attempts") {
		t.Fatalf("a first launch printed an attempts notice: %q", stderr.String())
	}
}

func TestLaunchPreservesNonZeroExitAndRecordsIt(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return exitCodeError{code: 7} })
	err := a.run(attemptLaunchArgs(home))
	var exit exitCodeError
	if !errors.As(err, &exit) || exit.code != 7 {
		t.Fatalf("run error = %v, want exit code 7 unchanged", err)
	}
	rec := oneAttempt(t, root).Record
	if rec.ClosureReason != attempt.ClosureExited || rec.ObservedExit == nil || rec.ObservedExit.Code != 7 {
		t.Fatalf("record = %+v", rec)
	}
}

func TestLaunchStartFailureIsRecordedWithoutAnExit(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	failure := errors.New("fork/exec /test/bin/codex: no such file or directory")
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return failure })
	if err := a.run(attemptLaunchArgs(home)); !errors.Is(err, failure) {
		t.Fatalf("run error = %v, want the start failure unchanged", err)
	}
	rec := oneAttempt(t, root).Record
	if rec.ClosureReason != attempt.ClosureStartFailed || rec.ObservedExit != nil || rec.StartError == "" || rec.Child != nil {
		t.Fatalf("record = %+v", rec)
	}
}

func TestLaunchRecordsRealChildIdentityAndExitStatus(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	script := filepath.Join(t.TempDir(), "codex")
	writeCLITestFile(t, script, "#!/bin/sh\nexit 3\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, lookPath: func(string) (string, error) { return script, nil }}
	err := a.run(attemptLaunchArgs(home))
	var exit exitCodeError
	if !errors.As(err, &exit) || exit.code != 3 {
		t.Fatalf("run error = %v, want exit code 3", err)
	}
	rec := oneAttempt(t, root).Record
	if rec.Child == nil || rec.Child.PID <= 0 {
		t.Fatalf("child identity not recorded: %+v", rec.Child)
	}
	if rec.Child.PID == os.Getpid() || rec.Launcher.PID != os.Getpid() {
		t.Fatalf("launcher=%+v child=%+v", rec.Launcher, rec.Child)
	}
	if rec.ObservedExit == nil || rec.ObservedExit.Code != 3 {
		t.Fatalf("observed_exit = %+v", rec.ObservedExit)
	}
}

func TestLaunchPrintRecordsAndReconcilesNothing(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	// Materialize the umbrella, then leave one launch whose launcher is gone.
	var stdout, stderr bytes.Buffer
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return nil })
	if err := a.run(attemptLaunchArgs(home)); err != nil {
		t.Fatal(err)
	}
	lost, err := attempt.Begin(root, attempt.Record{Harness: "codex", Umbrella: root, WorkDir: root, StartedAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	lost.Close()

	stdout.Reset()
	stderr.Reset()
	if err := a.run(attemptLaunchArgs(home, "--print")); err != nil {
		t.Fatal(err)
	}
	entries := attemptEntries(t, root)
	if len(entries) != 2 {
		t.Fatalf("--print changed the number of launch records: %d", len(entries))
	}
	for _, entry := range entries {
		if entry.ID == lost.Record().ID && (entry.Record.ClosureReason != "" || entry.Record.ReconciledAt != "") {
			t.Fatalf("--print reconciled a record: %+v", entry.Record)
		}
	}
	if strings.Contains(stderr.String(), "attempts") {
		t.Fatalf("--print printed an attempts notice: %q", stderr.String())
	}
}

func TestLaunchObservationFailureNeverChangesTheResult(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return nil })
	if err := a.run(attemptLaunchArgs(home)); err != nil { // materialize the umbrella
		t.Fatal(err)
	}
	// A mount whose Git metadata is unreadable.
	writeCLITestFile(t, filepath.Join(root, "broken", ".git"), "not a gitdir pointer\n")
	if err := a.run(attemptLaunchArgs(home)); err != nil {
		t.Fatalf("an unobservable checkout failed the launch: %v", err)
	}
	entries := attemptEntries(t, root)
	rec := entries[len(entries)-1].Record
	var broken *attempt.RepoObs
	for i := range rec.After.Repos {
		if rec.After.Repos[i].Label == "broken" {
			broken = &rec.After.Repos[i]
		}
	}
	if broken == nil || broken.Error == "" {
		t.Fatalf("the failed observation did not surface: %+v", rec.After.Repos)
	}
	if rec.ObservedExit == nil || rec.ObservedExit.Code != 0 {
		t.Fatalf("observed_exit = %+v", rec.ObservedExit)
	}
	if rec.LocalChangeObserved != nil {
		t.Fatal("local_change_observed must be unknown when an observation failed")
	}
	var out bytes.Buffer
	a.stdout = &out
	if err := a.run([]string{"my", "attempts", "list", "--home", home, "--umbrella", root}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "error=could not observe broken (after)") {
		t.Fatalf("list hid the observation error:\n%s", out.String())
	}
}

func TestLaunchNoticeReportsHistoricalLocalChangesOnce(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return nil })
	if err := a.run(attemptLaunchArgs(home)); err != nil { // materialize the umbrella
		t.Fatal(err)
	}
	mount := attemptMountRepo(t, root, "handbook")

	// A launch that leaves an untracked file, then dies without closing.
	host, _ := os.Hostname()
	targets := []attempt.RepoTarget{{Label: "handbook", Path: mount}}
	lost, err := attempt.Begin(root, attempt.Record{
		Harness: "codex", Umbrella: root, WorkDir: root, Host: host, StartedAt: time.Now().UTC().Format(time.RFC3339),
		Before: attempt.Observe(attempt.KindBefore, targets, attempt.RunGit, time.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(mount, "half-done.md"), "wip\n")
	lost.Close()

	stderr.Reset()
	if err := a.run(attemptLaunchArgs(home)); err != nil {
		t.Fatal(err)
	}
	want := "1 earlier my ai launch observed local changes; run my attempts list --local-changes. No work-service tracking was established."
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want the historical notice", stderr.String())
	}
	var reconciled *attempt.Record
	for _, entry := range attemptEntries(t, root) {
		if entry.ID == lost.Record().ID {
			reconciled = entry.Record
		}
	}
	if reconciled == nil || reconciled.ClosureReason != attempt.ClosureLauncherLost || reconciled.ObservedExit != nil {
		t.Fatalf("lost launch = %+v", reconciled)
	}
	if reconciled.After == nil || reconciled.After.Kind != attempt.KindAtReconcile {
		t.Fatalf("reconcile observation = %+v", reconciled.After)
	}
	if reconciled.ChildState != attempt.ChildUnknown {
		t.Fatalf("child_state = %q, want unknown with no recorded child", reconciled.ChildState)
	}

	var out bytes.Buffer
	a.stdout = &out
	if err := a.run([]string{"my", "attempts", "list", "--local-changes", "--json", "--home", home, "--umbrella", root}); err != nil {
		t.Fatal(err)
	}
	var report attemptListReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("list output is not JSON: %v\n%s", err, out.String())
	}
	if report.SchemaVersion != attempt.SchemaVersion || !report.Supported {
		t.Fatalf("report header = %+v", report)
	}
	// The two launches that ran after the file appeared also observed it:
	// observation of a shared checkout, not attribution.
	found := false
	for _, entry := range report.Attempts {
		if !attemptLocalChange(entry) {
			t.Fatalf("--local-changes returned a launch without one: %+v", entry.Record)
		}
		found = found || entry.ID == lost.Record().ID
	}
	if !found {
		t.Fatalf("the lost launch is missing from --local-changes: %s", out.String())
	}
}

func TestDoctorSummarizesLaunchesWithoutReconciling(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return nil })
	if err := a.run(attemptLaunchArgs(home)); err != nil {
		t.Fatal(err)
	}
	lost, err := attempt.Begin(root, attempt.Record{Harness: "codex", Umbrella: root, WorkDir: root, StartedAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	lost.Close()

	items := doctorAttempts(root)
	if len(items) == 0 || items[0].Name != "launches" || !strings.Contains(items[0].Message, "2 recorded, 0 running, 1 stopped without a recorded exit") {
		t.Fatalf("doctor items = %+v", items)
	}
	for _, entry := range attemptEntries(t, root) {
		if entry.ID == lost.Record().ID && entry.Record.ClosureReason != "" {
			t.Fatal("doctor reconciled a launch record; it must only inspect")
		}
	}
}

func TestAttemptTargetsAndSessionID(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"handbook", "repos/svc", "sessions/s1/handbook", "sessions/s2/handbook", "personal/notes", "plain"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if dir != "plain" {
			writeCLITestFile(t, filepath.Join(root, dir, ".git"), "gitdir: elsewhere\n")
		}
	}
	session := filepath.Join(root, "sessions", "s1")
	if got := attemptSessionID(root, filepath.Join(session, "handbook")); got != "s1" {
		t.Fatalf("session id = %q", got)
	}
	if got := attemptSessionID(root, root); got != "" {
		t.Fatalf("base launch session id = %q", got)
	}
	var labels []string
	for _, target := range attemptRepoTargets(root, session) {
		labels = append(labels, target.Label)
	}
	want := "handbook,repos/svc,sessions/s1/handbook"
	if got := strings.Join(labels, ","); got != want {
		t.Fatalf("targets = %s, want %s (another session's worktrees and personal/ stay out)", got, want)
	}
}

func TestLaunchRecordsSignalExitWithoutChangingTheResult(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	script := filepath.Join(t.TempDir(), "codex")
	writeCLITestFile(t, script, "#!/bin/sh\nkill -TERM $$\nsleep 5\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, lookPath: func(string) (string, error) { return script, nil }}
	err := a.run(attemptLaunchArgs(home))
	var exit exitCodeError
	// A signalled child has always surfaced as exit code -1; that must not change.
	if !errors.As(err, &exit) || exit.code != -1 {
		t.Fatalf("run error = %v, want the existing -1 for a signalled child", err)
	}
	rec := oneAttempt(t, root).Record
	if rec.ClosureReason != attempt.ClosureExited || rec.ObservedExit == nil {
		t.Fatalf("record = %+v", rec)
	}
	if rec.ObservedExit.Code != -1 || rec.ObservedExit.Signal != "terminated" {
		t.Fatalf("observed_exit = %+v, want code -1 with signal terminated", rec.ObservedExit)
	}
}

func TestResumedSessionIsANewLaunchLinkedToTheSameSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	root := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, root)
	var stdout, stderr bytes.Buffer
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return nil })
	base := []string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}
	if err := a.run(append(append([]string{}, base...), "--new-session", "codex")); err != nil {
		t.Fatalf("%v\nstderr: %s", err, stderr.String())
	}
	first := oneAttempt(t, root)
	if first.Record.SessionID == "" || !strings.HasPrefix(first.Record.WorkDir, filepath.Join(root, "sessions")) {
		t.Fatalf("session launch record = %+v", first.Record)
	}
	if err := a.run(append(append([]string{}, base...), "--resume", first.Record.SessionID, "codex")); err != nil {
		t.Fatal(err)
	}
	entries := attemptEntries(t, root)
	if len(entries) != 2 {
		t.Fatalf("a resume must be its own launch record, got %d", len(entries))
	}
	if entries[0].ID == entries[1].ID || entries[1].Record.SessionID != first.Record.SessionID {
		t.Fatalf("resume record = %+v, want a new id with session %s", entries[1].Record, first.Record.SessionID)
	}
}

func TestListFiltersNeverHideRecordedErrors(t *testing.T) {
	home, root := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := attemptTestApp(&stdout, &stderr, func(string, []string, string) error { return nil })
	if err := a.run(attemptLaunchArgs(home)); err != nil { // materialize the umbrella
		t.Fatal(err)
	}
	// One closed launch with no local change but an unreadable checkout, and
	// one with a record-level error.
	for _, rec := range []attempt.Record{
		{Harness: "codex", ClosureReason: attempt.ClosureExited, After: &attempt.Observation{Kind: attempt.KindAfter, Repos: []attempt.RepoObs{{Label: "gone", Path: "/missing", Error: "checkout unavailable"}}}},
		{Harness: "codex", ClosureReason: attempt.ClosureExited, Errors: []string{"child wait: interrupted"}},
	} {
		h, err := attempt.Begin(root, rec)
		if err != nil {
			t.Fatal(err)
		}
		h.Close()
	}
	for _, filter := range []string{"--local-changes", "--running", "--unclosed"} {
		var out bytes.Buffer
		a.stdout = &out
		if err := a.run([]string{"my", "attempts", "list", filter, "--home", home, "--umbrella", root}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "checkout unavailable") || !strings.Contains(out.String(), "child wait: interrupted") {
			t.Fatalf("%s hid a recorded error:\n%s", filter, out.String())
		}
	}
	// Doctor names how many records carry errors, and stays bounded.
	var warned bool
	for _, item := range doctorAttempts(root) {
		warned = warned || item.Status == "warn" && strings.Contains(item.Message, "2 launch records")
	}
	if !warned {
		t.Fatalf("doctor did not warn about recorded errors: %+v", doctorAttempts(root))
	}
}
