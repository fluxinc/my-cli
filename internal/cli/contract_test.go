package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
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

func jsonString(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
