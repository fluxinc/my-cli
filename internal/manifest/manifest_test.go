package manifest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddAndLoadRegistry(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "acme" {
		t.Fatalf("ref.Name = %q", ref.Name)
	}
	if !strings.HasPrefix(ref.LocalPath, filepath.Join(home, ".local", "share", appDir, "manifests")) {
		t.Fatalf("ref.LocalPath = %q", ref.LocalPath)
	}

	reg, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Manifests) != 1 || reg.Manifests[0].GitURL != ref.GitURL {
		t.Fatalf("registry = %#v", reg)
	}
}

func TestSyncDryRunPlansCloneAndPull(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}

	results, err := Sync(home, []string{"acme"}, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := results[0].Message; !strings.Contains(got, "git clone") {
		t.Fatalf("clone dry-run message = %q", got)
	}

	if err := os.MkdirAll(filepath.Join(ref.LocalPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	results, err = Sync(home, []string{"acme"}, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := results[0].Message; !strings.Contains(got, "pull --ff-only") {
		t.Fatalf("pull dry-run message = %q", got)
	}
}

func TestSyncChecksGitHubAuthBeforeClone(t *testing.T) {
	home := t.TempDir()
	if _, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git"); err != nil {
		t.Fatal(err)
	}
	var commands []string
	results, err := Sync(home, []string{"acme"}, false, false, func(name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "gh" {
			return []byte("not logged in"), errors.New("exit 1")
		}
		return []byte("unexpected git"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "failed" || !strings.Contains(results[0].Error, "gh auth login") {
		t.Fatalf("results = %#v", results)
	}
	if len(commands) != 1 || !strings.HasPrefix(commands[0], "gh auth status") {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestValidateManifest(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "allowed_external_namespaces": ["spark"],
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    {
      "id": "acme:handbook",
      "install_slug": "acme-handbook",
      "path": "skills/acme-handbook",
      "requires": ["workspace:handbook", "tool:qmd"]
    },
    {
      "id": "spark:use-spark",
      "install_slug": "use-spark",
      "source": { "type": "tool", "tool": "spark" },
      "requires": ["tool:spark"]
    }
  ],
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required",
      "include_paths": ["meetings", "decisions"]
    }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.flux/workspaces/handbook"
    }
  ],
  "tools": [
    { "id": "qmd" },
    {
      "id": "spark",
      "skill_install": {
        "command": "spark",
        "args": ["skill", "--install", "{{ skills_root }}"]
      }
    }
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("valid result = %#v", result)
	}
}

func TestValidateManifestCatchesNamespaceAndSSHWarnings(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    {
      "id": "acme:handbook",
      "install_slug": "Acme Handbook",
      "path": "../skills/acme-handbook",
      "requires": ["workspace:missing", "tool:missing", "service:spark", "bad requirement"]
    },
    {
      "id": "flux:mail",
      "install_slug": "flux-mail",
      "source": { "type": "tool", "tool": "spark" }
    }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "git@github.com:acme/acme-handbook.git",
      "local_path": "~/.flux/workspaces/handbook"
    }
  ],
  "tools": [
    { "id": "spark", "skill_install": { "args": ["{{ skills_root }}"] } }
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 9 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "SSH") {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestValidateManifestCatchesInvalidMounts(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": " " },
  "mounts": [
    {
      "id": "Bad Mount",
      "kind": "unknown",
      "git_url": "",
      "mode": "sometimes"
    }
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 5 {
		t.Fatalf("errors = %#v", result.Errors)
	}
}

func TestValidateManifestCatchesInvalidMountIncludePaths(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required",
      "include_paths": ["meetings", "../skills", "/tmp", "docs\\windows", "meetings/../skills"]
    }
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 4 {
		t.Fatalf("errors = %#v", result.Errors)
	}
}

func TestValidateManifestAllowsWorkspaceRequirementFromMount(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    {
      "id": "acme:handbook",
      "install_slug": "acme-handbook",
      "path": "skills/acme-handbook",
      "requires": ["workspace:handbook"]
    }
  ],
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required"
    }
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v", result.Errors)
	}
}

func TestEffectiveMountsIncludesLegacyWorkspaces(t *testing.T) {
	doc := Document{
		Mounts: []Mount{{
			ID:     "handbook",
			Kind:   "handbook",
			GitURL: "https://github.com/acme/acme-handbook.git",
			Mode:   "required",
		}},
		Workspaces: []Workspace{
			{ID: "handbook", GitURL: "ignored"},
			{ID: "engineering", GitURL: "https://github.com/acme/engineering.git"},
		},
	}
	mounts := EffectiveMounts(doc)
	if len(mounts) != 2 {
		t.Fatalf("mounts = %#v", mounts)
	}
	if mounts[1].ID != "engineering" || mounts[1].Kind != "repo" || mounts[1].Mode != "required" {
		t.Fatalf("legacy mount = %#v", mounts[1])
	}
}

func TestLoadCatalogReadsProducts(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, ProductCatalogPath(ref), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "git_url": "https://github.com/acme/sample-product.git",
    "description": "Sample service"
  }
]`)
	products, err := LoadCatalog(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || products[0].ID != "sample-product" {
		t.Fatalf("products = %#v", products)
	}
	product, ok, err := FindProduct(home, "acme", "sample-product")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || product.GitURL == "" {
		t.Fatalf("product = %#v, ok=%v", product, ok)
	}
}

func TestLoadCatalogMissingFileIsEmpty(t *testing.T) {
	home := t.TempDir()
	if _, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git"); err != nil {
		t.Fatal(err)
	}
	products, err := LoadCatalog(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 0 {
		t.Fatalf("products = %#v", products)
	}
}

func TestLoadCatalogRejectsMalformedJSON(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, ProductCatalogPath(ref), `[{`)
	_, err = LoadCatalog(home, "acme")
	if err == nil || !strings.Contains(err.Error(), "products.json") || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
