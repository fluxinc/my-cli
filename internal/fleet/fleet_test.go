package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListGetSearchAddAndSet(t *testing.T) {
	root := Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeFleetTestFile(t, filepath.Join(root.Path, "fleet", "acme-box-1.md"), `---
id: acme-box-1
customer: sampleco.example.com
partner: samplepartner
status: live
device: Sample Scanner X
serial: SN-0001
identifiers:
  - "SO 100045"
  - "FL 400-123401"
config_repo: acme/sample-configs
config_branch: partner/site-1
deployed_site: Springfield
ship_to: Centerville
install_date: 2026-06-01
assigned: alex
source: fleet
---

# acme-box-1

Routing hub for the sample site.
`)
	writeFleetTestFile(t, filepath.Join(root.Path, "fleet", "acme-box-2.md"), `---
id: acme-box-2
customer: otherco.example.com
status: build
identifiers:
  - "SO 200031"
source: fleet
---

# acme-box-2
`)

	found, err := List([]Root{root}, Filter{Status: "live", Customer: "sampleco.example.com", Partner: "samplepartner", Identifier: "SO 100045", Branch: "partner/site-1", Where: map[string]string{"assigned": "alex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "acme-box-1" || found[0].Device != "Sample Scanner X" {
		t.Fatalf("List = %#v", found)
	}
	if found[0].DeployedSite != "Springfield" || found[0].ShipTo != "Centerville" {
		t.Fatalf("List sites = %#v", found[0])
	}

	none, err := List([]Root{root}, Filter{Where: map[string]string{"assigned": "bo"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("List with unmatched where = %#v", none)
	}

	all, err := List([]Root{root}, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != "acme-box-1" || all[1].ID != "acme-box-2" {
		t.Fatalf("List order = %#v", all)
	}

	rec, content, err := Get([]Root{root}, "fl 400-123401")
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "acme-box-1" || !strings.Contains(content, "Routing hub") {
		t.Fatalf("Get by identifier = %#v", rec)
	}
	if _, _, err := Get([]Root{root}, "missing-box"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Get missing err = %v", err)
	}

	results, err := Search([]Root{root}, "routing hub", Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.Contains(results[0].Snippet, "Routing hub") {
		t.Fatalf("Search = %#v", results)
	}

	added, body, err := Add(root, "ACME-BOX-3", AddOptions{
		Customer:     "sampleco.example.com",
		Device:       "Sample Scanner Y",
		Identifiers:  []string{"SO 300101"},
		ConfigBranch: "partner/site-3",
		DryRun:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if added.ID != "acme-box-3" || added.Status != "new" {
		t.Fatalf("Add record = %#v", added)
	}
	for _, want := range []string{"id: acme-box-3", "status: new", `  - "SO 300101"`, "partner:\n", "ship_to:\n", "source: fleet", "## Notes"} {
		if !strings.Contains(body, want) {
			t.Fatalf("Add scaffold missing %q:\n%s", want, body)
		}
	}

	updated, changes, err := Set([]Root{root}, "acme-box-2", map[string]string{"status": "install", "deployed_site": "Lakeside"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "install" || updated.DeployedSite != "Lakeside" {
		t.Fatalf("Set record = %#v", updated)
	}
	if len(changes) != 2 || changes[1].Key != "status" || changes[1].Old != "build" || changes[1].New != "install" {
		t.Fatalf("Set changes = %#v", changes)
	}
	data, err := os.ReadFile(updated.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `  - "SO 200031"`) || !strings.Contains(string(data), "# acme-box-2") {
		t.Fatalf("Set damaged untouched content:\n%s", data)
	}

	if _, _, err := Set([]Root{root}, "acme-box-2", map[string]string{"identifiers": "SO 9"}); err == nil {
		t.Fatal("Set on list field did not error")
	}
}

func TestAddAndSetRoundTripAddressValues(t *testing.T) {
	root := Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	added, _, err := Add(root, "box-hash", AddOptions{ShipTo: "Suite #100", Status: "wait #verify"})
	if err != nil {
		t.Fatal(err)
	}
	if added.ShipTo != "Suite #100" {
		t.Fatalf("Add record = %#v", added)
	}
	parsed, err := parseRecord(root, added.Path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ShipTo != "Suite #100" || parsed.Status != "wait #verify" {
		t.Fatalf("scaffold round-trip = %#v", parsed)
	}
	updated, _, err := Set([]Root{root}, "box-hash", map[string]string{"deployed_site": "Building 2 #north"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.DeployedSite != "Building 2 #north" || updated.ShipTo != "Suite #100" {
		t.Fatalf("Set round-trip = %#v", updated)
	}
}

func TestGetAmbiguousIdentifier(t *testing.T) {
	root := Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	for _, id := range []string{"box-a", "box-b"} {
		writeFleetTestFile(t, filepath.Join(root.Path, "fleet", id+".md"), `---
id: `+id+`
identifiers:
  - "SO 1"
source: fleet
---
`)
	}
	_, _, err := Get([]Root{root}, "SO 1")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v", err)
	}
}

func writeFleetTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
