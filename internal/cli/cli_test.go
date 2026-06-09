package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/flux/internal/manifest"
	"github.com/fluxinc/flux/internal/meetings"
	"github.com/fluxinc/flux/internal/umbrella"
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

func TestSkillsInstallHelpMentionsGuidance(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"flux", "skills", "install", "--help"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("err = %v, want flag.ErrHelp", err)
	}
	if !strings.Contains(stderr.String(), "only changes harness skill directories") ||
		!strings.Contains(stderr.String(), "Run flux onboard") {
		t.Fatalf("stderr = %q", stderr.String())
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

func TestSkillsSelfInstallAndStatus(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "skills", "self", "install", "codex", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "codex\tflux\tinstalled") {
		t.Fatalf("install stdout = %q", stdout.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "flux")); err != nil {
		t.Fatalf("self skill was not installed: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"flux", "skills", "self", "status", "codex", "--json", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"harness": "codex"`,
		`"skill": "flux"`,
		`"canonical_id": "flux:self"`,
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
	if err := a.run([]string{"flux", "skills", "list", "--json", "--source", source}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"Name": "demo-skill"`) {
		t.Fatalf("json stdout = %q", stdout.String())
	}
}

func TestSkillsListHumanFormatting(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"flux", "skills", "list", "--manifest", "acme", "--home", home}); err != nil {
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

func TestSkillsShowFromManifest(t *testing.T) {
	home := setupCLISkillsManifestFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"flux", "skills", "show", "acme:handbook", "--manifest", "acme", "--home", home}); err != nil {
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
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"flux", "skills", "install", "claude-code",
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
		"flux", "skills", "uninstall", "claude-code",
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
		"flux", "skills", "install", "claude-code",
		"--copy", "--manifest", "acme", "--home", home, "--skill", "acme:handbook",
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"flux", "skills", "status",
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
		`"remedy": "flux skills install codex --skill acme:handbook --manifest acme --home `,
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
		"flux", "skills", "install", "claude-code",
		"--copy", "--manifest", "acme", "--home", home, "--skill", "acme:handbook",
	}); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(home, ".local", "share", "flux", "manifests", "acme", "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Changed handbook
---
`)

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"flux", "skills", "status",
		"--json", "--manifest", "acme", "--home", home, "--skill", "acme:handbook",
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"status": "stale"`,
		`"remedy": "flux skills sync claude-code --skill acme:handbook --manifest acme --home `,
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
		"flux", "skills", "sync", "claude-code",
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
		"flux", "skills", "sync", "claude-code",
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
		"flux", "skills", "purge", "claude-code",
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

func TestAdminSkillsAddCopiesAndDeclares(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")
	sourceRoot := makeCLISkill(t, "demo-skill")
	skillDir := filepath.Join(sourceRoot, "demo-skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"flux", "admin", "skills", "add", skillDir,
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
		"flux", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "--keep-original") {
		t.Fatalf("admin add err = %v, want explicit keep/remove-original choice", err)
	}

	if err := a.run([]string{
		"flux", "admin", "skills", "add", skillDir,
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
    "git_url": "https://github.com/acme/demo-product.git",
    "description": "Demo",
    "related_skills": ["acme:demo-skill", "acme:other-skill"]
  }
]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"flux", "admin", "skills", "remove", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "related_skills") {
		t.Fatalf("remove err = %v, want related_skills blocker", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"flux", "admin", "skills", "remove", "demo-skill",
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
			"flux", "admin", "skills", "remove", "spark:use-spark",
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
			"flux", "admin", "skills", "remove", "spark:use-spark",
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
		"flux", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty add err = %v, want dirty checkout refusal", err)
	}

	if err := a.run([]string{
		"flux", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
		"--force",
	}); err != nil {
		t.Fatalf("force add failed: %v", err)
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

func TestSyncExplicitNitBackendReportsMissingWorkspace(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "sync", "--backend", "nit", "--manifest", "acme", "--home", home, "--print", "--json"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"backend": "nit"`,
		"Nit workspace not initialized",
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

func TestCatalogListHumanFormatting(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "git_url": "https://github.com/acme/sample-product.git",
    "description": "Sample service",
    "purpose": "Synthetic source used by tests.",
    "related_skills": ["acme:handbook"]
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
	if err := a.run([]string{"flux", "catalog", "list", "products", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"sample-product - Sample Product\n",
		"  source: https://github.com/acme/sample-product.git\n",
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
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"flux", "meetings", "search", "SampleCo", "--home", home, "--json"})
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
	if !strings.Contains(stdout.String(), "umbrella\tguidance\tok") {
		t.Fatalf("doctor stdout = %q, want guidance ok", stdout.String())
	}
}

func TestRootCommandPrintsUmbrellaAndProductPaths(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "root", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	umbrellaRoot := filepath.Join(home, "acme")
	if strings.TrimSpace(stdout.String()) != umbrellaRoot {
		t.Fatalf("root stdout = %q, want %q", stdout.String(), umbrellaRoot)
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "root", "--manifest", "acme", "--home", home, "--product", "sample-product"}); err != nil {
		t.Fatal(err)
	}
	wantProduct := filepath.Join(umbrellaRoot, "products", "sample-product")
	if strings.TrimSpace(stdout.String()) != wantProduct {
		t.Fatalf("root --product stdout = %q, want %q", stdout.String(), wantProduct)
	}
}

func TestDoctorReportsGuidanceDrift(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "onboard", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(umbrellaRoot, "AGENTS.md"), "<!-- flux:generated workspace-guidance v1 -->\n\nstale\n")

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"flux", "doctor", "--umbrella", umbrellaRoot, "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "umbrella\tguidance\tstale") ||
		!strings.Contains(stdout.String(), "run flux onboard") {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}
}

func TestDoctorReportsFreshnessNoFetch(t *testing.T) {
	home, _, _, _ := setupCLITrackedManifest(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"flux", "doctor", "--manifest", "acme", "--home", home, "--no-fetch"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "freshness\tmanifest:acme\tok") ||
		!strings.Contains(out, "up to date (as of last fetch)") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestDoctorReportsRemoteFreshnessUnknown(t *testing.T) {
	home, _, manifestCache, _ := setupCLITrackedManifest(t)
	runCLIGit(t, manifestCache, "remote", "set-url", "origin", filepath.Join(home, "missing.git"))
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"flux", "doctor", "--manifest", "acme", "--home", home}); err != nil {
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
	if err := a.run([]string{"flux", "doctor", "--manifest", "acme", "--home", home}); err != nil {
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
	if err := a.run([]string{"flux", "doctor", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "derived\tskill:claude-code:acme-handbook\tabsent") ||
		!strings.Contains(out, "flux skills install claude-code --skill acme:handbook") {
		t.Fatalf("doctor stdout = %q", out)
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
		"flux", "sync",
		"--backend", "flux",
		"--publish", "never",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(umbrellaRoot, ".flux", "last-sync.json")
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
	if err := a.run([]string{"flux", "doctor", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "last-sync\tlast publish\tok") ||
		!strings.Contains(out, "publish=never") ||
		!strings.Contains(out, "already_landed=1") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestLaunchPrintsResolvedCommandWithoutCheckingGuidance(t *testing.T) {
	home, _ := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"flux", "launch",
		"--manifest", "acme",
		"--home", home,
		"--product", "sample-product",
		"--print",
		"codex", "--model", "gpt-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "cd " + filepath.Join(home, "acme", "products", "sample-product") + " && codex --model gpt-5\n"
	if stdout.String() != want {
		t.Fatalf("launch --print stdout = %q, want %q", stdout.String(), want)
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
	err := a.run([]string{"flux", "launch", "--manifest", "acme", "--home", home, "codex"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "workspace guidance missing") ||
		!strings.Contains(stderr.String(), "run flux onboard") {
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
	err := a.run([]string{"flux", "launch", "--manifest", "acme", "--home", home, "--onboard", "codex", "--model", "gpt-5", "--full-auto"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/test/bin/codex" || gotDir != umbrellaRoot || strings.Join(gotArgs, " ") != "--model gpt-5 --full-auto" {
		t.Fatalf("exec path=%q dir=%q args=%#v", gotPath, gotDir, gotArgs)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "AGENTS.md")); err != nil {
		t.Fatalf("launch --onboard did not write guidance: %v", err)
	}
	if !strings.Contains(stdout.String(), "launch\tcodex\tcd "+umbrellaRoot+" && codex") {
		t.Fatalf("onboard stdout missing launch hint: %q", stdout.String())
	}
}

func TestLaunchMissingHarnessPrintsFallbackAndFails(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "onboard", "--manifest", "acme", "--home", home}); err != nil {
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
	err := a.run([]string{"flux", "launch", "--manifest", "acme", "--home", home, "codex", "--model", "gpt-5"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	wantLine := "cd " + umbrellaRoot + " && codex --model gpt-5"
	if !strings.Contains(stderr.String(), "codex not found on PATH") || !strings.Contains(stderr.String(), wantLine) {
		t.Fatalf("stderr = %q", stderr.String())
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
	if fields := strings.Split(strings.TrimSpace(stdout.String()), "\t"); len(fields) != 8 {
		t.Fatalf("meetings list fields = %#v, want 8 fixed columns", fields)
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
	if err := a.run([]string{"flux", "meetings", "add", "sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-05-13", "--customer", "sampleco", "--attendees", "Alex Example", "--partner", "integratorco", "--source-id", "spark-123", "--print"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-05-13-sampleco-followup") || !strings.Contains(stdout.String(), "## Promises") || !strings.Contains(stdout.String(), `source_id: spark-123`) {
		t.Fatalf("meetings add stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "add", "2026-05-28-sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--attendees", "Heather (PMH, mammo tech)", "--partner", "Siemens, Healthineers", "--print"}); err != nil {
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
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "customers", "list", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "sampleco.example.com") || !strings.Contains(stdout.String(), "sampleco") {
		t.Fatalf("customers list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "list", "--home", home, "--customer", "sampleco"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "search", "sampleco cleanup", "--home", home}); err != nil {
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

func TestCustomersListAndMeetingCustomerAlias(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".flux", "workspaces", "handbook")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.flux/workspaces/handbook"
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "customers", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "sampleco.example.com") || !strings.Contains(stdout.String(), "sampleco,sc") {
		t.Fatalf("customers list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "list", "--manifest", "acme", "--home", home, "--customer", "sc"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "2026-03-12-sampleco-implementation") {
		t.Fatalf("meetings list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "meetings", "add", "sampleco-followup", "--manifest", "acme", "--workspace", "handbook", "--home", home, "--date", "2026-05-13", "--customer", "sc", "--print"}); err != nil {
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
			"flux", "admin", "customers", "add", "otherco.example.com",
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
			"flux", "admin", "customers", "add", "sampleco.example.com",
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
			"flux", "admin", "customers", "edit", "sampleco.example.com",
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
			"flux", "admin", "customers", "edit", "missingco.example.com",
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
			"flux", "admin", "customers", "add", "otherco.example.com",
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
			"flux", "admin", "customers", "add", "localco",
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
	if err := a.run([]string{"flux", "--help"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "flux onboard") ||
		!strings.Contains(stdout.String(), "flux skills install") ||
		!strings.Contains(stdout.String(), "flux admin manifest add|sync|validate") ||
		!strings.Contains(stdout.String(), "flux version") {
		t.Fatalf("help output = %q", stdout.String())
	}
}

func TestVersionCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"flux", "version"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "0.1.0" {
		t.Fatalf("version stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"flux", "--version"}); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != "0.1.0" {
		t.Fatalf("--version stdout = %q", stdout.String())
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

func writeCLIManagedSkill(t *testing.T, dir, canonicalID string) {
	t.Helper()
	writeCLITestFile(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+filepath.Base(dir)+"\n---\n")
	writeCLITestFile(t, filepath.Join(dir, ".flux-managed.json"), `{
  "installer": "flux",
  "version": "test",
  "mode": "copy",
  "source": "/tmp/flux-test-source",
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
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "acme-workspace", "catalog", "customers.json"))
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
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
}

func setupCLILaunchFixture(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"flux", "manifest", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(home, "acme")
}

func setupCLITrackedManifest(t *testing.T) (string, string, string, string) {
	t.Helper()
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	initCLIGitRepo(t, manifestCache)
	remote := filepath.Join(home, "manifest.git")
	runCLIGit(t, home, "init", "--bare", "-q", remote)
	runCLIGit(t, manifestCache, "remote", "add", "origin", remote)
	runCLIGit(t, manifestCache, "branch", "-M", "master")
	runCLIGit(t, manifestCache, "push", "-q", "-u", "origin", "master")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"flux", "manifest", "add", "acme",
		remote,
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(home, "acme"), manifestCache, remote
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

func TestAdminRoutingDelegatesToTopLevelHandlers(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
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
      "local_path": "~/.flux/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(home, ".flux", "workspaces", "handbook", "meetings", ".keep"), "")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	t.Run("manifest add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{
			"flux", "admin", "manifest", "add", "extra",
			"https://github.com/acme/extra-manifest.git",
			"--home", home,
		}); err != nil {
			t.Fatalf("flux admin manifest add err = %v", err)
		}
		stdout.Reset()
		if err := a.run([]string{"flux", "manifest", "list", "--home", home}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "extra-manifest") {
			t.Fatalf("manifest list stdout = %q", stdout.String())
		}
	})

	t.Run("mount add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{"flux", "admin", "mount", "add", "meetings:leadership", "--manifest", "acme", "--home", home, "--print"}); err != nil {
			t.Fatalf("flux admin mount add err = %v", err)
		}
		if !strings.Contains(stdout.String(), "leadership\tdry-run") {
			t.Fatalf("mount add stdout = %q", stdout.String())
		}
	})

	t.Run("meetings add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{
			"flux", "admin", "meetings", "add", "sampleco-followup",
			"--manifest", "acme",
			"--workspace", "handbook",
			"--home", home,
			"--date", "2026-05-13",
			"--customer", "sampleco",
			"--print",
		}); err != nil {
			t.Fatalf("flux admin meetings add err = %v", err)
		}
		if !strings.Contains(stdout.String(), "2026-05-13-sampleco-followup") {
			t.Fatalf("meetings add stdout = %q", stdout.String())
		}
	})

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"flux", "admin", "skills", "list"}, "use flux skills list"},
		{[]string{"flux", "admin", "manifest", "list"}, "use flux manifest list"},
		{[]string{"flux", "admin", "mount", "list"}, "use flux mount list"},
		{[]string{"flux", "admin", "meetings", "search", "cleanup"}, "use flux meetings search"},
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
		{[]string{"flux", "admin", "manifest"}, "missing admin manifest subcommand"},
		{[]string{"flux", "admin", "mount"}, "missing admin mount subcommand"},
		{[]string{"flux", "admin", "meetings"}, "missing admin meetings subcommand"},
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
		err := a.run([]string{"flux", "admin", "onboard", "--home", t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "manifest") {
			t.Fatalf("flux admin onboard err = %v, want a manifest-related error", err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{"flux", "admin", "bogus"})
		if err == nil || !strings.Contains(err.Error(), "unknown admin subcommand") {
			t.Fatalf("flux admin bogus err = %v, want unknown admin subcommand", err)
		}
	})

	t.Run("help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{"flux", "admin", "--help"}); err != nil {
			t.Fatalf("flux admin --help err = %v", err)
		}
		for _, want := range []string{"flux admin onboard", "flux admin manifest", "flux admin mount", "flux admin meetings"} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("flux admin --help missing %q in:\n%s", want, stdout.String())
			}
		}
	})
}
