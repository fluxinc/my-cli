package umbrella

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fluxinc/flux/internal/manifest"
)

func TestResolveRootUsesManifestRecommendation(t *testing.T) {
	home := t.TempDir()
	root, err := ResolveRoot(home, "", "", manifest.Document{
		Organization: manifest.Organization{ID: "flux"},
		Umbrella:     manifest.Umbrella{RecommendedPath: "~/acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(home, "acme") {
		t.Fatalf("root = %q", root)
	}
}

func TestEnsureWritesWorkspaceAndState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "flux")
	ws, state, err := Ensure(root, "flux", "acme")
	if err != nil {
		t.Fatal(err)
	}
	if ws.Organization != "flux" || ws.ManifestRef != "acme" {
		t.Fatalf("workspace = %#v", ws)
	}
	if state.SchemaVersion != SchemaVersion || len(state.Mounts) != 0 {
		t.Fatalf("state = %#v", state)
	}
	for _, path := range []string{
		filepath.Join(root, DirName, WorkspaceFile),
		filepath.Join(root, DirName, StateFile),
		filepath.Join(root, "personal"),
		filepath.Join(root, "products"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	if found, ok := FindRoot(filepath.Join(root, "personal")); !ok || found != root {
		t.Fatalf("FindRoot = %q, %v", found, ok)
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
	state = AddSelectedProduct(state, "sample-product")
	state = AddSelectedProduct(state, "sample-product")
	if len(state.SelectedProducts) != 1 {
		t.Fatalf("state = %#v", state)
	}
	state = UpsertMount(state, MountStatus{ID: "product:sample-product", Kind: "product"})
	state = RemoveSelectedProduct(state, "sample-product")
	state = RemoveMount(state, "product:sample-product")
	if len(state.SelectedProducts) != 0 || len(state.Mounts) != 0 {
		t.Fatalf("state = %#v", state)
	}
}
