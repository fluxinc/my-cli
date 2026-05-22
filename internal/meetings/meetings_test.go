package meetings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListSearchGetAndAdd(t *testing.T) {
	root := Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeMeeting(t, root.Path, "2026-03-12-sampleco-implementation.md", `---
id: 2026-03-12-sampleco-implementation
date: 2026-03-12
title: "SampleCo implementation"
attendees:
  - "Alex Example"
  - "Casey Example"
customer: sampleco
partners: [integratorco, reviewco]
product: sample-product
source_id: spark-123
status: finalized
---

# SampleCo implementation

Promised onboarding review and data cleanup.
`)
	writeMeeting(t, root.Path, "2026-02-01-other.md", `---
date: 2026-02-01
title: "Other"
customer: other
---

No match.
`)

	listed, err := List([]Root{root}, Filter{Customer: "sampleco"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "2026-03-12-sampleco-implementation" {
		t.Fatalf("listed = %#v", listed)
	}

	found, err := Search([]Root{root}, "data cleanup", Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.Contains(found[0].Snippet, "data cleanup") {
		t.Fatalf("found = %#v", found)
	}

	found, err = Search([]Root{root}, "sampleco cleanup", Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("multi-word found = %#v", found)
	}

	found, err = Search([]Root{root}, "sampleco", Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || strings.HasPrefix(found[0].Snippet, "id:") {
		t.Fatalf("frontmatter snippet = %#v, want body snippet", found)
	}

	meeting, content, err := Get([]Root{root}, "2026-03-12-sampleco-implementation")
	if err != nil {
		t.Fatal(err)
	}
	if meeting.Customer != "sampleco" || !strings.Contains(content, "Promised onboarding review") {
		t.Fatalf("meeting = %#v content = %q", meeting, content)
	}
	if strings.Join(meeting.Attendees, ",") != "Alex Example,Casey Example" {
		t.Fatalf("attendees = %#v", meeting.Attendees)
	}
	if strings.Join(meeting.Partners, ",") != "integratorco,reviewco" || meeting.SourceID != "spark-123" {
		t.Fatalf("meeting metadata = %#v", meeting)
	}

	added, scaffold, err := Add(root, "sampleco-followup", AddOptions{
		Date:      "2026-05-13",
		Customer:  "sampleco",
		Attendees: []string{"Alex Example"},
		Partners:  []string{"integratorco"},
		Product:   "sample-product",
		SourceID:  "spark-456",
		DryRun:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID != "2026-05-13-sampleco-followup" || !strings.Contains(scaffold, "## Promises") || !strings.Contains(scaffold, `source_id: spark-456`) {
		t.Fatalf("added = %#v scaffold = %q", added, scaffold)
	}
}

func writeMeeting(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, "meetings")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
