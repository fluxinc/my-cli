package outbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A crash-orphaned or tampered event file must block only its own item:
// ListWithIssues keeps every other item publishable and reports the issue,
// while strict List and the item's own Append stay fail-closed.
func TestCrashOrphanedEventFileBlocksOnlyItsOwnItem(t *testing.T) {
	root := t.TempDir()
	digest := ContentDigest([]byte("payload"))
	good := Event{
		ItemID:       ItemID("org", "decisions", "workspace", "decisions/a.md", digest),
		Organization: "org", Manifest: "m", Domain: "decisions", Mount: "workspace",
		RepoPath: filepath.Join(root, "mount"), RelativePath: "decisions/a.md",
		ContentSHA256: digest, State: StateQueued,
	}
	if _, err := Append(root, good, time.Now()); err != nil {
		t.Fatalf("append: %v", err)
	}
	corruptID := ItemID("org", "decisions", "workspace", "decisions/b.md", digest)
	dir := filepath.Join(Root(root), corruptID, "events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "20260721T000000-000000000Z-0000000000000000.json")
	if err := os.WriteFile(orphan, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	events, issues, err := ListWithIssues(root)
	if err != nil {
		t.Fatalf("ListWithIssues root error: %v", err)
	}
	if len(events) != 1 || events[0].ItemID != good.ItemID {
		t.Fatalf("expected the valid item to survive, got %v", events)
	}
	if len(issues) != 1 || issues[0].ItemID != corruptID {
		t.Fatalf("expected one item issue for %s, got %v", corruptID, issues)
	}

	if _, err := List(root); err == nil {
		t.Fatal("strict List must still fail closed on a corrupt item")
	}

	bad := good
	bad.ItemID = corruptID
	bad.RelativePath = "decisions/b.md"
	if _, err := Append(root, bad, time.Now()); err == nil {
		t.Fatal("appending to a corrupt item must stay blocked")
	}
}

// A pending item may converge directly to merged when the exact content is
// proven at the trusted upstream without this machine opening a PR.
func TestPendingItemMayConvergeDirectlyToMerged(t *testing.T) {
	root := t.TempDir()
	digest := ContentDigest([]byte("payload"))
	event := Event{
		ItemID:       ItemID("org", "decisions", "workspace", "decisions/a.md", digest),
		Organization: "org", Manifest: "m", Domain: "decisions", Mount: "workspace",
		RepoPath: filepath.Join(root, "mount"), RelativePath: "decisions/a.md",
		ContentSHA256: digest, State: StateQueued,
	}
	if _, err := Append(root, event, time.Now()); err != nil {
		t.Fatalf("queue: %v", err)
	}
	event.State = StateMerged
	event.MergedCommit = strings.Repeat("c", 40)
	if _, err := Append(root, event, time.Now()); err != nil {
		t.Fatalf("queued item must accept upstream-proven merged: %v", err)
	}
	event.State = StateSubmitted
	if _, err := Append(root, event, time.Now()); err == nil {
		t.Fatal("merged item must stay terminal")
	}
}
