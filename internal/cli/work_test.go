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
