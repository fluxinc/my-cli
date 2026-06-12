package guidance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/manifest"
)

func TestCheckStatuses(t *testing.T) {
	doc := manifest.Document{
		Organization: manifest.Organization{ID: "acme", Name: "Acme Example"},
	}
	manifestRoot := t.TempDir()
	expected, err := Compose(manifestRoot, doc)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("missing", func(t *testing.T) {
		res, err := Check(t.TempDir(), manifestRoot, doc)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "missing" || res.Message != "run our setup" {
			t.Fatalf("Check() = %#v", res)
		}
	})

	t.Run("unmanaged", func(t *testing.T) {
		root := t.TempDir()
		writeGuidanceTestFile(t, filepath.Join(root, agentsFile), "local instructions\n")
		res, err := Check(root, manifestRoot, doc)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "unmanaged" || !strings.Contains(res.Message, "--force") {
			t.Fatalf("Check() = %#v", res)
		}
	})

	t.Run("stale", func(t *testing.T) {
		root := t.TempDir()
		writeGuidanceTestFile(t, filepath.Join(root, agentsFile), marker+"\n\nold guidance\n")
		res, err := Check(root, manifestRoot, doc)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "stale" || res.Message != "run our setup" {
			t.Fatalf("Check() = %#v", res)
		}
	})

	t.Run("alias broken", func(t *testing.T) {
		root := t.TempDir()
		writeGuidanceTestFile(t, filepath.Join(root, agentsFile), string(expected))
		res, err := Check(root, manifestRoot, doc)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "alias-broken" || res.Message != "run our setup" {
			t.Fatalf("Check() = %#v", res)
		}
	})

	t.Run("ok with managed copy", func(t *testing.T) {
		root := t.TempDir()
		writeGuidanceTestFile(t, filepath.Join(root, agentsFile), string(expected))
		writeGuidanceTestFile(t, filepath.Join(root, claudeFile), string(expected))
		res, err := Check(root, manifestRoot, doc)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "ok" {
			t.Fatalf("Check() = %#v", res)
		}
	})

	t.Run("ok with symlink", func(t *testing.T) {
		root := t.TempDir()
		writeGuidanceTestFile(t, filepath.Join(root, agentsFile), string(expected))
		if err := os.Symlink(agentsFile, filepath.Join(root, claudeFile)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		res, err := Check(root, manifestRoot, doc)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "ok" {
			t.Fatalf("Check() = %#v", res)
		}
	})
}

func TestComposeWithOptionsAppendsRoleGuidance(t *testing.T) {
	manifestRoot := t.TempDir()
	writeGuidanceTestFile(t, filepath.Join(manifestRoot, "guidance", "base.md"), "base guidance\n")
	writeGuidanceTestFile(t, filepath.Join(manifestRoot, "guidance", "operator.md"), "operator guidance\n")
	doc := manifest.Document{
		AgentGuidance: manifest.AgentGuidance{Paths: []string{"guidance/base.md"}},
	}
	data, err := ComposeWithOptions(manifestRoot, doc, Options{
		RoleGuidancePaths: []string{"guidance/operator.md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "base guidance") || !strings.Contains(got, "operator guidance") {
		t.Fatalf("composed guidance missing fragments:\n%s", got)
	}
	if strings.Index(got, "base guidance") > strings.Index(got, "operator guidance") {
		t.Fatalf("role guidance should follow base guidance:\n%s", got)
	}
}

func TestComposeRendersOrganizationContract(t *testing.T) {
	manifestRoot := t.TempDir()
	writeGuidanceTestFile(t, filepath.Join(manifestRoot, "guidance", "base.md"), "base guidance\n")
	doc := manifest.Document{
		AgentGuidance: manifest.AgentGuidance{Paths: []string{"guidance/base.md"}},
		Contract: []string{
			"Always create and update a support record when working on any fleet member.",
		},
	}
	data, err := Compose(manifestRoot, doc)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "## Organization Contract") {
		t.Fatalf("composed guidance missing contract section:\n%s", got)
	}
	if !strings.Contains(got, "- Always create and update a support record when working on any fleet member.") {
		t.Fatalf("composed guidance missing contract rule:\n%s", got)
	}
	if strings.Index(got, "## Organization Contract") > strings.Index(got, "base guidance") {
		t.Fatalf("contract section should precede manifest guidance fragments:\n%s", got)
	}
}

func TestComposeOmitsEmptyOrganizationContract(t *testing.T) {
	data, err := Compose(t.TempDir(), manifest.Document{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "## Organization Contract") {
		t.Fatalf("contract section rendered with no rules:\n%s", string(data))
	}
}

func writeGuidanceTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
