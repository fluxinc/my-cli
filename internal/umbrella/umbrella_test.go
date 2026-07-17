package umbrella

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestResolveRootUsesManifestRecommendation(t *testing.T) {
	home := t.TempDir()
	root, err := ResolveRoot(home, "", "", manifest.Document{
		Organization: manifest.Organization{ID: "my"},
		Umbrella:     manifest.Umbrella{RecommendedPath: "~/acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(home, "acme") {
		t.Fatalf("root = %q", root)
	}
}

func TestResolveRootIgnoresUnrelatedDiscoveredUmbrella(t *testing.T) {
	home := t.TempDir()
	other := filepath.Join(home, "other")
	if _, _, err := Ensure(other, "other", "other"); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(other, "repos", "sample")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := manifest.Document{
		Organization: manifest.Organization{ID: "acme"},
		Umbrella:     manifest.Umbrella{RecommendedPath: "~/acme"},
	}
	got, err := ResolveRoot(home, nested, "", doc)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "acme")
	if got != want {
		t.Fatalf("ResolveRoot = %q, want unrelated umbrella ignored in favor of %q", got, want)
	}
}

func TestEnsureWritesWorkspaceAndState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "my")
	ws, state, err := Ensure(root, "my", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Organization != "my" || ws.ManifestRef != "acme" {
		t.Fatalf("workspace = %#v", ws)
	}
	if state.SchemaVersion != SchemaVersion || len(state.Mounts) != 0 {
		t.Fatalf("state = %#v", state)
	}
	for _, path := range []string{
		filepath.Join(root, DirName, WorkspaceFile),
		filepath.Join(root, DirName, StateFile),
		filepath.Join(root, "personal"),
		filepath.Join(root, "repos"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "products")); !os.IsNotExist(err) {
		t.Fatalf("legacy products dir scaffolded: %v", err)
	}
	if found, ok := FindRoot(filepath.Join(root, "personal")); !ok || found != root {
		t.Fatalf("FindRoot = %q, %v", found, ok)
	}
}

func TestEnsureMigratesLegacyProductsToRepos(t *testing.T) {
	root := filepath.Join(t.TempDir(), "my")
	legacyFile := filepath.Join(root, "products", "sample", "README.md")
	if err := os.MkdirAll(filepath.Dir(legacyFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyFile, []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(root, "my", "acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "repos", "sample", "README.md")); err != nil {
		t.Fatalf("legacy clone not migrated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "products")); !os.IsNotExist(err) {
		t.Fatalf("legacy products dir still present: %v", err)
	}
}

func TestUpsertMount(t *testing.T) {
	state := State{SchemaVersion: SchemaVersion}
	state = UpsertMount(state, MountStatus{ID: "handbook", Kind: "handbook", Status: "synced"})
	state = UpsertMount(state, MountStatus{ID: "handbook", Kind: "handbook", Status: "failed"})
	if len(state.Mounts) != 1 || state.Mounts[0].Status != "failed" {
		t.Fatalf("state = %#v", state)
	}
}

func TestProductStateHelpers(t *testing.T) {
	state := State{SchemaVersion: SchemaVersion}
	state = AddSelectedRepo(state, "sample-product")
	state = AddSelectedRepo(state, "sample-product")
	if len(state.SelectedRepos) != 1 {
		t.Fatalf("state = %#v", state)
	}
	state = UpsertMount(state, MountStatus{ID: "product:sample-product", Kind: "product"})
	state = RemoveSelectedRepo(state, "sample-product")
	state = RemoveMount(state, "product:sample-product")
	if len(state.SelectedRepos) != 0 || len(state.Mounts) != 0 {
		t.Fatalf("state = %#v", state)
	}
}
