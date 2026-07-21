package outbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAppendOnlyOutboxTransitionsAndKeepsContentOut(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 17, 4, 5, 6, 0, time.UTC)
	digest := ContentDigest([]byte("private record body"))
	event := Event{
		ItemID:       ItemID("acme", "decisions", "handbook", "decisions/one.md", digest),
		Organization: "acme", Manifest: "acme", Domain: "decisions", Mount: "handbook",
		RepoPath: filepath.Join(root, "handbook"), RelativePath: "decisions/one.md",
		ContentSHA256: digest, State: StateQueued,
	}
	queued, err := Append(root, event, now)
	if err != nil {
		t.Fatal(err)
	}
	event.State = StateAttemptFailed
	event.Message = "offline"
	if _, err := Append(root, event, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	event.State = StateSubmitted
	event.PRURL = "https://github.com/example/records/pull/1"
	event.PRHeadSHA = strings.Repeat("a", 40)
	event.PRBase = "main"
	submitted, err := Append(root, event, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Append(root, event, now.Add(3*time.Second)); err == nil || !strings.Contains(err.Error(), "invalid outbox transition") {
		t.Fatalf("duplicate submitted transition error = %v", err)
	}
	current, ok, err := Current(root, event.ItemID)
	if err != nil || !ok || current.State != StateSubmitted || current.PRURL != submitted.PRURL {
		t.Fatalf("current=%#v ok=%v err=%v", current, ok, err)
	}
	events, err := os.ReadDir(filepath.Dir(queued.EventPath))
	if err != nil || len(events) != 3 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	for _, entry := range events {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(queued.EventPath), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private record body") {
			t.Fatal("outbox copied record content outside its governed mount")
		}
	}
}

func TestOutboxRejectsNonQueuedFirstEvent(t *testing.T) {
	digest := ContentDigest([]byte("body"))
	event := Event{
		ItemID: ItemID("acme", "decisions", "handbook", "decisions/a.md", digest), Organization: "acme", Manifest: "acme", Domain: "decisions",
		Mount: "handbook", RepoPath: "/tmp/example", RelativePath: "decisions/a.md",
		ContentSHA256: digest, State: StateSubmitted,
	}
	if _, err := Append(t.TempDir(), event, time.Now()); err == nil || !strings.Contains(err.Error(), "first outbox event") {
		t.Fatalf("error = %v", err)
	}
}

func TestOutboxRejectsItemIDMismatchAndIdentityMutation(t *testing.T) {
	root := t.TempDir()
	digest := ContentDigest([]byte("body"))
	event := Event{
		ItemID:       ItemID("acme", "decisions", "handbook", "decisions/a.md", digest),
		Organization: "acme", Manifest: "acme", Domain: "decisions", Mount: "handbook",
		RepoPath: filepath.Join(root, "handbook"), RelativePath: "decisions/a.md",
		ContentSHA256: digest, State: StateQueued,
	}
	mismatch := event
	mismatch.RelativePath = "decisions/b.md"
	if _, err := Append(root, mismatch, time.Now()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("item id mismatch error = %v", err)
	}
	if _, err := Append(root, event, time.Now()); err != nil {
		t.Fatal(err)
	}
	mutated := event
	mutated.Manifest = "other"
	mutated.ItemID = ItemID(mutated.Organization, mutated.Domain, mutated.Mount, mutated.RelativePath, mutated.ContentSHA256)
	// Point the mutation at the original item directory to exercise frozen
	// identity validation rather than only the recomputed-id guard.
	mutated.ItemID = event.ItemID
	mutated.State = StateAttemptFailed
	if _, err := Append(root, mutated, time.Now().Add(time.Second)); err == nil || !strings.Contains(err.Error(), "immutable identity") {
		t.Fatalf("identity mutation error = %v", err)
	}
}

func TestOutboxRejectsEscapingRelativePath(t *testing.T) {
	root := t.TempDir()
	digest := ContentDigest([]byte("body"))
	event := Event{
		Organization: "acme", Manifest: "acme", Domain: "decisions", Mount: "handbook",
		RepoPath: root, RelativePath: "../outside.md", ContentSHA256: digest, State: StateQueued,
	}
	event.ItemID = ItemID(event.Organization, event.Domain, event.Mount, event.RelativePath, digest)
	if _, err := Append(root, event, time.Now()); err == nil || !strings.Contains(err.Error(), "stay inside") {
		t.Fatalf("escaping path error = %v", err)
	}
}

func TestOutboxRejectsSymlinkEventDirectory(t *testing.T) {
	root := t.TempDir()
	digest := ContentDigest([]byte("body"))
	id := ItemID("acme", "decisions", "handbook", "decisions/a.md", digest)
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".my-cli", "outbox", id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, ".my-cli", "outbox", id, "events")); err != nil {
		t.Fatal(err)
	}
	event := Event{
		ItemID: id, Organization: "acme", Manifest: "acme", Domain: "decisions",
		Mount: "handbook", RepoPath: "/tmp/example", RelativePath: "decisions/a.md",
		ContentSHA256: digest, State: StateQueued,
	}
	if _, err := Append(root, event, time.Now()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}
