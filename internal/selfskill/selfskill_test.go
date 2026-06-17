package selfskill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/skills"
)

func TestMaterializeBundledMyCLISkill(t *testing.T) {
	home := t.TempDir()

	self, sourceRoot, err := Materialize(home)
	if err != nil {
		t.Fatal(err)
	}

	if self.Name != Name || self.CanonicalID != CanonicalID {
		t.Fatalf("self skill = %#v", self)
	}
	if sourceRoot != filepath.Join(home, ".local", "share", "my-cli", "skills") {
		t.Fatalf("sourceRoot = %q", sourceRoot)
	}
	data, err := os.ReadFile(filepath.Join(sourceRoot, "my-cli", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "This skill teaches a harness how to operate inside a My AI workspace.") {
		t.Fatalf("materialized skill content = %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(sourceRoot, ".my-cli-managed.json")); err != nil {
		t.Fatalf("managed marker missing: %v", err)
	}
}

func TestInstallAndInspectSelfSkill(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	results, err := Install([]harness.Harness{harness.Codex}, Options{Home: home, Link: true, SkipMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != skills.StatusInstalled {
		t.Fatalf("install results = %#v", results)
	}

	target := filepath.Join(home, ".codex", "skills", "my-cli")
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("target is not a symlink: info=%v err=%v", info, err)
	}

	rows, err := Inspect([]harness.Harness{harness.Codex}, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "installed" || rows[0].Kind != "symlink" {
		t.Fatalf("status rows = %#v", rows)
	}
}

func TestInstallMigratesManagedLegacySelfSkill(t *testing.T) {
	home := t.TempDir()
	legacyTarget := filepath.Join(home, ".codex", "skills", "my")
	writeLegacySelfSkill(t, legacyTarget, true)

	results, err := Install([]harness.Harness{harness.Codex}, Options{Home: home, Link: true, SkipMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStatus(results, skills.StatusMigrated) || !hasStatus(results, skills.StatusUpdated) {
		t.Fatalf("install results = %#v, want migrated and updated", results)
	}
	if _, err := os.Lstat(legacyTarget); !os.IsNotExist(err) {
		t.Fatalf("legacy target still exists: %v", err)
	}
	target := filepath.Join(home, ".codex", "skills", "my-cli")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("new target is not a symlink: %v", info.Mode())
	}
}

func TestSyncExistingRefreshesCopiesOnlyWhenInstalled(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	results, err := Install([]harness.Harness{harness.Codex}, Options{Home: home, Link: false, SkipMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != skills.StatusInstalled {
		t.Fatalf("install results = %#v", results)
	}

	target := filepath.Join(home, ".codex", "skills", "my-cli")
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("---\nname: my-cli\n---\nstale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err = SyncExisting(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != skills.StatusUpdated {
		t.Fatalf("sync results = %#v", results)
	}
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target did not remain a copy: info=%v err=%v", info, err)
	}
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "---\nname: my-cli\n---\nstale\n") || !strings.Contains(string(data), "my sync --print") {
		t.Fatalf("synced skill content = %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("sync created missing claude harness: %v", err)
	}
}

func TestSyncExistingMigratesManagedLegacyCopy(t *testing.T) {
	home := t.TempDir()
	legacyTarget := filepath.Join(home, ".codex", "skills", "my")
	writeLegacySelfSkill(t, legacyTarget, true)

	results, err := SyncExisting(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStatus(results, skills.StatusMigrated) || !hasStatus(results, skills.StatusUpdated) {
		t.Fatalf("sync results = %#v, want migrated and updated", results)
	}
	if _, err := os.Lstat(legacyTarget); !os.IsNotExist(err) {
		t.Fatalf("legacy target still exists: %v", err)
	}
	target := filepath.Join(home, ".codex", "skills", "my-cli")
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("new target missing: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: my-cli") {
		t.Fatalf("new skill content = %q", string(data))
	}

	results, err = SyncExisting(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("second sync results = %#v, want no-op", results)
	}
}

func TestSyncExistingMigratesManagedLegacySymlink(t *testing.T) {
	home := t.TempDir()
	sourceRoot := filepath.Join(home, ".local", "share", "my-cli", "skills")
	legacySource := filepath.Join(sourceRoot, "my")
	writeLegacySelfSkill(t, legacySource, true)
	if err := os.MkdirAll(filepath.Join(home, ".codex", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyTarget := filepath.Join(home, ".codex", "skills", "my")
	if err := os.Symlink(legacySource, legacyTarget); err != nil {
		t.Fatal(err)
	}

	results, err := SyncExisting(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStatus(results, skills.StatusMigrated) || !hasStatus(results, skills.StatusUpdated) {
		t.Fatalf("sync results = %#v, want migrated and updated", results)
	}
	if _, err := os.Lstat(legacyTarget); !os.IsNotExist(err) {
		t.Fatalf("legacy target still exists: %v", err)
	}
	target := filepath.Join(home, ".codex", "skills", "my-cli")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("new target is not a symlink: %v", info.Mode())
	}
	link, err := os.Readlink(target)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(link) != "my-cli" {
		t.Fatalf("new symlink target = %q, want my-cli source", link)
	}
	if _, err := os.Stat(legacySource); !os.IsNotExist(err) {
		t.Fatalf("legacy source still exists: %v", err)
	}
}

func TestSyncExistingRemovesLegacyDuplicateWhenNewManagedTargetExists(t *testing.T) {
	home := t.TempDir()
	legacyTarget := filepath.Join(home, ".codex", "skills", "my")
	writeLegacySelfSkill(t, legacyTarget, true)
	target := filepath.Join(home, ".codex", "skills", "my-cli")
	writeLegacySelfSkill(t, target, true)

	results, err := SyncExisting(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStatus(results, skills.StatusMigrated) {
		t.Fatalf("sync results = %#v, want migrated", results)
	}
	if _, err := os.Lstat(legacyTarget); !os.IsNotExist(err) {
		t.Fatalf("legacy target still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("new target missing: %v", err)
	}
}

func TestSyncExistingLeavesNonManagedLegacySkill(t *testing.T) {
	home := t.TempDir()
	legacyTarget := filepath.Join(home, ".codex", "skills", "my")
	writeLegacySelfSkill(t, legacyTarget, false)

	results, err := SyncExisting(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("sync results = %#v, want no-op", results)
	}
	if _, err := os.Stat(filepath.Join(legacyTarget, "SKILL.md")); err != nil {
		t.Fatalf("non-managed legacy skill was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "skills", "my-cli")); !os.IsNotExist(err) {
		t.Fatalf("new target should not be created for non-managed legacy skill: %v", err)
	}
}

func writeLegacySelfSkill(t *testing.T, dir string, managed bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: my\n---\nlegacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !managed {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, ".my-cli-managed.json"), []byte(`{
  "installer": "my",
  "version": "test",
  "mode": "copy",
  "source": "/tmp/my-test-source",
  "canonical_id": "my:self"
}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasStatus(results []skills.Result, status string) bool {
	for _, result := range results {
		if result.Status == status {
			return true
		}
	}
	return false
}
