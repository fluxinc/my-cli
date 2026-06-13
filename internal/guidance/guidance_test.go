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
			"Continue an existing relevant support record or create a new dated record when working on any fleet member.",
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
	if !strings.Contains(got, "- Continue an existing relevant support record or create a new dated record when working on any fleet member.") {
		t.Fatalf("composed guidance missing contract rule:\n%s", got)
	}
	if strings.Index(got, "## Organization Contract") > strings.Index(got, "base guidance") {
		t.Fatalf("contract section should precede manifest guidance fragments:\n%s", got)
	}
}

func TestComposeRendersDomainNotesSeparateFromContract(t *testing.T) {
	manifestRoot := t.TempDir()
	writeGuidanceTestFile(t, filepath.Join(manifestRoot, "guidance", "customers-domain.md"), "Archive customers; never hard-delete.\n")
	writeGuidanceTestFile(t, filepath.Join(manifestRoot, "guidance", "customers-pii.md"), "Keep customer contact details out of public notes.\n")
	doc := manifest.Document{
		Contract: []string{"Record decisions in the handbook before acting."},
		DataBindings: map[string]manifest.DataBinding{
			"customers": {
				Surface:  "mount:handbook",
				Guidance: []string{"guidance/customers-domain.md", "guidance/customers-pii.md"},
			},
		},
	}
	data, err := Compose(manifestRoot, doc)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "## Domain Notes: customers") {
		t.Fatalf("missing domain notes section:\n%s", got)
	}
	if !strings.Contains(got, "_Source: mount:handbook_") {
		t.Fatalf("missing source attribution:\n%s", got)
	}
	if !strings.Contains(got, "Archive customers; never hard-delete.") {
		t.Fatalf("missing domain notes fragment content:\n%s", got)
	}
	if !strings.Contains(got, "Keep customer contact details out of public notes.") {
		t.Fatalf("missing second domain notes fragment content:\n%s", got)
	}
	if strings.Count(got, "## Domain Notes: customers") != 1 {
		t.Fatalf("domain notes should render one section per data type:\n%s", got)
	}
	if strings.Index(got, "Archive customers; never hard-delete.") > strings.Index(got, "Keep customer contact details out of public notes.") {
		t.Fatalf("domain notes fragments should preserve manifest order:\n%s", got)
	}
	if !strings.Contains(got, "## Organization Contract") {
		t.Fatalf("missing contract section:\n%s", got)
	}
	// Domain notes are a distinct, attributed section that follows the org contract.
	if strings.Index(got, "## Organization Contract") > strings.Index(got, "## Domain Notes: customers") {
		t.Fatalf("domain notes should follow the org contract:\n%s", got)
	}
}

func TestComposeOmitsDomainNotesWithoutGuidance(t *testing.T) {
	doc := manifest.Document{
		DataBindings: map[string]manifest.DataBinding{
			"customers": {Surface: "mount:handbook"},
		},
	}
	data, err := Compose(t.TempDir(), doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "## Domain Notes") {
		t.Fatalf("domain notes rendered with no guidance:\n%s", string(data))
	}
}

func TestComposeExampleWorkspaceDomainNotes(t *testing.T) {
	manifestRoot := filepath.Join("..", "..", "examples", "acme-workspace", "manifest")
	doc, _, err := manifest.LoadDocument(filepath.Join(manifestRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := Compose(manifestRoot, doc)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"## Domain Notes: customers",
		"_Source: mount:handbook_",
		"Archive customer records instead of hard-deleting them",
		"Treat customer contact details as sensitive",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("example guidance missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "## Domain Notes: customers") != 1 {
		t.Fatalf("example guidance should render one customers domain section:\n%s", got)
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

func TestComposeIncludesBaselineFleetWorkContract(t *testing.T) {
	data, err := Compose(t.TempDir(), manifest.Document{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"Fleet work contract:",
		"Before substantive work on a deployed instance",
		"Continue an existing relevant support record",
		"repeated `--identifier` flags",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("composed guidance missing %q:\n%s", want, got)
		}
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
