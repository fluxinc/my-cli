package access

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const inventorySchemaVersion = 1

type Inventory struct {
	SchemaVersion int                 `json:"schema_version"`
	Repositories  []ManagedRepository `json:"repositories"`
}

type ManagedRepository struct {
	CanonicalPath string             `json:"canonical_path"`
	AllowedRoot   string             `json:"allowed_root"`
	Repository    Repository         `json:"repository"`
	References    []ManagedReference `json:"references"`
	Baselines     []PositiveBaseline `json:"positive_baselines"`
}

type ManagedReference struct {
	Organization string `json:"organization"`
	Manifest     string `json:"manifest"`
	Umbrella     string `json:"umbrella,omitempty"`
	SourceRef    string `json:"source_ref"`
	Kind         string `json:"kind"`
}

type PositiveBaseline struct {
	Actor      Actor      `json:"actor"`
	Permission Permission `json:"permission"`
	CheckedAt  string     `json:"checked_at"`
}

type RecordInput struct {
	Home         string
	Path         string
	AllowedRoot  string
	Organization string
	Manifest     string
	Umbrella     string
	SourceRef    string
	Kind         string
	Decision     Decision
	CheckedAt    time.Time
}

// InventoryPath returns the machine-local inventory path. This state lives
// outside every organization-controlled manifest and mount.
func InventoryPath(home string) (string, error) {
	homeDir, err := resolveHome(home)
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".local", "state", "my-cli", "access", "inventory.json"), nil
}

func LoadInventory(home string) (Inventory, error) {
	path, err := InventoryPath(home)
	if err != nil {
		return Inventory{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Inventory{SchemaVersion: inventorySchemaVersion}, nil
	}
	if err != nil {
		return Inventory{}, err
	}
	var inventory Inventory
	if err := json.Unmarshal(data, &inventory); err != nil {
		return Inventory{}, fmt.Errorf("read managed repository inventory: %w", err)
	}
	if inventory.SchemaVersion != inventorySchemaVersion {
		return Inventory{}, fmt.Errorf("unsupported managed repository inventory schema %d", inventory.SchemaVersion)
	}
	return inventory, nil
}

// RecordPositive adds or refreshes one managed path only after the provider has
// positively identified both the actor and repository with at least read
// permission.
func RecordPositive(input RecordInput) (ManagedRepository, error) {
	if !input.Decision.Allows(PermissionRead) || input.Decision.Actor.ID == 0 || input.Decision.Repository.NodeID == "" {
		return ManagedRepository{}, fmt.Errorf("managed repository inventory requires a positive provider decision")
	}
	canonicalPath, canonicalRoot, err := canonicalManagedPath(input.Path, input.AllowedRoot)
	if err != nil {
		return ManagedRepository{}, err
	}
	if input.Organization == "" || input.Manifest == "" || input.SourceRef == "" || input.Kind == "" {
		return ManagedRepository{}, fmt.Errorf("managed repository reference requires organization, manifest, source_ref, and kind")
	}
	checkedAt := input.CheckedAt.UTC()
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}

	inventory, err := LoadInventory(input.Home)
	if err != nil {
		return ManagedRepository{}, err
	}
	index := -1
	for i := range inventory.Repositories {
		if inventory.Repositories[i].CanonicalPath == canonicalPath {
			index = i
			break
		}
	}
	if index < 0 {
		inventory.Repositories = append(inventory.Repositories, ManagedRepository{
			CanonicalPath: canonicalPath,
			AllowedRoot:   canonicalRoot,
			Repository:    input.Decision.Repository,
		})
		index = len(inventory.Repositories) - 1
	}
	entry := &inventory.Repositories[index]
	if entry.Repository.NodeID != "" && entry.Repository.NodeID != input.Decision.Repository.NodeID {
		return ManagedRepository{}, fmt.Errorf("managed path %s was positively bound to repository %s and cannot be repointed to %s", canonicalPath, entry.Repository.NodeID, input.Decision.Repository.NodeID)
	}
	if entry.AllowedRoot != canonicalRoot {
		return ManagedRepository{}, fmt.Errorf("managed path %s changed allowed root from %s to %s", canonicalPath, entry.AllowedRoot, canonicalRoot)
	}
	entry.Repository = input.Decision.Repository
	reference := ManagedReference{
		Organization: input.Organization,
		Manifest:     input.Manifest,
		Umbrella:     input.Umbrella,
		SourceRef:    input.SourceRef,
		Kind:         input.Kind,
	}
	entry.References = upsertReference(entry.References, reference)
	entry.Baselines = upsertBaseline(entry.Baselines, PositiveBaseline{
		Actor:      input.Decision.Actor,
		Permission: input.Decision.Repository.Permission,
		CheckedAt:  checkedAt.Format(time.RFC3339Nano),
	})
	sortInventory(&inventory)
	if err := saveInventory(input.Home, inventory); err != nil {
		return ManagedRepository{}, err
	}
	for _, saved := range inventory.Repositories {
		if saved.CanonicalPath == canonicalPath {
			return saved, nil
		}
	}
	return ManagedRepository{}, fmt.Errorf("recorded managed repository disappeared from inventory")
}

func canonicalManagedPath(target, allowedRoot string) (string, string, error) {
	if strings.TrimSpace(target) == "" || strings.TrimSpace(allowedRoot) == "" {
		return "", "", fmt.Errorf("managed path and allowed root are required")
	}
	rootAbs, err := filepath.Abs(allowedRoot)
	if err != nil {
		return "", "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve managed root: %w", err)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(targetAbs)
	if err != nil {
		return "", "", fmt.Errorf("inspect managed path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("managed path must not be a symlink: %s", targetAbs)
	}
	targetReal, err := filepath.EvalSymlinks(targetAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve managed path: %w", err)
	}
	if !strictlyWithin(rootReal, targetReal) {
		return "", "", fmt.Errorf("managed path %s is outside allowed root %s", targetReal, rootReal)
	}
	return targetReal, rootReal, nil
}

func upsertReference(references []ManagedReference, next ManagedReference) []ManagedReference {
	for i := range references {
		if references[i].Organization == next.Organization && references[i].Manifest == next.Manifest && references[i].Umbrella == next.Umbrella && references[i].SourceRef == next.SourceRef {
			references[i] = next
			return references
		}
	}
	return append(references, next)
}

func upsertBaseline(baselines []PositiveBaseline, next PositiveBaseline) []PositiveBaseline {
	for i := range baselines {
		if baselines[i].Actor.ID == next.Actor.ID {
			baselines[i] = next
			return baselines
		}
	}
	return append(baselines, next)
}

func sortInventory(inventory *Inventory) {
	for i := range inventory.Repositories {
		sort.Slice(inventory.Repositories[i].References, func(a, b int) bool {
			left := inventory.Repositories[i].References[a]
			right := inventory.Repositories[i].References[b]
			return left.Organization+"\x00"+left.Manifest+"\x00"+left.SourceRef < right.Organization+"\x00"+right.Manifest+"\x00"+right.SourceRef
		})
		sort.Slice(inventory.Repositories[i].Baselines, func(a, b int) bool {
			return inventory.Repositories[i].Baselines[a].Actor.ID < inventory.Repositories[i].Baselines[b].Actor.ID
		})
	}
	sort.Slice(inventory.Repositories, func(i, j int) bool {
		return inventory.Repositories[i].CanonicalPath < inventory.Repositories[j].CanonicalPath
	})
}

func saveInventory(home string, inventory Inventory) error {
	path, err := InventoryPath(home)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".inventory-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return os.Chmod(path, 0o600)
}

func resolveHome(home string) (string, error) {
	if home != "" {
		return filepath.Abs(home)
	}
	return os.UserHomeDir()
}

func strictlyWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
