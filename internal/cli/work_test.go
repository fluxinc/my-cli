package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/worksession"
)

func TestWorkStartCreatesSessionAndRegistry(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Join(home, "acme")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"our", "work", "start",
		"--slug", "notes",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}

	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if session.Status != worksession.StatusActive || !strings.Contains(session.ID, "-notes-") {
		t.Fatalf("session = %#v", session)
	}
	if len(session.Mounts) != 1 || session.Mounts[0].ID != "handbook" {
		t.Fatalf("mounts = %#v", session.Mounts)
	}
	worktree := session.Mounts[0].WorktreePath
	if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
		t.Fatalf("worktree missing: %v", err)
	}
	branch := strings.TrimSpace(gitCLIOutput(t, worktree, "rev-parse", "--abbrev-ref", "HEAD"))
	if branch != "our/work/"+session.ID {
		t.Fatalf("worktree branch = %q", branch)
	}
	if _, err := worksession.Load(umbrellaRoot, session.ID); err != nil {
		t.Fatalf("registry record missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(session.Path, "scratch")); err != nil {
		t.Fatalf("scratch missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(session.Path, "SESSION.md")); err != nil {
		t.Fatalf("SESSION.md missing: %v", err)
	}
}

func TestWorkStartExcludesRepoKindAndMissingMounts(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	manifestPath := filepath.Join(home, ".local", "share", "our", "manifests", "acme", "manifest.json")
	writeCLITestFile(t, manifestPath, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "default"
    },
    {
      "id": "tools-repo",
      "kind": "repo",
      "git_url": "https://github.com/acme/acme-tools.git",
      "mode": "default"
    },
    {
      "id": "notes",
      "kind": "docs",
      "git_url": "https://github.com/acme/acme-notes.git",
      "mode": "optional"
    }
  ]
}`)
	toolsRepo := filepath.Join(home, "acme", "tools-repo")
	writeCLITestFile(t, filepath.Join(toolsRepo, "main.go"), "package main\n")
	initCLIGitRepo(t, toolsRepo)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}

	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if len(session.Mounts) != 1 || session.Mounts[0].ID != "handbook" {
		t.Fatalf("mounts = %#v, want handbook only", session.Mounts)
	}
	if _, err := os.Stat(filepath.Join(session.Path, "tools-repo")); !os.IsNotExist(err) {
		t.Fatalf("repo-kind mount got a session worktree: %v", err)
	}
}

func TestWorkStartExpandsTildeUmbrella(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"our", "work", "start",
		"--home", home,
		"--umbrella", "~/acme",
		"--json",
	}); err != nil {
		t.Fatalf("work start with tilde umbrella: %v\nstderr: %s", err, stderr.String())
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	wantPrefix := filepath.Join(home, "acme", "work")
	if !strings.HasPrefix(session.Path, wantPrefix) {
		t.Fatalf("session path = %q, want under %q", session.Path, wantPrefix)
	}
}

func TestWorkStartWithoutEligibleMountsFails(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "work", "start", "--home", home})
	if err == nil {
		t.Fatal("want error without umbrella/mounts")
	}
}

func TestWorkStatusReportsActiveSessionState(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--slug", "fix", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "draft.md"), "draft\n")

	stdout.Reset()
	if err := a.run([]string{"our", "work", "status", "--home", home, "--json"}); err != nil {
		t.Fatalf("work status: %v\nstderr: %s", err, stderr.String())
	}
	var statuses []worksession.SessionStatus
	if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if len(statuses) != 1 || statuses[0].ID != session.ID {
		t.Fatalf("statuses = %#v", statuses)
	}
	mount := statuses[0].Mounts[0]
	if len(mount.Dirty) != 1 || !strings.Contains(mount.Dirty[0], "meetings/draft.md") {
		t.Fatalf("dirty = %#v", mount.Dirty)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "work", "status", "--home", home}); err != nil {
		t.Fatal(err)
	}
	human := stdout.String()
	if !strings.Contains(human, session.ID) || !strings.Contains(human, "handbook") {
		t.Fatalf("human status output = %q", human)
	}
}

func TestWorkStatusEmptyWithoutSessions(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "status", "--home", home, "--json"}); err != nil {
		t.Fatalf("work status: %v\nstderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "[]" {
		t.Fatalf("stdout = %q, want []", got)
	}
}

func TestWorkFinishLandCommitsDirtySessionContent(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--slug", "finish", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	worktree := session.Mounts[0].WorktreePath
	writeCLITestFile(t, filepath.Join(worktree, "meetings", "landed.md"), "landed\n")
	runCLIGit(t, worktree, "add", "-N", "meetings/landed.md")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "work", "finish", session.ID,
		"--land",
		"--message", "Land session content",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("work finish --land: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}
	var report workFinishCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if report.Mode != "land" || report.Finish.Session.Status != worksession.StatusFinished || report.Finish.Session.Outcome != worksession.OutcomeLanded {
		t.Fatalf("report = %#v", report)
	}
	if got := strings.TrimSpace(readCLITestFile(t, filepath.Join(workspaceRoot, "meetings", "landed.md"))); got != "landed" {
		t.Fatalf("landed file = %q", got)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after land: %v", err)
	}
	if log := gitCLIOutput(t, workspaceRoot, "log", "--oneline", "-1"); !strings.Contains(log, "Land session content") {
		t.Fatalf("base log = %q", log)
	}
}

func TestWorkFinishDefaultsToSingleActiveSessionAndHoldsUnadopted(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "draft.md"), "draft\n")

	stdout.Reset()
	stderr.Reset()
	err := a.run([]string{"our", "work", "finish", "--land", "--home", home})
	if err == nil || !strings.Contains(err.Error(), "unadopted untracked content file") {
		t.Fatalf("err = %v, want unadopted hold", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspaceRoot, "meetings", "draft.md")); !os.IsNotExist(statErr) {
		t.Fatalf("draft landed despite hold: %v", statErr)
	}
	loaded, loadErr := worksession.Load(filepath.Join(home, "acme"), session.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.Status != worksession.StatusActive {
		t.Fatalf("session status = %q, want active", loaded.Status)
	}
}

func TestWorkFinishDiscardRemovesSession(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "draft.md"), "draft\n")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "work", "finish", session.ID, "--discard", "--home", home, "--json"}); err != nil {
		t.Fatalf("work finish --discard: %v\nstderr: %s", err, stderr.String())
	}
	var report workFinishCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Finish.Session.Status != worksession.StatusDiscarded || report.Finish.Session.Outcome != worksession.OutcomeDiscarded {
		t.Fatalf("report = %#v", report)
	}
	if _, err := os.Stat(session.Path); !os.IsNotExist(err) {
		t.Fatalf("session path remains after discard: %v", err)
	}
}

func TestWorkFinishPublishLandsAndReportsLocalOnlySync(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "work", "start", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "publish.md"), "publish\n")
	runCLIGit(t, session.Mounts[0].WorktreePath, "add", "-N", "meetings/publish.md")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "work", "finish", session.ID, "--publish", "--home", home, "--json"}); err != nil {
		t.Fatalf("work finish --publish: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}
	var report workFinishCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if report.Mode != "publish" || report.Sync == nil || len(report.Sync.Results) != 1 {
		t.Fatalf("report = %#v", report)
	}
	if got := report.Sync.Results[0].Status; got != "local-only" {
		t.Fatalf("sync status = %q, want local-only", got)
	}
	if report.Finish.Session.Outcome != worksession.OutcomeLanded {
		t.Fatalf("outcome = %q, want landed until sync actually publishes", report.Finish.Session.Outcome)
	}
	if got := strings.TrimSpace(readCLITestFile(t, filepath.Join(workspaceRoot, "meetings", "publish.md"))); got != "publish" {
		t.Fatalf("landed file = %q", got)
	}
}

func readCLITestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
