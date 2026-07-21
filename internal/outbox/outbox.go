// Package outbox stores an append-only local publication event ledger. It
// never stores record contents; the mounted Git working tree remains the
// recoverable source, and reconciliation can recreate a missing queued event.
package outbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/safefs"
)

const SchemaVersion = 1

const (
	StateQueued        = "queued"
	StateAttemptFailed = "attempt-failed"
	StateSubmitted     = "submitted"
	StateMerged        = "merged"
)

type Event struct {
	SchemaVersion  int      `json:"schema_version"`
	ItemID         string   `json:"item_id"`
	Organization   string   `json:"organization"`
	Manifest       string   `json:"manifest"`
	Domain         string   `json:"domain"`
	Mount          string   `json:"mount"`
	RepoPath       string   `json:"repo_path"`
	RelativePath   string   `json:"relative_path"`
	ContentSHA256  string   `json:"content_sha256"`
	State          string   `json:"state"`
	OccurredAt     string   `json:"occurred_at"`
	PRURL          string   `json:"pr_url,omitempty"`
	PRHeadSHA      string   `json:"pr_head_sha,omitempty"`
	PRBase         string   `json:"pr_base,omitempty"`
	MergedCommit   string   `json:"merged_commit,omitempty"`
	PublishedPaths []string `json:"published_paths,omitempty"`
	Message        string   `json:"message,omitempty"`
	EventPath      string   `json:"event_path,omitempty"`
}

func ItemID(organization, domain, mount, relativePath, contentDigest string) string {
	sum := sha256.Sum256([]byte(organization + "\x00" + domain + "\x00" + mount + "\x00" + filepath.ToSlash(relativePath) + "\x00" + contentDigest))
	return hex.EncodeToString(sum[:])
}

func ContentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func Root(umbrellaRoot string) string {
	return filepath.Join(umbrellaRoot, ".my-cli", "outbox")
}

func Append(umbrellaRoot string, event Event, now time.Time) (Event, error) {
	if event.ItemID == "" || event.Organization == "" || event.Manifest == "" || event.Domain == "" || event.Mount == "" || event.RepoPath == "" || event.RelativePath == "" || event.ContentSHA256 == "" {
		return Event{}, fmt.Errorf("outbox event identity is incomplete")
	}
	if !validItemID(event.ItemID) {
		return Event{}, fmt.Errorf("outbox item id must be 64 lowercase hexadecimal characters")
	}
	if !validContentDigest(event.ContentSHA256) {
		return Event{}, fmt.Errorf("outbox content digest must be sha256 followed by 64 lowercase hexadecimal characters")
	}
	if !validRelativePath(event.RelativePath) {
		return Event{}, fmt.Errorf("outbox relative path must stay inside the repository")
	}
	expectedID := ItemID(event.Organization, event.Domain, event.Mount, event.RelativePath, event.ContentSHA256)
	if event.ItemID != expectedID {
		return Event{}, fmt.Errorf("outbox item id does not match its immutable identity fields")
	}
	if !validState(event.State) {
		return Event{}, fmt.Errorf("unsupported outbox state %q", event.State)
	}
	if now.IsZero() {
		now = time.Now()
	}
	event.SchemaVersion = SchemaVersion
	event.OccurredAt = now.UTC().Format(time.RFC3339Nano)
	event.EventPath = ""
	dir, err := ensureEventDirectory(umbrellaRoot, event.ItemID)
	if err != nil {
		return Event{}, err
	}
	current, ok, err := Current(umbrellaRoot, event.ItemID)
	if err != nil {
		return Event{}, err
	}
	if ok && !sameIdentity(current, event) {
		return Event{}, fmt.Errorf("outbox event cannot change immutable identity fields")
	}
	if err := validTransition(current.State, ok, event.State); err != nil {
		return Event{}, err
	}
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return Event{}, err
	}
	data = append(data, '\n')
	for attempts := 0; attempts < 8; attempts++ {
		name, err := eventFileName(now)
		if err != nil {
			return Event{}, err
		}
		path := filepath.Join(dir, name)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return Event{}, err
		}
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(path)
			return Event{}, err
		}
		if err := safefs.SyncDirectory(dir); err != nil {
			_ = os.Remove(path)
			return Event{}, err
		}
		event.EventPath = path
		return event, nil
	}
	return Event{}, fmt.Errorf("could not allocate unique outbox event path")
}

func Current(umbrellaRoot, itemID string) (Event, bool, error) {
	if !validItemID(itemID) {
		return Event{}, false, fmt.Errorf("invalid outbox item id")
	}
	itemDir := filepath.Join(Root(umbrellaRoot), itemID)
	if err := rejectSymlinkDirectory(itemDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Event{}, false, err
	}
	if err := rejectSymlinkDirectory(filepath.Join(itemDir, "events")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Event{}, false, err
	}
	events, err := readItemEvents(umbrellaRoot, itemID)
	if err != nil || len(events) == 0 {
		return Event{}, false, err
	}
	return events[len(events)-1], true, nil
}

// ItemIssue reports one outbox item whose event log could not be read as a
// valid append-only sequence. The item itself stays blocked (Append re-reads
// its log), but other items keep publishing.
type ItemIssue struct {
	ItemID string
	Err    error
}

func List(umbrellaRoot string) ([]Event, error) {
	events, issues, err := ListWithIssues(umbrellaRoot)
	if err != nil {
		return nil, err
	}
	if len(issues) != 0 {
		return nil, issues[0].Err
	}
	return events, nil
}

// ListWithIssues enumerates current item states while item-scoping event-log
// corruption: a crash-orphaned or tampered event file blocks only its own
// item instead of making the whole outbox unreadable.
func ListWithIssues(umbrellaRoot string) ([]Event, []ItemIssue, error) {
	if err := rejectSymlinkDirectory(filepath.Join(umbrellaRoot, ".my-cli")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if err := rejectSymlinkDirectory(Root(umbrellaRoot)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	entries, err := os.ReadDir(Root(umbrellaRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var out []Event
	var issues []ItemIssue
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		current, ok, err := Current(umbrellaRoot, entry.Name())
		if err != nil {
			issues = append(issues, ItemIssue{ItemID: entry.Name(), Err: err})
			continue
		}
		if ok {
			out = append(out, current)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OccurredAt != out[j].OccurredAt {
			return out[i].OccurredAt < out[j].OccurredAt
		}
		return out[i].ItemID < out[j].ItemID
	})
	return out, issues, nil
}

func ensureEventDirectory(umbrellaRoot, itemID string) (string, error) {
	current := filepath.Clean(umbrellaRoot)
	for _, component := range []string{".my-cli", "outbox", itemID, "events"} {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return "", err
			}
			if err := safefs.SyncDirectory(filepath.Dir(current)); err != nil {
				return "", err
			}
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("outbox path %s must be a real directory, not a symlink or file", current)
		}
		if component != ".my-cli" {
			if err := os.Chmod(current, 0o700); err != nil {
				return "", err
			}
		}
	}
	return current, nil
}

func rejectSymlinkDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("outbox path %s must be a real directory, not a symlink or file", path)
	}
	return nil
}

func validItemID(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func validContentDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	return validItemID(strings.TrimPrefix(value, "sha256:"))
}

func validRelativePath(value string) bool {
	raw := strings.TrimSpace(value)
	if strings.Contains(raw, "\\") {
		return false
	}
	value = filepath.ToSlash(raw)
	clean := filepath.ToSlash(filepath.Clean(value))
	return value != "" && value == clean && clean != "." && clean != ".." &&
		!strings.HasPrefix(clean, "../") && !filepath.IsAbs(value)
}

func sameIdentity(left, right Event) bool {
	return left.ItemID == right.ItemID && left.Organization == right.Organization &&
		left.Manifest == right.Manifest && left.Domain == right.Domain &&
		left.Mount == right.Mount && filepath.Clean(left.RepoPath) == filepath.Clean(right.RepoPath) &&
		filepath.ToSlash(left.RelativePath) == filepath.ToSlash(right.RelativePath) &&
		left.ContentSHA256 == right.ContentSHA256
}

func readItemEvents(umbrellaRoot, itemID string) ([]Event, error) {
	dir := filepath.Join(Root(umbrellaRoot), itemID, "events")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []Event
	var previous Event
	var havePrevious bool
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var event Event
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("read outbox event %s: %w", path, err)
		}
		if event.SchemaVersion != SchemaVersion || event.ItemID != itemID || !validState(event.State) ||
			!validContentDigest(event.ContentSHA256) || !validRelativePath(event.RelativePath) ||
			ItemID(event.Organization, event.Domain, event.Mount, event.RelativePath, event.ContentSHA256) != itemID {
			return nil, fmt.Errorf("outbox event %s has invalid schema, item id, or state", path)
		}
		if havePrevious {
			if !sameIdentity(previous, event) {
				return nil, fmt.Errorf("outbox event %s changes immutable identity fields", path)
			}
			if err := validTransition(previous.State, true, event.State); err != nil {
				return nil, fmt.Errorf("outbox event %s: %w", path, err)
			}
		} else if err := validTransition("", false, event.State); err != nil {
			return nil, fmt.Errorf("outbox event %s: %w", path, err)
		}
		event.EventPath = path
		events = append(events, event)
		previous = event
		havePrevious = true
	}
	sort.Slice(events, func(i, j int) bool { return events[i].EventPath < events[j].EventPath })
	return events, nil
}

func validTransition(current string, exists bool, next string) error {
	if !exists {
		if next != StateQueued {
			return fmt.Errorf("first outbox event must be queued")
		}
		return nil
	}
	switch current {
	case StateQueued, StateAttemptFailed:
		// merged directly from a pending state records proof that the exact
		// content already reached the trusted upstream through another
		// publication path (for example my sync --push or another machine).
		if next == StateAttemptFailed || next == StateSubmitted || next == StateMerged {
			return nil
		}
	case StateSubmitted:
		if next == StateMerged {
			return nil
		}
	}
	return fmt.Errorf("invalid outbox transition %s -> %s", current, next)
}

func validState(value string) bool {
	switch value {
	case StateQueued, StateAttemptFailed, StateSubmitted, StateMerged:
		return true
	default:
		return false
	}
}

func eventFileName(now time.Time) (string, error) {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	timestamp := strings.ReplaceAll(now.UTC().Format("20060102T150405.000000000Z"), ".", "-")
	return timestamp + "-" + hex.EncodeToString(suffix[:]) + ".json", nil
}
