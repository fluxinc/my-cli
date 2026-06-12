package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/manifest"
)

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
