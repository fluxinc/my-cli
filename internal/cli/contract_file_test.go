package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/guidance"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func runAdminContractJSON(t *testing.T, a app, args ...string) (adminContractResult, error) {
	t.Helper()
	stdout := a.stdout.(*bytes.Buffer)
	stdout.Reset()
	err := a.run(append(append([]string{"my", "admin", "contract"}, args...), "--json"))
	var result adminContractResult
	if err != nil {
		// --json reports the error on stdout.
		return result, errors.New(stdout.String())
	}
	{
		if jsonErr := json.Unmarshal(stdout.Bytes(), &result); jsonErr != nil {
			t.Fatalf("decode result: %v in %s", jsonErr, stdout.String())
		}
	}
	return result, err
}

func composeCLIGuidance(t *testing.T, dir string) string {
	t.Helper()
	doc, _, err := manifest.LoadDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := guidance.Compose(dir, doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAdminContractMigrateManifestDir(t *testing.T) {
	dir := t.TempDir()
	writeCLITestFile(t, filepath.Join(dir, "manifest.json"), `{
  "manifest_version": 1,
  "organization": {"id": "acme", "name": "Acme Example", "future_org_key": {"z": 1, "a": 2}},
  "future_top_level": ["kept"],
  "contract": ["  Ask before touching external assets. ", "Logs live under logs/."],
  "skills": []
}
`)
	before := composeCLIGuidance(t, dir)
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	result, err := runAdminContractJSON(t, a, "migrate", "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "migrated" || result.ContractFile != "guidance/contract.md" || result.MinMyVersion != "" ||
		len(result.Contract) != 2 || len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "no member upgrade") {
		t.Fatalf("result = %#v", result)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "guidance", "contract.md"))
	if string(data) != "# Organization contract\n\n- Ask before touching external assets.\n- Logs live under logs/.\n" {
		t.Fatalf("contract file = %q", data)
	}
	manifestData, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	want := `{
  "manifest_version": 1,
  "organization": {
    "id": "acme",
    "name": "Acme Example",
    "future_org_key": {
      "z": 1,
      "a": 2
    }
  },
  "future_top_level": [
    "kept"
  ],
  "contract": [
    "Ask before touching external assets.",
    "Logs live under logs/."
  ],
  "contract_source": "guidance/contract.md",
  "skills": []
}
`
	if string(manifestData) != want {
		t.Fatalf("manifest:\n%s\nwant:\n%s", manifestData, want)
	}
	if after := composeCLIGuidance(t, dir); after != before {
		t.Fatalf("migration changed AGENTS.md\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := runAdminContractJSON(t, a, "migrate", "--manifest-dir", dir); err != nil {
		t.Fatalf("second migrate err = %v", err)
	}
}

func TestAdminContractMigrateKeepsHigherFloorAndRefusesExistingFile(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `,
  "min_my_version": "0.0.1",
  "contract": ["One rule."]`)
	// A declared floor above the feature floor stays as written.
	raw, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	writeCLITestFile(t, filepath.Join(dir, "manifest.json"), strings.Replace(string(raw), "0.0.1", "0.47.5", 1))
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	result, err := runAdminContractJSON(t, a, "migrate", "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.MinMyVersion != "0.47.5" {
		t.Fatalf("min_my_version = %q, want the higher declared floor", result.MinMyVersion)
	}

	existing := t.TempDir()
	writeAdminManifest(t, existing, `,
  "contract": ["One rule."]`)
	writeCLITestFile(t, filepath.Join(existing, "guidance", "contract.md"), "- someone else's file\n")
	beforeManifest, _ := os.ReadFile(filepath.Join(existing, "manifest.json"))
	if _, err := runAdminContractJSON(t, a, "migrate", "--manifest-dir", existing); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("migrate over existing file err = %v", err)
	}
	afterManifest, _ := os.ReadFile(filepath.Join(existing, "manifest.json"))
	file, _ := os.ReadFile(filepath.Join(existing, "guidance", "contract.md"))
	if !bytes.Equal(beforeManifest, afterManifest) || string(file) != "- someone else's file\n" {
		t.Fatal("refused migration changed the checkout")
	}
}

func TestAdminContractFileEditsManifestDir(t *testing.T) {
	dir := t.TempDir()
	// A hand-written contract_file without the feature floor.
	writeAdminManifest(t, dir, `,
  "contract_file": "guidance/contract.md"`)
	writeCLITestFile(t, filepath.Join(dir, "guidance", "contract.md"), "# Organization contract\n\n- First rule.\n\n## Logs\n- Logs live under logs/.\n")
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}

	result, err := runAdminContractJSON(t, a, "add", "Rotate logs weekly.", "--under", "Logs", "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "added" || result.Source != "contract_file" || result.MinMyVersion != "0.47.0" || result.BaseBlob == "" || result.Blob == result.BaseBlob {
		t.Fatalf("add result = %#v", result)
	}
	manifestData, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if !strings.Contains(string(manifestData), `"min_my_version": "0.47.0"`) {
		t.Fatalf("file edit did not raise the floor:\n%s", manifestData)
	}

	if _, err := runAdminContractJSON(t, a, "remove", "1", "--expect-blob", result.BaseBlob, "--manifest-dir", dir); err == nil || !strings.Contains(err.Error(), "changed since you read it") {
		t.Fatalf("stale --expect-blob err = %v", err)
	}
	replaced, err := runAdminContractJSON(t, a, "replace", "Logs live under logs/.", "Logs live under /var/log/acme.", "--expect-blob", result.Blob, "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Previous != "Logs live under logs/." || replaced.Rule != "Logs live under /var/log/acme." {
		t.Fatalf("replace result = %#v", replaced)
	}
	if _, err := runAdminContractJSON(t, a, "remove", "First rule.", "--manifest-dir", dir); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "guidance", "contract.md"))
	if string(data) != "# Organization contract\n\n\n## Logs\n- Logs live under /var/log/acme.\n- Rotate logs weekly.\n" {
		t.Fatalf("contract file = %q", data)
	}

	if _, err := runAdminContractJSON(t, a, "remove", "Missing rule.", "--manifest-dir", dir); err == nil {
		t.Fatal("removing a missing rule succeeded")
	}
	if _, err := runAdminContractJSON(t, a, "remove", "1", "--under", "Logs", "--manifest-dir", dir); err == nil || !strings.Contains(err.Error(), "--under") {
		t.Fatalf("remove --under err = %v", err)
	}
}

func TestWriteContractEditRefusesChangedCheckout(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `,
  "contract_file": "guidance/contract.md"`)
	writeCLITestFile(t, filepath.Join(dir, "guidance", "contract.md"), "- First rule.\n")
	doc, manifestPath, _ := manifest.LoadDocument(dir)
	edit, err := planContractEdit("add", []string{"Second rule."}, adminContractOpts{}, dir, manifestPath, doc)
	if err != nil {
		t.Fatal(err)
	}
	// Someone edits the file after the edit was planned.
	writeCLITestFile(t, filepath.Join(dir, "guidance", "contract.md"), "- First rule.\n- Concurrent rule.\n")
	if err := writeContractEdit(dir, edit); err == nil || !strings.Contains(err.Error(), "changed while the contract edit was prepared") {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "guidance", "contract.md"))
	if string(data) != "- First rule.\n- Concurrent rule.\n" {
		t.Fatalf("concurrent change overwritten: %q", data)
	}
}

func TestContractListAndShowReadContractFile(t *testing.T) {
	body := `{
  "manifest_version": 1,
  "organization": {"id":"acme","name":"Acme Example"},
  "contract_file": "guidance/contract.md"
}`
	home, _, cache, _, _ := setupCLITrackedManifestBody(t, body)
	contract := "# Organization contract\n\n- First rule.\n\n## Logs\n- Logs live under logs/.\n"
	writeCLITestFile(t, filepath.Join(cache, "guidance", "contract.md"), contract)

	var stdout bytes.Buffer
	a := app{stdout: &stdout, stderr: &bytes.Buffer{}}
	if err := a.run([]string{"my", "contract", "list", "--manifest", "acme", "--home", home, "--no-refresh", "--json"}); err != nil {
		t.Fatal(err)
	}
	var entries []contractEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[1].Index != 2 || entries[1].Heading != "Logs" || entries[1].Rule != "Logs live under logs/." {
		t.Fatalf("entries = %#v", entries)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "contract", "show", "--manifest", "acme", "--home", home, "--no-refresh"}); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != contract {
		t.Fatalf("show = %q", stdout.String())
	}
	stdout.Reset()
	if err := a.run([]string{"my", "contract", "show", "--manifest", "acme", "--home", home, "--no-refresh", "--json"}); err != nil {
		t.Fatal(err)
	}
	var shows []contractShow
	if err := json.Unmarshal(stdout.Bytes(), &shows); err != nil {
		t.Fatal(err)
	}
	if len(shows) != 1 || shows[0].Source != "contract_file" || shows[0].Blob != manifest.ContractBlob([]byte(contract)) || len(shows[0].Sections) != 2 {
		t.Fatalf("show json = %#v", shows)
	}
}

func setupGovernedContractCache(t *testing.T, body string) (home, umbrellaRoot, cache, remote, writer string) {
	t.Helper()
	home, umbrellaRoot, cache, remote, writer = setupCLITrackedManifestBody(t, body)
	if err := os.MkdirAll(umbrellaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	reg.Manifests[0].GitURL = "https://github.com/example/control.git"
	if err := manifest.SaveRegistry(home, reg); err != nil {
		t.Fatal(err)
	}
	return home, umbrellaRoot, cache, remote, writer
}

const governedContractBody = `{
  "manifest_version": 1,
  "organization": {"id":"acme","name":"Acme Example"},
  "umbrella": {"recommended_path":"~/acme"},
  "future_top_level": {"kept": true},
  "governance": {
    "authorization": {"provider":"github","manifest_repository":"example/control","admin_permission":"admin"}
  },
  "contract": ["Ask before touching external assets."]
}`

func TestAdminContractRegisteredMigratePublishesBothFiles(t *testing.T) {
	home, umbrellaRoot, cache, remote, _ := setupGovernedContractCache(t, governedContractBody)
	before, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	headBefore := strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD"))
	state := &governedPRRunnerState{remote: remote, permission: "admin", repository: "example/control"}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminAccessRunner(), publishRunner: state.run}
	result, err := runAdminContractJSON(t, a, "migrate", "--home", home, "--umbrella", umbrellaRoot)
	if err != nil {
		t.Fatalf("registered migrate: %v\nstderr=%s", err, a.stderr.(*bytes.Buffer).String())
	}
	if result.Action != "migrated" || result.Publication != "pull request opened" || result.BaseCommit != headBefore {
		t.Fatalf("result = %#v", result)
	}
	proposedManifest := gitCLIOutput(t, remote, "show", state.commit+":manifest.json")
	if !strings.Contains(proposedManifest, `"contract_source": "guidance/contract.md"`) || !strings.Contains(proposedManifest, `"contract":`) ||
		!strings.Contains(proposedManifest, `"future_top_level"`) || strings.Contains(proposedManifest, `"min_my_version": "0.47.0"`) {
		t.Fatalf("proposed manifest:\n%s", proposedManifest)
	}
	proposedFile := gitCLIOutput(t, remote, "show", state.commit+":guidance/contract.md")
	if proposedFile != "# Organization contract\n\n- Ask before touching external assets.\n" {
		t.Fatalf("proposed contract file = %q", proposedFile)
	}
	after, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	if !bytes.Equal(before, after) || strings.TrimSpace(gitCLIOutput(t, cache, "status", "--porcelain")) != "" ||
		strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD")) != headBefore {
		t.Fatal("registered migration changed the sync-managed cache")
	}
}

func TestAdminContractRegisteredRefusesWhenUpstreamMovesBeforePush(t *testing.T) {
	home, umbrellaRoot, _, remote, writer := setupGovernedContractCache(t, governedContractBody)
	state := &governedPRRunnerState{remote: remote, permission: "admin", repository: "example/control"}
	moved := false
	publish := func(name string, args ...string) ([]byte, error) {
		// Another admin merges while this edit is being published.
		if !moved && strings.Join(args, " ") == "api user" {
			moved = true
			writeCLITestFile(t, filepath.Join(writer, "README.md"), "concurrent change\n")
			commitCLIGit(t, writer, "concurrent change")
			runCLIGit(t, writer, "push", "-q", "origin", "HEAD")
		}
		return state.run(name, args...)
	}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminAccessRunner(), publishRunner: publish}
	_, err := runAdminContractJSON(t, a, "add", "Late rule.", "--home", home, "--umbrella", umbrellaRoot)
	if err == nil || !strings.Contains(err.Error(), "base_moved") {
		t.Fatalf("err = %v, want base_moved", err)
	}
	if !moved || state.created {
		t.Fatalf("moved=%v created=%v; the PR must not be opened", moved, state.created)
	}
	if out, _ := gitCLIOutputErr(remote, "for-each-ref", "refs/heads/"); strings.Count(string(out), "\n") != 1 {
		t.Fatalf("a proposal branch was pushed:\n%s", out)
	}
}
