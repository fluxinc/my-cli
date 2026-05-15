package bundle

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveSkillsSourcePrecedence(t *testing.T) {
	explicit := makeSkillsRoot(t, "explicit-skill")
	envRoot := t.TempDir()
	makeSkill(t, filepath.Join(envRoot, "skills"), "env-skill")
	repoRoot := makeRepoRoot(t)
	nested := filepath.Join(repoRoot, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	source, err := ResolveSkillsSource(ResolveOptions{
		ExplicitSource: explicit,
		Cwd:            nested,
		Home:           t.TempDir(),
		Env:            map[string]string{"FLUX_AI_HOME": envRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != sourceFlag || source.SkillsDir != explicit {
		t.Fatalf("explicit source = %#v, want kind %q dir %q", source, sourceFlag, explicit)
	}

	source, err = ResolveSkillsSource(ResolveOptions{
		Cwd:  nested,
		Home: t.TempDir(),
		Env:  map[string]string{"FLUX_AI_HOME": envRoot},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(envRoot, "skills"); source.Kind != sourceEnv || source.SkillsDir != want {
		t.Fatalf("env source = %#v, want kind %q dir %q", source, sourceEnv, want)
	}

	source, err = ResolveSkillsSource(ResolveOptions{
		Cwd:  nested,
		Home: t.TempDir(),
		Env:  map[string]string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repoRoot, "skills"); source.Kind != sourceRepo || source.SkillsDir != want {
		t.Fatalf("repo source = %#v, want kind %q dir %q", source, sourceRepo, want)
	}
}

func TestResolveSkillsSourceEnvMissingDoesNotFallThrough(t *testing.T) {
	repoRoot := makeRepoRoot(t)
	_, err := ResolveSkillsSource(ResolveOptions{
		Cwd:  repoRoot,
		Home: t.TempDir(),
		Env:  map[string]string{"FLUX_AI_HOME": filepath.Join(t.TempDir(), "missing")},
	})
	if err == nil {
		t.Fatal("missing FLUX_AI_HOME returned nil error")
	}
}

func TestResolveSkillsSourceEmbeddedMaterializeIdempotent(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	source, err := ResolveSkillsSource(ResolveOptions{Cwd: cwd, Home: home, Env: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if source.Kind != sourceEmbed || !source.Materialized {
		t.Fatalf("source = %#v, want embedded materialized", source)
	}
	marker := filepath.Join(source.SkillsDir, MarkerName)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
	old := time.Unix(123, 0)
	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSkillsSource(ResolveOptions{Cwd: cwd, Home: home, Env: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Fatalf("marker mtime changed on idempotent materialize: got %s want %s", info.ModTime(), old)
	}
}

func makeRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+modulePath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	makeSkill(t, filepath.Join(root, "skills"), "repo-skill")
	return root
}

func makeSkillsRoot(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	makeSkill(t, root, name)
	return root
}

func makeSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("---\nname: " + name + "\ndescription: Test skill\n---\n")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}
