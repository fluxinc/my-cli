package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/umbrella"
)

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
		!strings.Contains(out, "derived-skill\tclaude-code\t*\tskipped") ||
		!strings.Contains(out, "launch-scoped") {
		t.Fatalf("manifest sync stdout = %q", out)
	}
	data, err := os.ReadFile(filepath.Join(umbrellaRoot, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fresh guidance from manifest") {
		t.Fatalf("AGENTS.md was not regenerated from synced manifest:\n%s", data)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("manifest sync installed org skill globally: %v", err)
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
