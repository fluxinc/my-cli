package manifest

import (
	"errors"
	"os"
	"os/exec"
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

func TestSetLocalPathRepointsRegistry(t *testing.T) {
	home := t.TempDir()
	if _, err := Add(home, "acme", "https://github.com/acme/acme-workspace.git"); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "acme", "handbook")
	ref, err := SetLocalPath(home, "acme", target)
	if err != nil {
		t.Fatal(err)
	}
	if ref.LocalPath != target {
		t.Fatalf("ref.LocalPath = %q, want %q", ref.LocalPath, target)
	}
	found, ok, err := Find(home, "acme")
	if err != nil || !ok {
		t.Fatalf("Find after SetLocalPath: ok=%v err=%v", ok, err)
	}
	if found.LocalPath != target {
		t.Fatalf("registry LocalPath = %q, want %q", found.LocalPath, target)
	}
	if _, err := SetLocalPath(home, "ghost", target); err == nil {
		t.Fatal("SetLocalPath for unknown manifest should fail")
	}
}

func TestAddPreservesRepointedLocalPathForSameURL(t *testing.T) {
	home := t.TempDir()
	url := "https://github.com/acme/acme-workspace.git"
	if _, err := Add(home, "acme", url); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "acme", "handbook")
	if _, err := SetLocalPath(home, "acme", target); err != nil {
		t.Fatal(err)
	}
	ref, err := Add(home, "acme", url)
	if err != nil {
		t.Fatal(err)
	}
	if ref.LocalPath != target {
		t.Fatalf("re-add same URL reset LocalPath to %q, want preserved %q", ref.LocalPath, target)
	}
	ref, err = Add(home, "acme", "https://github.com/acme/other.git")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref.LocalPath, filepath.Join(home, ".local", "share", appDir, "manifests")) {
		t.Fatalf("re-add with new URL kept LocalPath %q, want cache path", ref.LocalPath)
	}
}

func TestSyncReportsLocalOnlyCheckoutWithoutOrigin(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", filepath.Join(home, "acme", "handbook"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ref.LocalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real repository without an origin remote, run through real git: the
	// local-only classification must not depend on a stubbed runner.
	if out, err := exec.Command("git", "-C", ref.LocalPath, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	results, err := Sync(home, []string{"acme"}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "local-only" {
		t.Fatalf("results = %#v, want local-only status", results)
	}
	if !strings.Contains(results[0].Message, "no origin remote") {
		t.Fatalf("message = %q, want no-origin explanation", results[0].Message)
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

func TestSyncMarksChangedWhenHeadReadFailsAfterPull(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", filepath.Join(home, "remote.git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ref.LocalPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	headReads := 0
	results, err := Sync(home, []string{"acme"}, false, false, func(name string, args ...string) ([]byte, error) {
		if name != "git" {
			return nil, errors.New("unexpected command")
		}
		if len(args) >= 4 && args[0] == "-C" && args[2] == "remote" {
			return []byte("https://github.com/acme/remote.git\n"), nil
		}
		if len(args) >= 4 && args[0] == "-C" && args[2] == "rev-parse" {
			headReads++
			if headReads == 1 {
				return []byte("before\n"), nil
			}
			return nil, errors.New("rev-parse failed")
		}
		if len(args) >= 4 && args[0] == "-C" && args[2] == "pull" {
			return []byte("Already up to date.\n"), nil
		}
		return nil, errors.New("unexpected git command")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "synced" || !results[0].Changed {
		t.Fatalf("results = %#v, want successful changed sync", results)
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
      "include_paths": ["meetings", "support", "decisions"]
    },
    {
      "id": "support",
      "kind": "support",
      "git_url": "https://github.com/acme/acme-support.git",
      "mode": "optional"
    },
    {
      "id": "customers",
      "kind": "customers",
      "git_url": "https://github.com/acme/acme-customers.git",
      "mode": "optional"
    },
    {
      "id": "fleet",
      "kind": "fleet",
      "git_url": "https://github.com/acme/acme-fleet.git",
      "mode": "optional"
    }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
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
      "id": "our:mail",
      "install_slug": "our-mail",
      "source": { "type": "tool", "tool": "spark" }
    }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "git@github.com:acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
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

func TestValidateManifestRejectsSelfMountGitURL(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": ".",
      "mode": "default"
    }
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "self-mounts are no longer supported") {
		t.Fatalf("errors = %#v, want self-mount rejection", result.Errors)
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

func TestValidateManifestCatchesInvalidAgentGuidancePaths(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "agent_guidance": {
    "paths": ["agent-guidance/acme.md", "../private.md", "/tmp/guide.md"]
  }
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 2 {
		t.Fatalf("errors = %#v", result.Errors)
	}
}

func TestValidateManifestAllowsContractRules(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "contract": [
    "Continue an existing relevant support record or create a new dated record when working on any fleet member.",
    "Record decisions in the handbook before acting on them."
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 0 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	doc, _, err := LoadDocument(dir)
	if err != nil {
		t.Fatalf("LoadDocument: %v", err)
	}
	if len(doc.Contract) != 2 {
		t.Fatalf("contract = %#v", doc.Contract)
	}
}

func TestValidateManifestCatchesInvalidContractRules(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "contract": [
    "  ",
    "Record decisions before acting.\nThen publish them.",
    "Continue an existing relevant support record or create a new dated record when working on any fleet member.",
    "Continue an existing relevant support record or create a new dated record when working on any fleet member."
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 3 {
		t.Fatalf("errors = %#v", result.Errors)
	}
	for _, err := range result.Errors {
		if !strings.Contains(err, "contract") {
			t.Fatalf("error %q does not mention contract", err)
		}
	}
}

func TestValidateManifestCatchesInvalidSyncPolicy(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "sync": { "publish_policy": "direct" }
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "sync.publish_policy") {
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

func TestValidateManifestAllowsServicesRolesAndServiceRequirements(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "agent_guidance": {
    "paths": ["agent-guidance/acme.md"]
  },
  "skills": [
    {
      "id": "acme:handbook",
      "install_slug": "acme-handbook",
      "path": "skills/acme-handbook",
      "requires": ["workspace:handbook", "tool:qmd", "service:docs-search"]
    }
  ],
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required"
    }
  ],
  "tools": [
    { "id": "qmd", "mode": "optional" }
  ],
  "services": [
    {
      "id": "docs-search",
      "kind": "mcp",
      "purpose": "Search the checked-in handbook index",
      "describe_ref": "services/docs-search.server.json",
      "auth_ref": "env://ACME_DOCS_TOKEN",
      "grant": { "scope": "read" },
      "connection": {
        "type": "stdio",
        "command": "acme-docs-mcp",
        "args": ["--stdio"],
        "env": { "ACME_DOCS_TOKEN": "${ACME_DOCS_TOKEN}" }
      }
    },
    {
      "id": "status-api",
      "kind": "http",
      "purpose": "Read-only status API",
      "describe_ref": "https://api.example.com/openapi.json",
      "auth_ref": "none",
      "grant": "read"
    }
  ],
  "roles": [
    {
      "id": "operator",
      "purpose": "Default operator role",
      "guidance_paths": ["agent-guidance/operator.md"],
      "mounts": ["handbook"],
      "skills": ["acme:handbook"],
      "tools": ["qmd"],
      "services": ["docs-search", "status-api"]
    }
  ]
}`)
	result := ValidateFile(dir)
	if len(result.Errors) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestValidateManifestCatchesInvalidServicesRolesAndServiceRequirements(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    {
      "id": "acme:handbook",
      "install_slug": "acme-handbook",
      "path": "skills/acme-handbook",
      "requires": ["service:missing-service"]
    }
  ],
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required"
    }
  ],
  "tools": [
    { "id": "qmd", "mode": "optional" }
  ],
  "services": [
    {
      "id": "Bad Service",
      "kind": "a2a",
      "purpose": "",
      "describe_ref": "../server.json",
      "auth_ref": "secret-value"
    },
    {
      "id": "status-api",
      "kind": "http",
      "purpose": "Status API",
      "auth_ref": "none",
      "connection": { "command": "status-mcp" }
    }
  ],
  "roles": [
    {
      "id": "bad-role",
      "purpose": "",
      "guidance_paths": ["../private.md"],
      "mounts": ["missing-mount"],
      "skills": ["acme:missing"],
      "tools": ["missing-tool"],
      "services": ["missing-service"]
    }
  ]
}`)
	result := ValidateFile(dir)
	for _, want := range []string{
		`service id "Bad Service" must be lowercase kebab-case`,
		`service "Bad Service" kind "a2a" is unsupported`,
		`service "Bad Service" purpose is required`,
		`service "Bad Service" auth_ref "secret-value" must use op://, env://, broker://, or none`,
		`service "Bad Service" describe_ref "../server.json" must be an http(s) URL or a relative path inside the manifest repo`,
		`service "status-api" connection is only supported for kind "mcp"`,
		`skill "acme:handbook" requires unknown service "missing-service"`,
		`role "bad-role" purpose is required`,
		`role "bad-role" guidance_paths entry "../private.md" must be a relative path that stays inside the manifest repo`,
		`role "bad-role" grants unknown mount "missing-mount"`,
		`role "bad-role" grants unknown skill "acme:missing"`,
		`role "bad-role" grants unknown tool "missing-tool"`,
		`role "bad-role" grants unknown service "missing-service"`,
	} {
		if !containsString(result.Errors, want) {
			t.Fatalf("errors missing %q:\n%#v", want, result.Errors)
		}
	}
}

func TestSaveDocumentRoundTripsExampleManifest(t *testing.T) {
	source := filepath.Join("..", "..", "examples", "acme-workspace", "manifest", "manifest.json")
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := LoadDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "manifest.json")
	if err := SaveDocument(target, doc); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(roundTrip) != string(original) {
		t.Fatalf("round-trip manifest changed:\n%s", roundTrip)
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
	writeFile(t, filepath.Join(ref.LocalPath, manifestFile), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeFile(t, ProductCatalogPath(ref), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "description": "Sample service",
    "purpose": "Synthetic product source for public fixture tests",
    "related_skills": ["acme:handbook"]
  }
]`)
	products, err := LoadCatalog(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || products[0].ID != "sample-product" || products[0].Purpose == "" {
		t.Fatalf("products = %#v", products)
	}
	if len(products[0].RelatedSkills) != 1 || products[0].RelatedSkills[0] != "acme:handbook" {
		t.Fatalf("related skills = %#v", products[0].RelatedSkills)
	}
	product, ok, err := FindProduct(home, "acme", "sample-product")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || product.Name != "Sample Product" {
		t.Fatalf("product = %#v, ok=%v", product, ok)
	}
}

func TestValidateManifestCatchesCatalogUnknownRelatedSkill(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeFile(t, filepath.Join(dir, "catalog", "products.json"), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "related_skills": ["acme:missing"]
  }
]`)
	result := ValidateFile(dir)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "not declared by manifest") {
		t.Fatalf("errors = %#v", result.Errors)
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

func TestLoadCatalogRejectsMalformedRelatedSkill(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ref.LocalPath, manifestFile), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeFile(t, ProductCatalogPath(ref), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "related_skills": ["Acme Handbook"]
  }
]`)
	_, err = LoadCatalog(home, "acme")
	if err == nil || !strings.Contains(err.Error(), "related skill") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadCatalogRejectsUnknownRelatedSkillWhenManifestPresent(t *testing.T) {
	home := t.TempDir()
	ref, err := Add(home, "acme", "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(ref.LocalPath, manifestFile), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeFile(t, ProductCatalogPath(ref), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "related_skills": ["acme:missing"]
  }
]`)
	_, err = LoadCatalog(home, "acme")
	if err == nil || !strings.Contains(err.Error(), "not declared by manifest") {
		t.Fatalf("err = %v", err)
	}
}

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, manifestFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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
