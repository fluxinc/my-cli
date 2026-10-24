package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/contractstate"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/worksession"
)

func TestContractSyncRetriesPendingRootsWithoutNewPull(t *testing.T) {
	body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"umbrella":{"recommended_path":"~/acme"},"contract":["Old rule."]}`
	home, root, cache, _, writer := setupCLITrackedManifestBody(t, body)
	if _, _, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"good", "broken"} {
		session := worksession.Session{SchemaVersion: 1, ID: id, Status: worksession.StatusActive, Path: filepath.Join(root, "sessions", id)}
		if err := os.MkdirAll(session.Path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := worksession.Save(root, session); err != nil {
			t.Fatal(err)
		}
	}
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	a := app{stdout: out, stderr: stderr}
	if _, err := a.reconcileContractRoots(home, "acme", root); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, "sessions", "broken", "AGENTS.md")
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(broken, 0755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), strings.Replace(body, "Old rule.", "New rule.", 1))
	commitAndPushCLIGit(t, writer, "Change contract")
	args := []string{"--home", home, "--manifest", "acme", "--umbrella", root, "--scope", "manifest", "--json"}
	if err := a.runSync(append(append([]string{}, args...), "--no-derived")); err != nil {
		t.Fatal(err)
	}
	var report syncCommandReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.ContractChange == nil || !strings.Contains(report.ContractChange.Diff, "-Old rule.") && !strings.Contains(report.ContractChange.Diff, "-- Old rule.") {
		t.Fatalf("missing diff: %s", out)
	}
	data, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Contains(string(data), "New rule.") {
		t.Fatal("--no-derived wrote guidance")
	}
	state, err := contractstate.Load(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Pending(root) || state.LastSuccessfulRemoteCheck == "" {
		t.Fatalf("state=%#v", state)
	}
	out.Reset()
	stderr.Reset()
	if err := a.runSync(args); err == nil {
		t.Fatal("broken root was not reported")
	}
	for _, path := range []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, "sessions", "good", "AGENTS.md")} {
		data, _ := os.ReadFile(path)
		if !strings.Contains(string(data), "New rule.") {
			t.Fatalf("successful root not reconciled: %s", path)
		}
	}
	state, _ = contractstate.Load(home, "acme")
	if state.Pending(root) || state.Pending(filepath.Join(root, "sessions", "good")) || !state.Pending(filepath.Join(root, "sessions", "broken")) {
		t.Fatalf("partial state=%#v", state.Roots)
	}
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if err := a.runSync(args); err != nil {
		t.Fatal(err)
	}
	state, _ = contractstate.Load(home, "acme")
	if state.Pending(filepath.Join(root, "sessions", "broken")) {
		t.Fatal("retry did not clear pending root")
	}
	doc, _, err := manifest.LoadDocument(cache)
	if err != nil || doc.Contract[0] != "New rule." {
		t.Fatal("bad fixture checkout")
	}
}

func TestContractStatusAndHistoricalDiffAreReadOnly(t *testing.T) {
	body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":["Old rule."]}`
	home, _, cache, _, _ := setupCLITrackedManifestBody(t, body)
	old := strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD"))
	writeCLITestFile(t, filepath.Join(cache, "manifest.json"), strings.Replace(body, "Old rule.", "New rule.", 1))
	commitCLIGit(t, cache, "New contract")
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}}
	if err := a.runContractStateCommand("status", []string{"--home", home, "--manifest", "acme", "--json"}); err != nil {
		t.Fatal(err)
	}
	var report contractStatusReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Local.Provenance != "unpublished" || report.Active != nil {
		t.Fatalf("unpublished revision called active: %#v", report)
	}
	out.Reset()
	if err := a.runContractStateCommand("diff", []string{"--home", home, "--manifest", "acme", "--from", old, "--to", "HEAD"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "-- Old rule.") || !strings.Contains(out.String(), "+- New rule.") {
		t.Fatalf("diff=%s", out)
	}
	path, _ := contractstate.Path(home, "acme")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read commands persisted state")
	}
}

func TestContractManifestSyncRetractionAndClosedSession(t *testing.T) {
	body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":["Old rule."]}`
	home, root, _, _, writer := setupCLITrackedManifestBody(t, body)
	if _, _, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	session := worksession.Session{SchemaVersion: 1, ID: "closed", Status: worksession.StatusActive, Path: filepath.Join(root, "sessions", "closed")}
	if err := os.MkdirAll(session.Path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := worksession.Save(root, session); err != nil {
		t.Fatal(err)
	}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if _, err := a.reconcileContractRoots(home, "acme", root); err != nil {
		t.Fatal(err)
	}
	session.Status = "finished"
	if err := worksession.Save(root, session); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), strings.Replace(body, `,"contract":["Old rule."]`, "", 1))
	commitAndPushCLIGit(t, writer, "Retract final rule")
	out := &bytes.Buffer{}
	a.stdout = out
	args := []string{"acme", "--home", home, "--umbrella", root, "--json", "--no-derived"}
	if err := a.runManifestSync(args); err != nil {
		t.Fatal(err)
	}
	var reports []manifestSyncCommandResult
	if err := json.Unmarshal(out.Bytes(), &reports); err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].ContractChange == nil || !strings.Contains(reports[0].ContractChange.Diff, "-- Old rule.") {
		t.Fatalf("report=%s", out)
	}
	state, err := contractstate.Load(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if state.Active == nil || state.Active.Configured || !state.Pending(root) {
		t.Fatalf("retraction lost: %#v", state)
	}
	if _, found := state.Roots[session.Path]; found {
		t.Fatal("closed session remains pending")
	}
	out.Reset()
	if err := a.runManifestSync(args[:len(args)-1]); err != nil {
		t.Fatal(err)
	}
	state, _ = contractstate.Load(home, "acme")
	if state.Pending(root) {
		t.Fatal("no-new-pull manifest sync did not retry")
	}
	text, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if strings.Contains(string(text), "Old rule.") {
		t.Fatal("retracted rule still installed")
	}
}

func TestContractFailedFetchDoesNotAdvanceSuccessTime(t *testing.T) {
	body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":["Old rule."]}`
	home, root, cache, _, _ := setupCLITrackedManifestBody(t, body)
	if _, _, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := observeContract(home, "acme", root, true); err != nil {
		t.Fatal(err)
	}
	before, err := contractstate.Update(home, "acme", func(s *contractstate.State) error { s.LastSuccessfulRemoteCheck = "2000-01-01T00:00:00Z"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	gitCLIOutput(t, cache, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := a.runManifestSync([]string{"acme", "--home", home, "--umbrella", root, "--json"}); err == nil {
		t.Fatal("failed fetch reported success")
	}
	after, _ := contractstate.Load(home, "acme")
	if before.LastSuccessfulRemoteCheck != after.LastSuccessfulRemoteCheck {
		t.Fatal("failed fetch advanced success timestamp")
	}
}

func TestContractFanoutRejectsUnexpectedSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.md")
	writeCLITestFile(t, outside, "untouched")
	if err := os.Symlink(outside, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if err := safeContractOutput(root); err == nil {
		t.Fatal("unsafe symlink accepted")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "untouched" {
		t.Fatal("outside file modified")
	}
}

func TestContractCorruptStateDoesNotPreventManifestPull(t *testing.T) {
	for _, command := range []string{"sync", "manifests"} {
		t.Run(command, func(t *testing.T) {
			body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":["Old rule."]}`
			home, root, cache, _, writer := setupCLITrackedManifestBody(t, body)
			if _, _, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
				t.Fatal(err)
			}
			path, _ := contractstate.Path(home, "acme")
			writeCLITestFile(t, path, "{corrupt")
			writeCLITestFile(t, filepath.Join(writer, "manifest.json"), strings.Replace(body, "Old rule.", "Recovered rule.", 1))
			commitAndPushCLIGit(t, writer, "Recover contract")
			out := &bytes.Buffer{}
			a := app{stdout: out, stderr: &bytes.Buffer{}}
			var err error
			if command == "sync" {
				err = a.runSync([]string{"--home", home, "--manifest", "acme", "--umbrella", root, "--scope", "manifest", "--json"})
			} else {
				err = a.runManifestSync([]string{"acme", "--home", home, "--umbrella", root, "--json"})
			}
			if err == nil {
				t.Fatal("corrupt state must remain visible")
			}
			var report any
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("not one JSON report: %v: %s", err, out)
			}
			doc, _, err := manifest.LoadDocument(cache)
			if err != nil || len(doc.Contract) != 1 || doc.Contract[0] != "Recovered rule." {
				t.Fatal("state corruption prevented pull")
			}
			bytes, _ := os.ReadFile(path)
			if string(bytes) != "{corrupt" {
				t.Fatal("corrupt evidence silently overwritten")
			}
		})
	}
}

func TestContractSessionParentSymlinkCannotEscapeUmbrella(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "sessions")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(outside, "escape"), 0755); err != nil {
		t.Fatal(err)
	}
	if pathWithinContractRoot(root, filepath.Join(root, "sessions", "escape")) {
		t.Fatal("symlink ancestor escaped umbrella")
	}
}
