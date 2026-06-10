// Package umbrella manages a local organization workspace envelope.
package umbrella

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fluxinc/our-ai/internal/manifest"
)

const (
	DirName       = ".our"
	WorkspaceFile = "workspace.json"
	StateFile     = "state.json"
	SchemaVersion = 1
)

// Workspace is the stable identity of a local umbrella.
type Workspace struct {
	SchemaVersion int    `json:"schema_version"`
	Organization  string `json:"organization"`
	ManifestRef   string `json:"manifest_ref"`
	WorkspaceRoot string `json:"workspace_root"`
	CreatedAt     string `json:"created_at"`
}

// State is the dynamic, per-machine state for an umbrella.
type State struct {
	SchemaVersion    int           `json:"schema_version"`
	SelectedProducts []string      `json:"selected_products"`
	Mounts           []MountStatus `json:"mounts"`
}

// MountStatus records the last known state of one local mount.
type MountStatus struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	SourceRef  string `json:"source_ref"`
	Status     string `json:"status"`
	LastSync   string `json:"last_sync"`
	LastError  string `json:"last_error"`
	LastCommit string `json:"last_commit"`
}

// ResolveRoot returns the umbrella root using explicit path, walk-up discovery,
// manifest recommendation, then the organization id under home.
func ResolveRoot(home, cwd, explicit string, doc manifest.Document) (string, error) {
	homeDir, err := resolveHome(home)
	if err != nil {
		return "", err
	}
	if explicit != "" {
		return expandPath(homeDir, explicit)
	}
	if found, ok := FindRoot(cwd); ok {
		return found, nil
	}
	if doc.Umbrella.RecommendedPath != "" {
		return expandPath(homeDir, doc.Umbrella.RecommendedPath)
	}
	return filepath.Join(homeDir, doc.Organization.ID), nil
}

// FindRoot walks up from start looking for .our/workspace.json.
func FindRoot(start string) (string, bool) {
	if start == "" {
		start = "."
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(workspacePath(dir)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Ensure creates the umbrella directories and writes workspace/state files.
func Ensure(root, organization, manifestRef string) (Workspace, State, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return Workspace{}, State{}, err
	}
	if err := os.MkdirAll(filepath.Join(rootAbs, DirName), 0o755); err != nil {
		return Workspace{}, State{}, err
	}
	if err := migrateLegacyProducts(rootAbs); err != nil {
		return Workspace{}, State{}, err
	}
	for _, dir := range []string{"personal", "repos"} {
		if err := os.MkdirAll(filepath.Join(rootAbs, dir), 0o755); err != nil {
			return Workspace{}, State{}, err
		}
	}

	ws, err := LoadWorkspace(rootAbs)
	if errors.Is(err, os.ErrNotExist) {
		ws = Workspace{
			SchemaVersion: SchemaVersion,
			CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		}
	} else if err != nil {
		return Workspace{}, State{}, err
	}
	ws.SchemaVersion = SchemaVersion
	ws.Organization = organization
	ws.ManifestRef = manifestRef
	ws.WorkspaceRoot = rootAbs
	if err := SaveWorkspace(rootAbs, ws); err != nil {
		return Workspace{}, State{}, err
	}

	state, err := LoadState(rootAbs)
	if errors.Is(err, os.ErrNotExist) {
		state = State{SchemaVersion: SchemaVersion}
	} else if err != nil {
		return Workspace{}, State{}, err
	}
	state.SchemaVersion = SchemaVersion
	if state.SelectedProducts == nil {
		state.SelectedProducts = []string{}
	}
	if state.Mounts == nil {
		state.Mounts = []MountStatus{}
	}
	if err := SaveState(rootAbs, state); err != nil {
		return Workspace{}, State{}, err
	}
	return ws, state, nil
}

// LoadWorkspace reads the identity file under root.
func LoadWorkspace(root string) (Workspace, error) {
	data, err := os.ReadFile(workspacePath(root))
	if err != nil {
		return Workspace{}, err
	}
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return Workspace{}, fmt.Errorf("read %s: %w", workspacePath(root), err)
	}
	return ws, nil
}

// SaveWorkspace writes the identity file under root.
func SaveWorkspace(root string, ws Workspace) error {
	return writeJSON(workspacePath(root), ws)
}

// LoadState reads the dynamic state file under root.
func LoadState(root string) (State, error) {
	data, err := os.ReadFile(statePath(root))
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("read %s: %w", statePath(root), err)
	}
	return state, nil
}

// SaveState writes the dynamic state file under root.
func SaveState(root string, state State) error {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = SchemaVersion
	}
	if state.SelectedProducts == nil {
		state.SelectedProducts = []string{}
	}
	if state.Mounts == nil {
		state.Mounts = []MountStatus{}
	}
	return writeJSON(statePath(root), state)
}

// UpsertMount replaces or appends one mount status.
func UpsertMount(state State, status MountStatus) State {
	for i, existing := range state.Mounts {
		if existing.ID == status.ID {
			state.Mounts[i] = status
			return state
		}
	}
	state.Mounts = append(state.Mounts, status)
	return state
}

// RemoveMount drops one mount status by id.
func RemoveMount(state State, id string) State {
	out := state.Mounts[:0]
	for _, existing := range state.Mounts {
		if existing.ID != id {
			out = append(out, existing)
		}
	}
	state.Mounts = out
	return state
}

// AddSelectedProduct records one selected product id idempotently.
func AddSelectedProduct(state State, id string) State {
	for _, existing := range state.SelectedProducts {
		if existing == id {
			return state
		}
	}
	state.SelectedProducts = append(state.SelectedProducts, id)
	return state
}

// RemoveSelectedProduct drops one selected product id.
func RemoveSelectedProduct(state State, id string) State {
	out := state.SelectedProducts[:0]
	for _, existing := range state.SelectedProducts {
		if existing != id {
			out = append(out, existing)
		}
	}
	state.SelectedProducts = out
	return state
}

// MountPath returns the filesystem path for one manifest mount.
func MountPath(root, id string) string {
	return filepath.Join(root, id)
}

// WorkspacePath returns the canonical checkout location for a self-mounted
// organization repository (manifest, catalog, and content in one repo). It is
// named workspace rather than after any single mount because several mounts
// may be views into the same checkout.
func WorkspacePath(root string) string {
	return filepath.Join(root, "workspace")
}

// ProductPath returns the filesystem path for one selected product clone.
// New clones default to repos/<id>; an existing legacy products/<id> checkout
// keeps resolving until it is migrated.
func ProductPath(root, id string) string {
	preferred := filepath.Join(root, "repos", id)
	if _, err := os.Stat(preferred); err == nil {
		return preferred
	}
	legacy := filepath.Join(root, "products", id)
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return preferred
}

// migrateLegacyProducts moves clones from the legacy products/ directory into
// repos/ and removes products/ once it is empty. Entries that already exist
// under repos/ are left in place.
func migrateLegacyProducts(root string) error {
	legacyDir := filepath.Join(root, "products")
	entries, err := os.ReadDir(legacyDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(root, "repos"), 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		target := filepath.Join(root, "repos", entry.Name())
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if err := os.Rename(filepath.Join(legacyDir, entry.Name()), target); err != nil {
			return err
		}
	}
	remaining, err := os.ReadDir(legacyDir)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return os.Remove(legacyDir)
	}
	return nil
}

func workspacePath(root string) string {
	return filepath.Join(root, DirName, WorkspaceFile)
}

func statePath(root string) string {
	return filepath.Join(root, DirName, StateFile)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return os.UserHomeDir()
}

func expandPath(home, path string) (string, error) {
	if path == "~" {
		return home, nil
	}
	if len(path) > 2 && path[:2] == "~/" {
		return filepath.Join(home, path[2:]), nil
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Abs(path)
}
