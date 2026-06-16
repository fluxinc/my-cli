package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceCommands(t *testing.T) {
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
	if err := a.run([]string{"my", "workspaces", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook") {
		t.Fatalf("workspace list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "workspaces", "sync", "handbook", "--manifest", "acme", "--print", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "git clone") {
		t.Fatalf("workspace sync stdout = %q", stdout.String())
	}
}

func TestMountCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
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
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"my", "mounts", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook\thandbook\trequired") {
		t.Fatalf("mount list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "mounts", "add", "meetings:leadership", "--manifest", "acme", "--home", home, "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "leadership\tdry-run") {
		t.Fatalf("mount add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "mounts", "sync", "handbook", "--manifest", "acme", "--home", home, "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook\tdry-run") {
		t.Fatalf("mount sync stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "mounts", "remove", "handbook", "--umbrella", filepath.Join(home, "acme"), "--home", home, "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook\tdry-run") {
		t.Fatalf("mount remove stdout = %q", stdout.String())
	}
}

func TestMountAddProductRecordsState(t *testing.T) {
	home := t.TempDir()
	productSource := filepath.Join(home, "product-source")
	writeCLITestFile(t, filepath.Join(productSource, "README.md"), "product repo\n")
	initCLIGitRepo(t, productSource)

	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
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
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "products", "list", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"id": "sample-product"`) || !strings.Contains(stdout.String(), `"sample-service"`) {
		t.Fatalf("catalog list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"my", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	umbrellaRoot := filepath.Join(home, "acme")
	if err := a.run([]string{"my", "mounts", "add", "repo:sample-service", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "acme", "repos", "sample-service", ".git")); err != nil {
		t.Fatalf("repo was not cloned: %v", err)
	}
	state, err := os.ReadFile(filepath.Join(home, "acme", ".my-cli", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"sample-service"`, `"repo:sample-service"`, `"kind": "repo"`} {
		if !strings.Contains(string(state), want) {
			t.Fatalf("state = %s, missing %q", state, want)
		}
	}
	stdout.Reset()
	if err := a.run([]string{"my", "mounts", "list", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"id": "repo:sample-service"`) || !strings.Contains(stdout.String(), `"kind": "repo"`) {
		t.Fatalf("mount list json stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "mounts", "sync", "repo:sample-service", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	err = a.run([]string{"my", "mounts", "add", "product:sample-product", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot})
	if err == nil || !strings.Contains(err.Error(), "business catalog entries") {
		t.Fatalf("err = %v, want product mount removal error", err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "repos", "remove", "sample-service", "--force", "--umbrella", umbrellaRoot, "--home", home}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "acme", "repos", "sample-service")); !os.IsNotExist(err) {
		t.Fatalf("repo dir still exists or stat failed unexpectedly: %v", err)
	}
	state, err = os.ReadFile(filepath.Join(home, "acme", ".my-cli", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "sample-service") {
		t.Fatalf("state still references removed repo: %s", state)
	}
}

func TestMountAddProductUnknownJSON(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.run([]string{"my", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	err := a.run([]string{"my", "repos", "add", "missing", "--manifest", "acme", "--home", home, "--umbrella", filepath.Join(home, "acme"), "--json"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stdout.String(), `"error": "unknown_repo"`) || !strings.Contains(stdout.String(), "my repos list") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
