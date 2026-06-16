package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/worksession"
)

func configureCLIRecordWorkspaceContractAndRole(t *testing.T, home, umbrellaRoot string) {
	t.Helper()
	manifestDir := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestDir, "guidance", "operator.md"), "Operator role guidance applies.\n")
	writeCLITestFile(t, filepath.Join(manifestDir, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "default"
    }
  ],
  "roles": [
    {
      "id": "operator",
      "purpose": "Operate the example workspace",
      "guidance_paths": ["guidance/operator.md"]
    }
  ],
  "contract": [
    "Always preserve the example contract."
  ]
}`)
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.SelectedRole = "operator"
	if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
}

func TestWorkStartCreatesSessionAndRegistry(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Join(home, "acme")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"my", "work", "start",
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
	if branch != "my/work/"+session.ID {
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

func TestWorkStartSessionGuidanceIncludesConcreteContextAndContract(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	configureCLIRecordWorkspaceContractAndRole(t, home, umbrellaRoot)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "work", "start",
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
	guidance, err := os.ReadFile(filepath.Join(session.Path, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(guidance)
	for _, want := range []string{
		"## Session Context",
		"- Organization: Acme Example (acme)",
		"- Manifest: acme",
		"- Selected role: operator",
		"- Umbrella root: " + umbrellaRoot,
		"- Session id: " + session.ID,
		"- Session path: " + session.Path,
		"- Status: active",
		session.Mounts[0].WorktreePath,
		"branch my/work/" + session.ID,
		"my ai -r " + session.ID + " <harness>",
		"my work finish " + session.ID + " --land | --publish | --discard",
		"## Organization Contract",
		"Always preserve the example contract.",
		"## Manifest Guidance: guidance/operator.md",
		"Operator role guidance applies.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("session guidance missing %q:\n%s", want, body)
		}
	}
}

func TestWorkStartHumanOutputIncludesSessionFinishCommand(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "notes", "--home", home}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "started session ") {
		t.Fatalf("work start stdout = %q", stdout.String())
	}
	sessionID := strings.TrimPrefix(lines[0], "started session ")
	want := "finish with: my work finish " + sessionID + " --land | --publish | --discard"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("work start stdout = %q, want %q", stdout.String(), want)
	}
}

func TestWorkStartExcludesCatalogReposAndMissingMounts(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	manifestDir := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestDir, "manifest.json"), `{
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
      "id": "notes",
      "kind": "docs",
      "git_url": "https://github.com/acme/acme-notes.git",
      "mode": "optional"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestDir, "catalog", "repos.json"), `[
  { "id": "tools", "git_url": "https://github.com/acme/acme-tools.git" }
]`)
	toolsRepo := filepath.Join(home, "acme", "repos", "tools")
	writeCLITestFile(t, filepath.Join(toolsRepo, "main.go"), "package main\n")
	initCLIGitRepo(t, toolsRepo)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}

	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if len(session.Mounts) != 1 || session.Mounts[0].ID != "handbook" {
		t.Fatalf("mounts = %#v, want handbook only", session.Mounts)
	}
	if _, err := os.Stat(filepath.Join(session.Path, "tools")); !os.IsNotExist(err) {
		t.Fatalf("catalog repo got a session worktree: %v", err)
	}
}

func TestWorkStartExpandsTildeUmbrella(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"my", "work", "start",
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
	err := a.run([]string{"my", "work", "start", "--home", home})
	if err == nil {
		t.Fatal("want error without umbrella/mounts")
	}
}

type doctorSessionsReport struct {
	Sessions []struct {
		Name    string `json:"name"`
		Status  string `json:"status"`
		Path    string `json:"path"`
		Message string `json:"message"`
	} `json:"sessions"`
}

func startCLIDoctorSession(t *testing.T, a *app, home string) worksession.Session {
	t.Helper()
	var stdout, stderr bytes.Buffer
	starter := app{stdout: &stdout, stderr: &stderr}
	if err := starter.run([]string{"my", "work", "start", "--slug", "notes", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	return session
}

func TestDoctorReportsActiveSessionDirty(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Join(home, "acme")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	session := startCLIDoctorSession(t, &a, home)
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "notes.md"), "draft\n")

	if err := a.run([]string{"my", "doctor", "--home", home, "--umbrella", umbrellaRoot, "--no-fetch", "--json"}); err != nil {
		t.Fatalf("doctor: %v\nstderr: %s", err, stderr.String())
	}
	var report doctorSessionsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if len(report.Sessions) != 1 {
		t.Fatalf("sessions = %#v", report.Sessions)
	}
	item := report.Sessions[0]
	if item.Name != session.ID || item.Status != "warning" || item.Path != session.Path {
		t.Fatalf("item = %#v", item)
	}
	if !strings.Contains(item.Message, "1 dirty") || !strings.Contains(item.Message, "my work finish "+session.ID) {
		t.Fatalf("message = %q", item.Message)
	}

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--home", home, "--umbrella", umbrellaRoot, "--no-fetch"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "session\t"+session.ID+"\twarning") {
		t.Fatalf("human output = %q", stdout.String())
	}
}

func TestDoctorReportsCleanActiveSessionOK(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Join(home, "acme")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	session := startCLIDoctorSession(t, &a, home)

	if err := a.run([]string{"my", "doctor", "--home", home, "--umbrella", umbrellaRoot, "--no-fetch", "--json"}); err != nil {
		t.Fatalf("doctor: %v\nstderr: %s", err, stderr.String())
	}
	var report doctorSessionsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].Status != "ok" || report.Sessions[0].Name != session.ID {
		t.Fatalf("sessions = %#v", report.Sessions)
	}
}

func TestDoctorReportsSessionMissingWorktree(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Join(home, "acme")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	session := startCLIDoctorSession(t, &a, home)
	if err := os.RemoveAll(session.Mounts[0].WorktreePath); err != nil {
		t.Fatal(err)
	}

	if err := a.run([]string{"my", "doctor", "--home", home, "--umbrella", umbrellaRoot, "--no-fetch", "--json"}); err != nil {
		t.Fatalf("doctor: %v\nstderr: %s", err, stderr.String())
	}
	var report doctorSessionsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].Status != "error" {
		t.Fatalf("sessions = %#v", report.Sessions)
	}
	if !strings.Contains(report.Sessions[0].Message, "worktree missing") {
		t.Fatalf("message = %q", report.Sessions[0].Message)
	}
}

func TestDoctorCountsArchivedSessions(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Join(home, "acme")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	session := startCLIDoctorSession(t, &a, home)
	if err := a.run([]string{"my", "work", "finish", session.ID, "--discard", "--home", home}); err != nil {
		t.Fatalf("work finish --discard: %v\nstderr: %s", err, stderr.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--home", home, "--umbrella", umbrellaRoot, "--no-fetch", "--json"}); err != nil {
		t.Fatalf("doctor: %v\nstderr: %s", err, stderr.String())
	}
	var report doctorSessionsReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sessions) != 1 {
		t.Fatalf("sessions = %#v", report.Sessions)
	}
	item := report.Sessions[0]
	if item.Name != "archived" || item.Status != "ok" || !strings.Contains(item.Message, "discarded=1") {
		t.Fatalf("item = %#v", item)
	}
}

func TestDoctorOmitsSessionsWhenNone(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Join(home, "acme")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "doctor", "--home", home, "--umbrella", umbrellaRoot, "--no-fetch", "--json"}); err != nil {
		t.Fatalf("doctor: %v\nstderr: %s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), `"sessions"`) {
		t.Fatalf("doctor JSON has sessions section without sessions: %s", stdout.String())
	}
}

func TestWorkStatusReportsActiveSessionState(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "fix", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "draft.md"), "draft\n")

	stdout.Reset()
	if err := a.run([]string{"my", "work", "status", "--home", home, "--json"}); err != nil {
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
	if err := a.run([]string{"my", "work", "status", "--home", home}); err != nil {
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
	if err := a.run([]string{"my", "work", "status", "--home", home, "--json"}); err != nil {
		t.Fatalf("work status: %v\nstderr: %s", err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "[]" {
		t.Fatalf("stdout = %q, want []", got)
	}
}

func TestWorkListAliasesStatus(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "list", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "work", "status", "--home", home, "--json"}); err != nil {
		t.Fatalf("work status: %v\nstderr: %s", err, stderr.String())
	}
	statusOut := stdout.String()

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "work", "list", "--home", home, "--json"}); err != nil {
		t.Fatalf("work list: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.String() != statusOut {
		t.Fatalf("work list stdout = %q, want status output %q", stdout.String(), statusOut)
	}
}

func TestWorkResumePrintsSessionPath(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "resume", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "work", "resume", session.ID, "--home", home}); err != nil {
		t.Fatalf("work resume: %v\nstderr: %s", err, stderr.String())
	}
	if stdout.String() != "cd "+session.Path+"\n" {
		t.Fatalf("resume stdout = %q, want session path", stdout.String())
	}
}

func TestSyncHoldsContentMountWithActiveSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	remote := filepath.Join(home, "remote.git")
	runCLIGit(t, home, "init", "--bare", "-q", "remote.git")
	runCLIGit(t, workspaceRoot, "remote", "add", "origin", remote)
	runCLIGit(t, workspaceRoot, "push", "-q", "origin", "HEAD")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "hold", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v\nstderr: %s", err, stderr.String())
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "wip.md"), "wip\n")
	writeCLITestFile(t, filepath.Join(workspaceRoot, "meetings", "base-note.md"), "base\n")
	runCLIGit(t, workspaceRoot, "add", "-N", "meetings/base-note.md")

	stdout.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--print",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("sync --print: %v\nstderr: %s", err, stderr.String())
	}
	var report struct {
		Results []struct {
			ID      string `json:"id"`
			Role    string `json:"role"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"results"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	var found bool
	for _, result := range report.Results {
		if result.Role != "content" || result.ID != "handbook" {
			continue
		}
		found = true
		if result.Status != "held back" ||
			!strings.Contains(result.Message, session.ID) ||
			!strings.Contains(result.Message, "my work finish "+session.ID) {
			t.Fatalf("content result = %#v, want session hold naming %s", result, session.ID)
		}
	}
	if !found {
		t.Fatalf("no content result in report: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "work", "finish", session.ID, "--discard", "--home", home, "--json"}); err != nil {
		t.Fatalf("work finish --discard: %v\nstderr: %s", err, stderr.String())
	}
	stdout.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--print",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("second sync --print: %v\nstderr: %s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), session.ID) {
		t.Fatalf("discarded session still holds sync: %s", stdout.String())
	}
}

func TestWorkFinishPublishHeldByOtherActiveSession(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	remote := filepath.Join(home, "remote.git")
	runCLIGit(t, home, "init", "--bare", "-q", "remote.git")
	runCLIGit(t, workspaceRoot, "remote", "add", "origin", remote)
	runCLIGit(t, workspaceRoot, "push", "-q", "origin", "HEAD")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "first", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var finishing worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &finishing); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "work", "start", "--slug", "second", "--home", home, "--json"}); err != nil {
		t.Fatalf("second work start: %v", err)
	}
	var other worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &other); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(finishing.Mounts[0].WorktreePath, "meetings", "done.md"), "done\n")
	runCLIGit(t, finishing.Mounts[0].WorktreePath, "add", "-N", "meetings/done.md")
	writeCLITestFile(t, filepath.Join(other.Mounts[0].WorktreePath, "meetings", "wip.md"), "wip\n")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "work", "finish", finishing.ID,
		"--publish",
		"--message", "Publish finished session",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("work finish --publish: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}
	var report workFinishCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if report.Finish.Session.Status != worksession.StatusFinished || report.Finish.Session.Outcome != worksession.OutcomeLanded {
		t.Fatalf("session = %#v, want landed (not published) while other session is dirty", report.Finish.Session)
	}
	if report.Sync == nil || len(report.Sync.Results) == 0 {
		t.Fatalf("report.Sync = %#v, want results", report.Sync)
	}
	result := report.Sync.Results[0]
	if result.Status != "held back" || !strings.Contains(result.Message, other.ID) {
		t.Fatalf("sync result = %#v, want hold naming %s", result, other.ID)
	}
}

func TestWorkFinishLandCommitsDirtySessionContent(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--slug", "finish", "--home", home, "--json"}); err != nil {
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
		"my", "work", "finish", session.ID,
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
	if err := a.run([]string{"my", "work", "start", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "draft.md"), "draft\n")

	stdout.Reset()
	stderr.Reset()
	err := a.run([]string{"my", "work", "finish", "--land", "--home", home})
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
	if err := a.run([]string{"my", "work", "start", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "draft.md"), "draft\n")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "work", "finish", session.ID, "--discard", "--home", home, "--json"}); err != nil {
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

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "work", "status", "--all", "--home", home, "--json"}); err != nil {
		t.Fatalf("work status --all: %v\nstderr: %s", err, stderr.String())
	}
	var statuses []worksession.SessionStatus
	if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil {
		t.Fatalf("parse status JSON: %v\nstdout: %s", err, stdout.String())
	}
	if len(statuses) != 1 || statuses[0].Status != worksession.StatusDiscarded {
		t.Fatalf("statuses = %#v, want discarded session", statuses)
	}
	if len(statuses[0].Mounts) != 1 || statuses[0].Mounts[0].Error != "" {
		t.Fatalf("archived mounts = %#v, want registry-only mount without probe error", statuses[0].Mounts)
	}
}

func TestWorkFinishHumanOutputIncludesNextCommand(t *testing.T) {
	home, _ := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--home", home, "--json"}); err != nil {
		t.Fatalf("work start: %v", err)
	}
	var session worksession.Session
	if err := json.Unmarshal(stdout.Bytes(), &session); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "work", "finish", session.ID, "--discard", "--home", home}); err != nil {
		t.Fatalf("work finish --discard: %v\nstderr: %s", err, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "session\t"+session.ID) ||
		!strings.Contains(out, "next\tstatus\tmy work status") {
		t.Fatalf("work finish stdout = %q", out)
	}
}

func TestWorkFinishPublishLandsAndReportsLocalOnlySync(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "work", "start", "--home", home, "--json"}); err != nil {
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
	if err := a.run([]string{"my", "work", "finish", session.ID, "--publish", "--home", home, "--json"}); err != nil {
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
