package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestCompatibleContractAuthoringAndRepair(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `, "min_my_version":"0.45.0", "contract":["One.","Two."]`)
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	run := func(args ...string) adminContractResult {
		t.Helper()
		result, err := runAdminContractJSON(t, a, append(args, "--manifest-dir", dir)...)
		if err != nil {
			t.Fatal(err)
		}
		doc, _, err := manifest.LoadDocument(dir)
		if err != nil {
			t.Fatal(err)
		}
		if doc.ContractSource == "" || doc.ContractFile != "" || doc.Contract == nil || doc.MinMyVersion != "0.45.0" {
			t.Fatalf("bad compatibility document: %#v", doc)
		}
		if err := manifest.RequireContractSource(dir, doc); err != nil {
			t.Fatal(err)
		}
		return result
	}
	run("migrate")
	run("add", "Three.")
	run("replace", "2", "Replacement.")
	run("remove", "1")
	run("remove", "1")
	run("remove", "1")
	doc, _, _ := manifest.LoadDocument(dir)
	if len(doc.Contract) != 0 {
		t.Fatalf("last removal failed: %#v", doc.Contract)
	}
	// Canonical writes by other current admin commands must preserve explicit [].
	if err := manifest.SaveDocument(dir, doc); err != nil {
		t.Fatal(err)
	}
	run("migrate")
	writeCLITestFile(t, filepath.Join(dir, "guidance", "contract.md"), "# Rules\n\n- Edited in the file.\n")
	if _, err := runAdminContractJSON(t, a, "add", "Refuse drift.", "--manifest-dir", dir); err == nil || !strings.Contains(err.Error(), "compatibility copy") {
		t.Fatalf("drift add: %v", err)
	}
	doc, _, _ = manifest.LoadDocument(dir)
	if _, err := a.publishOrg("", registeredDoc{ref: manifest.Ref{Name: "acme", LocalPath: dir}, doc: doc}, true); err == nil {
		t.Fatal("publish accepted drift")
	}
	run("migrate") // explicitly project file edits into the compatibility copy
	doc, _, _ = manifest.LoadDocument(dir)
	if len(doc.Contract) != 1 || doc.Contract[0] != "Edited in the file." {
		t.Fatalf("repair: %#v", doc.Contract)
	}
	writeCLITestFile(t, filepath.Join(dir, "guidance", "contract.md"), "- Unpublished file edit.\n")
	run("migrate", "--from-inline")
	doc, _, _ = manifest.LoadDocument(dir)
	if doc.Contract[0] != "Edited in the file." {
		t.Fatal("from-inline changed canonical rules")
	}
	before, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	run("migrate")
	after, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("repeated migrate changed manifest")
	}
}

func TestCompatibleContractConvertsLegacyFileAndRepairsMissingSource(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `, "min_my_version":"0.47.0", "contract_file":"guidance/contract.md"`)
	writeCLITestFile(t, filepath.Join(dir, "guidance", "contract.md"), "## Group\n- A rule.\n")
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	before := composeCLIGuidance(t, dir)
	if _, err := runAdminContractJSON(t, a, "migrate", "--manifest-dir", dir); err != nil {
		t.Fatal(err)
	}
	if after := composeCLIGuidance(t, dir); after != before {
		t.Fatal("legacy conversion changed guidance")
	}
	doc, _, _ := manifest.LoadDocument(dir)
	if doc.ContractSource == "" || doc.ContractFile != "" || len(doc.Contract) != 1 || doc.MinMyVersion != "0.47.0" {
		t.Fatalf("conversion: %#v", doc)
	}
	if err := os.Remove(filepath.Join(dir, "guidance", "contract.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := runAdminContractJSON(t, a, "migrate", "--manifest-dir", dir, "--from-inline"); err != nil {
		t.Fatal(err)
	}
	if err := manifest.RequireContractSource(dir, doc); err != nil {
		t.Fatal(err)
	}
}

func TestCompatibleContractReadWarningAndCommittedSnapshot(t *testing.T) {
	home, _, cache, _, _ := setupCLITrackedManifestBody(t, `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract_source":"guidance/contract.md","contract":["Visible to all versions."]}`)
	writeCLITestFile(t, filepath.Join(cache, "guidance", "contract.md"), "- Different rule.\n")
	gitCLIOutput(t, cache, "add", "guidance/contract.md")
	gitCLIOutput(t, cache, "commit", "-m", "Add mismatched source fixture")
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := a.run([]string{"my", "contract", "show", "--home", home, "--manifest", "acme", "--no-refresh", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.stderr.(*bytes.Buffer).String(), "compatibility copy") {
		t.Fatalf("missing warning: %s", a.stderr)
	}
	if !strings.Contains(a.stdout.(*bytes.Buffer).String(), "Visible to all versions.") || strings.Contains(a.stdout.(*bytes.Buffer).String(), `"rule": "Different rule."`) {
		t.Fatalf("wrong view: %s", a.stdout)
	}
	snap, err := contractSnapshotAt(manifest.Ref{LocalPath: cache}, "HEAD")
	if err != nil || !strings.Contains(snap.Text, "Visible to all versions.") || strings.Contains(snap.Text, "Different rule.") {
		t.Fatalf("snapshot: %#v %v", snap, err)
	}
}

func TestCompatibleContractSyncRefusesPublishingDrift(t *testing.T) {
	home, root, cache, _, _ := setupCLITrackedManifestBody(t, `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract_source":"guidance/contract.md","contract":["Keep this rule."]}`)
	writeCLITestFile(t, filepath.Join(cache, "guidance", "contract.md"), "- Keep this rule.\n")
	gitCLIOutput(t, cache, "add", "guidance/contract.md")
	gitCLIOutput(t, cache, "commit", "-m", "Add source")
	gitCLIOutput(t, cache, "push", "origin", "HEAD")
	writeCLITestFile(t, filepath.Join(cache, "guidance", "contract.md"), "- Changed only in file.\n")
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	err := a.run([]string{"my", "sync", "--home", home, "--umbrella", root, "--scope", "manifest", "--publish", "direct", "--print", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.stdout.(*bytes.Buffer).String(), "contract_source_drift") {
		t.Fatalf("missing publish hold: %s", a.stdout)
	}
	if !strings.Contains(a.stderr.(*bytes.Buffer).String(), "compatibility copy") {
		t.Fatalf("missing warning: %s", a.stderr)
	}
}
