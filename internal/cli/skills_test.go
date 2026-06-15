package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/harness"
	"github.com/fluxinc/our-ai/internal/skills"
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
		`"harness": "antigravity"`,
		`"remedy": "our skills install antigravity --skill acme:handbook --manifest acme --home `,
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

func TestSkillsShowSurfacesServiceRequirements(t *testing.T) {
	home := t.TempDir()
	writeServicesRolesManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "skills", "show", "acme:handbook", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "service:docs-search") {
		t.Fatalf("skills show stdout should surface service requirement, got:\n%s", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "skills", "show", "acme:handbook", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Requires []string `json:"Requires"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &shown); err != nil {
		t.Fatalf("skills show --json: %v in:\n%s", err, stdout.String())
	}
	if len(shown.Requires) != 1 || shown.Requires[0] != "service:docs-search" {
		t.Fatalf("skills show --json requires = %+v", shown.Requires)
	}
}
