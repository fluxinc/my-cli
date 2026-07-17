package access

import (
	"archive/tar"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRecoveryCapsuleRoundTripsDirtyBinaryIgnoredUntrackedAndLocalRefs(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repository")
	remote := filepath.Join(root, "remote.git")
	runRecoveryGit(t, root, "init", "--bare", "-q", remote)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runRecoveryGit(t, repo, "init", "-q")
	runRecoveryGit(t, repo, "config", "user.name", "Recovery Test")
	runRecoveryGit(t, repo, "config", "user.email", "recovery@example.invalid")
	writeRecoveryFile(t, filepath.Join(repo, ".gitignore"), "ignored.env\n")
	writeRecoveryFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	writeRecoveryFile(t, filepath.Join(repo, "binary.dat"), string([]byte{0, 1, 2, 3}))
	runRecoveryGit(t, repo, "add", ".")
	runRecoveryGit(t, repo, "commit", "-q", "-m", "base")
	runRecoveryGit(t, repo, "branch", "-M", "main")
	runRecoveryGit(t, repo, "remote", "add", "origin", remote)
	runRecoveryGit(t, repo, "push", "-q", "-u", "origin", "main")
	runRecoveryGit(t, repo, "checkout", "-q", "-b", "local-work")
	writeRecoveryFile(t, filepath.Join(repo, "local-commit.txt"), "local commit\n")
	runRecoveryGit(t, repo, "add", "local-commit.txt")
	runRecoveryGit(t, repo, "commit", "-q", "-m", "local only")
	writeRecoveryFile(t, filepath.Join(repo, "tracked.txt"), "staged\n")
	runRecoveryGit(t, repo, "add", "tracked.txt")
	writeRecoveryFile(t, filepath.Join(repo, "tracked.txt"), "modified\n")
	writeRecoveryFile(t, filepath.Join(repo, "binary.dat"), string([]byte{0, 9, 8, 7, 6}))
	writeRecoveryFile(t, filepath.Join(repo, "untracked.txt"), "untracked\n")
	writeRecoveryFile(t, filepath.Join(repo, "ignored.env"), "SECRET=test-only\n")
	if err := os.Mkdir(filepath.Join(repo, "empty-local-directory"), 0o710); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(repo, "tracked.txt"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(repo, "untracked.txt"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("untracked.txt", filepath.Join(repo, "untracked-link")); err != nil {
			t.Fatal(err)
		}
	}
	sourceBefore := snapshotRecoveryTree(t, repo)

	destination := filepath.Join(root, "quarantine", "capsule")
	capsule, err := BuildRecoveryCapsule(repo, destination, Repository{ID: 29, NodeID: "R_repo", FullName: "example/repo", Permission: PermissionRead}, time.Date(2026, 7, 16, 22, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if capsule.PurgeEligible || !containsRecoveryReason(capsule.RetentionReasons, "working-tree-content") || !containsRecoveryReason(capsule.RetentionReasons, "local-only-commits-or-refs") {
		t.Fatalf("purge verdict = %v reasons=%#v", capsule.PurgeEligible, capsule.RetentionReasons)
	}
	if runtime.GOOS != "windows" {
		if mode := recoveredMode(t, capsule.Inventory, "tracked.txt"); mode.Perm() != 0o600 {
			t.Fatalf("tracked.txt mode = %o, want 600", mode.Perm())
		}
		if mode := recoveredMode(t, capsule.Inventory, "untracked.txt"); mode.Perm() != 0o600 {
			t.Fatalf("untracked.txt mode = %o, want 600", mode.Perm())
		}
		if mode := recoveredMode(t, capsule.Inventory, "empty-local-directory"); mode.Perm() != 0o710 {
			t.Fatalf("empty-local-directory mode = %o, want 710", mode.Perm())
		}
	}
	if err := VerifyRecoveryCapsule(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Fatalf("source checkout changed or moved: %v", err)
	}
	if sourceAfter := snapshotRecoveryTree(t, repo); !reflect.DeepEqual(sourceBefore, sourceAfter) {
		t.Fatalf("recovery capture changed source checkout bytes")
	}
	patch, err := os.ReadFile(filepath.Join(destination, "working-tree.patch"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(patch, []byte("tracked.txt")) || !bytes.Contains(patch, []byte("GIT binary patch")) {
		t.Fatalf("recovery patch missing tracked or binary changes:\n%s", patch)
	}
	indexPatch, err := os.ReadFile(filepath.Join(destination, "index.patch"))
	if err != nil {
		t.Fatal(err)
	}
	unstagedPatch, err := os.ReadFile(filepath.Join(destination, "unstaged.patch"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(indexPatch, []byte("staged")) || !bytes.Contains(unstagedPatch, []byte("modified")) {
		t.Fatalf("capsule did not preserve staged and unstaged states")
	}
	movedSource := filepath.Join(root, "source-moved-after-capture")
	if err := os.Rename(repo, movedSource); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecoveryCapsule(destination); err != nil {
		t.Fatalf("capsule depends on original checkout after capture: %v", err)
	}
}

func TestRecoveryCapsuleCorruptionFailsVerificationAndLeavesSource(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repository")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runRecoveryGit(t, repo, "init", "-q")
	runRecoveryGit(t, repo, "config", "user.name", "Recovery Test")
	runRecoveryGit(t, repo, "config", "user.email", "recovery@example.invalid")
	writeRecoveryFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runRecoveryGit(t, repo, "add", ".")
	runRecoveryGit(t, repo, "commit", "-q", "-m", "base")
	writeRecoveryFile(t, filepath.Join(repo, "tracked.txt"), "dirty\n")
	destination := filepath.Join(root, "capsule")
	if _, err := BuildRecoveryCapsule(repo, destination, Repository{ID: 29, NodeID: "R_repo", FullName: "example/repo"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "working-tree.patch"), []byte("corrupt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecoveryCapsule(destination); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("verification err = %v, want checksum mismatch", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if err != nil || string(data) != "dirty\n" {
		t.Fatalf("source changed after capsule corruption: data=%q err=%v", data, err)
	}
}

func TestRecoveryCapsuleRejectsDestinationInsideSource(t *testing.T) {
	repo := t.TempDir()
	runRecoveryGit(t, repo, "init", "-q")
	runRecoveryGit(t, repo, "config", "user.name", "Recovery Test")
	runRecoveryGit(t, repo, "config", "user.email", "recovery@example.invalid")
	writeRecoveryFile(t, filepath.Join(repo, "tracked.txt"), "base\n")
	runRecoveryGit(t, repo, "add", ".")
	runRecoveryGit(t, repo, "commit", "-q", "-m", "base")
	if _, err := BuildRecoveryCapsule(repo, filepath.Join(repo, ".recovery"), Repository{}, time.Now()); err == nil || !strings.Contains(err.Error(), "outside the source") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecoveryCapsuleFailureLeavesSourceAndMarksPartialCapsuleIncomplete(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "unborn-repository")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runRecoveryGit(t, repo, "init", "-q")
	sourceBefore := snapshotRecoveryTree(t, repo)
	destination := filepath.Join(root, "partial-capsule")
	if _, err := BuildRecoveryCapsule(repo, destination, Repository{}, time.Now()); err == nil {
		t.Fatal("BuildRecoveryCapsule succeeded for a repository without HEAD")
	}
	if _, err := os.Stat(filepath.Join(destination, "INCOMPLETE")); err != nil {
		t.Fatalf("partial capsule was deleted or not marked incomplete: %v", err)
	}
	if err := VerifyRecoveryCapsule(destination); err == nil || !strings.Contains(err.Error(), "marked incomplete") {
		t.Fatalf("VerifyRecoveryCapsule error = %v, want incomplete marker rejection", err)
	}
	if sourceAfter := snapshotRecoveryTree(t, repo); !reflect.DeepEqual(sourceBefore, sourceAfter) {
		t.Fatalf("failed recovery capture changed source checkout bytes")
	}
}

func TestRecoveryCapsuleCapturesEmbeddedGitRepository(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repository")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runRecoveryGit(t, repo, "init", "-q")
	runRecoveryGit(t, repo, "config", "user.name", "Recovery Test")
	runRecoveryGit(t, repo, "config", "user.email", "recovery@example.invalid")
	writeRecoveryFile(t, filepath.Join(repo, "tracked.txt"), "root\n")
	runRecoveryGit(t, repo, "add", ".")
	runRecoveryGit(t, repo, "commit", "-q", "-m", "root")

	embedded := filepath.Join(repo, "vendor", "embedded")
	if err := os.MkdirAll(embedded, 0o755); err != nil {
		t.Fatal(err)
	}
	runRecoveryGit(t, embedded, "init", "-q")
	runRecoveryGit(t, embedded, "config", "user.name", "Embedded Test")
	runRecoveryGit(t, embedded, "config", "user.email", "embedded@example.invalid")
	writeRecoveryFile(t, filepath.Join(embedded, ".gitignore"), "secret.txt\n")
	writeRecoveryFile(t, filepath.Join(embedded, "committed.txt"), "embedded commit\n")
	runRecoveryGit(t, embedded, "add", ".")
	runRecoveryGit(t, embedded, "commit", "-q", "-m", "embedded local history")
	writeRecoveryFile(t, filepath.Join(embedded, "committed.txt"), "embedded dirty\n")
	writeRecoveryFile(t, filepath.Join(embedded, "untracked.txt"), "embedded untracked\n")
	writeRecoveryFile(t, filepath.Join(embedded, "secret.txt"), "embedded ignored\n")
	sourceBefore := snapshotRecoveryTree(t, repo)

	destination := filepath.Join(root, "capsule")
	if _, err := BuildRecoveryCapsule(repo, destination, Repository{ID: 29, NodeID: "R_repo", FullName: "example/repo"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecoveryCapsule(destination); err != nil {
		t.Fatal(err)
	}
	if sourceAfter := snapshotRecoveryTree(t, repo); !reflect.DeepEqual(sourceBefore, sourceAfter) {
		t.Fatalf("embedded repository capture changed source checkout bytes")
	}
	archive, err := os.ReadFile(filepath.Join(destination, "untracked-and-ignored.tar"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"vendor/embedded/.git/", "vendor/embedded/committed.txt", "vendor/embedded/untracked.txt", "vendor/embedded/secret.txt"} {
		if !bytes.Contains(archive, []byte(marker)) {
			t.Fatalf("embedded repository archive missing %s", marker)
		}
	}
}

func TestExtractRecoveryArchiveRejectsSymlinkTargetTypeSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on Windows")
	}
	root := t.TempDir()
	destination := filepath.Join(root, "restored")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(root, "crafted.tar")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	if err := tw.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: outside, Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeDir, Mode: 0}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractUntrackedArchive(archivePath, destination); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("extractUntrackedArchive error = %v, want target type rejection", err)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() == 0 {
		t.Fatal("crafted archive changed permissions outside restoration root")
	}
}

func runRecoveryGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeRecoveryFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsRecoveryReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func recoveredMode(t *testing.T, inventory []RecoveredFile, path string) os.FileMode {
	t.Helper()
	for _, item := range inventory {
		if item.Path == path {
			return item.Mode
		}
	}
	t.Fatalf("recovery inventory missing %s", path)
	return 0
}

func snapshotRecoveryTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ":" + target
		case info.Mode().IsRegular():
			hash, _, err := fileSHA256(path)
			if err != nil {
				return err
			}
			value += ":" + hash
		}
		snapshot[filepath.ToSlash(rel)] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
