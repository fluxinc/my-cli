package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/harness"
)

func TestDiscoverFrontmatterAndWarnings(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "plain", "plain", "Plain description")
	writeRawSkill(t, root, "folded", "---\nname: folded\ndescription: >\n  Folded\n  description\n---\n")
	writeRawSkill(t, root, "literal", "---\nname: literal\ndescription: |\n  Literal\n  description\n---\n")
	writeRawSkill(t, root, "fallback", "---\ndescription: No name\n---\n")
	writeSkill(t, root, "mismatch", "other-name", "Mismatch")

	found, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Skill{}
	for _, s := range found {
		byName[s.Name] = s
	}
	if byName["plain"].Description != "Plain description" {
		t.Fatalf("plain description = %q", byName["plain"].Description)
	}
	if byName["folded"].Description != "Folded description" {
		t.Fatalf("folded description = %q", byName["folded"].Description)
	}
	if byName["literal"].Description != "Literal description" {
		t.Fatalf("literal description = %q", byName["literal"].Description)
	}
	if byName["fallback"].SkillName != "fallback" {
		t.Fatalf("fallback SkillName = %q", byName["fallback"].SkillName)
	}
	if len(byName["mismatch"].Warnings) != 1 {
		t.Fatalf("mismatch warnings = %#v", byName["mismatch"].Warnings)
	}
}

func TestDiscoverDeclaredUsesManifestIdentity(t *testing.T) {
	root := t.TempDir()
	writeRawSkill(t, filepath.Join(root, "skills"), "acme-handbook", "---\nname: acme-handbook\ndescription: Handbook\n---\n")

	found, err := DiscoverDeclared(root, []DeclaredSkill{
		{ID: "acme:handbook", InstallSlug: "acme-handbook", Path: "skills/acme-handbook"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("found = %#v, want one skill", found)
	}
	if found[0].Name != "acme-handbook" || found[0].CanonicalID != "acme:handbook" {
		t.Fatalf("found[0] = %#v", found[0])
	}
	if found[0].SourceRoot != root {
		t.Fatalf("SourceRoot = %q, want %q", found[0].SourceRoot, root)
	}
}

func TestInstallUninstallSymlinkRoundTrip(t *testing.T) {
	source := t.TempDir()
	skill := writeSkill(t, source, "demo-skill", "demo-skill", "Demo")
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".claude"))

	opts := InstallOpts{Link: true, Home: home, SourceRoot: source}
	res := Install(skill, harness.ClaudeCode, opts)
	if res.Status != StatusInstalled {
		t.Fatalf("install status = %s, want installed (%v)", res.Status, res.Err)
	}
	target := filepath.Join(home, ".claude", "skills", "demo-skill")
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("target is not symlink: info=%v err=%v", info, err)
	}

	res = Install(skill, harness.ClaudeCode, opts)
	if res.Status != StatusUpdated {
		t.Fatalf("reinstall status = %s, want updated", res.Status)
	}
	res = Uninstall("demo-skill", harness.ClaudeCode, opts)
	if res.Status != StatusRemoved {
		t.Fatalf("uninstall status = %s, want removed", res.Status)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists after uninstall: %v", err)
	}
	kind, err := Inspect("demo-skill", harness.ClaudeCode, home)
	if err != nil {
		t.Fatal(err)
	}
	if kind.Kind != "absent" {
		t.Fatalf("Inspect after uninstall = %#v, want absent", kind)
	}
}

func TestInstallUninstallCopyRoundTrip(t *testing.T) {
	source := t.TempDir()
	skill := writeSkill(t, source, "demo-skill", "demo-skill", "Demo")
	skill.CanonicalID = "my:demo-skill"
	mustMkdir(t, filepath.Join(skill.SourcePath, "references"))
	if err := os.WriteFile(filepath.Join(skill.SourcePath, "references", "note.md"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("references/note.md", filepath.Join(skill.SourcePath, "linked-note.md")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".claude"))

	opts := InstallOpts{Home: home, SourceRoot: source}
	res := Install(skill, harness.ClaudeCode, opts)
	if res.Status != StatusInstalled {
		t.Fatalf("install status = %s, want installed (%v)", res.Status, res.Err)
	}
	target := filepath.Join(home, ".claude", "skills", "demo-skill")
	if _, err := os.Stat(filepath.Join(target, ".my-cli-managed.json")); err != nil {
		t.Fatalf("managed marker missing: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(target, ".my-cli-managed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(marker), `"canonical_id": "my:demo-skill"`) {
		t.Fatalf("marker = %s", marker)
	}
	if _, err := os.Stat(filepath.Join(target, "references", "note.md")); err != nil {
		t.Fatalf("nested file missing from copy: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(target, "linked-note.md")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("linked note was not copied as symlink: info=%v err=%v", info, err)
	}
	inspection, err := InspectDeclared(skill, harness.ClaudeCode, opts)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Stale {
		t.Fatalf("fresh copy reported stale: %#v", inspection)
	}
	kind, err := Inspect("demo-skill", harness.ClaudeCode, home)
	if err != nil {
		t.Fatal(err)
	}
	if kind.Kind != "copy" {
		t.Fatalf("Inspect copy = %#v, want copy", kind)
	}
	res = Install(skill, harness.ClaudeCode, opts)
	if res.Status != StatusUpdated {
		t.Fatalf("reinstall status = %s, want updated", res.Status)
	}
	res = Uninstall("demo-skill", harness.ClaudeCode, InstallOpts{DryRun: true, Home: home, SourceRoot: source})
	if res.Status != StatusDryRun {
		t.Fatalf("dry-run uninstall managed copy = %s, want dry-run", res.Status)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("dry-run uninstall removed target: %v", err)
	}
	res = Uninstall("demo-skill", harness.ClaudeCode, opts)
	if res.Status != StatusRemoved {
		t.Fatalf("uninstall status = %s, want removed", res.Status)
	}
}

func TestDryRunAndUninstallProvenance(t *testing.T) {
	source := t.TempDir()
	skill := writeSkill(t, source, "demo-skill", "demo-skill", "Demo")
	home := t.TempDir()
	userDir := filepath.Join(home, ".claude", "skills", "demo-skill")
	mustMkdir(t, userDir)
	if err := os.WriteFile(filepath.Join(userDir, "README.md"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Install(skill, harness.ClaudeCode, InstallOpts{Link: true, DryRun: true, Home: home, SourceRoot: source})
	if res.Status != StatusBlocked {
		t.Fatalf("dry-run install over user dir = %s, want blocked", res.Status)
	}
	res = Uninstall("demo-skill", harness.ClaudeCode, InstallOpts{DryRun: true, Home: home, SourceRoot: source})
	if res.Status != StatusBlocked {
		t.Fatalf("dry-run uninstall user dir = %s, want blocked", res.Status)
	}
	res = Uninstall("demo-skill", harness.ClaudeCode, InstallOpts{Home: home, SourceRoot: source, Force: true})
	if res.Status != StatusRemoved {
		t.Fatalf("force uninstall user dir = %s, want removed", res.Status)
	}
	res = Uninstall("demo-skill", harness.ClaudeCode, InstallOpts{Home: home, SourceRoot: source})
	if res.Status != StatusNotInstalled {
		t.Fatalf("uninstall missing = %s, want not-installed", res.Status)
	}
}

func TestDryRunInstallDoesNotCreateTarget(t *testing.T) {
	source := t.TempDir()
	skill := writeSkill(t, source, "demo-skill", "demo-skill", "Demo")
	home := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".claude"))

	res := Install(skill, harness.ClaudeCode, InstallOpts{Link: true, DryRun: true, Home: home, SourceRoot: source})
	if res.Status != StatusDryRun {
		t.Fatalf("dry-run install = %s, want dry-run", res.Status)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "demo-skill")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created target: %v", err)
	}
}

func TestProvenanceBlocksUserManagedTargets(t *testing.T) {
	source := t.TempDir()
	skill := writeSkill(t, source, "demo-skill", "demo-skill", "Demo")
	home := t.TempDir()
	userDir := filepath.Join(home, ".claude", "skills", "demo-skill")
	mustMkdir(t, userDir)
	if err := os.WriteFile(filepath.Join(userDir, "README.md"), []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := InstallOpts{Home: home, SourceRoot: source}
	res := Install(skill, harness.ClaudeCode, opts)
	if res.Status != StatusBlocked {
		t.Fatalf("install over user dir = %s, want blocked", res.Status)
	}
	res = Install(skill, harness.ClaudeCode, InstallOpts{Home: home, SourceRoot: source, Force: true})
	if res.Status != StatusUpdated {
		t.Fatalf("force install = %s, want updated", res.Status)
	}
}

func TestProvenanceAcceptsManagedSymlinks(t *testing.T) {
	source := t.TempDir()
	skill := writeSkill(t, source, "demo-skill", "demo-skill", "Demo")
	home := t.TempDir()
	targetDir := filepath.Join(home, ".claude", "skills")
	mustMkdir(t, targetDir)

	if err := os.Symlink(skill.SourcePath, filepath.Join(targetDir, "demo-skill")); err != nil {
		t.Fatal(err)
	}
	kind, err := Inspect("demo-skill", harness.ClaudeCode, home)
	if err != nil {
		t.Fatal(err)
	}
	if kind.Kind != "symlink" || kind.Target != skill.SourcePath {
		t.Fatalf("Inspect symlink = %#v, want symlink to %s", kind, skill.SourcePath)
	}
	res := Install(skill, harness.ClaudeCode, InstallOpts{Link: true, Home: home, SourceRoot: source})
	if res.Status != StatusUpdated {
		t.Fatalf("source symlink install = %s, want updated", res.Status)
	}

	if err := os.Remove(filepath.Join(targetDir, "demo-skill")); err != nil {
		t.Fatal(err)
	}
	materialized := filepath.Join(home, ".local", "share", "my-cli", "skills", "demo-skill")
	mustMkdir(t, materialized)
	if err := os.Symlink(materialized, filepath.Join(targetDir, "demo-skill")); err != nil {
		t.Fatal(err)
	}
	res = Install(skill, harness.ClaudeCode, InstallOpts{Link: true, Home: home, SourceRoot: source})
	if res.Status != StatusUpdated {
		t.Fatalf("materialized symlink install = %s, want updated", res.Status)
	}
}

func TestListInstalledReportsManagedEntries(t *testing.T) {
	source := t.TempDir()
	skill := writeSkill(t, source, "demo-skill", "demo-skill", "Demo")
	home := t.TempDir()
	targetDir := filepath.Join(home, ".claude", "skills")
	mustMkdir(t, targetDir)
	if err := os.Symlink(skill.SourcePath, filepath.Join(targetDir, "demo-skill")); err != nil {
		t.Fatal(err)
	}
	userDir := filepath.Join(targetDir, "user-skill")
	mustMkdir(t, userDir)
	if err := os.WriteFile(filepath.Join(userDir, "SKILL.md"), []byte("---\nname: user-skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(targetDir, "copy-skill")
	mustMkdir(t, copyDir)
	if err := writeManagedMarker(copyDir, "copy", source, "my:copy-skill", ""); err != nil {
		t.Fatal(err)
	}

	found, err := ListInstalled(harness.ClaudeCode, InstallOpts{Home: home, SourceRoot: source})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]InstalledSkill{}
	for _, entry := range found {
		byName[entry.Skill] = entry
	}
	if !byName["demo-skill"].Managed || byName["demo-skill"].Kind != "symlink" {
		t.Fatalf("demo-skill entry = %#v, want managed symlink", byName["demo-skill"])
	}
	if !byName["copy-skill"].Managed || byName["copy-skill"].CanonicalID != "my:copy-skill" {
		t.Fatalf("copy-skill entry = %#v, want managed marker with canonical id", byName["copy-skill"])
	}
	if byName["user-skill"].Managed {
		t.Fatalf("user-skill entry = %#v, want unmanaged", byName["user-skill"])
	}
}

func TestMissingHarnessSkip(t *testing.T) {
	source := t.TempDir()
	skill := writeSkill(t, source, "demo-skill", "demo-skill", "Demo")
	res := Install(skill, harness.Codex, InstallOpts{Link: true, Home: t.TempDir(), SourceRoot: source, SkipMissing: true})
	if res.Status != StatusSkipped {
		t.Fatalf("missing harness status = %s, want skipped", res.Status)
	}
}

func writeSkill(t *testing.T, root, dirName, skillName, desc string) Skill {
	t.Helper()
	writeRawSkill(t, root, dirName, "---\nname: "+skillName+"\ndescription: "+desc+"\n---\n")
	found, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range found {
		if s.Name == dirName {
			return s
		}
	}
	t.Fatalf("skill %s not discovered", dirName)
	return Skill{}
}

func writeRawSkill(t *testing.T, root, dirName, content string) {
	t.Helper()
	dir := filepath.Join(root, dirName)
	mustMkdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}
