package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestInitCreatesManifestRepoAndRegisters(t *testing.T) {
	home := t.TempDir()
	content := filepath.Join(t.TempDir(), "acme-handbook")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "init", "acme",
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
		{Action: "setup", Command: "my setup --manifest acme"},
		{Action: "launch", Command: "my ai --manifest acme claude"},
		{Action: "launch", Command: "my ai --manifest acme codex"},
		{Action: "publish", Command: "my publish --manifest acme"},
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
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	// Content lives only in the visible content repo.
	for _, path := range []string{
		filepath.Join(content, ".git"),
		filepath.Join(content, "README.md"),
		filepath.Join(content, "customers", "README.md"),
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
	if err := a.run([]string{"my", "init", "acme", "--home", home, "--json"}); err != nil {
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
	err := a.run([]string{"my", "init", "acme", "--path", repo, "--home", home})
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
	if err := a.run([]string{"my", "init", "acme", "--path", repo, "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"next\tsetup\tmy setup --manifest acme\n",
		"next\tlaunch\tmy ai --manifest acme claude\n",
		"next\tlaunch\tmy ai --manifest acme codex\n",
		"next\tpublish\tmy publish --manifest acme\n",
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
	if err := a.run([]string{"my", "init", "acme", "--path", repo, "--home", home}); err != nil {
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
	if err := a.run([]string{"my", "init", "acme", "--path", repo, "--home", home}); err != nil {
		t.Fatal(err)
	}
	author := initCommitAuthor(t, repo)
	if author != "My AI <my-cli@example.invalid>" {
		t.Fatalf("author = %q, want fallback identity", author)
	}
}

func TestInitScaffoldREADMETeachesTeammateFirstRun(t *testing.T) {
	home := t.TempDir()

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "init", "acme", "--home", home}); err != nil {
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
		"my manifests add acme",
		"my manifests sync acme",
		"my setup --manifest acme",
		"my ai --manifest acme codex",
		"my publish --manifest acme",
		"reviewed manifest control-plane changes",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("manifest README missing %q:\n%s", want, readme)
		}
	}
	contentREADME, err := os.ReadFile(filepath.Join(home, "acme", "workspace", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"my customers add", "my meetings add", "my support add", "my fleet add"} {
		if !strings.Contains(string(contentREADME), want) {
			t.Fatalf("content README missing %q:\n%s", want, string(contentREADME))
		}
	}
	if !strings.Contains(string(contentREADME), "my sync --push") {
		t.Fatalf("content README should teach publish with my sync --push:\n%s", string(contentREADME))
	}
	meetingsREADME, err := os.ReadFile(filepath.Join(home, "acme", "workspace", "meetings", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(meetingsREADME), "my sync --push") {
		t.Fatalf("meetings README should teach publish with my sync --push:\n%s", string(meetingsREADME))
	}
	skillDoc, err := os.ReadFile(filepath.Join(manifestRepo, "skills", "acme-handbook", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"`my customers`", "`my meetings`", "`my support`", "`my fleet`"} {
		if !strings.Contains(string(skillDoc), want) {
			t.Fatalf("handbook skill missing %q:\n%s", want, string(skillDoc))
		}
	}
}

// fakeGH emulates gh repo create by provisioning a local bare repository and
// wiring it as origin, mirroring what gh does against GitHub.

func TestPublishCreatesRemotesRewritesMountsAndRegistry(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	remotes := t.TempDir()
	var calls []string
	a.publishRunner = fakeGH(t, remotes, &calls)

	stdout.Reset()
	if err := a.run([]string{"my", "publish", "--home", home}); err != nil {
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
	if !strings.Contains(stdout.String(), "my manifests add acme") {
		t.Fatalf("stdout = %q, want teammate instructions", stdout.String())
	}

	// Idempotent: a second publish pushes but never recreates remotes.
	calls = nil
	if err := a.run([]string{"my", "publish", "--home", home}); err != nil {
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
	if err := a.run([]string{"my", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	var calls []string
	a.publishRunner = fakeGH(t, t.TempDir(), &calls)

	stdout.Reset()
	if err := a.run([]string{"my", "publish", "--home", home, "--print"}); err != nil {
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

func TestPublishPrintPlansKnownMountRewriteCommit(t *testing.T) {
	home := t.TempDir()
	content := filepath.Join(t.TempDir(), "acme-handbook")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "init", "acme", "--home", home, "--path", content}); err != nil {
		t.Fatal(err)
	}
	remoteRoot := t.TempDir()
	contentRemote := filepath.Join(remoteRoot, "acme-workspace.git")
	runCLIGit(t, remoteRoot, "init", "--bare", "-q", contentRemote)
	runCLIGit(t, content, "remote", "add", "origin", contentRemote)
	runCLIGit(t, content, "branch", "-M", "master")
	runCLIGit(t, content, "push", "-q", "-u", "origin", "master")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "publish", "--home", home, "--print"}); err != nil {
		t.Fatalf("publish --print: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"publish\tcontent:workspace\twould push",
		"publish\tmanifest\twould rewrite-mounts",
		"publish\tmanifest\twould commit\tmanifest.json",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("publish --print stdout = %q, missing %q", stdout.String(), want)
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
	if len(doc.Mounts) != 1 || doc.Mounts[0].GitURL != content {
		t.Fatalf("print mode rewrote mounts: %#v", doc.Mounts)
	}
	if out := gitCLIOutput(t, manifestRepo, "status", "--porcelain"); out != "" {
		t.Fatalf("manifest checkout status = %q, want clean after print", out)
	}
}

func TestPublishManifestCommitsControlPlaneChanges(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  { "id": "demo", "name": "Demo", "description": "Demo product" }
]
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "publish", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatalf("publish: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	for _, want := range []string{"publish\tmanifest\tcommitted", "publish\tmanifest\tpushed"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("publish stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if got := gitCLIOutput(t, remote, "show", "master:catalog/products.json"); !strings.Contains(got, "Demo product") {
		t.Fatalf("remote catalog = %q, want published control-plane file", got)
	}
	if out := gitCLIOutput(t, manifestCache, "status", "--porcelain"); out != "" {
		t.Fatalf("manifest checkout status = %q, want clean", out)
	}
}

func TestPublishManifestCommitsControlPlanePathWithSpaces(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "demo product.json"), `{
  "id": "demo",
  "name": "Demo Product"
}
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "publish", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatalf("publish: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "publish\tmanifest\tcommitted\tcatalog/demo product.json") {
		t.Fatalf("publish stdout = %q, want committed spaced path", stdout.String())
	}
	if got := gitCLIOutput(t, remote, "show", "master:catalog/demo product.json"); !strings.Contains(got, "Demo Product") {
		t.Fatalf("remote catalog = %q, want published spaced path", got)
	}
	if out := gitCLIOutput(t, manifestCache, "status", "--porcelain"); out != "" {
		t.Fatalf("manifest checkout status = %q, want clean", out)
	}
}

func TestPublishManifestCommitsControlPlaneRename(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "old.json"), `{
  "id": "old",
  "name": "Old Catalog Entry"
}
`)
	runCLIGit(t, manifestCache, "add", "catalog/old.json")
	runCLIGit(t, manifestCache, "commit", "-q", "-m", "Add old catalog entry")
	runCLIGit(t, manifestCache, "push", "-q", "origin", "master")
	runCLIGit(t, manifestCache, "mv", "catalog/old.json", "catalog/new.json")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "publish", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatalf("publish: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "publish\tmanifest\tcommitted") {
		t.Fatalf("publish stdout = %q, want committed manifest rename", stdout.String())
	}
	if got := gitCLIOutput(t, remote, "show", "master:catalog/new.json"); !strings.Contains(got, "Old Catalog Entry") {
		t.Fatalf("remote new catalog = %q, want renamed catalog entry", got)
	}
	if _, err := exec.Command("git", "-C", remote, "cat-file", "-e", "master:catalog/old.json").CombinedOutput(); err == nil {
		t.Fatalf("publish left old catalog path in remote")
	}
	if out := gitCLIOutput(t, manifestCache, "status", "--porcelain"); out != "" {
		t.Fatalf("manifest checkout status = %q, want clean", out)
	}
}

func TestPublishPrintPlansManifestControlPlaneCommitWithoutChanges(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  { "id": "demo", "name": "Demo", "description": "Demo product" }
]
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "publish", "--manifest", "acme", "--home", home, "--print"}); err != nil {
		t.Fatalf("publish --print: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	for _, want := range []string{"publish\tmanifest\twould commit", "publish\tmanifest\twould push"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("publish --print stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if _, err := exec.Command("git", "-C", remote, "cat-file", "-e", "master:catalog/products.json").CombinedOutput(); err == nil {
		t.Fatalf("publish --print pushed catalog to remote")
	}
	if out := gitCLIOutput(t, manifestCache, "status", "--porcelain"); !strings.Contains(out, "?? catalog/") {
		t.Fatalf("manifest checkout status = %q, want untracked catalog file preserved", out)
	}
}

func TestPublishManifestHoldsFilesOutsideControlPaths(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "README.md"), "local scratch\n")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "publish", "--manifest", "acme", "--home", home})
	if err == nil || !strings.Contains(err.Error(), "outside manifest control paths") {
		t.Fatalf("publish err = %v, want outside-control-path hold", err)
	}
	if !strings.Contains(stdout.String(), "publish\tmanifest\theld back") {
		t.Fatalf("publish stdout = %q, want held back step", stdout.String())
	}
	if _, err := exec.Command("git", "-C", remote, "cat-file", "-e", "master:README.md").CombinedOutput(); err == nil {
		t.Fatalf("publish pushed README outside manifest control paths")
	}
}

func TestPublishManifestHoldsRenameFromOutsideControlPaths(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "README.md"), "operator scratch\n")
	runCLIGit(t, manifestCache, "add", "README.md")
	runCLIGit(t, manifestCache, "commit", "-q", "-m", "Add readme")
	runCLIGit(t, manifestCache, "push", "-q", "origin", "master")
	if err := os.MkdirAll(filepath.Join(manifestCache, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	runCLIGit(t, manifestCache, "mv", "README.md", "catalog/readme.md")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "publish", "--manifest", "acme", "--home", home})
	if err == nil || !strings.Contains(err.Error(), "outside manifest control paths") {
		t.Fatalf("publish err = %v, want outside-control-path hold", err)
	}
	if !strings.Contains(stdout.String(), "publish\tmanifest\theld back") {
		t.Fatalf("publish stdout = %q, want held back step", stdout.String())
	}
	if _, err := exec.Command("git", "-C", remote, "cat-file", "-e", "master:catalog/readme.md").CombinedOutput(); err == nil {
		t.Fatalf("publish pushed renamed file from outside control paths")
	}
	if got := gitCLIOutput(t, remote, "show", "master:README.md"); !strings.Contains(got, "operator scratch") {
		t.Fatalf("remote README = %q, want original file preserved", got)
	}
}
