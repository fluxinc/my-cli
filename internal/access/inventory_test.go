package access

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRecordPositivePersistsExactManagedPathAndBaseline(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "umbrella")
	target := filepath.Join(root, "repos", "sample")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Date(2026, 7, 16, 21, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	entry, err := RecordPositive(RecordInput{
		Home:         home,
		Path:         target,
		AllowedRoot:  root,
		Organization: "acme",
		Manifest:     "acme",
		Umbrella:     root,
		SourceRef:    "manifest:acme:repo:sample",
		Kind:         "repo",
		Decision:     positiveDecision("R_repo"),
		CheckedAt:    checkedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	targetReal, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if entry.CanonicalPath != targetReal || entry.AllowedRoot != rootReal || len(entry.References) != 1 || len(entry.Baselines) != 1 {
		t.Fatalf("entry = %#v", entry)
	}
	if entry.Baselines[0].Actor.ID != 17 || entry.Baselines[0].CheckedAt != "2026-07-17T01:00:00Z" {
		t.Fatalf("baseline = %#v", entry.Baselines[0])
	}
	path, err := InventoryPath(home)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("inventory mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRecordPositiveRejectsPathEscapeAndSymlink(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "umbrella")
	outside := filepath.Join(home, "outside")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	input := RecordInput{
		Home: home, Path: outside, AllowedRoot: root, Organization: "acme", Manifest: "acme",
		SourceRef: "manifest:acme:handbook", Kind: "handbook", Decision: positiveDecision("R_repo"),
	}
	if _, err := RecordPositive(input); err == nil || !strings.Contains(err.Error(), "outside allowed root") {
		t.Fatalf("escape err = %v", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(root, "handbook")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	input.Path = link
	if _, err := RecordPositive(input); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink err = %v", err)
	}
}

func TestRecordPositiveRefusesRepositoryRepoint(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "umbrella")
	target := filepath.Join(root, "handbook")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	input := RecordInput{
		Home: home, Path: target, AllowedRoot: root, Organization: "acme", Manifest: "acme",
		SourceRef: "manifest:acme:handbook", Kind: "handbook", Decision: positiveDecision("R_original"),
	}
	if _, err := RecordPositive(input); err != nil {
		t.Fatal(err)
	}
	input.Decision = positiveDecision("R_attacker")
	if _, err := RecordPositive(input); err == nil || !strings.Contains(err.Error(), "cannot be repointed") {
		t.Fatalf("repoint err = %v", err)
	}
}

func positiveDecision(nodeID string) Decision {
	return Decision{
		State: StateAllowed,
		Actor: Actor{ID: 17, NodeID: "U_actor", Login: "operator"},
		Repository: Repository{
			ID: 29, NodeID: nodeID, FullName: "example/control", Private: true, Permission: PermissionAdmin,
		},
	}
}
