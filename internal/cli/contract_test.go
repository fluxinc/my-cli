package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func writeContractManifest(t *testing.T, home string) {
	t.Helper()
	cache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "contract": [
    "Continue an existing relevant support record or create a new dated record when working on any fleet member.",
    "Record decisions in the handbook before acting on them."
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestContractList(t *testing.T) {
	home := t.TempDir()
	writeContractManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "contract", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"acme\t1\tContinue an existing relevant support record or create a new dated record when working on any fleet member.",
		"acme\t2\tRecord decisions in the handbook before acting on them.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("contract list stdout missing %q in:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	if err := a.run([]string{"my", "contract", "list", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Manifest string `json:"manifest"`
		Index    int    `json:"index"`
		Rule     string `json:"rule"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("contract list --json: %v in:\n%s", err, stdout.String())
	}
	if len(entries) != 2 || entries[0].Manifest != "acme" || entries[0].Index != 1 ||
		!strings.HasPrefix(entries[0].Rule, "Continue an existing") || entries[1].Index != 2 {
		t.Fatalf("contract list --json = %#v", entries)
	}
}

func TestAdminContractAddAndRemove(t *testing.T) {
	rule1 := "Continue an existing relevant support record or create a new dated record when working on any fleet member."
	rule2 := "Record decisions in the handbook before acting on them."

	t.Run("add appends and rejects duplicates", func(t *testing.T) {
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, "")
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"my", "admin", "contract", "add", rule1,
			"--manifest-dir", manifestDir,
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminContractResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "added" || result.Rule != rule1 || len(result.Contract) != 1 {
			t.Fatalf("result = %#v", result)
		}

		stdout.Reset()
		if err := a.run([]string{
			"my", "admin", "contract", "add", rule2,
			"--manifest-dir", manifestDir,
		}); err != nil {
			t.Fatal(err)
		}
		doc, _, err := manifest.LoadDocument(manifestDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Contract) != 2 || doc.Contract[0] != rule1 || doc.Contract[1] != rule2 {
			t.Fatalf("contract after adds = %#v", doc.Contract)
		}

		err = a.run([]string{
			"my", "admin", "contract", "add", rule1,
			"--manifest-dir", manifestDir,
		})
		if err == nil || !strings.Contains(err.Error(), "already") {
			t.Fatalf("duplicate add err = %v, want already-exists error", err)
		}

		err = a.run([]string{
			"my", "admin", "contract", "add", "   ",
			"--manifest-dir", manifestDir,
		})
		if err == nil {
			t.Fatal("blank rule add should fail")
		}
	})

	t.Run("remove by index and by text", func(t *testing.T) {
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, `,
  "contract": [
    `+jsonString(rule1)+`,
    `+jsonString(rule2)+`
  ]`)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"my", "admin", "contract", "remove", "1",
			"--manifest-dir", manifestDir,
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminContractResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "removed" || result.Rule != rule1 || len(result.Contract) != 1 {
			t.Fatalf("result = %#v", result)
		}

		stdout.Reset()
		if err := a.run([]string{
			"my", "admin", "contract", "remove", rule2,
			"--manifest-dir", manifestDir,
		}); err != nil {
			t.Fatal(err)
		}
		doc, _, err := manifest.LoadDocument(manifestDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Contract) != 0 {
			t.Fatalf("contract after removes = %#v", doc.Contract)
		}

		err = a.run([]string{
			"my", "admin", "contract", "remove", "no such rule",
			"--manifest-dir", manifestDir,
		})
		if err == nil {
			t.Fatal("remove of unknown rule should fail")
		}
	})
}

func TestAdminContractRegisteredManifestPublishesIsolatedPRAndPreservesCache(t *testing.T) {
	for _, tc := range []struct {
		name       string
		action     string
		target     string
		contract   string
		wantAction string
		wantRule   string
		wantInPR   bool
	}{
		{name: "add", action: "add", target: "Require reviewed changes.", wantAction: "added", wantRule: "Require reviewed changes.", wantInPR: true},
		{name: "remove", action: "remove", target: "1", contract: `, "contract":["Retire this rule."]`, wantAction: "removed", wantRule: "Retire this rule."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{
  "manifest_version": 1,
  "organization": {"id":"acme","name":"Acme Example"},
  "umbrella": {"recommended_path":"~/acme"},
  "governance": {
    "authorization": {"provider":"github","manifest_repository":"example/control","admin_permission":"admin"}
  }` + tc.contract + `
}`
			home, umbrellaRoot, cache, remote, _ := setupCLITrackedManifestBody(t, body)
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
			before, err := os.ReadFile(filepath.Join(cache, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			headBefore := strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD"))
			state := &governedPRRunnerState{remote: remote, permission: "admin", repository: "example/control"}
			var stdout, stderr bytes.Buffer
			a := app{
				stdout: &stdout, stderr: &stderr, accessRunner: governedAdminAccessRunner(),
				publishRunner: state.run,
			}
			if err := a.run([]string{
				"my", "admin", "contract", tc.action, tc.target,
				"--home", home, "--umbrella", umbrellaRoot, "--json",
			}); err != nil {
				t.Fatalf("registered contract %s: %v\nstdout=%s\nstderr=%s", tc.action, err, stdout.String(), stderr.String())
			}
			var result adminContractResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Action != tc.wantAction || result.Rule != tc.wantRule || result.Publication != "pull request opened" || result.PRURL == "" {
				t.Fatalf("result = %#v", result)
			}
			after, err := os.ReadFile(filepath.Join(cache, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) || strings.TrimSpace(gitCLIOutput(t, cache, "status", "--porcelain")) != "" ||
				strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD")) != headBefore {
				t.Fatal("sync-managed manifest cache changed during isolated contract publication")
			}
			proposal := gitCLIOutput(t, remote, "show", state.commit+":manifest.json")
			contains := strings.Contains(proposal, tc.wantRule)
			if contains != tc.wantInPR {
				t.Fatalf("proposal contains rule = %v, want %v:\n%s", contains, tc.wantInPR, proposal)
			}
		})
	}
}

func TestAdminContractRegisteredManifestFailsClosedBeforePublication(t *testing.T) {
	body := `{
  "manifest_version": 1,
  "organization": {"id":"acme","name":"Acme Example"},
  "governance": {
    "authorization": {"provider":"github","manifest_repository":"example/control","admin_permission":"admin"}
  }
}`
	home, umbrellaRoot, cache, _, _ := setupCLITrackedManifestBody(t, body)
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	reg.Manifests[0].GitURL = "https://github.com/example/control.git"
	if err := manifest.SaveRegistry(home, reg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminRunner("write")}
	err = a.run([]string{
		"my", "admin", "contract", "add", "Must not publish.",
		"--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "governed manifest authoring denied") {
		t.Fatalf("err = %v, want fail-closed admin denial", err)
	}
	after, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("denied registered authoring changed manifest cache")
	}
}

func TestAdminContractRegisteredManifestRejectsStaleCache(t *testing.T) {
	body := `{
  "manifest_version": 1,
  "organization": {"id":"acme","name":"Acme Example"},
  "governance": {
    "authorization": {"provider":"github","manifest_repository":"example/control","admin_permission":"admin"}
  }
}`
	home, umbrellaRoot, cache, _, writer := setupCLITrackedManifestBody(t, body)
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	reg.Manifests[0].GitURL = "https://github.com/example/control.git"
	if err := manifest.SaveRegistry(home, reg); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "README.md"), "manifest advanced remotely\n")
	commitCLIGit(t, writer, "advance manifest remotely")
	runCLIGit(t, writer, "push", "-q", "origin", "HEAD")

	headBefore := strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD"))
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminAccessRunner()}
	err = a.run([]string{
		"my", "admin", "contract", "add", "Stale caches must not author.",
		"--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "not at its trusted upstream") {
		t.Fatalf("stale cache error = %v", err)
	}
	if strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD")) != headBefore {
		t.Fatal("stale-cache rejection moved the cache HEAD")
	}
}

func TestProspectiveFileContentsPathContainment(t *testing.T) {
	for path, want := range map[string]bool{
		"manifest.json":            true,
		"catalog/products.json":    true,
		"../escape.json":           false,
		"/abs/manifest.json":       false,
		"manifest.json/../../evil": false,
		"a\\b.json":                false,
		"":                         false,
		"secrets.env":              false,
		"manifest.json.bak":        false,
	} {
		got := portableProspectivePath(path) && prospectivePathDeclared(path, manifestControlPaths())
		if got != want {
			t.Errorf("containment(%q) = %v, want %v", path, got, want)
		}
	}
}

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
