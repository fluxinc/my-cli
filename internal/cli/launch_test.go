package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/bundle"
	"github.com/fluxinc/my-cli/internal/selfupdate"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/worksession"
)

func TestRootCommandPrintsUmbrellaAndProductPaths(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	umbrellaRoot := filepath.Join(home, "acme")
	if strings.TrimSpace(stdout.String()) != umbrellaRoot {
		t.Fatalf("root stdout = %q, want %q", stdout.String(), umbrellaRoot)
	}

	stdout.Reset()
	if err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home, "--repo", "sample-service"}); err != nil {
		t.Fatal(err)
	}
	wantRepo := filepath.Join(umbrellaRoot, "repos", "sample-service")
	if strings.TrimSpace(stdout.String()) != wantRepo {
		t.Fatalf("root --repo stdout = %q, want %q", stdout.String(), wantRepo)
	}
}

func TestRootAutoRefreshFastForwardsCleanStaleMountAndKeepsStdoutPure(t *testing.T) {
	remote, clone, writer := setupCLIRemoteRepo(t, t.TempDir(), "handbook", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "meetings", "2026-06-09-remote.md"), "remote\n")
	commitAndPushCLIGit(t, writer, "remote meeting")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != umbrellaRoot+"\n" {
		t.Fatalf("root stdout = %q, want only root path", got)
	}
	if !strings.Contains(stderr.String(), "refresh\tacme:content:handbook\tfixed") {
		t.Fatalf("root stderr = %q, want refresh note", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(mountPath, "meetings", "2026-06-09-remote.md")); err != nil {
		t.Fatalf("mount did not auto-refresh: %v", err)
	}
}

func TestRootAutoRefreshSkipsRecentlyRefreshedMount(t *testing.T) {
	remote, clone, writer := setupCLIRemoteRepo(t, t.TempDir(), "handbook", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	state := autoRefreshState{SchemaVersion: 1, Repos: map[string]autoRefreshRecord{
		"content:acme:handbook": {LastAutoRefresh: time.Now().UTC().Format(time.RFC3339)},
	}}
	if err := saveAutoRefreshState(umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "remote.md"), "remote\n")
	commitAndPushCLIGit(t, writer, "remote meeting")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "remote.md")); !os.IsNotExist(err) {
		t.Fatalf("mount refreshed despite recent TTL state: %v", err)
	}
	if strings.Contains(stderr.String(), "handbook\tfixed") {
		t.Fatalf("root stderr = %q, want no fixed refresh", stderr.String())
	}
}

func TestRootAutoRefreshOptOutsSkipRefresh(t *testing.T) {
	remote, clone, writer := setupCLIRemoteRepo(t, t.TempDir(), "handbook", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "remote.md"), "remote\n")
	commitAndPushCLIGit(t, writer, "remote meeting")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	t.Setenv("MYCLI_NO_AUTO_REFRESH", "1")
	if err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "remote.md")); !os.IsNotExist(err) {
		t.Fatalf("mount refreshed despite MYCLI_NO_AUTO_REFRESH: %v", err)
	}
	t.Setenv("MYCLI_NO_AUTO_REFRESH", "")
	if err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-refresh"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "remote.md")); !os.IsNotExist(err) {
		t.Fatalf("mount refreshed despite --no-refresh: %v", err)
	}
}

func TestRootAutoRefreshSkipsDirtyDivergedAndProductRepos(t *testing.T) {
	dirtyRemote, dirtyClone, dirtyWriter := setupCLIRemoteRepo(t, t.TempDir(), "dirty", map[string]string{"README.md": "seed\n"})
	divergedRemote, divergedClone, divergedWriter := setupCLIRemoteRepo(t, t.TempDir(), "diverged", map[string]string{"README.md": "seed\n"})
	productRemote, productClone, productWriter := setupCLIRemoteRepo(t, t.TempDir(), "product", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, manifestCache, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "dirty", "kind": "handbook", "git_url": "`+dirtyRemote+`", "mode": "required" },
    { "id": "diverged", "kind": "handbook", "git_url": "`+divergedRemote+`", "mode": "required" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "repos.json"), `[
  { "id": "sample-product", "git_url": "`+productRemote+`" }
]`)
	_, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	state = umbrella.AddSelectedRepo(state, "sample-product")
	if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
	dirtyPath := filepath.Join(umbrellaRoot, "dirty")
	divergedPath := filepath.Join(umbrellaRoot, "diverged")
	productPath := filepath.Join(umbrellaRoot, "products", "sample-product")
	if err := os.Rename(dirtyClone, dirtyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(divergedClone, divergedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(productPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(productClone, productPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(dirtyWriter, "remote.md"), "remote\n")
	commitAndPushCLIGit(t, dirtyWriter, "remote dirty")
	writeCLITestFile(t, filepath.Join(dirtyPath, "local.md"), "dirty\n")
	writeCLITestFile(t, filepath.Join(divergedPath, "local.md"), "local\n")
	commitCLIGit(t, divergedPath, "local diverged")
	writeCLITestFile(t, filepath.Join(divergedWriter, "remote.md"), "remote\n")
	commitAndPushCLIGit(t, divergedWriter, "remote diverged")
	writeCLITestFile(t, filepath.Join(productWriter, "remote.md"), "remote\n")
	commitAndPushCLIGit(t, productWriter, "remote product")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(dirtyPath, "remote.md"),
		filepath.Join(divergedPath, "remote.md"),
		filepath.Join(productPath, "remote.md"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s refreshed despite guard: %v", path, err)
		}
	}
}

func TestRootAutoRefreshNoticesHeldDirtyMountOnStderr(t *testing.T) {
	remote, clone, writer := setupCLIRemoteRepo(t, t.TempDir(), "handbook", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "remote.md"), "remote\n")
	commitAndPushCLIGit(t, writer, "remote update")
	writeCLITestFile(t, filepath.Join(mountPath, "local.md"), "dirty\n")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != umbrellaRoot+"\n" {
		t.Fatalf("root stdout = %q, want only root path", got)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "notice\tacme:content:handbook\t") {
		t.Fatalf("root stderr = %q, want held-repo notice", errOut)
	}
	if !strings.Contains(errOut, "my sync") {
		t.Fatalf("root stderr = %q, want remediation command", errOut)
	}
}

func TestLaunchPrintsResolvedCommandWithoutCheckingGuidance(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"my", "ai",
		"--manifest", "acme",
		"--home", home,
		"--repo", "sample-service",
		"--print",
		"codex", "--model", "gpt-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd " + filepath.Join(home, "acme", "repos", "sample-service") + " && codex --model gpt-5\n"
	if stdout.String() != want {
		t.Fatalf("launch --print stdout = %q, want %q", stdout.String(), want)
	}
}

func TestLaunchPrintDefaultsToBaseUmbrella(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "ai",
		"--manifest", "acme",
		"--home", home,
		"--print",
		"codex", "--model", "gpt-5",
	}); err != nil {
		t.Fatal(err)
	}
	sessions, err := worksession.List(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want none", sessions)
	}
	want := "cd " + umbrellaRoot + " && codex --model gpt-5\n"
	if stdout.String() != want {
		t.Fatalf("launch --print stdout = %q, want %q", stdout.String(), want)
	}
}

func TestLaunchPrintCreatesNewSessionWhenRequested(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "ai",
		"--manifest", "acme",
		"--home", home,
		"--new-session",
		"--print",
		"codex", "--model", "gpt-5",
	}); err != nil {
		t.Fatal(err)
	}
	sessions, err := worksession.List(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Status != worksession.StatusActive {
		t.Fatalf("sessions = %#v, want one active session", sessions)
	}
	want := "cd " + sessions[0].Path + " && codex --model gpt-5\n"
	if stdout.String() != want {
		t.Fatalf("launch --print stdout = %q, want %q", stdout.String(), want)
	}
	if _, err := os.Stat(filepath.Join(sessions[0].Path, "handbook")); err != nil {
		t.Fatalf("session handbook worktree missing: %v", err)
	}
}

func TestLaunchRefusesMissingGuidance(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(string) (string, error) {
			t.Fatal("lookPath called before guidance gate")
			return "", nil
		},
	}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "workspace guidance missing") ||
		!strings.Contains(stderr.String(), "run my setup") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestLaunchOnboardThenExecsWithArgs(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	var gotPath, gotDir string
	var gotArgs []string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			gotDir = dir
			return nil
		},
	}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--setup", "--no-session", "codex", "--model", "gpt-5", "--full-auto"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/test/bin/codex" || gotDir != umbrellaRoot || strings.Join(gotArgs, " ") != "--model gpt-5 --full-auto" {
		t.Fatalf("exec path=%q dir=%q args=%#v", gotPath, gotDir, gotArgs)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "AGENTS.md")); err != nil {
		t.Fatalf("ai --setup did not write guidance: %v", err)
	}
	if !strings.Contains(stdout.String(), "launch\tcodex\tcd "+umbrellaRoot+" && codex") {
		t.Fatalf("onboard stdout missing launch hint: %q", stdout.String())
	}
}

func TestLaunchInstallsAbsentSelfSkillBeforeExec(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".codex", "skills", "my")); err != nil {
		t.Fatal(err)
	}

	var gotPath, gotDir string
	a = app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "my")); err != nil {
				t.Fatalf("self-skill was not installed before lookupPath: %v", err)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotDir = dir
			return nil
		},
	}
	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-session", "codex"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/test/bin/codex" || gotDir != umbrellaRoot {
		t.Fatalf("exec path=%q dir=%q", gotPath, gotDir)
	}
	if stdout.String() != "" {
		t.Fatalf("launch stdout = %q, want quiet self-skill repair", stdout.String())
	}
}

func TestLaunchMissingHarnessPrintsFallbackAndFails(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	a = app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "", exec.ErrNotFound
		},
	}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-session", "codex", "--model", "gpt-5"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	wantLine := "cd " + umbrellaRoot + " && codex --model gpt-5"
	if !strings.Contains(stderr.String(), "codex not found on PATH") || !strings.Contains(stderr.String(), wantLine) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestLaunchMissingHarnessDefaultDoesNotCreateSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)

	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "", exec.ErrNotFound
		},
	}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	wantLine := "cd " + umbrellaRoot + " && codex"
	if !strings.Contains(stderr.String(), "codex not found on PATH") ||
		!strings.Contains(stderr.String(), wantLine) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	sessions, err := worksession.List(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want none", sessions)
	}
}

func TestLaunchMissingHarnessNewSessionDoesNotCreateSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)

	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "", exec.ErrNotFound
		},
	}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--new-session", "--no-refresh", "--no-update-check", "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "codex not found on PATH") ||
		!strings.Contains(stderr.String(), "no work session was created") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	sessions, err := worksession.List(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want none", sessions)
	}
}

func TestLaunchDefaultsToBaseUmbrella(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	var stdout, stderr bytes.Buffer
	var gotPath, gotDir string
	var gotArgs []string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "codex", "--model", "gpt-5"}); err != nil {
		t.Fatal(err)
	}
	sessions, err := worksession.List(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 || gotDir != umbrellaRoot {
		t.Fatalf("gotDir=%q sessions=%#v, want launch from base umbrella", gotDir, sessions)
	}
	if gotPath != "/test/bin/codex" || strings.Join(gotArgs, " ") != "--model gpt-5" {
		t.Fatalf("exec path=%q args=%#v", gotPath, gotArgs)
	}
}

func TestLaunchCreatesNewSessionWhenRequested(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	var stdout, stderr bytes.Buffer
	var gotPath, gotDir string
	var gotArgs []string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--new-session", "--no-refresh", "--no-update-check", "codex", "--model", "gpt-5"}); err != nil {
		t.Fatal(err)
	}
	sessions, err := worksession.List(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || gotDir != sessions[0].Path {
		t.Fatalf("gotDir=%q sessions=%#v, want launch from new session", gotDir, sessions)
	}
	if gotPath != "/test/bin/codex" || strings.Join(gotArgs, " ") != "--model gpt-5" {
		t.Fatalf("exec path=%q args=%#v", gotPath, gotArgs)
	}
}

func TestLaunchResumesExplicitSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "resume", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	var gotDir string
	stdout.Reset()
	stderr.Reset()
	a = app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--session", session.ID, "--no-refresh", "--no-update-check", "codex"}); err != nil {
		t.Fatal(err)
	}
	sessions, err := worksession.List(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || gotDir != session.Path {
		t.Fatalf("gotDir=%q sessions=%#v, want existing session only", gotDir, sessions)
	}
}

func TestLaunchResumeRegeneratesSessionGuidance(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	configureCLIRecordWorkspaceContractAndRole(t, home, umbrellaRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	session := startLaunchTestSession(t, home, "resume")
	if err := os.WriteFile(filepath.Join(session.Path, "AGENTS.md"), []byte("stale session guidance\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	var gotDir string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "-r", session.ID, "codex"}); err != nil {
		t.Fatalf("ai -r: %v\nstderr: %s", err, stderr.String())
	}
	if gotDir != session.Path {
		t.Fatalf("gotDir=%q, want %q", gotDir, session.Path)
	}
	agents, err := os.ReadFile(filepath.Join(session.Path, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(agents)
	for _, want := range []string{
		"## Session Context",
		"- Umbrella root: " + umbrellaRoot,
		"- Session id: " + session.ID,
		"my ai -r " + session.ID + " <harness>",
		"Always preserve the example contract.",
		"Operator role guidance applies.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("regenerated guidance missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "stale session guidance") {
		t.Fatalf("stale guidance was not replaced:\n%s", body)
	}
	claude, err := os.ReadFile(filepath.Join(session.Path, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(claude) != body {
		t.Fatal("CLAUDE.md does not match regenerated AGENTS.md")
	}
}

func TestLaunchResumeShortcutExplicitSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	session := startLaunchTestSession(t, home, "shortcut")

	var stdout, stderr bytes.Buffer
	var gotDir string
	var gotArgs []string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotDir = dir
			gotArgs = append([]string(nil), args...)
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "-r", session.ID, "codex", "--model", "gpt-5"}); err != nil {
		t.Fatalf("ai -r explicit: %v\nstderr: %s", err, stderr.String())
	}
	if gotDir != session.Path {
		t.Fatalf("gotDir=%q, want session path %q", gotDir, session.Path)
	}
	if strings.Join(gotArgs, " ") != "--model gpt-5" {
		t.Fatalf("gotArgs=%#v, want harness args", gotArgs)
	}
}

func TestLaunchResumeShortcutHarnessTokenAutoSelectsSingleSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	session := startLaunchTestSession(t, home, "single")

	var stdout, stderr bytes.Buffer
	var gotPath, gotDir string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "-r", "codex"}); err != nil {
		t.Fatalf("ai -r codex: %v\nstderr: %s", err, stderr.String())
	}
	if gotPath != "/test/bin/codex" || gotDir != session.Path {
		t.Fatalf("path=%q dir=%q, want codex in %q", gotPath, gotDir, session.Path)
	}
}

func TestLaunchResumeShortcutMultipleActiveNonInteractiveListsIDs(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	first := startLaunchTestSession(t, home, "alpha")
	second := startLaunchTestSession(t, home, "beta")

	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			t.Fatalf("lookPath called despite unresolved resume selection for %q", name)
			return "", exec.ErrNotFound
		},
	}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "-r", "codex"})
	if err == nil {
		t.Fatal("ai -r with multiple active sessions succeeded without a TTY")
	}
	msg := err.Error()
	if !strings.Contains(msg, "multiple active sessions; pass a session id") ||
		!strings.Contains(msg, first.ID) ||
		!strings.Contains(msg, second.ID) ||
		!strings.Contains(msg, "my ai -r "+first.ID+" codex") {
		t.Fatalf("error = %q, want active ids and example", msg)
	}
	if stdout.String() != "" || stderr.String() != "" {
		t.Fatalf("stdout=%q stderr=%q, want no prompt output", stdout.String(), stderr.String())
	}
}

func TestLaunchResumeShortcutInteractivePickerKeepsPrintStdoutPure(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	_ = startLaunchTestSession(t, home, "alpha")
	second := startLaunchTestSession(t, home, "beta")

	var stdout, stderr bytes.Buffer
	a := app{
		stdout:      &stdout,
		stderr:      &stderr,
		stdin:       strings.NewReader("2\n"),
		interactive: true,
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "--print", "-r", "codex"}); err != nil {
		t.Fatalf("ai -r picker print: %v\nstderr: %s", err, stderr.String())
	}
	want := "cd " + second.Path + " && codex\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want only command %q", stdout.String(), want)
	}
	if !strings.Contains(stderr.String(), "Select a work session:") ||
		!strings.Contains(stderr.String(), second.ID) {
		t.Fatalf("stderr = %q, want picker", stderr.String())
	}
}

func TestLaunchFromInsideSessionUsesCurrentSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "current", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	t.Chdir(session.Mounts[0].WorktreePath)

	var gotDir string
	stdout.Reset()
	stderr.Reset()
	a = app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--home", home, "--no-refresh", "--no-update-check", "codex"}); err != nil {
		t.Fatal(err)
	}
	if gotDir != session.Path {
		t.Fatalf("gotDir=%q, want current session path %q", gotDir, session.Path)
	}
}

func startLaunchTestSession(t *testing.T, home, slug string) worksession.Session {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	args := []string{"my", "work", "start", "--home", home, "--json"}
	if slug != "" {
		args = append(args, "--slug", slug)
	}
	if err := a.run(args); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatalf("parse session JSON: %v\nstdout: %s", err, stdout.String())
	}
	return session
}

func TestLaunchNewSessionInsideSessionCreatesFreshSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "current", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	t.Chdir(session.Mounts[0].WorktreePath)

	var gotDir string
	stdout.Reset()
	stderr.Reset()
	a = app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--home", home, "--new-session", "--no-refresh", "--no-update-check", "codex"}); err != nil {
		t.Fatal(err)
	}
	sessions, err := worksession.List(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions = %#v, want a second fresh session", sessions)
	}
	if gotDir == session.Path {
		t.Fatalf("gotDir=%q resumed the current session, want a fresh session", gotDir)
	}
	var fresh worksession.Session
	for _, candidate := range sessions {
		if candidate.ID != session.ID {
			fresh = candidate
		}
	}
	if gotDir != fresh.Path {
		t.Fatalf("gotDir=%q, want fresh session path %q", gotDir, fresh.Path)
	}
}

func TestLaunchRepoDefaultsToBaseCheckout(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--repo", "sample-service", "--print", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd " + filepath.Join(home, "acme", "repos", "sample-service") + " && codex\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestLaunchRepoRefusesSessionFlags(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--new-session", "--repo", "sample-service", "--print", "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "repo worktrees are not included in work sessions") ||
		!strings.Contains(stderr.String(), "omit --new-session") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestLaunchMaterializesOrgSkillsIntoLaunchRootAndMirror(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	var stdout, stderr bytes.Buffer
	var gotDir string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "claude-code"}); err != nil {
		t.Fatal(err)
	}
	if gotDir != umbrellaRoot {
		t.Fatalf("gotDir = %q, want %q", gotDir, umbrellaRoot)
	}
	for _, base := range []string{
		filepath.Join(umbrellaRoot, ".agents", "skills"),
		filepath.Join(umbrellaRoot, ".claude", "skills"),
	} {
		assertLaunchSkill(t, filepath.Join(base, "acme-handbook"), "acme:handbook")
		assertLaunchSkill(t, filepath.Join(base, "acme-calendar"), "acme:calendar")
		if _, err := os.Stat(filepath.Join(base, "my")); !os.IsNotExist(err) {
			t.Fatalf("self-skill was materialized into launch root at %s: %v", filepath.Join(base, "my"), err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("org skill was installed globally: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "my", "SKILL.md")); err != nil {
		t.Fatalf("global self-skill missing: %v", err)
	}
}

func TestLaunchCodexUsesAgentsSkillsWithoutMirror(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error { return nil },
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "codex"}); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(umbrellaRoot, ".agents", "skills")
	assertLaunchSkill(t, filepath.Join(base, "acme-handbook"), "acme:handbook")
	assertLaunchSkill(t, filepath.Join(base, "acme-calendar"), "acme:calendar")
	if _, err := os.Stat(filepath.Join(umbrellaRoot, ".codex", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("codex mirror should not be written because codex reads .agents/skills: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "my", "SKILL.md")); err != nil {
		t.Fatalf("global self-skill missing: %v", err)
	}
}

func TestLaunchOpenCodeUsesCompatibilityGlobalSkills(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error { return nil },
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "opencode"}); err != nil {
		t.Fatal(err)
	}
	globalSkillsDir := filepath.Join(home, ".config", "opencode", "skills")
	assertIndexedGlobalSkill(t, globalSkillsDir, "acme-handbook", "acme:handbook", compatibilityGlobalSkillScope)
	assertIndexedGlobalSkill(t, globalSkillsDir, "acme-calendar", "acme:calendar", compatibilityGlobalSkillScope)
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", "my", "SKILL.md")); err != nil {
		t.Fatalf("global self-skill missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, ".agents", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("opencode should not write unread .agents launch skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, ".config", "opencode", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("opencode should not write unread launch-root mirror: %v", err)
	}
}

func TestLaunchOpenCodeRejectsSkillSelectors(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			t.Fatal("execHarness called despite unsupported selector")
			return nil
		},
	}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "--profile", "support", "opencode"})
	if err == nil || !strings.Contains(err.Error(), "does not support launch-scoped skill profiles") {
		t.Fatalf("err = %v, want launch-scoped profile support error", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "skills", "acme-calendar")); !os.IsNotExist(err) {
		t.Fatalf("unsupported profile should not install selected org skill globally: %v", err)
	}
}

func TestLaunchProfileSelectorAndSkillsNone(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error { return nil },
	}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "--profile", "support", "codex"}); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(umbrellaRoot, ".agents", "skills")
	assertLaunchSkill(t, filepath.Join(base, "acme-calendar"), "acme:calendar")
	if _, err := os.Stat(filepath.Join(base, "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("unselected skill exists after profile launch: %v", err)
	}

	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "--skills", "none", "codex"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "acme-calendar")); !os.IsNotExist(err) {
		t.Fatalf("launch-scoped skill was not wiped by --skills none: %v", err)
	}
}

func TestLaunchPrintWithSelectorsDoesNotMaterializeOrValidateProfile(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--print", "--profile", "missing", "codex"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "cd "+umbrellaRoot+" && codex") {
		t.Fatalf("stdout = %q, want command preview", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, ".agents", "skills")); !os.IsNotExist(err) {
		t.Fatalf("--print materialized launch skills: %v", err)
	}
}

func TestLaunchRefusesNonMySkillCollisionWithoutPartialMaterialization(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	writeCLITestFile(t, filepath.Join(umbrellaRoot, ".agents", "skills", "acme-handbook", "SKILL.md"), "---\nname: acme-handbook\n---\nmanual\n")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			t.Fatal("execHarness called despite collision")
			return nil
		},
	}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "codex"})
	if err == nil || !strings.Contains(err.Error(), "collides with non-My AI entry") {
		t.Fatalf("err = %v, want collision error", err)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, ".agents", "skills", "acme-calendar")); !os.IsNotExist(err) {
		t.Fatalf("other selected skill was materialized despite preflight failure: %v", err)
	}
}

func TestOnboardingLaunchPromptsAndReplacesSkillCollision(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	writeLegacyOurLaunchSkill(t, filepath.Join(umbrellaRoot, ".agents", "skills", "acme-handbook"), "acme:handbook")
	var stdout, stderr bytes.Buffer
	var execCalled bool
	a := app{
		stdout:      &stdout,
		stderr:      &stderr,
		stdin:       strings.NewReader("y\n"),
		interactive: true,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			execCalled = true
			return nil
		},
	}
	err := a.runLaunchWithInitialPrompt([]string{"--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "codex"}, "hello")
	if err != nil {
		t.Fatalf("launch with collision replace: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !execCalled {
		t.Fatal("execHarness was not called after replacing collision")
	}
	if !strings.Contains(stdout.String(), `Launch skill "acme-handbook" already exists`) ||
		!strings.Contains(stdout.String(), "Replace it for this launch? [y/N]:") {
		t.Fatalf("stdout missing replace prompt:\n%s", stdout.String())
	}
	assertLaunchSkill(t, filepath.Join(umbrellaRoot, ".agents", "skills", "acme-handbook"), "acme:handbook")
	assertLaunchSkill(t, filepath.Join(umbrellaRoot, ".agents", "skills", "acme-calendar"), "acme:calendar")
	if _, err := os.Stat(filepath.Join(umbrellaRoot, ".agents", "skills", "acme-handbook", ".our-managed.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy marker should be removed on replace: %v", err)
	}
}

func TestOnboardingLaunchPromptsAndSkipsSkillCollision(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	target := filepath.Join(umbrellaRoot, ".agents", "skills", "acme-handbook")
	writeLegacyOurLaunchSkill(t, target, "acme:handbook")
	var stdout, stderr bytes.Buffer
	var execCalled bool
	a := app{
		stdout:      &stdout,
		stderr:      &stderr,
		stdin:       strings.NewReader("n\n"),
		interactive: true,
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			execCalled = true
			return nil
		},
	}
	err := a.runLaunchWithInitialPrompt([]string{"--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-session", "--no-refresh", "--no-update-check", "codex"}, "hello")
	if err != nil {
		t.Fatalf("launch with collision skip: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !execCalled {
		t.Fatal("execHarness was not called after skipping collision")
	}
	if !strings.Contains(stdout.String(), `Skipping launch skill "acme-handbook"; existing entry left in place.`) {
		t.Fatalf("stdout missing skip notice:\n%s", stdout.String())
	}
	if got := readCLITestFile(t, filepath.Join(target, "SKILL.md")); !strings.Contains(got, "legacy") {
		t.Fatalf("collision target was replaced despite skip:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(target, ".our-managed.json")); err != nil {
		t.Fatalf("legacy marker should remain on skip: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, bundle.MarkerName)); !os.IsNotExist(err) {
		t.Fatalf("my marker should not be written on skip: %v", err)
	}
	assertLaunchSkill(t, filepath.Join(umbrellaRoot, ".agents", "skills", "acme-calendar"), "acme:calendar")
}

func TestLaunchProductFlagRemoved(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--product", "sample-product", "--print", "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "--product was removed") ||
		!strings.Contains(stderr.String(), "--repo") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writeLegacyOurLaunchSkill(t *testing.T, dir, canonicalID string) {
	t.Helper()
	writeCLITestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+filepath.Base(dir)+"\n---\nlegacy\n")
	writeCLITestFile(t, filepath.Join(dir, ".our-managed.json"), `{
  "installer": "our",
  "version": "v0.27.0",
  "mode": "copy",
  "source": "/tmp/our",
  "canonical_id": "`+canonicalID+`",
  "scope": "launch"
}`)
}

func setupCLILaunchProfileFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	umbrellaRoot := filepath.Join(home, "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" },
    { "id": "acme:calendar", "install_slug": "acme-calendar", "path": "skills/acme-calendar" }
  ],
  "profiles": [
    { "id": "support", "purpose": "Support loadout", "skills": ["acme:calendar"] }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "skills", "acme-handbook", "SKILL.md"), "---\nname: acme-handbook\n---\n")
	writeCLITestFile(t, filepath.Join(manifestCache, "skills", "acme-calendar", "SKILL.md"), "---\nname: acme-calendar\n---\n")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "manifests", "add", "acme", "https://github.com/acme/acme-ai-manifest.git", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	ensureCLIGuidance(t, home, umbrellaRoot)
	return home, umbrellaRoot
}

func assertLaunchSkill(t *testing.T, dir, canonicalID string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatalf("launch skill missing at %s: %v", dir, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, bundle.MarkerName))
	if err != nil {
		t.Fatalf("launch marker missing at %s: %v", dir, err)
	}
	var marker struct {
		Installer   string `json:"installer"`
		CanonicalID string `json:"canonical_id"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		t.Fatalf("marker JSON: %v\n%s", err, data)
	}
	if marker.Installer != "my" || marker.CanonicalID != canonicalID || marker.Scope != "launch" {
		t.Fatalf("marker = %+v, want canonical_id=%q scope=launch", marker, canonicalID)
	}
}

func assertIndexedGlobalSkill(t *testing.T, skillsDir, skillName, canonicalID, scope string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(skillsDir, skillName, "SKILL.md")); err != nil {
		t.Fatalf("global skill missing at %s: %v", filepath.Join(skillsDir, skillName), err)
	}
	data, err := os.ReadFile(filepath.Join(skillsDir, bundle.MarkerName))
	if err != nil {
		t.Fatalf("global skill index missing at %s: %v", skillsDir, err)
	}
	var index struct {
		Installer string `json:"installer"`
		Mode      string `json:"mode"`
		Skills    map[string]struct {
			CanonicalID string `json:"canonical_id"`
			Scope       string `json:"scope"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("index JSON: %v\n%s", err, data)
	}
	item, ok := index.Skills[skillName]
	if index.Installer != "my" || index.Mode != "index" || !ok || item.CanonicalID != canonicalID || item.Scope != scope {
		t.Fatalf("index = %+v, want %s canonical_id=%q scope=%s", index, skillName, canonicalID, scope)
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "version"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "0.1.0" {
		t.Fatalf("version stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "--version"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "0.1.0" {
		t.Fatalf("--version stdout = %q", stdout.String())
	}
}

func TestUpdateCheckJSON(t *testing.T) {
	server := newCLILatestServer(t, "0.2.0")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:               &stdout,
		stderr:               &stderr,
		updateCurrentVersion: "0.1.0",
		updateSource:         server.source(),
	}
	if err := a.run([]string{"my", "update", "--check", "--json"}); err != nil {
		t.Fatal(err)
	}
	var result selfupdate.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("parse update JSON: %v\n%s", err, stdout.String())
	}
	if !result.UpdateAvailable || result.TargetVersion != "0.2.0" || !result.CheckOnly {
		t.Fatalf("result = %#v", result)
	}
	if server.assetRequests != 0 {
		t.Fatalf("asset requests = %d, want 0 for --check", server.assetRequests)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRootUpdateNoticeKeepsStdoutPure(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "umbrella")
	server := newCLILatestServer(t, "0.2.0")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:               &stdout,
		stderr:               &stderr,
		updateCurrentVersion: "0.1.0",
		updateSource:         server.source(),
		updateNow:            func() time.Time { return time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC) },
	}
	if err := a.run([]string{"my", "root", "--home", home, "--umbrella", root}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != root+"\n" {
		t.Fatalf("root stdout = %q, want only root path", got)
	}
	if got := stderr.String(); !strings.Contains(got, "a newer my (v0.2.0) is available") ||
		!strings.Contains(got, "my update") {
		t.Fatalf("stderr = %q, want update notice", got)
	}
}

func TestRootUpdateNoticeOptOutSkipsNetwork(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "umbrella")
	server := newCLILatestServer(t, "0.2.0")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:               &stdout,
		stderr:               &stderr,
		updateCurrentVersion: "0.1.0",
		updateSource:         server.source(),
	}
	if err := a.run([]string{"my", "root", "--home", home, "--umbrella", root, "--no-update-check"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != root+"\n" {
		t.Fatalf("root stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if server.latestRequests != 0 {
		t.Fatalf("latest requests = %d, want 0", server.latestRequests)
	}
}

func TestRootUpdateNoticeBestEffortOnNetworkError(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(t.TempDir(), "umbrella")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:               &stdout,
		stderr:               &stderr,
		updateCurrentVersion: "0.1.0",
		updateSource: selfupdate.Source{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline")
		})}},
	}
	if err := a.run([]string{"my", "root", "--home", home, "--umbrella", root}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != root+"\n" {
		t.Fatalf("root stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want silent best-effort failure", stderr.String())
	}
}
