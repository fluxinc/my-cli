package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsInstallParsesInterspersedFlags(t *testing.T) {
	source := makeCLISkill(t, "demo-skill")
	home := t.TempDir()

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"flux", "skills", "install", "claude-code",
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

func TestSkillsInstallConflictingModes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"flux", "skills", "install", "--copy", "--link", "--all"})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want mutually exclusive", err)
	}
}

func TestSkillsListJSON(t *testing.T) {
	source := makeCLISkill(t, "demo-skill")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "skills", "list", "--json", "--source", source}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"Name": "demo-skill"`) {
		t.Fatalf("json stdout = %q", stdout.String())
	}
}

func TestSkillsInstallFromManifestRecordsCanonicalID(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()

	if err := a.run([]string{"flux", "skills", "install", "claude-code", "--copy", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme:handbook") {
		t.Fatalf("install stdout = %q, want canonical id", stdout.String())
	}
	marker, err := os.ReadFile(filepath.Join(home, ".claude", "skills", "acme-handbook", ".flux-managed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(marker), `"canonical_id": "acme:handbook"`) {
		t.Fatalf("marker = %s", marker)
	}
}

func TestSkillsInstallSelectionErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "skills", "install", "--all", "codex"}); err == nil || !strings.Contains(err.Error(), "--all") {
		t.Fatalf("all+explicit err = %v", err)
	}
	if err := a.run([]string{"flux", "skills", "install", "unknown"}); err == nil || !strings.Contains(err.Error(), "unknown harness") {
		t.Fatalf("unknown harness err = %v", err)
	}
	if err := a.run([]string{"flux", "skills", "list", "extra"}); err == nil || !strings.Contains(err.Error(), "positional") {
		t.Fatalf("list positional err = %v", err)
	}
}

func TestUnimplementedAndUnknownCommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "catalog"}); err == nil || !strings.Contains(err.Error(), "missing catalog") {
		t.Fatalf("catalog err = %v", err)
	}
	if err := a.run([]string{"flux", "tools", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown tools") {
		t.Fatalf("unknown tools err = %v", err)
	}
	if err := a.run([]string{"flux", "workspace", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown workspace") {
		t.Fatalf("unknown workspace err = %v", err)
	}
	if err := a.run([]string{"flux", "skills", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown skills") {
		t.Fatalf("unknown skills err = %v", err)
	}
	if err := a.run([]string{"flux", "wat"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
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
      "local_path": "~/.flux/workspaces/handbook"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme") {
		t.Fatalf("add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "manifest", "list", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "acme-ai-manifest") {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "manifest", "sync", "acme", "--print", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "git clone") {
		t.Fatalf("sync stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "manifest", "validate", manifestDir}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Fatalf("validate stdout = %q", stdout.String())
	}
}

func TestWorkspaceCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
      "local_path": "~/.flux/workspaces/handbook"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "workspace", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook") {
		t.Fatalf("workspace list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "workspace", "sync", "handbook", "--manifest", "acme", "--print", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "git clone") {
		t.Fatalf("workspace sync stdout = %q", stdout.String())
	}
}

func TestMountCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook\thandbook\trequired") {
		t.Fatalf("mount list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "add", "meetings:leadership", "--manifest", "acme", "--home", home, "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "leadership\tdry-run") {
		t.Fatalf("mount add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "sync", "handbook", "--manifest", "acme", "--home", home, "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "handbook\tdry-run") {
		t.Fatalf("mount sync stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "remove", "handbook", "--umbrella", filepath.Join(home, "acme"), "--home", home, "--print"}); err != nil {
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

	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "git_url": "`+productSource+`",
    "description": "Sample service"
  }
]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "catalog", "list", "products", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"id": "sample-product"`) {
		t.Fatalf("catalog list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "onboard", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	umbrellaRoot := filepath.Join(home, "acme")
	if err := a.run([]string{"flux", "mount", "add", "product:sample-product", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "acme", "products", "sample-product", ".git")); err != nil {
		t.Fatalf("product was not cloned: %v", err)
	}
	state, err := os.ReadFile(filepath.Join(home, "acme", ".flux", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"sample-product"`, `"product:sample-product"`, `"kind": "product"`} {
		if !strings.Contains(string(state), want) {
			t.Fatalf("state = %s, missing %q", state, want)
		}
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "list", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "product:sample-product\tproduct") {
		t.Fatalf("mount list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "list", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"id": "product:sample-product"`) || !strings.Contains(stdout.String(), `"kind": "product"`) {
		t.Fatalf("mount list json stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "add", "product:sample-product", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "sync", "product:sample-product", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "remove", "product:sample-product", "--umbrella", umbrellaRoot, "--home", home, "--force"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "acme", "products", "sample-product")); !os.IsNotExist(err) {
		t.Fatalf("product dir still exists or stat failed unexpectedly: %v", err)
	}
	state, err = os.ReadFile(filepath.Join(home, "acme", ".flux", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "sample-product") {
		t.Fatalf("state still references removed product: %s", state)
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "mount", "list", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "product:sample-product") {
		t.Fatalf("mount list still shows removed product: %q", stdout.String())
	}
}

func TestMountAddProductUnknownJSON(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.run([]string{"flux", "onboard", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	err := a.run([]string{"flux", "mount", "add", "product:missing", "--manifest", "acme", "--home", home, "--umbrella", filepath.Join(home, "acme"), "--json"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stdout.String(), `"error": "unknown_product"`) || !strings.Contains(stdout.String(), "flux catalog list products") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestMeetingJSONErrorWithoutUmbrella(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"flux", "meetings", "search", "SampleCo", "--json"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stdout.String(), `"error": "no_umbrella"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestOnboardJSONAndDoctorUmbrella(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"flux", "onboard", "claude-code", "--copy", "--json", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"umbrella"`, `"skills"`, `"acme-handbook"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("onboard stdout = %q, missing %q", stdout.String(), want)
		}
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "doctor", "--umbrella", filepath.Join(home, "acme"), "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "umbrella\tacme\tok") {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}
}

func TestToolsInfoAndDoctorCommands(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
      "local_path": "~/.flux/workspaces/handbook"
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "tools", "info", "qmd", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "npm install -g @tobilu/qmd") {
		t.Fatalf("tools info stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "doctor", "--manifest", "acme", "--home", home}); err != nil {
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
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".flux", "workspaces", "handbook")
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
      "local_path": "~/.flux/workspaces/handbook"
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "list", "--manifest", "acme", "--home", home, "--customer", "sampleco"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}
	if fields := strings.Split(strings.TrimSpace(stdout.String()), "\t"); len(fields) != 7 {
		t.Fatalf("meetings list fields = %#v, want 7 fixed columns", fields)
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "search", "data cleanup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Promised onboarding review") {
		t.Fatalf("meetings search stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "get", "2026-03-12-sampleco-implementation", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "data cleanup") {
		t.Fatalf("meetings get stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "add", "sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-05-13", "--customer", "sampleco", "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-05-13-sampleco-followup") || !strings.Contains(stdout.String(), "## Promises") {
		t.Fatalf("meetings add stdout = %q", stdout.String())
	}
}

func TestTopLevelHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "flux onboard") || !strings.Contains(stdout.String(), "flux skills install") {
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

func writeCLITestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initCLIGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCLIGit(t, dir, "init", "-q")
	runCLIGit(t, dir, "add", ".")
	runCLIGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=flux-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed repository")
}

func runCLIGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
