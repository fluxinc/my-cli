package cli

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/guidance"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/selfupdate"
	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func initCommitAuthor(t *testing.T, repo string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repo, "log", "-1", "--format=%an <%ae>").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

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

func initResultHasNext(result initResult, want initNextCommand) bool {
	for _, got := range result.NextCommands {
		if got == want {
			return true
		}
	}
	return false
}

func TestUnimplementedAndUnknownCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "products"}); err == nil || !strings.Contains(err.Error(), "missing products") {
		t.Fatalf("catalog err = %v", err)
	}
	if err := a.run([]string{"my", "tools", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown tools") {
		t.Fatalf("unknown tools err = %v", err)
	}
	if err := a.run([]string{"my", "workspaces", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown workspace") {
		t.Fatalf("unknown workspace err = %v", err)
	}
	if err := a.run([]string{"my", "skills", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown skills") {
		t.Fatalf("unknown skills err = %v", err)
	}
	if err := a.run([]string{"my", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unknown command err = %v", err)
	}
}

func TestCatalogListHumanFormatting(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
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
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "products", "list", "--manifest", "acme", "--home", home}); err != nil {
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

func TestChangedManifestForDerivedUsesManifestRole(t *testing.T) {
	report := syncer.Report{Results: []syncer.Result{
		{Manifest: "acme", ID: "acme", Role: "manifest", Status: "pulled"},
	}}
	name, ok := changedManifestForDerived(report)
	if !ok || name != "acme" {
		t.Fatalf("changedManifestForDerived = %q, %v; want acme, true", name, ok)
	}
}

func TestToolsInfoAndDoctorCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
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
      "local_path": "~/.my-cli/workspaces/handbook"
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
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"my", "tools", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme\tqmd\toptional\tsearch ranking helper") {
		t.Fatalf("tools list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "tools", "info", "qmd", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "npm install -g @tobilu/qmd") {
		t.Fatalf("tools info stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"manifest\tacme\tok", "workspace\tacme:handbook", "tool\tacme:qmd"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor stdout = %q, missing %q", out, want)
		}
	}
}

func TestTopLevelHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "my setup") ||
		!strings.Contains(stdout.String(), "my skills install") ||
		!strings.Contains(stdout.String(), "my admin manifests add|sync|validate") ||
		!strings.Contains(stdout.String(), "my version") {
		t.Fatalf("help output = %q", stdout.String())
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
	writeCLITestFile(t, filepath.Join(dir, ".my-cli-managed.json"), `{
  "installer": "my",
  "version": "test",
  "mode": "copy",
  "source": "/tmp/my-test-source",
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

func setupCLISkillsManifestFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
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
		"my", "manifests", "add", "acme",
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
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "manifests", "add", "acme",
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
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
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
		"my", "manifests", "add", "acme",
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
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
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
		"my", "manifests", "add", "acme",
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
	configureCLIGitIdentity(t, dir)
	runCLIGit(t, dir, "add", ".")
	runCLIGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=my-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed repository")
}

func configureCLIGitIdentity(t *testing.T, dir string) {
	t.Helper()
	runCLIGit(t, dir, "config", "user.name", "Example Test")
	runCLIGit(t, dir, "config", "user.email", "my-test@example.com")
	runCLIGit(t, dir, "config", "commit.gpgsign", "false")
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
	configureCLIGitIdentity(t, clone)
	configureCLIGitIdentity(t, writer)
	return remote, clone, writer
}

func commitAndPushCLIGit(t *testing.T, dir, message string) {
	t.Helper()
	runCLIGit(t, dir, "add", ".")
	runCLIGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=my-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", message)
	runCLIGit(t, dir, "push", "-q", "origin", "HEAD:master")
}

func commitCLIGit(t *testing.T, dir, message string) {
	t.Helper()
	runCLIGit(t, dir, "add", ".")
	runCLIGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=my-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", message)
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

func writeServicesRolesManifest(t *testing.T, home string) {
	t.Helper()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    {
      "id": "acme:handbook",
      "install_slug": "acme-handbook",
      "path": "skills/acme-handbook",
      "requires": ["service:docs-search"]
    }
  ],
  "services": [
    {
      "id": "docs-search",
      "kind": "mcp",
      "purpose": "Search the handbook",
      "describe_ref": "services/docs-search.server.json",
      "auth_ref": "env://ACME_DOCS_TOKEN",
      "connection": {
        "type": "stdio",
        "command": "acme-docs-mcp",
        "args": ["--stdio"]
      }
    },
    {
      "id": "status-api",
      "kind": "http",
      "purpose": "Status API",
      "describe_ref": "https://status.acme.example/openapi.json",
      "auth_ref": "none"
    }
  ],
  "roles": [
    {
      "id": "operator",
      "purpose": "Default operator role",
      "skills": ["acme:handbook"],
      "services": ["docs-search"]
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Handbook skill.
---

Test skill.
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
}

func writeRoleSetupManifest(t *testing.T, home string) string {
	t.Helper()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/base.md"] },
  "services": [
    {
      "id": "docs-search",
      "kind": "mcp",
      "purpose": "Search the handbook",
      "auth_ref": "none",
      "connection": {
        "type": "stdio",
        "command": "acme-docs-mcp"
      }
    },
    {
      "id": "status-api",
      "kind": "http",
      "purpose": "Status API",
      "describe_ref": "https://status.acme.example/openapi.json",
      "auth_ref": "none"
    }
  ],
  "roles": [
    {
      "id": "operator",
      "purpose": "Default operator role",
      "guidance_paths": ["guidance/operator.md"],
      "services": ["docs-search"]
    },
    {
      "id": "auditor",
      "purpose": "Audit role",
      "guidance_paths": ["guidance/auditor.md"],
      "services": ["status-api"]
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "guidance", "base.md"), "base guidance\n")
	writeCLITestFile(t, filepath.Join(manifestCache, "guidance", "operator.md"), "operator role guidance\n")
	writeCLITestFile(t, filepath.Join(manifestCache, "guidance", "auditor.md"), "auditor role guidance\n")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, "acme")
}
