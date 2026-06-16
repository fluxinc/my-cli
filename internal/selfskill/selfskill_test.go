package selfskill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/skills"
)

func TestMaterializeBundledMySkill(t *testing.T) {
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
	data, err := os.ReadFile(filepath.Join(sourceRoot, "my", "SKILL.md"))
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

	target := filepath.Join(home, ".codex", "skills", "my")
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

	target := filepath.Join(home, ".codex", "skills", "my")
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("---\nname: my\n---\nstale\n"), 0o644); err != nil {
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
	if strings.Contains(string(data), "---\nname: my\n---\nstale\n") || !strings.Contains(string(data), "my sync --print") {
		t.Fatalf("synced skill content = %q", string(data))
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("sync created missing claude harness: %v", err)
	}
}
