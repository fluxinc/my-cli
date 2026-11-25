package selfskill

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/skills"
)

func TestInstallAntigravityUsesGeminiConfigAndCleansLegacyAgentsSkill(t *testing.T) {
	home := t.TempDir()
	// Antigravity creates ~/.gemini before ~/.gemini/config exists.
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Managed legacy link in ~/.agents/skills/my-cli should be removed.
	oldManaged := filepath.Join(home, ".agents", "skills", "my-cli")
	writeLegacySelfSkill(t, oldManaged, true)
	// Unmanaged user entry in ~/.gemini/skills/my-cli must be preserved.
	unmanaged := filepath.Join(home, ".gemini", "skills", "my-cli")
	writeLegacySelfSkill(t, unmanaged, false)

	results, err := Install([]harness.Harness{harness.Antigravity}, Options{Home: home, Link: true, SkipMissing: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStatus(results, skills.StatusInstalled) || !hasStatus(results, skills.StatusMigrated) {
		t.Fatalf("install results = %#v, want installed and migrated", results)
	}
	target := filepath.Join(home, ".gemini", "config", "skills", "my-cli")
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("antigravity target not installed as symlink: info=%v err=%v", info, err)
	}
	if _, err := os.Lstat(oldManaged); !os.IsNotExist(err) {
		t.Fatalf("managed legacy ~/.agents/skills/my-cli still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unmanaged, "SKILL.md")); err != nil {
		t.Fatalf("unmanaged ~/.gemini/skills/my-cli was removed: %v", err)
	}

	rows, err := Inspect([]harness.Harness{harness.Antigravity}, Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "installed" || rows[0].TargetPath != target {
		t.Fatalf("inspect rows = %#v", rows)
	}
}

func TestSyncExistingMigratesAntigravityLegacyAgentsSkill(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldManaged := filepath.Join(home, ".agents", "skills", "my-cli")
	writeLegacySelfSkill(t, oldManaged, true)

	results, err := SyncExisting(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStatus(results, skills.StatusInstalled) || !hasStatus(results, skills.StatusMigrated) {
		t.Fatalf("sync results = %#v, want installed and migrated", results)
	}
	target := filepath.Join(home, ".gemini", "config", "skills", "my-cli")
	if _, err := os.Stat(filepath.Join(target, "SKILL.md")); err != nil {
		t.Fatalf("antigravity target missing: %v", err)
	}
	if _, err := os.Lstat(oldManaged); !os.IsNotExist(err) {
		t.Fatalf("old managed ~/.agents/skills/my-cli still exists: %v", err)
	}
}
