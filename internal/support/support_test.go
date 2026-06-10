package support

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListSearchGetAndAdd(t *testing.T) {
	root := Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeSupportTestFile(t, filepath.Join(root.Path, "support", "2026-06-10-routing-timeout.md"), `---
id: 2026-06-10-routing-timeout
date: 2026-06-10
title: Routing timeout
customer: sampleco
identifiers: [ws-12, so-100045]
claimed_by: alex
observed_by: [bo]
approved_by: casey
product: sample-product
area: routing
status: resolved
tags: [timeout, delivery]
feature_candidate: true
source: support
---

The delivery failed with a clear timeout.
`)
	writeSupportTestFile(t, filepath.Join(root.Path, "support", "2026-06-09-queue-workaround.md"), `---
id: 2026-06-09-queue-workaround
date: 2026-06-09
title: Queue workaround
product: sample-product
area: queue
status: workaround
tags:
  - queue
---

Operators used a temporary queue workaround.
`)

	found, err := List([]Root{root}, Filter{Product: "sample-product", Area: "routing", Tag: "timeout", Identifier: "ws-12", ClaimedBy: "alex", FeatureOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "2026-06-10-routing-timeout" || !found[0].FeatureCandidate {
		t.Fatalf("List = %#v", found)
	}
	if len(found[0].Identifiers) != 2 || found[0].Identifiers[1] != "so-100045" {
		t.Fatalf("List identifiers = %#v", found[0].Identifiers)
	}
	if found[0].ClaimedBy != "alex" || len(found[0].ObservedBy) != 1 || found[0].ObservedBy[0] != "bo" || found[0].ApprovedBy != "casey" {
		t.Fatalf("List people = %#v", found[0])
	}

	none, err := List([]Root{root}, Filter{Identifier: "ws-99"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("List with unmatched identifier = %#v", none)
	}

	results, err := Search([]Root{root}, "clear timeout", Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Snippet, "clear timeout") {
		t.Fatalf("Search = %#v", results)
	}

	record, content, err := Get([]Root{root}, "2026-06-10-routing-timeout")
	if err != nil {
		t.Fatal(err)
	}
	if record.Customer != "sampleco" || !strings.Contains(content, "clear timeout") {
		t.Fatalf("Get record = %#v content = %q", record, content)
	}

	added, scaffold, err := Add(root, "2026-06-11-delivery-error", AddOptions{
		Title:            "Delivery error",
		Customer:         "sampleco",
		Identifiers:      []string{"ws-12", "fl-400-123401"},
		ClaimedBy:        "alex",
		ObservedBy:       []string{"bo"},
		Product:          "sample-product",
		Area:             "routing",
		Tags:             []string{"delivery"},
		Status:           "open",
		FeatureCandidate: true,
		DryRun:           true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID != "2026-06-11-delivery-error" || !strings.Contains(scaffold, "## Feature Signal") || !strings.Contains(scaffold, "status: open") {
		t.Fatalf("Add record = %#v scaffold = %q", added, scaffold)
	}
	if !strings.Contains(scaffold, "identifiers:") || !strings.Contains(scaffold, `  - "fl-400-123401"`) {
		t.Fatalf("Add scaffold missing identifiers: %q", scaffold)
	}
	if !strings.Contains(scaffold, "claimed_by: alex") || !strings.Contains(scaffold, `  - "bo"`) || !strings.Contains(scaffold, "approved_by:\n") {
		t.Fatalf("Add scaffold missing people fields: %q", scaffold)
	}
}

func TestAddRejectsUnsupportedStatus(t *testing.T) {
	_, _, err := Add(Root{Path: t.TempDir()}, "bad-status", AddOptions{Status: "closed"})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err = %v", err)
	}
}

func writeSupportTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
