package domainrecord

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/record"
)

func TestAddListGetCreatesCanonicalAdditiveRecord(t *testing.T) {
	root := record.Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	domain := manifest.RecordDomain{ID: "decisions", Path: "decisions"}
	now := time.Date(2026, 7, 17, 3, 4, 5, 6, time.UTC)
	item, preview, err := Add(root, domain, "choose-safe-default", AddOptions{
		Title: "Choose safe default", Status: "final", Actor: "operator",
		Sources: []string{"git:abc", "git:abc", "email:thread"},
		Related: []string{"bug:123"}, Fields: map[string]string{"decision_type": "security"},
		Now: now, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(item.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("preview wrote file: %v", err)
	}
	for _, want := range []string{"id: 2026-07-17-choose-safe-default", "domain: decisions", "decision_type: security", "created_at: 2026-07-17T03:04:05.000000006Z", "## Evidence"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q:\n%s", want, preview)
		}
	}
	item, _, err = Add(root, domain, "choose-safe-default", AddOptions{
		Title: "Choose safe default", Status: "final", Actor: "operator",
		Sources: []string{"git:abc", "email:thread"}, Related: []string{"bug:123"},
		Fields: map[string]string{"decision_type": "security"}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Add(root, domain, "choose-safe-default", AddOptions{Now: now}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate add error = %v", err)
	}
	items, err := List([]record.Root{root}, domain)
	if err != nil || len(items) != 1 || items[0].Fields["decision_type"] != "security" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	got, content, err := Get([]record.Root{root}, domain, filepath.Base(item.Path))
	if err != nil || got.ID != item.ID || !strings.Contains(content, "# Choose safe default") {
		t.Fatalf("get=%#v err=%v content=%s", got, err, content)
	}
}

func TestAddRejectsReservedOrUnsafeCustomFields(t *testing.T) {
	root := record.Root{Path: t.TempDir()}
	domain := manifest.RecordDomain{ID: "decisions", Path: "decisions"}
	for _, field := range []string{"id", "bad-field", ""} {
		if _, _, err := Add(root, domain, "example", AddOptions{Fields: map[string]string{field: "x"}}); err == nil {
			t.Fatalf("field %q should be rejected", field)
		}
	}
}
