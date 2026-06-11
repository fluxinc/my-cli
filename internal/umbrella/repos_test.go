package umbrella

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoPathDefaultsToRepos(t *testing.T) {
	root := t.TempDir()
	if got := RepoPath(root, "alpha"); got != filepath.Join(root, "repos", "alpha") {
		t.Fatalf("RepoPath = %q", got)
	}
}

func TestRepoPathPrefersExistingLegacyProductsClone(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "products", "alpha")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := RepoPath(root, "alpha"); got != legacy {
		t.Fatalf("RepoPath = %q, want legacy %q", got, legacy)
	}
}

func TestLoadStateMigratesSelectedProductsAndProductMounts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, DirName, StateFile)
	if err := os.WriteFile(statePath, []byte(`{
  "schema_version": 1,
  "selected_products": ["alpha", "beta"],
  "mounts": [
    {
      "id": "product:alpha",
      "kind": "product",
      "source_ref": "manifest:acme:product:alpha",
      "status": "synced"
    },
    {
      "id": "handbook",
      "kind": "handbook",
      "source_ref": "manifest:acme:handbook",
      "status": "synced"
    }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	state, err := LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.SelectedRepos) != 2 || state.SelectedRepos[0] != "alpha" || state.SelectedRepos[1] != "beta" {
		t.Fatalf("selected repos = %#v", state.SelectedRepos)
	}
	var repoMount *MountStatus
	for i := range state.Mounts {
		if state.Mounts[i].ID == "repo:alpha" {
			repoMount = &state.Mounts[i]
		}
		if state.Mounts[i].ID == "product:alpha" || state.Mounts[i].Kind == "product" {
			t.Fatalf("unmigrated product mount remains: %#v", state.Mounts[i])
		}
	}
	if repoMount == nil || repoMount.Kind != "repo" {
		t.Fatalf("repo mount = %#v; mounts = %#v", repoMount, state.Mounts)
	}
}

func TestSaveStateRoundTripsSelectedRepos(t *testing.T) {
	root := t.TempDir()
	state := State{SelectedRepos: []string{"alpha"}}
	if err := SaveState(root, state); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.SelectedRepos) != 1 || loaded.SelectedRepos[0] != "alpha" {
		t.Fatalf("loaded = %#v", loaded)
	}
	data, err := os.ReadFile(filepath.Join(root, DirName, StateFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || !strings.Contains(string(data), "selected_repos") || strings.Contains(string(data), "selected_products") {
		t.Fatalf("state file = %s", data)
	}
}
