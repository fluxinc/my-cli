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

	"github.com/fluxinc/our-ai/internal/selfupdate"
	"github.com/fluxinc/our-ai/internal/umbrella"
	"github.com/fluxinc/our-ai/internal/worksession"
)

func TestRootCommandPrintsUmbrellaAndProductPaths(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "root", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	umbrellaRoot := filepath.Join(home, "acme")
	if strings.TrimSpace(stdout.String()) != umbrellaRoot {
		t.Fatalf("root stdout = %q, want %q", stdout.String(), umbrellaRoot)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "root", "--manifest", "acme", "--home", home, "--repo", "sample-service"}); err != nil {
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
	if err := a.run([]string{"our", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
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
	if err := a.run([]string{"our", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
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
	t.Setenv("OUR_NO_AUTO_REFRESH", "1")
	if err := a.run([]string{"our", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "remote.md")); !os.IsNotExist(err) {
		t.Fatalf("mount refreshed despite OUR_NO_AUTO_REFRESH: %v", err)
	}
	t.Setenv("OUR_NO_AUTO_REFRESH", "")
	if err := a.run([]string{"our", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-refresh"}); err != nil {
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
	if err := a.run([]string{"our", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
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
	if err := a.run([]string{"our", "root", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != umbrellaRoot+"\n" {
		t.Fatalf("root stdout = %q, want only root path", got)
	}
	errOut := stderr.String()
	if !strings.Contains(errOut, "notice\tacme:content:handbook\t") {
		t.Fatalf("root stderr = %q, want held-repo notice", errOut)
	}
	if !strings.Contains(errOut, "our sync") {
		t.Fatalf("root stderr = %q, want remediation command", errOut)
	}
}

func TestLaunchPrintsResolvedCommandWithoutCheckingGuidance(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"our", "ai",
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
		"our", "ai",
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
		"our", "ai",
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
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "workspace guidance missing") ||
		!strings.Contains(stderr.String(), "run our setup") {
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
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--setup", "--no-session", "codex", "--model", "gpt-5", "--full-auto"})
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
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".codex", "skills", "our")); err != nil {
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
			if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "our")); err != nil {
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
	if err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--no-session", "codex"}); err != nil {
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
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home}); err != nil {
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
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--no-session", "codex", "--model", "gpt-5"})
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
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "codex"})
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
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--new-session", "--no-refresh", "--no-update-check", "codex"})
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
	if err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "codex", "--model", "gpt-5"}); err != nil {
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
	if err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--new-session", "--no-refresh", "--no-update-check", "codex", "--model", "gpt-5"}); err != nil {
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
	if err := a.run([]string{"our", "work", "start", "--slug", "resume", "--home", home, "--json"}); err != nil {
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
	if err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--session", session.ID, "--no-refresh", "--no-update-check", "codex"}); err != nil {
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

func TestLaunchFromInsideSessionUsesCurrentSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--slug", "current", "--home", home, "--json"}); err != nil {
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
	if err := a.run([]string{"our", "ai", "--home", home, "--no-refresh", "--no-update-check", "codex"}); err != nil {
		t.Fatal(err)
	}
	if gotDir != session.Path {
		t.Fatalf("gotDir=%q, want current session path %q", gotDir, session.Path)
	}
}

func TestLaunchNewSessionInsideSessionCreatesFreshSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--slug", "current", "--home", home, "--json"}); err != nil {
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
	if err := a.run([]string{"our", "ai", "--home", home, "--new-session", "--no-refresh", "--no-update-check", "codex"}); err != nil {
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
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--repo", "sample-service", "--print", "codex"})
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
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--new-session", "--repo", "sample-service", "--print", "codex"})
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

func TestLaunchProductFlagRemoved(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--product", "sample-product", "--print", "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "--product was removed") ||
		!strings.Contains(stderr.String(), "--repo") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "version"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "0.1.0" {
		t.Fatalf("version stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "--version"}); err != nil {
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
	if err := a.run([]string{"our", "update", "--check", "--json"}); err != nil {
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
	if err := a.run([]string{"our", "root", "--home", home, "--umbrella", root}); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != root+"\n" {
		t.Fatalf("root stdout = %q, want only root path", got)
	}
	if got := stderr.String(); !strings.Contains(got, "a newer our (v0.2.0) is available") ||
		!strings.Contains(got, "our update") {
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
	if err := a.run([]string{"our", "root", "--home", home, "--umbrella", root, "--no-update-check"}); err != nil {
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
	if err := a.run([]string{"our", "root", "--home", home, "--umbrella", root}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != root+"\n" {
		t.Fatalf("root stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want silent best-effort failure", stderr.String())
	}
}
