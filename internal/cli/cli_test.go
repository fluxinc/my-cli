package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/our-ai/internal/fleet"
	"github.com/fluxinc/our-ai/internal/guidance"
	"github.com/fluxinc/our-ai/internal/harness"
	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/meetings"
	"github.com/fluxinc/our-ai/internal/selfupdate"
	"github.com/fluxinc/our-ai/internal/skills"
	"github.com/fluxinc/our-ai/internal/support"
	"github.com/fluxinc/our-ai/internal/syncer"
	"github.com/fluxinc/our-ai/internal/umbrella"
	"github.com/fluxinc/our-ai/internal/worksession"
	"github.com/fluxinc/our-ai/internal/workspace"
)

func TestSkillsInstallParsesInterspersedFlags(t *testing.T) {
	source := makeCLISkill(t, "demo-skill")
	home := t.TempDir()

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"our", "skills", "install", "claude-code",
		"--print", "--source", source, "--home", home,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Fatalf("stdout = %q, want dry-run result", stdout.String())
	}
	if !strings.Contains(stderr.String(), "# source: --source flag -> "+source) {
		t.Fatalf("stderr = %q, want source line", stderr.String())
	}
}

func TestSyncContentPathsIncludesSupport(t *testing.T) {
	tests := []struct {
		name  string
		entry workspace.Entry
		want  []string
	}{
		{
			name:  "handbook default",
			entry: workspace.Entry{Kind: "handbook"},
			want:  []string{"meetings", "support", "decisions", "projects", "policy", "people"},
		},
		{
			name:  "support default",
			entry: workspace.Entry{Kind: "support"},
			want:  []string{"support"},
		},
		{
			name:  "fleet default",
			entry: workspace.Entry{Kind: "fleet"},
			want:  []string{"fleet"},
		},
		{
			name:  "include paths override",
			entry: workspace.Entry{Kind: "support", IncludePaths: []string{"support/resolved"}},
			want:  []string{"support/resolved"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncContentPaths(tt.entry)
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("syncContentPaths() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestInitCreatesManifestRepoAndRegisters(t *testing.T) {
	home := t.TempDir()
	content := filepath.Join(t.TempDir(), "acme-handbook")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "init", "acme",
		"--name", "Acme Example",
		"--path", content,
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatal(err)
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	var result initResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode init JSON: %v\n%s", err, stdout.String())
	}
	if result.OrganizationID != "acme" || result.OrganizationName != "Acme Example" {
		t.Fatalf("organization = %q/%q", result.OrganizationID, result.OrganizationName)
	}
	manifestRepo, err := manifest.DefaultCachePath(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if result.RepoPath != manifestRepo {
		t.Fatalf("manifest repo path = %q, want private %q", result.RepoPath, manifestRepo)
	}
	if result.ContentPath != content {
		t.Fatalf("content repo path = %q, want %q", result.ContentPath, content)
	}
	if result.Manifest.Name != "acme" || result.Manifest.GitURL != manifestRepo {
		t.Fatalf("manifest ref = %#v", result.Manifest)
	}
	if len(result.Sync) != 1 || result.Sync[0].Status != "local-only" {
		t.Fatalf("sync result = %#v, want local-only (no origin until published)", result.Sync)
	}
	for _, want := range []initNextCommand{
		{Action: "setup", Command: "our setup"},
		{Action: "launch", Command: "our ai claude"},
		{Action: "launch", Command: "our ai codex"},
		{Action: "publish", Command: "our publish"},
	} {
		if !initResultHasNext(result, want) {
			t.Fatalf("next commands = %#v, missing %#v", result.NextCommands, want)
		}
	}

	doc, _, err := manifest.LoadDocument(manifestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Organization.ID != "acme" || doc.Organization.Name != "Acme Example" {
		t.Fatalf("document organization = %#v", doc.Organization)
	}
	if doc.Umbrella.RecommendedPath != "~/acme" {
		t.Fatalf("umbrella path = %q", doc.Umbrella.RecommendedPath)
	}
	if len(doc.Mounts) != 1 || doc.Mounts[0].GitURL != content {
		t.Fatalf("mounts = %#v, want git_url pointing at the local content repo %q", doc.Mounts, content)
	}
	if len(doc.Skills) != 1 || doc.Skills[0].ID != "acme:handbook" {
		t.Fatalf("skills = %#v", doc.Skills)
	}
	if validation := manifest.ValidateFile(manifestRepo); len(validation.Errors) != 0 {
		t.Fatalf("generated manifest invalid: %#v", validation.Errors)
	}
	// Control plane stays in the private manifest repo.
	for _, path := range []string{
		filepath.Join(manifestRepo, ".git"),
		filepath.Join(manifestRepo, "README.md"),
		filepath.Join(manifestRepo, "agent-guidance", "acme.md"),
		filepath.Join(manifestRepo, "skills", "acme-handbook", "SKILL.md"),
		filepath.Join(manifestRepo, "catalog", "customers.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	// Content lives only in the visible content repo.
	for _, path := range []string{
		filepath.Join(content, ".git"),
		filepath.Join(content, "README.md"),
		filepath.Join(content, "meetings", "README.md"),
		filepath.Join(content, "support", "README.md"),
		filepath.Join(content, "fleet", "README.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(content, "manifest.json"),
		filepath.Join(content, "catalog"),
		filepath.Join(manifestRepo, "meetings"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("control/data plane mixed: %s exists: %v", path, err)
		}
	}
	if result.Manifest.LocalPath != manifestRepo {
		t.Fatalf("registry LocalPath = %q, want %q", result.Manifest.LocalPath, manifestRepo)
	}
}

func TestInitDefaultsContentRepoToUmbrellaMountPath(t *testing.T) {
	home := t.TempDir()

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var result initResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode init JSON: %v\n%s", err, stdout.String())
	}
	wantContent := filepath.Join(home, "acme", "workspace")
	if result.ContentPath != wantContent {
		t.Fatalf("content path = %q, want umbrella mount path %q", result.ContentPath, wantContent)
	}
	if _, err := os.Stat(filepath.Join(wantContent, ".git")); err != nil {
		t.Fatalf("content checkout missing: %v", err)
	}
	manifestRepo, err := manifest.DefaultCachePath(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.LocalPath != manifestRepo {
		t.Fatalf("registry LocalPath = %q, want private %q", result.Manifest.LocalPath, manifestRepo)
	}
	if _, err := os.Stat(filepath.Join(manifestRepo, ".git")); err != nil {
		t.Fatalf("manifest checkout missing: %v", err)
	}
}

func TestInitRefusesNonEmptyPath(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "acme-workspace")
	writeCLITestFile(t, filepath.Join(repo, "existing.txt"), "already here\n")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "init", "acme", "--path", repo, "--home", home})
	if err == nil || !strings.Contains(err.Error(), "is not empty") {
		t.Fatalf("err = %v, want non-empty path error", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); !os.IsNotExist(err) {
		t.Fatalf(".git exists after failed init: %v", err)
	}
}

func TestInitNextCommandsUseManifestWhenRegistryHasSeveralManifests(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "acme-workspace")
	if _, err := manifest.Add(home, "extra", "https://github.com/example/extra-workspace.git"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--path", repo, "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"next\tsetup\tour setup --manifest acme\n",
		"next\tlaunch\tour ai --manifest acme claude\n",
		"next\tlaunch\tour ai --manifest acme codex\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, missing %q", out, want)
		}
	}
}

func TestInitCommitUsesConfiguredGitIdentity(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "acme-workspace")
	gitConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitConfig, []byte("[user]\n\tname = Config User\n\temail = config@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--path", repo, "--home", home}); err != nil {
		t.Fatal(err)
	}
	author := initCommitAuthor(t, repo)
	if author != "Config User <config@example.com>" {
		t.Fatalf("author = %q, want configured git identity", author)
	}
}

func TestInitCommitFallsBackWithoutGitIdentity(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "acme-workspace")
	gitConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitConfig, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, key := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "EMAIL"} {
		if value, ok := os.LookupEnv(key); ok {
			t.Setenv(key, value) // register restore, then unset below
			os.Unsetenv(key)
		}
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--path", repo, "--home", home}); err != nil {
		t.Fatal(err)
	}
	author := initCommitAuthor(t, repo)
	if author != "Our AI <our-ai@example.invalid>" {
		t.Fatalf("author = %q, want fallback identity", author)
	}
}

func initCommitAuthor(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%an <%ae>").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestInitScaffoldREADMETeachesTeammateFirstRun(t *testing.T) {
	home := t.TempDir()

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	manifestRepo, err := manifest.DefaultCachePath(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(manifestRepo, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	for _, want := range []string{
		"our manifests add acme",
		"our manifests sync acme",
		"our setup",
		"our publish",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("manifest README missing %q:\n%s", want, readme)
		}
	}
}

// fakeGH emulates gh repo create by provisioning a local bare repository and
// wiring it as origin, mirroring what gh does against GitHub.
func fakeGH(t *testing.T, remotesDir string, calls *[]string) manifest.Runner {
	t.Helper()
	return func(name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		if name != "gh" || len(args) < 3 || args[0] != "repo" || args[1] != "create" {
			return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
		}
		repoName := args[2]
		var source string
		for i, arg := range args {
			if arg == "--source" && i+1 < len(args) {
				source = args[i+1]
			}
		}
		if source == "" {
			return nil, fmt.Errorf("missing --source in %v", args)
		}
		bare := filepath.Join(remotesDir, repoName+".git")
		runCLIGit(t, remotesDir, "init", "--bare", "-q", bare)
		runCLIGit(t, source, "remote", "add", "origin", bare)
		runCLIGit(t, source, "push", "-q", "origin", "HEAD:master")
		return []byte("https://example.invalid/" + repoName + "\n"), nil
	}
}

func TestPublishCreatesRemotesRewritesMountsAndRegistry(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	remotes := t.TempDir()
	var calls []string
	a.publishRunner = fakeGH(t, remotes, &calls)

	stdout.Reset()
	if err := a.run([]string{"our", "publish", "--home", home}); err != nil {
		t.Fatalf("publish: %v\nstderr: %s\nstdout: %s", err, stderr.String(), stdout.String())
	}
	if len(calls) != 2 {
		t.Fatalf("gh calls = %#v, want content + manifest repo creation", calls)
	}
	for _, want := range []string{"repo create acme-workspace --private", "repo create acme-manifest --private"} {
		found := false
		for _, call := range calls {
			if strings.Contains(call, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("gh calls = %#v, missing %q", calls, want)
		}
	}
	manifestRepo, err := manifest.DefaultCachePath(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestRepo)
	if err != nil {
		t.Fatal(err)
	}
	contentRemote := filepath.Join(remotes, "acme-workspace.git")
	if len(doc.Mounts) != 1 || doc.Mounts[0].GitURL != contentRemote {
		t.Fatalf("mounts = %#v, want rewritten to %q", doc.Mounts, contentRemote)
	}
	// The rewrite must be committed and pushed, so teammates never see local paths.
	out, err := exec.Command("git", "-C", manifestRepo, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "manifest.json") {
		t.Fatalf("manifest.json left uncommitted after publish: %q", out)
	}
	ref, ok, err := manifest.Find(home, "acme")
	if err != nil || !ok {
		t.Fatalf("Find: %v %v", ok, err)
	}
	if ref.GitURL != filepath.Join(remotes, "acme-manifest.git") {
		t.Fatalf("registry GitURL = %q, want published manifest remote", ref.GitURL)
	}
	if !strings.Contains(stdout.String(), "our manifests add acme") {
		t.Fatalf("stdout = %q, want teammate instructions", stdout.String())
	}

	// Idempotent: a second publish pushes but never recreates remotes.
	calls = nil
	if err := a.run([]string{"our", "publish", "--home", home}); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("second publish invoked gh again: %#v", calls)
	}
}

func TestPublishPrintPlansWithoutChanges(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	var calls []string
	a.publishRunner = fakeGH(t, t.TempDir(), &calls)

	stdout.Reset()
	if err := a.run([]string{"our", "publish", "--home", home, "--print"}); err != nil {
		t.Fatalf("publish --print: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("print mode invoked gh: %#v", calls)
	}
	manifestRepo, err := manifest.DefaultCachePath(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestRepo)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Mounts) != 1 || !filepath.IsAbs(doc.Mounts[0].GitURL) {
		t.Fatalf("print mode rewrote mounts: %#v", doc.Mounts)
	}
	if !strings.Contains(stdout.String(), "would create") {
		t.Fatalf("stdout = %q, want plan output", stdout.String())
	}
}

func TestSyncHoldsManifestWithLocalMountURL(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	manifestRepo, err := manifest.DefaultCachePath(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(t.TempDir(), "acme-manifest.git")
	runCLIGit(t, home, "init", "--bare", "-q", remote)
	runCLIGit(t, manifestRepo, "remote", "add", "origin", remote)
	runCLIGit(t, manifestRepo, "push", "-q", "-u", "origin", "master")
	writeCLITestFile(t, filepath.Join(manifestRepo, "catalog", "customers.json"), "[{\"id\":\"local\"}]\n")
	commitCLIGit(t, manifestRepo, "Edit catalog locally")
	localHead := strings.TrimSpace(gitCLIOutput(t, manifestRepo, "rev-parse", "HEAD"))

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
	}); err != nil {
		t.Fatalf("sync: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"manifest\theld back", "local mount URL", "run our publish --manifest acme"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync stdout = %q, missing %q", out, want)
		}
	}
	remoteHead := strings.TrimSpace(gitCLIOutput(t, manifestRepo, "rev-parse", "origin/master"))
	if remoteHead == localHead {
		t.Fatalf("sync pushed manifest despite local mount URL guard")
	}
}

func initResultHasNext(result initResult, want initNextCommand) bool {
	for _, got := range result.NextCommands {
		if got == want {
			return true
		}
	}
	return false
}

func TestSkillsInstallHelpMentionsGuidance(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "skills", "install", "--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(stderr.String(), "only changes harness skill directories") ||
		!strings.Contains(stderr.String(), "Run our setup") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSkillsInstallConflictingModes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "skills", "install", "--copy", "--link", "--all"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want mutually exclusive", err)
	}
}

func TestSkillsSelfInstallAndStatus(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "skills", "self", "install", "codex", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "codex\tour\tinstalled") {
		t.Fatalf("install stdout = %q", stdout.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "our")); err != nil {
		t.Fatalf("self skill was not installed: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "skills", "self", "status", "codex", "--json", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"harness": "codex"`,
		`"skill": "our"`,
		`"canonical_id": "our:self"`,
		`"status": "installed"`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("status stdout = %q, missing %q", stdout.String(), want)
		}
	}
}

func TestSkillsListJSON(t *testing.T) {
	source := makeCLISkill(t, "demo-skill")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "skills", "list", "--json", "--source", source}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"Name": "demo-skill"`) {
		t.Fatalf("json stdout = %q", stdout.String())
	}
}

func TestSkillsListHumanFormatting(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: >
  Use the Acme handbook for customer commitments, meeting context, policy details, and project history before asking the operator for facts.
---
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "skills", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"acme-handbook\n",
		"  id: acme:handbook\n",
		"  description: Use the Acme handbook",
		"\n               details",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("skills list stdout = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "\t") {
		t.Fatalf("skills list stdout contains tabbed columns: %q", out)
	}
	if strings.Contains(stderr.String(), "# source:") {
		t.Fatalf("skills list stderr contains source noise: %q", stderr.String())
	}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if len(line) > 88 {
			t.Fatalf("skills list line too long (%d): %q", len(line), line)
		}
	}
}

func TestSkillsInstallFromManifestRecordsCanonicalID(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	skillDir := filepath.Join(manifestCache, "skills", "acme-handbook")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: acme-handbook\ndescription: Acme handbook\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()

	if err := a.run([]string{"our", "skills", "install", "claude-code", "--copy", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme:handbook") {
		t.Fatalf("install stdout = %q, want canonical id", stdout.String())
	}
	marker, err := os.ReadFile(filepath.Join(home, ".claude", "skills", "acme-handbook", ".our-managed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(marker), `"canonical_id": "acme:handbook"`) {
		t.Fatalf("marker = %s", marker)
	}
}

func TestSkillsShowFromManifest(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "skills", "show", "acme:handbook", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"acme-handbook\n",
		"  id: acme:handbook\n",
		"  description: Acme handbook",
		"  source: ",
		"skills/acme-handbook",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("skills show stdout = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "acme-calendar") {
		t.Fatalf("skills show stdout included the wrong skill: %q", out)
	}
}

func TestSkillsInstallAndUninstallSkillFilter(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "install", "claude-code",
		"--copy", "--manifest", "acme", "--home", home, "--skill", "acme:calendar",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "acme-calendar")); err != nil {
		t.Fatalf("filtered skill was not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("unselected skill was installed, err=%v", err)
	}
	if strings.Contains(stdout.String(), "acme:handbook") {
		t.Fatalf("install stdout included unselected skill: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "uninstall", "claude-code",
		"--manifest", "acme", "--home", home, "--skill", "acme-calendar",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "acme-calendar")); !os.IsNotExist(err) {
		t.Fatalf("filtered skill was not removed, err=%v", err)
	}
}

func TestSkillsStatusJSON(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "install", "claude-code",
		"--copy", "--manifest", "acme", "--home", home, "--skill", "acme:handbook",
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "status",
		"--json", "--manifest", "acme", "--home", home, "--skill", "acme:handbook",
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"harness": "claude-code"`,
		`"skill": "acme-handbook"`,
		`"canonical_id": "acme:handbook"`,
		`"status": "installed"`,
		`"kind": "copy"`,
		`"harness": "codex"`,
		`"status": "absent"`,
		`"remedy": "our skills install codex --skill acme:handbook --manifest acme --home `,
		`"harness": "gemini"`,
		`"status": "managed-by-gemini"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("skills status json = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "acme-calendar") {
		t.Fatalf("skills status json included unselected skill: %q", out)
	}
}

func TestSkillsStatusReportsStaleCopy(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	if err := a.run([]string{
		"our", "skills", "install", "claude-code",
		"--copy", "--manifest", "acme", "--home", home, "--skill", "acme:handbook",
	}); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(home, ".local", "share", "our", "manifests", "acme", "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Changed handbook
---
`)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "status",
		"--json", "--manifest", "acme", "--home", home, "--skill", "acme:handbook",
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"status": "stale"`,
		`"remedy": "our skills sync claude-code --skill acme:handbook --manifest acme --home `,
		`"message": "copy differs from source"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("skills status json = %q, missing %q", out, want)
		}
	}
}

func TestSkillsSyncPrunesStaleManagedSkill(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLIManagedSkill(t, filepath.Join(home, ".claude", "skills", "old-skill"), "acme:old-skill")
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", "user-skill", "SKILL.md"), "---\nname: user-skill\n---\n")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "sync", "claude-code",
		"--copy", "--manifest", "acme", "--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "old-skill")); !os.IsNotExist(err) {
		t.Fatalf("stale managed skill still exists, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "user-skill")); err != nil {
		t.Fatalf("unmanaged skill was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "acme-handbook")); err != nil {
		t.Fatalf("declared skill was not installed: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "claude-code\told-skill\tremoved") {
		t.Fatalf("sync stdout = %q, want stale removal", out)
	}
	if strings.Contains(out, "user-skill\tremoved") {
		t.Fatalf("sync stdout removed unmanaged skill: %q", out)
	}
}

func TestSkillsSyncKeepsBundledSelfSkill(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)
	if err := a.run([]string{"our", "skills", "self", "install", "claude-code", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "sync", "claude-code",
		"--manifest", "acme", "--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "our")); err != nil {
		t.Fatalf("self-skill was pruned by manifest sync: %v", err)
	}
	if strings.Contains(stdout.String(), "claude-code\tour\tremoved") {
		t.Fatalf("sync stdout removed self-skill: %q", stdout.String())
	}
}

func TestSkillsSyncPrunesManifestSkillNamedOur(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	sourceRoot := makeCLISkill(t, "our")
	found, err := skills.Discover(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("discovered skills = %#v", found)
	}
	found[0].CanonicalID = "acme:our"
	if result := skills.Install(found[0], harness.ClaudeCode, skills.InstallOpts{
		Home:       home,
		SourceRoot: sourceRoot,
		Link:       false,
	}); result.Status != skills.StatusInstalled {
		t.Fatalf("install result = %#v", result)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)
	if err := a.run([]string{
		"our", "skills", "sync", "claude-code",
		"--manifest", "acme", "--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "our")); !os.IsNotExist(err) {
		t.Fatalf("manifest-owned skill named our was not pruned: %v", err)
	}
	if !strings.Contains(stdout.String(), "claude-code\tour\tremoved") {
		t.Fatalf("sync stdout = %q, want named our skill removed", stdout.String())
	}
}

func TestSkillsSyncNoPruneKeepsStaleManagedSkill(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLIManagedSkill(t, filepath.Join(home, ".claude", "skills", "old-skill"), "acme:old-skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "sync", "claude-code",
		"--copy", "--no-prune", "--manifest", "acme", "--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "old-skill")); err != nil {
		t.Fatalf("stale managed skill was pruned despite --no-prune: %v", err)
	}
	if strings.Contains(stdout.String(), "old-skill\tremoved") {
		t.Fatalf("sync stdout pruned despite --no-prune: %q", stdout.String())
	}
}

func TestSkillsPurgeSkillFilterRemovesStaleManagedSkill(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLIManagedSkill(t, filepath.Join(home, ".claude", "skills", "old-skill"), "acme:old-skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "purge", "claude-code",
		"--manifest", "acme", "--home", home, "--skill", "acme:old-skill",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "old-skill")); !os.IsNotExist(err) {
		t.Fatalf("stale managed skill was not purged, err=%v", err)
	}
	if !strings.Contains(stdout.String(), "claude-code\told-skill\tremoved\tacme:old-skill") {
		t.Fatalf("purge stdout = %q, want stale canonical removal", stdout.String())
	}
}

func TestSkillsPurgeKeepsBundledSelfSkill(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)
	if err := a.run([]string{"our", "skills", "self", "install", "claude-code", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "purge", "claude-code",
		"--manifest", "acme", "--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "our")); err != nil {
		t.Fatalf("self-skill was purged by manifest purge: %v", err)
	}
	if strings.Contains(stdout.String(), "claude-code\tour\tremoved") {
		t.Fatalf("purge stdout removed self-skill: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "skills", "purge", "claude-code",
		"--manifest", "acme", "--home", home, "--skill", "our",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "our")); err != nil {
		t.Fatalf("self-skill was purged by explicit manifest purge: %v", err)
	}
	if strings.Contains(stdout.String(), "claude-code\tour\tremoved") {
		t.Fatalf("explicit purge stdout removed self-skill: %q", stdout.String())
	}
}

func TestSkillsInstallSelectionErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "skills", "install", "--all", "codex"}); err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("all+explicit err = %v", err)
	}
	if err := a.run([]string{"our", "skills", "install", "unknown"}); err == nil || !strings.Contains(err.Error(), "unknown harness") {
		t.Fatalf("unknown harness err = %v", err)
	}
	if err := a.run([]string{"our", "skills", "list", "extra"}); err == nil || !strings.Contains(err.Error(), "positional") {
		t.Fatalf("list positional err = %v", err)
	}
}

func TestAdminSkillsAddCopiesAndDeclares(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")
	sourceRoot := makeCLISkill(t, "demo-skill")
	skillDir := filepath.Join(sourceRoot, "demo-skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "added\tacme:demo-skill\tdemo-skill") {
		t.Fatalf("admin add stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(manifestDir, "skills", "demo-skill", "SKILL.md")); err != nil {
		t.Fatalf("skill was not copied into manifest: %v", err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Skills) != 1 || doc.Skills[0].ID != "acme:demo-skill" || doc.Skills[0].Path != "skills/demo-skill" {
		t.Fatalf("manifest skills = %#v", doc.Skills)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("original skill should remain by default: %v", err)
	}
}

func TestAdminSkillsAddHarnessVisibleSourceRequiresChoice(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")
	skillDir := filepath.Join(t.TempDir(), ".claude", "skills", "demo-skill")
	writeCLITestFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: demo-skill\n---\n")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"our", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "--keep-original") {
		t.Fatalf("admin add err = %v, want explicit keep/remove-original choice", err)
	}

	if err := a.run([]string{
		"our", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
		"--remove-original",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("source skill still exists after --remove-original, err=%v", err)
	}
}

func TestAdminSkillsRemoveBlocksThenPrunesRelatedProducts(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, `,
  "skills": [
    { "id": "acme:demo-skill", "install_slug": "demo-skill", "path": "skills/demo-skill" }
  ]`)
	writeCLITestFile(t, filepath.Join(manifestDir, "skills", "demo-skill", "SKILL.md"), "---\nname: demo-skill\n---\n")
	writeCLITestFile(t, filepath.Join(manifestDir, "catalog", "products.json"), `[
  {
    "id": "demo-product",
    "name": "Demo Product",
    "description": "Demo",
    "related_skills": ["acme:demo-skill", "acme:other-skill"]
  }
]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"our", "admin", "skills", "remove", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "related_skills") {
		t.Fatalf("remove err = %v, want related_skills blocker", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "admin", "skills", "remove", "demo-skill",
		"--manifest-dir", manifestDir,
		"--prune-related",
		"--delete-source",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Skills) != 0 {
		t.Fatalf("manifest skills after remove = %#v", doc.Skills)
	}
	if _, err := os.Stat(filepath.Join(manifestDir, "skills", "demo-skill")); !os.IsNotExist(err) {
		t.Fatalf("skill source still exists after --delete-source, err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(manifestDir, "catalog", "products.json"))
	if err != nil {
		t.Fatal(err)
	}
	var products []manifest.Product
	if err := json.Unmarshal(data, &products); err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || strings.Join(products[0].RelatedSkills, ",") != "acme:other-skill" {
		t.Fatalf("products after prune = %#v", products)
	}
}

func TestAdminSkillsRemoveReportsAndPrunesOrphanDependencies(t *testing.T) {
	setup := func(t *testing.T) string {
		t.Helper()
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, `,
  "allowed_external_namespaces": ["spark"],
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" },
    {
      "id": "spark:use-spark",
      "install_slug": "use-spark",
      "source": { "type": "tool", "tool": "spark" },
      "requires": ["tool:spark"]
    }
  ],
  "tools": [
    {
      "id": "qmd",
      "mode": "optional",
      "purpose": "Search local markdown",
      "install": {
        "commands": ["npm install -g @tobilu/qmd"],
        "docs_url": "https://github.com/tobilu/qmd"
      }
    },
    {
      "id": "spark",
      "mode": "optional",
      "purpose": "Install Spark-provided skills",
      "skill_install": {
        "command": "spark",
        "args": ["skills", "install"]
      }
    }
  ]`)
		return manifestDir
	}

	t.Run("reports by default", func(t *testing.T) {
		manifestDir := setup(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"our", "admin", "skills", "remove", "spark:use-spark",
			"--manifest-dir", manifestDir,
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminSkillResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if strings.Join(result.OrphanedTools, ",") != "spark" || strings.Join(result.OrphanedNS, ",") != "spark" || result.SourcePath != "" {
			t.Fatalf("result = %#v", result)
		}
		doc, _, err := manifest.LoadDocument(manifestDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Tools) != 2 || !stringInSlice(doc.AllowedExternalNamespaces, "spark") {
			t.Fatalf("doc after default remove = %#v", doc)
		}
	})

	t.Run("prunes when requested", func(t *testing.T) {
		manifestDir := setup(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"our", "admin", "skills", "remove", "spark:use-spark",
			"--manifest-dir", manifestDir,
			"--prune-orphans",
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminSkillResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if strings.Join(result.PrunedTools, ",") != "spark" || strings.Join(result.PrunedNS, ",") != "spark" {
			t.Fatalf("result = %#v", result)
		}
		data, err := os.ReadFile(filepath.Join(manifestDir, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, unwanted := range []string{`"source":`, `"requires": null`, `"workspaces": null`, `"skill_install":`, `"spark"`} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("manifest contains %q after prune:\n%s", unwanted, text)
			}
		}
		if !strings.Contains(text, `"id": "qmd"`) {
			t.Fatalf("manifest lost remaining tool:\n%s", text)
		}
	})
}

func TestAdminSkillsDirtyCheckoutRequiresForce(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")
	initCLIGitRepo(t, manifestDir)
	writeCLITestFile(t, filepath.Join(manifestDir, "dirty.txt"), "dirty\n")
	sourceRoot := makeCLISkill(t, "demo-skill")
	skillDir := filepath.Join(sourceRoot, "demo-skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"our", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty add err = %v, want dirty checkout refusal", err)
	}

	if err := a.run([]string{
		"our", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
		"--force",
	}); err != nil {
		t.Fatalf("force add failed: %v", err)
	}
}

func TestAdminToolsAddEditRemove(t *testing.T) {
	t.Run("add and edit tool", func(t *testing.T) {
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, "")
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"our", "admin", "tools", "add", "gnit",
			"--manifest-dir", manifestDir,
			"--mode", "required",
			"--purpose", "Multi-repo workspace publishing",
			"--install-command", "curl -fsSL https://raw.githubusercontent.com/mostlydev/gnit/master/install.sh | sh",
			"--docs-url", "https://github.com/mostlydev/gnit",
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminToolResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "added" || result.Tool.ID != "gnit" || result.Tool.Mode != "required" {
			t.Fatalf("result = %#v", result)
		}

		stdout.Reset()
		if err := a.run([]string{
			"our", "admin", "tools", "edit", "gnit",
			"--manifest-dir", manifestDir,
			"--purpose", "Gnit workspace publishing",
			"--clear-install-commands",
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		result = adminToolResult{}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "edited" || result.Tool.Purpose != "Gnit workspace publishing" || len(result.Tool.Install.Commands) != 0 {
			t.Fatalf("edited result = %#v", result)
		}
	})

	t.Run("remove blocks referenced tool", func(t *testing.T) {
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, `,
  "skills": [
    {
      "id": "acme:publisher",
      "install_slug": "publisher",
      "path": "skills/publisher",
      "requires": ["tool:gnit"]
    }
  ],
  "tools": [
    {
      "id": "gnit",
      "mode": "required",
      "purpose": "Multi-repo workspace publishing"
    }
  ]`)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{
			"our", "admin", "tools", "remove", "gnit",
			"--manifest-dir", manifestDir,
		})
		if err == nil || !strings.Contains(err.Error(), "referenced by skills") {
			t.Fatalf("remove err = %v, want referenced blocker", err)
		}
	})

	t.Run("remove unreferenced tool", func(t *testing.T) {
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, `,
  "tools": [
    {
      "id": "gnit",
      "mode": "required",
      "purpose": "Multi-repo workspace publishing"
    }
  ]`)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"our", "admin", "tools", "remove", "gnit",
			"--manifest-dir", manifestDir,
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminToolResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "removed" || result.Tool.ID != "gnit" {
			t.Fatalf("result = %#v", result)
		}
		doc, _, err := manifest.LoadDocument(manifestDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Tools) != 0 {
			t.Fatalf("tools after remove = %#v", doc.Tools)
		}
	})
}

func TestUnimplementedAndUnknownCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "products"}); err == nil || !strings.Contains(err.Error(), "missing products") {
		t.Fatalf("catalog err = %v", err)
	}
	if err := a.run([]string{"our", "tools", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown tools") {
		t.Fatalf("unknown tools err = %v", err)
	}
	if err := a.run([]string{"our", "workspaces", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown workspace") {
		t.Fatalf("unknown workspace err = %v", err)
	}
	if err := a.run([]string{"our", "skills", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown skills") {
		t.Fatalf("unknown skills err = %v", err)
	}
	if err := a.run([]string{"our", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command err = %v", err)
	}
}

func TestManifestCommands(t *testing.T) {
	home := t.TempDir()
	manifestDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme") {
		t.Fatalf("add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "manifests", "list", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme-ai-manifest") {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "manifests", "sync", "acme", "--print", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "git clone") {
		t.Fatalf("sync stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "manifests", "validate", manifestDir}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Fatalf("validate stdout = %q", stdout.String())
	}
}

func TestWorkspaceCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	if err := os.MkdirAll(manifestCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "workspaces", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook") {
		t.Fatalf("workspace list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "workspaces", "sync", "handbook", "--manifest", "acme", "--print", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "git clone") {
		t.Fatalf("workspace sync stdout = %q", stdout.String())
	}
}

func TestMountCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	if err := os.MkdirAll(manifestCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required"
    },
    {
      "id": "leadership",
      "kind": "meetings",
      "git_url": "https://github.com/acme/leadership.git",
      "mode": "optional"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "mounts", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook\thandbook\trequired") {
		t.Fatalf("mount list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "mounts", "add", "meetings:leadership", "--manifest", "acme", "--home", home, "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "leadership\tdry-run") {
		t.Fatalf("mount add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "mounts", "sync", "handbook", "--manifest", "acme", "--home", home, "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook\tdry-run") {
		t.Fatalf("mount sync stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "mounts", "remove", "handbook", "--umbrella", filepath.Join(home, "acme"), "--home", home, "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook\tdry-run") {
		t.Fatalf("mount remove stdout = %q", stdout.String())
	}
}

func TestSyncExplicitGnitBackendReportsMissingWorkspace(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required"
    }
  ]
}`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"our", "sync", "--backend", "gnit", "--manifest", "acme", "--home", home, "--print", "--json"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"backend": "gnit"`,
		"Gnit workspace not initialized",
		`"status": "held back"`,
		`"id": "handbook"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync stdout = %q, missing %q", out, want)
		}
	}
}

func TestMountAddProductRecordsState(t *testing.T) {
	home := t.TempDir()
	productSource := filepath.Join(home, "product-source")
	writeCLITestFile(t, filepath.Join(productSource, "README.md"), "product repo\n")
	initCLIGitRepo(t, productSource)

	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "repos.json"), `[
  {
    "id": "sample-service",
    "git_url": "`+productSource+`",
    "description": "Sample service source"
  }
]`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "description": "Sample service",
    "repos": ["sample-service"]
  }
]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"our", "products", "list", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"id": "sample-product"`) || !strings.Contains(stdout.String(), `"sample-service"`) {
		t.Fatalf("catalog list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	umbrellaRoot := filepath.Join(home, "acme")
	if err := a.run([]string{"our", "mounts", "add", "repo:sample-service", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "acme", "repos", "sample-service", ".git")); err != nil {
		t.Fatalf("repo was not cloned: %v", err)
	}
	state, err := os.ReadFile(filepath.Join(home, "acme", ".our", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"sample-service"`, `"repo:sample-service"`, `"kind": "repo"`} {
		if !strings.Contains(string(state), want) {
			t.Fatalf("state = %s, missing %q", state, want)
		}
	}
	stdout.Reset()
	if err := a.run([]string{"our", "mounts", "list", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"id": "repo:sample-service"`) || !strings.Contains(stdout.String(), `"kind": "repo"`) {
		t.Fatalf("mount list json stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "mounts", "sync", "repo:sample-service", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	err = a.run([]string{"our", "mounts", "add", "product:sample-product", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot})
	if err == nil || !strings.Contains(err.Error(), "business catalog entries") {
		t.Fatalf("err = %v, want product mount removal error", err)
	}
	stdout.Reset()
	if err := a.run([]string{"our", "repos", "remove", "sample-service", "--force", "--umbrella", umbrellaRoot, "--home", home}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "acme", "repos", "sample-service")); !os.IsNotExist(err) {
		t.Fatalf("repo dir still exists or stat failed unexpectedly: %v", err)
	}
	state, err = os.ReadFile(filepath.Join(home, "acme", ".our", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "sample-service") {
		t.Fatalf("state still references removed repo: %s", state)
	}
}

func TestCatalogListHumanFormatting(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "repos.json"), `[
  { "id": "sample-service", "git_url": "https://github.com/acme/sample-service.git" }
]`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "description": "Sample service",
    "purpose": "Synthetic source used by tests.",
    "repos": ["sample-service"],
    "related_skills": ["acme:handbook"]
  }
]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"our", "products", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"sample-product - Sample Product\n",
		"  repos: sample-service\n",
		"  purpose: Synthetic source used by tests.\n",
		"  skills: acme:handbook\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("catalog list stdout = %q, missing %q", out, want)
		}
	}
}

func TestMountAddProductUnknownJSON(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	err := a.run([]string{"our", "repos", "add", "missing", "--manifest", "acme", "--home", home, "--umbrella", filepath.Join(home, "acme"), "--json"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stdout.String(), `"error": "unknown_repo"`) || !strings.Contains(stdout.String(), "our repos list") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestMeetingJSONErrorWithoutUmbrella(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "meetings", "search", "SampleCo", "--home", home, "--json"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stdout.String(), `"error": "no_umbrella"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestOnboardJSONAndDoctorUmbrella(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	if err := os.MkdirAll(filepath.Join(manifestCache, "skills", "acme-handbook"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "skills", "acme-handbook", "SKILL.md"), []byte("---\nname: acme-handbook\ndescription: Acme handbook\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "setup", "claude-code", "--copy", "--json", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"umbrella"`, `"skills"`, `"acme-handbook"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("onboard stdout = %q, missing %q", stdout.String(), want)
		}
	}

	stdout.Reset()
	if err := a.run([]string{"our", "doctor", "--umbrella", filepath.Join(home, "acme"), "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "umbrella\tacme\tok") {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "umbrella\tguidance\tok") {
		t.Fatalf("doctor stdout = %q, want guidance ok", stdout.String())
	}
}

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

func TestDoctorReportsGuidanceDrift(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(umbrellaRoot, "AGENTS.md"), "<!-- our:generated workspace-guidance v1 -->\n\nstale\n")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "doctor", "--umbrella", umbrellaRoot, "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "umbrella\tguidance\tstale") ||
		!strings.Contains(stdout.String(), "run our setup") {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}
}

func TestDoctorReportsFreshnessNoFetch(t *testing.T) {
	home, _, _, _ := setupCLITrackedManifest(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home, "--no-fetch"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "freshness\tmanifest:acme\tok") ||
		!strings.Contains(out, "up to date (as of last fetch)") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestDoctorReportsLocalMountURLRequiresPublish(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home, "--no-fetch"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"manifest\tacme:mount:workspace\tlocal-only", "mount git_url is local-only", "run our publish --manifest acme"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor stdout = %q, missing %q", out, want)
		}
	}
	if strings.Contains(out, "manifest\tacme\tlocal-only") {
		t.Fatalf("doctor local-mount report should be a mount item, got %q", out)
	}
}

func TestDoctorReportsRemoteFreshnessUnknown(t *testing.T) {
	home, _, manifestCache, _ := setupCLITrackedManifest(t)
	runCLIGit(t, manifestCache, "remote", "set-url", "origin", filepath.Join(home, "missing.git"))
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "freshness\tmanifest:acme\tunknown") ||
		!strings.Contains(out, "behind=unknown (remote unreachable)") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestDoctorSkipsSkillDriftForMissingHarnessDirs(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "derived\tskill:claude-code:acme-handbook\tabsent") {
		t.Fatalf("doctor stdout = %q, want missing harness dir skipped", out)
	}
	if !strings.Contains(out, "derived\tskills\tok") ||
		!strings.Contains(out, "no present harness skill drift detected") {
		t.Fatalf("doctor stdout = %q, want no present harness drift", out)
	}
}

func TestDoctorReportsDerivedSkillDriftForPresentHarness(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", ".keep"), "")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "derived\tskill:claude-code:acme-handbook\tabsent") ||
		!strings.Contains(out, "our skills install claude-code --skill acme:handbook") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestDoctorReportsAbsentSelfSkillForPresentHarness(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", ".keep"), "")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "derived\tselfskill:claude-code\tabsent") ||
		!strings.Contains(out, "our skills self install") {
		t.Fatalf("doctor stdout = %q, want absent self-skill report", out)
	}
}

func TestDoctorSkipsSelfSkillForMissingHarnessDirs(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "selfskill:claude-code") {
		t.Fatalf("doctor stdout = %q, want missing harness dirs skipped", out)
	}
	if !strings.Contains(out, "derived\tselfskill\tok") {
		t.Fatalf("doctor stdout = %q, want self-skill ok line", out)
	}
}

func TestDoctorFixReinstallsAbsentSelfSkill(t *testing.T) {
	home, _, _, _ := setupCLITrackedManifest(t)
	writeCLITestFile(t, filepath.Join(home, ".claude", "skills", ".keep"), "")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"our", "doctor", "--fix", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tselfskill:claude-code\tfixed") {
		t.Fatalf("doctor stdout = %q, want self-skill fix", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "our", "SKILL.md")); err != nil {
		t.Fatalf("self-skill not reinstalled: %v", err)
	}
}

func TestSyncEmitsSeparateManifestAndContentEntries(t *testing.T) {
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "https://github.com/acme/acme-handbook.git", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "sync", "--print", "--json",
		"--backend", "builtin",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	var report syncCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode sync JSON: %v\n%s", err, stdout.String())
	}
	var roles []string
	for _, result := range report.Results {
		roles = append(roles, result.Role+":"+result.ID)
	}
	if strings.Join(roles, ",") != "manifest:acme,content:handbook" {
		t.Fatalf("results = %#v, want separate manifest and content entries", report.Results)
	}
}

func TestChangedManifestForDerivedUsesManifestRole(t *testing.T) {
	report := syncer.Report{Results: []syncer.Result{
		{Manifest: "acme", ID: "acme", Role: "manifest", Status: "pulled"},
	}}
	name, ok := changedManifestForDerived(report)
	if !ok || name != "acme" {
		t.Fatalf("changedManifestForDerived = %q, %v; want acme, true", name, ok)
	}
}

func TestMeetingsAddMarksCreatedRecordIntentToAdd(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"our", "meetings", "add", "sampleco-followup",
		"--manifest", "acme",
		"--workspace", "handbook",
		"--home", home,
		"--date", "2026-06-12",
	}); err != nil {
		t.Fatal(err)
	}

	status := strings.TrimRight(gitCLIOutput(t, workspaceRoot, "status", "--porcelain", "--", "meetings/2026-06-12-sampleco-followup.md"), "\n")
	if !strings.HasPrefix(status, " A ") {
		t.Fatalf("git status = %q, want intent-to-add status", status)
	}
}

func TestRecordAdoptMarksContentFileIntentToAdd(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	path := filepath.Join(workspaceRoot, "meetings", "manual-note.md")
	writeCLITestFile(t, path, "manual\n")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"our", "record", "adopt", path,
		"--manifest", "acme",
		"--workspace", "handbook",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	status := strings.TrimRight(gitCLIOutput(t, workspaceRoot, "status", "--porcelain", "--", "meetings/manual-note.md"), "\n")
	if !strings.HasPrefix(status, " A ") {
		t.Fatalf("git status = %q, want intent-to-add status", status)
	}
	if !strings.Contains(stdout.String(), path) {
		t.Fatalf("stdout = %q, want adopted path", stdout.String())
	}
}

func TestMeetingsAddWorksInLocalOnlyWorkspace(t *testing.T) {
	// A founder's freshly initialized org is local-only (no origin remotes);
	// recording must work before anything is published.
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if err := a.run([]string{"our", "setup", "--home", home, "claude"}); err != nil {
		t.Fatalf("setup: %v\nstderr: %s", err, stderr.String())
	}
	stdout.Reset()
	if err := a.run([]string{
		"our", "meetings", "add", "kickoff",
		"--home", home,
		"--date", "2026-06-12",
	}); err != nil {
		t.Fatalf("meetings add in local-only workspace: %v", err)
	}
	record := filepath.Join(home, "acme", "workspace", "meetings", "2026-06-12-kickoff.md")
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("record missing: %v", err)
	}
}

func TestSyncPersistsLastSyncAuditAndDoctorReportsIt(t *testing.T) {
	home, umbrellaRoot, _, _ := setupCLITrackedManifest(t)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"our", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(umbrellaRoot, ".our", "last-sync.json")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var audit lastSyncAudit
	if err := json.Unmarshal(data, &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Report.Publish != "never" || len(audit.Report.Results) != 1 || audit.Report.Results[0].Head == "" {
		t.Fatalf("audit = %#v, want publish/report/head", audit)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "last-sync\tlast publish\tok") ||
		!strings.Contains(out, "publish=never") ||
		!strings.Contains(out, "already_landed=1") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestDoctorWithoutFixReportsWouldFixPlan(t *testing.T) {
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
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "would fast-forward") {
		t.Fatalf("doctor stdout = %q, want would-fast-forward plan", out)
	}
	if !strings.Contains(out, "our doctor --fix") {
		t.Fatalf("doctor stdout = %q, want doctor --fix hint", out)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "meetings", "2026-06-09-remote.md")); !os.IsNotExist(err) {
		t.Fatalf("doctor without --fix mutated the mount: %v", err)
	}
}

func TestDoctorFixFastForwardsCleanStaleMount(t *testing.T) {
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
	if err := a.run([]string{"our", "doctor", "--fix", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tacme:content:handbook\tfixed") ||
		!strings.Contains(out, "pulled --ff-only") {
		t.Fatalf("doctor stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(mountPath, "meetings", "2026-06-09-remote.md")); err != nil {
		t.Fatalf("mount did not fast-forward: %v", err)
	}
}

func TestDoctorFixSkipsDirtyAndUnknownMounts(t *testing.T) {
	dirtyRemote, dirtyClone, dirtyWriter := setupCLIRemoteRepo(t, t.TempDir(), "dirty", map[string]string{"README.md": "seed\n"})
	unknownRemote, unknownClone, _ := setupCLIRemoteRepo(t, t.TempDir(), "unknown", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "dirty", "kind": "handbook", "git_url": "`+dirtyRemote+`", "mode": "required" },
    { "id": "unknown", "kind": "handbook", "git_url": "`+unknownRemote+`", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	dirtyPath := filepath.Join(umbrellaRoot, "dirty")
	unknownPath := filepath.Join(umbrellaRoot, "unknown")
	if err := os.Rename(dirtyClone, dirtyPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(unknownClone, unknownPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(dirtyWriter, "meetings", "remote.md"), "remote\n")
	commitAndPushCLIGit(t, dirtyWriter, "remote meeting")
	writeCLITestFile(t, filepath.Join(dirtyPath, "local.md"), "dirty\n")
	runCLIGit(t, unknownPath, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "doctor", "--fix", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tacme:content:dirty\tskipped") ||
		!strings.Contains(out, "dirty checkout") ||
		!strings.Contains(out, "fix\tacme:content:unknown\tskipped") ||
		!strings.Contains(out, "remote freshness unknown") {
		t.Fatalf("doctor stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dirtyPath, "meetings", "remote.md")); !os.IsNotExist(err) {
		t.Fatalf("dirty mount was pulled despite skip: %v", err)
	}
}

func TestDoctorFixSkipsStaleProduct(t *testing.T) {
	remote, clone, writer := setupCLIRemoteRepo(t, t.TempDir(), "product", map[string]string{"README.md": "seed\n"})
	home, umbrellaRoot, manifestCache, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "repos.json"), `[
  { "id": "sample-product", "git_url": "`+remote+`" }
]`)
	_, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	state = umbrella.AddSelectedRepo(state, "sample-product")
	if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
	productPath := filepath.Join(umbrellaRoot, "products", "sample-product")
	if err := os.MkdirAll(filepath.Dir(productPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, productPath); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "remote.md"), "remote\n")
	commitAndPushCLIGit(t, writer, "remote product")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "doctor", "--fix", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tacme:repo:repo:sample-product\tskipped") ||
		!strings.Contains(out, "repo checkouts are never fixed by doctor") {
		t.Fatalf("doctor stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(productPath, "remote.md")); !os.IsNotExist(err) {
		t.Fatalf("product was pulled despite skip: %v", err)
	}
}

func TestDoctorFixReconcilesDerivedArtifacts(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	umbrellaRoot := filepath.Join(home, "acme")
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(umbrellaRoot, "AGENTS.md"), "<!-- our:generated workspace-guidance v1 -->\n\nstale\n")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	if err := a.run([]string{"our", "doctor", "--fix", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "fix\tguidance\tfixed") ||
		!strings.Contains(out, "fix\tskill:claude-code:acme-handbook\tfixed") {
		t.Fatalf("doctor stdout = %q", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); err != nil {
		t.Fatalf("skill was not installed: %v", err)
	}
}

func TestSyncReconcilesDerivedAfterManifestPull(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(writer, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	commitAndPushCLIGit(t, writer, "add handbook skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "acme\tacme\tmanifest\tpulled") ||
		!strings.Contains(out, "derived-skill\tclaude-code\tacme-handbook\tinstalled") {
		t.Fatalf("sync stdout = %q", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); err != nil {
		t.Fatalf("skill was not installed: %v", err)
	}
}

func TestSyncNoDerivedSkipsDerivedReconcile(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(writer, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	commitAndPushCLIGit(t, writer, "add handbook skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--scope", "manifest",
		"--no-derived",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "derived-skill") {
		t.Fatalf("sync stdout = %q, want derived reconcile skipped", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("skill installed despite --no-derived: %v", err)
	}
}

func TestSyncContentScopeDoesNotReconcileDerived(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(writer, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	commitAndPushCLIGit(t, writer, "add handbook skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--scope", "content",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "derived-skill") {
		t.Fatalf("sync stdout = %q, want content scope to skip derived reconcile", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("skill installed despite content scope: %v", err)
	}
}

func TestManifestSyncReconcilesDerivedAfterPull(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "skills", "self", "install", "claude-code", "--home", home}); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/fresh.md"] },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(writer, "guidance", "fresh.md"), "fresh guidance from manifest\n")
	writeCLITestFile(t, filepath.Join(writer, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	commitAndPushCLIGit(t, writer, "add guidance and handbook skill")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "manifests", "sync", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "acme\tsynced\t") ||
		!strings.Contains(out, "derived\tguidance\t") ||
		!strings.Contains(out, "derived-skill\tclaude-code\tacme-handbook\tinstalled") {
		t.Fatalf("manifest sync stdout = %q", out)
	}
	data, err := os.ReadFile(filepath.Join(umbrellaRoot, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fresh guidance from manifest") {
		t.Fatalf("AGENTS.md was not regenerated from synced manifest:\n%s", data)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); err != nil {
		t.Fatalf("skill was not installed after manifest sync: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "our")); err != nil {
		t.Fatalf("self-skill was pruned by manifest sync derived reconcile: %v", err)
	}
}

func TestManifestSyncNoDerivedSkipsDerivedReconcile(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/fresh.md"] },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(writer, "guidance", "fresh.md"), "fresh guidance from manifest\n")
	writeCLITestFile(t, filepath.Join(writer, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	commitAndPushCLIGit(t, writer, "add guidance and handbook skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "sync", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--no-derived",
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "derived") {
		t.Fatalf("manifest sync stdout = %q, want no derived output", out)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md was regenerated despite --no-derived: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("skill installed despite --no-derived: %v", err)
	}
}

func TestManifestSyncPrintSkipsDerivedReconcile(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/fresh.md"] }
}`)
	writeCLITestFile(t, filepath.Join(writer, "guidance", "fresh.md"), "fresh guidance from manifest\n")
	commitAndPushCLIGit(t, writer, "add guidance")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "sync", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--print",
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "derived") {
		t.Fatalf("manifest sync --print stdout = %q, want no derived output", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md was regenerated despite --print: %v", err)
	}
}

func TestManifestSyncChangedManifestWithoutUmbrellaPrintsRemediation(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/fresh.md"] }
}`)
	writeCLITestFile(t, filepath.Join(writer, "guidance", "fresh.md"), "fresh guidance from manifest\n")
	commitAndPushCLIGit(t, writer, "add guidance")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "sync", "acme",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "derived\tmanifest:acme\tskipped") ||
		!strings.Contains(out, "no existing umbrella found") ||
		!strings.Contains(out, "run our setup --manifest acme --umbrella "+umbrellaRoot) {
		t.Fatalf("manifest sync stdout = %q", out)
	}
}

func TestManifestSyncWrongUmbrellaSkipsDerivedWithNotice(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "other", "other"); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/fresh.md"] }
}`)
	writeCLITestFile(t, filepath.Join(writer, "guidance", "fresh.md"), "fresh guidance from manifest\n")
	commitAndPushCLIGit(t, writer, "add guidance")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "sync", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "derived\tmanifest:acme\tskipped") ||
		!strings.Contains(out, "uses manifest \"other\", not \"acme\"") ||
		!strings.Contains(out, "pass --umbrella for the acme umbrella") {
		t.Fatalf("manifest sync stdout = %q", out)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md was regenerated for wrong umbrella: %v", err)
	}
}

func TestManifestSyncJSONIncludesDerivedOnChangedManifest(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/fresh.md"] }
}`)
	writeCLITestFile(t, filepath.Join(writer, "guidance", "fresh.md"), "fresh guidance from manifest\n")
	commitAndPushCLIGit(t, writer, "add guidance")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "sync", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--json",
	}); err != nil {
		t.Fatal(err)
	}
	var rows []manifestSyncCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("parse manifest sync JSON: %v\n%s", err, stdout.String())
	}
	if len(rows) != 1 || rows[0].Name != "acme" || !rows[0].Changed || rows[0].Derived == nil {
		t.Fatalf("manifest sync JSON rows = %#v", rows)
	}
	if rows[0].Derived.Guidance.Status == "" {
		t.Fatalf("manifest sync JSON missing derived guidance: %#v", rows[0].Derived)
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

func TestOnboardAutoRefreshesManifestBeforeGuidance(t *testing.T) {
	run := func(t *testing.T, noRefresh bool, wantFresh bool) {
		t.Helper()
		home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
		writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/fresh.md"] }
}`)
		writeCLITestFile(t, filepath.Join(writer, "guidance", "fresh.md"), "fresh guidance from manifest\n")
		commitAndPushCLIGit(t, writer, "add guidance")

		args := []string{"our", "setup", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}
		if noRefresh {
			args = append(args, "--no-refresh")
		}
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run(args); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(umbrellaRoot, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if gotFresh := strings.Contains(string(data), "fresh guidance from manifest"); gotFresh != wantFresh {
			t.Fatalf("AGENTS.md fresh=%v, want %v\n%s", gotFresh, wantFresh, data)
		}
	}

	t.Run("refreshes", func(t *testing.T) {
		run(t, false, true)
	})
	t.Run("opt out", func(t *testing.T) {
		run(t, true, false)
	})
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

func TestSyncScopeReposIsAcceptedAsProductsAlias(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "sync", "--backend", "builtin", "--scope", "repos", "--print", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatalf("sync --scope repos failed: %v", err)
	}
}

func TestSyncUsesManifestPublishPolicyAndCLIOverride(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "sync": { "publish_policy": "never" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "sync", "--backend", "builtin", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "never"`) {
		t.Fatalf("sync stdout = %q, want manifest publish policy", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "sync", "--backend", "builtin", "--publish", "direct", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "direct"`) {
		t.Fatalf("sync stdout = %q, want CLI override", stdout.String())
	}
}

func TestSyncUsesManifestPRPublishPolicy(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "sync": { "publish_policy": "pr" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "sync", "--backend", "builtin", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "pr"`) {
		t.Fatalf("sync stdout = %q, want manifest PR policy", stdout.String())
	}
}

func TestOnboardArgsForLaunchCarriesNoRefresh(t *testing.T) {
	args := onboardArgsForLaunch("/home/example", "acme", "/home/example/acme", true, false)
	if !strings.Contains(strings.Join(args, " "), "--no-refresh") {
		t.Fatalf("onboard args = %#v, want --no-refresh", args)
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
		"--no-session",
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

func TestLaunchPrintStartsSessionByDefault(t *testing.T) {
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

func TestLaunchDefaultsToNewSession(t *testing.T) {
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

func TestLaunchRepoRequiresNoSession(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "ai", "--manifest", "acme", "--home", home, "--repo", "sample-service", "--print", "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "default session mode does not include repo worktrees") ||
		!strings.Contains(stderr.String(), "pass --no-session") {
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

func TestToolsInfoAndDoctorCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	if err := os.MkdirAll(manifestCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ],
  "tools": [
    {
      "id": "qmd",
      "mode": "optional",
      "purpose": "search ranking helper",
      "install": {
        "commands": ["npm install -g @tobilu/qmd"],
        "docs_url": "https://github.com/tobilu/qmd"
      }
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "tools", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme\tqmd\toptional\tsearch ranking helper") {
		t.Fatalf("tools list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "tools", "info", "qmd", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "npm install -g @tobilu/qmd") {
		t.Fatalf("tools info stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"manifest\tacme\tok", "workspace\tacme:handbook", "tool\tacme:qmd"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor stdout = %q, missing %q", out, want)
		}
	}
}

func TestMeetingsCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".our", "workspaces", "handbook")
	if err := os.MkdirAll(filepath.Join(manifestCache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "meetings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceRoot, "meetings", "2026-03-12-sampleco-implementation.md"), []byte(`---
id: 2026-03-12-sampleco-implementation
date: 2026-03-12
title: "SampleCo implementation"
customer: sampleco
product: sample-product
status: finalized
---

Promised onboarding review and data cleanup.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "list", "--manifest", "acme", "--home", home, "--customer", "sampleco"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}
	if fields := strings.Split(strings.TrimSpace(stdout.String()), "\t"); len(fields) != 8 {
		t.Fatalf("meetings list fields = %#v, want 8 fixed columns", fields)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "search", "data cleanup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Promised onboarding review") {
		t.Fatalf("meetings search stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "get", "2026-03-12-sampleco-implementation", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "data cleanup") {
		t.Fatalf("meetings get stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "add", "sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-05-13", "--customer", "sampleco", "--attendees", "Alex Example", "--partner", "integratorco", "--source-id", "spark-123", "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-05-13-sampleco-followup") || !strings.Contains(stdout.String(), "## Promises") || !strings.Contains(stdout.String(), `source_id: spark-123`) {
		t.Fatalf("meetings add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "add", "2026-05-28-sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--attendees", "Heather (PMH, mammo tech)", "--partner", "Siemens, Healthineers", "--print"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"2026-05-28-sampleco-followup",
		`date: 2026-05-28`,
		`  - "Heather (PMH, mammo tech)"`,
		`  - "Siemens, Healthineers"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("meetings add stdout = %q, missing %q", out, want)
		}
	}
}

func TestMeetingsUseConfiguredUmbrella(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
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
  ]
}`)
	root := filepath.Join(home, "acme")
	if _, state, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
		t.Fatal(err)
	} else {
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        "handbook",
			Kind:      "handbook",
			SourceRef: "manifest:acme:handbook",
			Status:    "synced",
		})
		if err := umbrella.SaveState(root, state); err != nil {
			t.Fatal(err)
		}
	}
	writeCLITestFile(t, filepath.Join(root, "handbook", "meetings", "2026-03-12-sampleco-implementation.md"), `---
id: 2026-03-12-sampleco-implementation
date: 2026-03-12
title: "SampleCo implementation"
customer: sampleco.example.com
---

Data cleanup follow-up.
`)
	writeCLITestFile(t, filepath.Join(root, "handbook", "customers", "registry.md"), `# Customer Registry

## Registry - confirmed FQDN

| Canonical ID | Name | Partner(s) | Notes |
|---|---|---|---|
| `+"`sampleco.example.com`"+` | SampleCo | IntegratorCo | Merged `+"`sampleco`"+`. |
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"our", "customers", "list", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "sampleco.example.com") || !strings.Contains(stdout.String(), "sampleco") {
		t.Fatalf("customers list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "list", "--home", home, "--customer", "sampleco"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "search", "sampleco cleanup", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings search stdout = %q", stdout.String())
	}
}

func TestMeetingsSearchUsesQMDOrderWhenAvailable(t *testing.T) {
	root := meetings.Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCLITestFile(t, filepath.Join(root.Path, "meetings", "2026-01-01-alpha.md"), `---
id: 2026-01-01-alpha
date: 2026-01-01
title: Alpha
---

Data cleanup.
`)
	writeCLITestFile(t, filepath.Join(root.Path, "meetings", "2026-02-01-beta.md"), `---
id: 2026-02-01-beta
date: 2026-02-01
title: Beta
---

Data cleanup.
`)
	old := qmdMeetingSearch
	qmdMeetingSearch = func([]meetings.Root, string, meetings.Filter) ([]meetings.Meeting, bool) {
		return []meetings.Meeting{{
			Manifest:  "acme",
			Workspace: "handbook",
			ID:        "2026-01-01-alpha",
			Path:      filepath.Join(root.Path, "meetings", "2026-01-01-alpha.md"),
			Date:      "2026-01-01",
			Title:     "Alpha",
			Snippet:   "qmd snippet",
		}}, true
	}
	defer func() { qmdMeetingSearch = old }()

	found, err := defaultMeetingSearch([]meetings.Root{root}, "data cleanup", meetings.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "2026-01-01-alpha" || found[0].Snippet != "qmd snippet" {
		t.Fatalf("found = %#v", found)
	}
}

func TestSupportCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".our", "workspaces", "handbook")
	if err := os.MkdirAll(filepath.Join(manifestCache), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspaceRoot, "support"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "support", "2026-06-10-routing-timeout.md"), `---
id: 2026-06-10-routing-timeout
date: 2026-06-10
title: "Routing timeout"
customer: sampleco
identifiers: [ws-12, so-100045]
claimed_by: alex
observed_by: [bo]
approved_by: casey
product: sample-product
area: routing
status: resolved
tags: [timeout, delivery]
feature_candidate: true
source: support
---

The delivery failed with a clear timeout.
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "support", "list", "--manifest", "acme", "--home", home, "--customer", "sampleco", "--identifier", "ws-12", "--claimed-by", "alex", "--area", "routing", "--tag", "timeout", "--feature-candidate"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-06-10-routing-timeout") ||
		!strings.Contains(stdout.String(), "ws-12,so-100045") ||
		!strings.Contains(stdout.String(), "alex") {
		t.Fatalf("support list stdout = %q", stdout.String())
	}
	if fields := strings.Split(strings.TrimSpace(stdout.String()), "\t"); len(fields) != 13 {
		t.Fatalf("support list fields = %#v, want 13 fixed columns", fields)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "support", "search", "clear timeout", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "clear timeout") {
		t.Fatalf("support search stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "support", "get", "2026-06-10-routing-timeout", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "feature_candidate: true") {
		t.Fatalf("support get stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "support", "add", "sampleco-timeout", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-06-11", "--customer", "sampleco", "--identifier", "ws-12", "--identifier", "fl-400-123401", "--claimed-by", "alex", "--observed-by", "bo", "--product", "sample-product", "--area", "routing", "--tag", "timeout", "--feature-candidate", "--print"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"2026-06-11-sampleco-timeout", "## Diagnosis", "feature_candidate: true", `  - "timeout"`, `  - "fl-400-123401"`, "claimed_by: alex", `  - "bo"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("support add stdout = %q, missing %q", out, want)
		}
	}
}

func TestSupportSearchUsesQMDOrderWhenAvailable(t *testing.T) {
	root := support.Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCLITestFile(t, filepath.Join(root.Path, "support", "2026-01-01-alpha.md"), `---
id: 2026-01-01-alpha
date: 2026-01-01
title: Alpha
---

Timeout delivery.
`)
	writeCLITestFile(t, filepath.Join(root.Path, "support", "2026-02-01-beta.md"), `---
id: 2026-02-01-beta
date: 2026-02-01
title: Beta
---

Timeout delivery.
`)
	old := qmdSupportSearch
	qmdSupportSearch = func([]support.Root, string, support.Filter) ([]support.Record, bool) {
		return []support.Record{{
			Manifest:  "acme",
			Workspace: "handbook",
			ID:        "2026-01-01-alpha",
			Path:      filepath.Join(root.Path, "support", "2026-01-01-alpha.md"),
			Date:      "2026-01-01",
			Title:     "Alpha",
			Snippet:   "qmd snippet",
		}}, true
	}
	defer func() { qmdSupportSearch = old }()

	found, err := defaultSupportSearch([]support.Root{root}, "timeout delivery", support.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "2026-01-01-alpha" || found[0].Snippet != "qmd snippet" {
		t.Fatalf("found = %#v", found)
	}
}

func TestFleetCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".our", "workspaces", "handbook")
	if err := os.MkdirAll(manifestCache, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "fleet", "acme-box-1.md"), `---
id: acme-box-1
customer: sampleco.example.com
partner: integratorco
status: live
device: Sample Scanner X
serial: SN-0001
identifiers:
  - "SO 100045"
  - "FL 400-123401"
config_repo: acme/sample-configs
config_branch: partner/site-1
deployed_site: Springfield
ship_to: Centerville
assigned: alex
source: fleet
---

# acme-box-1

Routing hub for the sample site.
`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "support", "2026-06-10-routing-timeout.md"), `---
id: 2026-06-10-routing-timeout
date: 2026-06-10
title: "Routing timeout"
identifiers: [FL 400-123401]
status: resolved
source: support
---

The delivery failed with a clear timeout.
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "list", "--manifest", "acme", "--home", home, "--status", "live", "--customer", "sampleco.example.com", "--partner", "integratorco", "--identifier", "SO 100045", "--branch", "partner/site-1", "--where", "assigned=alex"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme-box-1") ||
		!strings.Contains(stdout.String(), "SO 100045,FL 400-123401") {
		t.Fatalf("fleet list stdout = %q", stdout.String())
	}
	if fields := strings.Split(strings.TrimSpace(stdout.String()), "\t"); len(fields) != 10 {
		t.Fatalf("fleet list fields = %#v, want 10 fixed columns", fields)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "list", "--manifest", "acme", "--home", home, "--where", "assigned=bo"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("fleet list with unmatched where = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "get", "SO 100045", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Routing hub for the sample site.") {
		t.Fatalf("fleet get stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "# Related support records") ||
		!strings.Contains(stdout.String(), "2026-06-10-routing-timeout") {
		t.Fatalf("fleet get related support missing: %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "search", "routing hub", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme-box-1") {
		t.Fatalf("fleet search stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "add", "ACME-BOX-2", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--customer", "sampleco.example.com", "--device", "Sample Scanner Y", "--identifier", "SO 200031", "--config-branch", "partner/site-2", "--ship-to", "Centerville", "--print"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"id: acme-box-2", "status: new", `  - "SO 200031"`, "config_branch: partner/site-2", "deployed_site:\n", "source: fleet", "## Notes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fleet add stdout = %q, missing %q", out, want)
		}
	}

	stdout.Reset()
	if err := a.run([]string{"our", "fleet", "set", "acme-box-1", "status=mourn", "deployed_site=Lakeside", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out = stdout.String()
	for _, want := range []string{"updated ", `status: "live" -> "mourn"`, "our sync --message", "Update fleet acme-box-1:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("fleet set stdout = %q, missing %q", out, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(workspaceRoot, "fleet", "acme-box-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "status: mourn") || !strings.Contains(string(data), `  - "SO 100045"`) {
		t.Fatalf("fleet set file = %q", data)
	}

	stderr.Reset()
	stdout.Reset()
	if err := a.run([]string{"our", "support", "add", "another-timeout", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--identifier", "FL 400-123401", "--identifier", "zz-unknown", "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), `"zz-unknown" is not in the fleet registry`) {
		t.Fatalf("support add stderr = %q, want unknown identifier note", stderr.String())
	}
	if strings.Contains(stderr.String(), `"FL 400-123401" is not`) {
		t.Fatalf("support add stderr flagged a known identifier: %q", stderr.String())
	}
}

func TestFleetSearchUsesQMDOrderWhenAvailable(t *testing.T) {
	root := fleet.Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCLITestFile(t, filepath.Join(root.Path, "fleet", "box-a.md"), `---
id: box-a
status: live
---

Timeout delivery.
`)
	writeCLITestFile(t, filepath.Join(root.Path, "fleet", "box-b.md"), `---
id: box-b
status: live
---

Timeout delivery.
`)
	old := qmdFleetSearch
	qmdFleetSearch = func([]fleet.Root, string, fleet.Filter) ([]fleet.Record, bool) {
		return []fleet.Record{{
			Manifest:  "acme",
			Workspace: "handbook",
			ID:        "box-b",
			Path:      filepath.Join(root.Path, "fleet", "box-b.md"),
			Status:    "live",
			Snippet:   "qmd snippet",
		}}, true
	}
	defer func() { qmdFleetSearch = old }()

	found, err := defaultFleetSearch([]fleet.Root{root}, "timeout delivery", fleet.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].ID != "box-b" || found[0].Snippet != "qmd snippet" {
		t.Fatalf("found = %#v", found)
	}
}

func TestCustomersListAndMeetingCustomerAlias(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".our", "workspaces", "handbook")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "customers.json"), `[
  {
    "id": "sampleco.example.com",
    "name": "SampleCo",
    "domain": "sampleco.example.com",
    "domain_confirmed": true,
    "aliases": ["sampleco", "sc"],
    "partners": ["integratorco"]
  }
]`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "meetings", "2026-03-12-sampleco-implementation.md"), `---
id: 2026-03-12-sampleco-implementation
date: 2026-03-12
title: "SampleCo implementation"
customer: sampleco.example.com
---

Alias filter match.
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "customers", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "sampleco.example.com") || !strings.Contains(stdout.String(), "sampleco,sc") {
		t.Fatalf("customers list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "list", "--manifest", "acme", "--home", home, "--customer", "sc"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "meetings", "add", "sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-05-13", "--customer", "sc", "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "customer: sampleco.example.com") {
		t.Fatalf("meetings add stdout = %q", stdout.String())
	}
}

func TestAdminCustomersAddAndEdit(t *testing.T) {
	t.Run("add customer", func(t *testing.T) {
		manifestDir := setupAdminCustomerManifest(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"our", "admin", "customers", "add", "otherco.example.com",
			"--manifest-dir", manifestDir,
			"--name", "OtherCo",
			"--domain", "otherco.example.com",
			"--alias", "otherco",
			"--alias", "oc",
			"--partner", "IntegratorCo",
			"--domain-confirmed",
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminCustomerResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "added" || result.Customer.ID != "otherco.example.com" || !result.Customer.DomainConfirmed {
			t.Fatalf("result = %#v", result)
		}
		customers := readAdminCustomers(t, manifestDir)
		if len(customers) != 2 || customers[1].ID != "otherco.example.com" || strings.Join(customers[1].Aliases, ",") != "otherco,oc" {
			t.Fatalf("customers = %#v", customers)
		}
	})

	t.Run("duplicate add errors", func(t *testing.T) {
		manifestDir := setupAdminCustomerManifest(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{
			"our", "admin", "customers", "add", "sampleco.example.com",
			"--manifest-dir", manifestDir,
		})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("add duplicate err = %v", err)
		}
	})

	t.Run("partial edit", func(t *testing.T) {
		manifestDir := setupAdminCustomerManifest(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"our", "admin", "customers", "edit", "sampleco.example.com",
			"--manifest-dir", manifestDir,
			"--name", "SampleCo Updated",
			"--partner", "IntegratorCo",
			"--partner", "ReviewCo",
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminCustomerResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		customer := result.Customer
		if customer.Name != "SampleCo Updated" || customer.Domain != "sampleco.example.com" || strings.Join(customer.Aliases, ",") != "sampleco,sc" || strings.Join(customer.Partners, ",") != "IntegratorCo,ReviewCo" || !customer.DomainConfirmed {
			t.Fatalf("customer = %#v", customer)
		}
	})

	t.Run("missing edit errors", func(t *testing.T) {
		manifestDir := setupAdminCustomerManifest(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{
			"our", "admin", "customers", "edit", "missingco.example.com",
			"--manifest-dir", manifestDir,
			"--name", "MissingCo",
		})
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("edit missing err = %v", err)
		}
	})

	t.Run("dirty checkout requires force", func(t *testing.T) {
		manifestDir := setupAdminCustomerManifest(t)
		initCLIGitRepo(t, manifestDir)
		writeCLITestFile(t, filepath.Join(manifestDir, "dirty.txt"), "dirty\n")
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{
			"our", "admin", "customers", "add", "otherco.example.com",
			"--manifest-dir", manifestDir,
		})
		if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Fatalf("dirty err = %v", err)
		}
	})

	t.Run("minimal write omits empty fields", func(t *testing.T) {
		manifestDir := setupAdminCustomerManifest(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"our", "admin", "customers", "add", "localco",
			"--manifest-dir", manifestDir,
		}); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(manifestDir, "catalog", "customers.json"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, unwanted := range []string{`"name": ""`, `"aliases": null`, `"partners": null`, `"domain_confirmed": false`} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("customer catalog contains %q:\n%s", unwanted, text)
			}
		}
	})
}

func TestTopLevelHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "our setup") ||
		!strings.Contains(stdout.String(), "our skills install") ||
		!strings.Contains(stdout.String(), "our admin manifests add|sync|validate") ||
		!strings.Contains(stdout.String(), "our version") {
		t.Fatalf("help output = %q", stdout.String())
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

func TestDoctorIncludesVersionItem(t *testing.T) {
	home := t.TempDir()
	server := newCLILatestServer(t, "0.2.0")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:               &stdout,
		stderr:               &stderr,
		updateCurrentVersion: "0.1.0",
		updateSource:         server.source(),
	}
	if err := a.run([]string{"our", "doctor", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse doctor JSON: %v\n%s", err, stdout.String())
	}
	if len(report.Version) != 1 || report.Version[0].Status != "stale" ||
		!strings.Contains(report.Version[0].Message, "run our update") {
		t.Fatalf("version report = %#v", report.Version)
	}
}

func TestDoctorReportsLegacyFluxState(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "acme")
	if err := os.MkdirAll(filepath.Join(root, ".flux"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "flux"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(home, ".config", "flux", "manifests.json"), `{"version":1}`)
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills", "flux"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	writeCLITestFile(t, filepath.Join(binDir, "flux"), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(binDir, "flux"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	t.Setenv("FLUX_HOME", filepath.Join(home, "old-flux"))

	server := newCLILatestServer(t, "0.1.0")
	var stdout, stderr bytes.Buffer
	a := app{
		stdout:               &stdout,
		stderr:               &stderr,
		updateCurrentVersion: "0.1.0",
		updateSource:         server.source(),
	}
	if err := a.run([]string{"our", "doctor", "--home", home, "--umbrella", root, "--json"}); err != nil {
		t.Fatal(err)
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse doctor JSON: %v\n%s", err, stdout.String())
	}
	seen := map[string]bool{}
	for _, item := range report.Legacy {
		seen[item.Name] = true
	}
	for _, want := range []string{".flux", "flux data", "flux manifest registry", "FLUX_* env", "flux binary", "codex:flux skill"} {
		if !seen[want] {
			t.Fatalf("legacy items = %#v, missing %q", report.Legacy, want)
		}
	}
}

func makeCLISkill(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: CLI test skill\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

type cliLatestServer struct {
	server         *httptest.Server
	version        string
	latestRequests int
	assetRequests  int
}

func newCLILatestServer(t *testing.T, version string) *cliLatestServer {
	t.Helper()
	s := &cliLatestServer{version: version}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

func (s *cliLatestServer) source() selfupdate.Source {
	return selfupdate.Source{Client: s.server.Client(), APIBaseURL: s.server.URL, DownloadBaseURL: s.server.URL}
}

func (s *cliLatestServer) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/releases/latest" {
		s.latestRequests++
		fmt.Fprintf(w, `{"tag_name":"v%s"}`, s.version)
		return
	}
	if strings.Contains(r.URL.Path, "/releases/download/") {
		s.assetRequests++
	}
	http.NotFound(w, r)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func writeCLITestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCLIManagedSkill(t *testing.T, dir, canonicalID string) {
	t.Helper()
	writeCLITestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+filepath.Base(dir)+"\n---\n")
	writeCLITestFile(t, filepath.Join(dir, ".our-managed.json"), `{
  "installer": "our",
  "version": "test",
  "mode": "copy",
  "source": "/tmp/our-test-source",
  "canonical_id": "`+canonicalID+`"
}`)
}

func writeAdminManifest(t *testing.T, dir, extra string) {
	t.Helper()
	writeCLITestFile(t, filepath.Join(dir, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" }`+extra+`
}`)
}

func setupAdminCustomerManifest(t *testing.T) string {
	t.Helper()
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "acme-workspace", "manifest", "catalog", "customers.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(manifestDir, "catalog", "customers.json"), string(data))
	return manifestDir
}

func readAdminCustomers(t *testing.T, manifestDir string) []manifest.Customer {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(manifestDir, "catalog", "customers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var customers []manifest.Customer
	if err := json.Unmarshal(data, &customers); err != nil {
		t.Fatal(err)
	}
	return customers
}

func setupCLISkillsManifestFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" },
    { "id": "acme:calendar", "install_slug": "acme-calendar", "path": "skills/acme-calendar" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	writeCLITestFile(t, filepath.Join(manifestCache, "skills", "acme-calendar", "SKILL.md"), `---
name: acme-calendar
description: Acme calendar
---
`)
	return home
}

func registerCLIManifest(t *testing.T, a app, home string) {
	t.Helper()
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
}

func ensureCLIGuidance(t *testing.T, home, umbrellaRoot string) {
	t.Helper()
	doc, err := loadSingleRegisteredDoc(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	result, err := guidance.Ensure(umbrellaRoot, doc.ref.LocalPath, doc.doc, guidance.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == "blocked" {
		t.Fatalf("guidance blocked: %#v", result)
	}
}

func setupCLILaunchFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(home, "acme")
}

func setupCLIRecordWorkspace(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	umbrellaRoot := filepath.Join(home, "acme")
	workspaceRoot := filepath.Join(umbrellaRoot, "handbook")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
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
  ]
}`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "README.md"), "seed\n")
	initCLIGitRepo(t, workspaceRoot)
	_, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	state = umbrella.UpsertMount(state, umbrella.MountStatus{
		ID:        "handbook",
		Kind:      "handbook",
		SourceRef: "manifest:acme:handbook",
		Status:    "synced",
	})
	if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	return home, workspaceRoot
}

func setupCLITrackedManifest(t *testing.T) (string, string, string, string) {
	t.Helper()
	home, umbrellaRoot, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	return home, umbrellaRoot, manifestCache, remote
}

func setupCLITrackedManifestBody(t *testing.T, body string) (string, string, string, string, string) {
	t.Helper()
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), body)
	initCLIGitRepo(t, manifestCache)
	remote := filepath.Join(home, "manifest.git")
	runCLIGit(t, home, "init", "--bare", "-q", remote)
	runCLIGit(t, manifestCache, "remote", "add", "origin", remote)
	runCLIGit(t, manifestCache, "branch", "-M", "master")
	runCLIGit(t, manifestCache, "push", "-q", "-u", "origin", "master")
	writer := filepath.Join(home, "manifest-writer")
	runCLIGit(t, home, "clone", "-q", remote, writer)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		remote,
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(home, "acme"), manifestCache, remote, writer
}

func initCLIGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCLIGit(t, dir, "init", "-q")
	runCLIGit(t, dir, "config", "user.name", "Example Test")
	runCLIGit(t, dir, "config", "user.email", "our-test@example.com")
	runCLIGit(t, dir, "config", "commit.gpgsign", "false")
	runCLIGit(t, dir, "add", ".")
	runCLIGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=our-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed repository")
}

func setupCLIRemoteRepo(t *testing.T, root, name string, files map[string]string) (string, string, string) {
	t.Helper()
	seed := filepath.Join(root, name+"-seed")
	for path, body := range files {
		writeCLITestFile(t, filepath.Join(seed, path), body)
	}
	initCLIGitRepo(t, seed)
	remote := filepath.Join(root, name+".git")
	runCLIGit(t, root, "init", "--bare", "-q", remote)
	runCLIGit(t, seed, "remote", "add", "origin", remote)
	runCLIGit(t, seed, "branch", "-M", "master")
	runCLIGit(t, seed, "push", "-q", "-u", "origin", "master")
	clone := filepath.Join(root, name)
	writer := filepath.Join(root, name+"-writer")
	runCLIGit(t, root, "clone", "-q", remote, clone)
	runCLIGit(t, root, "clone", "-q", remote, writer)
	return remote, clone, writer
}

func commitAndPushCLIGit(t *testing.T, dir, message string) {
	t.Helper()
	runCLIGit(t, dir, "add", ".")
	runCLIGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=our-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", message)
	runCLIGit(t, dir, "push", "-q", "origin", "HEAD:master")
}

func commitCLIGit(t *testing.T, dir, message string) {
	t.Helper()
	runCLIGit(t, dir, "add", ".")
	runCLIGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=our-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", message)
}

func runCLIGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitCLIOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestAdminRoutingDelegatesToTopLevelHandlers(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "leadership",
      "kind": "meetings",
      "git_url": "https://github.com/acme/leadership.git",
      "mode": "optional"
    }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(home, ".our", "workspaces", "handbook", "meetings", ".keep"), "")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	t.Run("manifest add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{
			"our", "admin", "manifests", "add", "extra",
			"https://github.com/acme/extra-manifest.git",
			"--home", home,
		}); err != nil {
			t.Fatalf("our admin manifests add err = %v", err)
		}
		stdout.Reset()
		if err := a.run([]string{"our", "manifests", "list", "--home", home}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "extra-manifest") {
			t.Fatalf("manifest list stdout = %q", stdout.String())
		}
	})

	t.Run("mount add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{"our", "admin", "mounts", "add", "meetings:leadership", "--manifest", "acme", "--home", home, "--print"}); err != nil {
			t.Fatalf("our admin mounts add err = %v", err)
		}
		if !strings.Contains(stdout.String(), "leadership\tdry-run") {
			t.Fatalf("mount add stdout = %q", stdout.String())
		}
	})

	t.Run("meetings add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{
			"our", "admin", "meetings", "add", "sampleco-followup",
			"--manifest", "acme",
			"--workspace", "handbook",
			"--home", home,
			"--date", "2026-05-13",
			"--customer", "sampleco",
			"--print",
		}); err != nil {
			t.Fatalf("our admin meetings add err = %v", err)
		}
		if !strings.Contains(stdout.String(), "2026-05-13-sampleco-followup") {
			t.Fatalf("meetings add stdout = %q", stdout.String())
		}
	})

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"our", "admin", "skills", "list"}, "use our skills list"},
		{[]string{"our", "admin", "manifests", "list"}, "use our manifests list"},
		{[]string{"our", "admin", "mounts", "list"}, "use our mounts list"},
		{[]string{"our", "admin", "meetings", "search", "cleanup"}, "use our meetings search"},
	} {
		t.Run(strings.Join(tc.args[2:], " "), func(t *testing.T) {
			err := a.run(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"our", "admin", "manifests"}, "missing admin manifests subcommand"},
		{[]string{"our", "admin", "mounts"}, "missing admin mounts subcommand"},
		{[]string{"our", "admin", "meetings"}, "missing admin meetings subcommand"},
	} {
		t.Run(strings.Join(tc.args[2:], " "), func(t *testing.T) {
			err := a.run(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
			}
		})
	}

	t.Run("onboard", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{"our", "admin", "setup", "--home", t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "manifest") {
			t.Fatalf("our admin setup err = %v, want a manifest-related error", err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{"our", "admin", "bogus"})
		if err == nil || !strings.Contains(err.Error(), "unknown admin subcommand") {
			t.Fatalf("our admin bogus err = %v, want unknown admin subcommand", err)
		}
	})

	t.Run("help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{"our", "admin", "--help"}); err != nil {
			t.Fatalf("our admin --help err = %v", err)
		}
		for _, want := range []string{"our admin setup", "our admin manifests", "our admin mounts", "our admin meetings"} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("our admin --help missing %q in:\n%s", want, stdout.String())
			}
		}
	})
}
