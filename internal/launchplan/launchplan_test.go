package launchplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/guidance"
	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestACMEOperatorGolden(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "acme-workspace", "manifest")
	doc, _, err := manifest.LoadDocument(root)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := Compile(doc, Options{Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "acme-operator.golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v\n\nactual:\n%s", err, got)
	}
	if string(got) != string(want) {
		t.Fatalf("projection mismatch\nwant:\n%s\n\ngot:\n%s", want, got)
	}
}

func TestBaselineFleetWorkContractMatchesGuidance(t *testing.T) {
	path := filepath.Join("..", "guidance", "baseline", "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := fleetWorkContractBullets(string(data))
	if !reflect.DeepEqual(got, baselineFleetWorkContract) {
		t.Fatalf("baseline fleet contract drifted\nwant:\n%q\n\ngot:\n%q", baselineFleetWorkContract, got)
	}
}

func TestCompileRequiresRoleWhenRolesDeclared(t *testing.T) {
	doc := baseDocument()
	doc.Roles = []manifest.Role{{
		ID:      "operator",
		Purpose: "Operate the workspace",
		Mounts:  []string{"handbook"},
	}}
	_, err := Compile(doc, Options{})
	if err == nil || !strings.Contains(err.Error(), "role is required") {
		t.Fatalf("Compile without role err = %v, want role required", err)
	}
}

func TestCompileErrorsOnUnknownRole(t *testing.T) {
	doc := baseDocument()
	doc.Roles = []manifest.Role{{
		ID:      "operator",
		Purpose: "Operate the workspace",
		Mounts:  []string{"handbook"},
	}}
	_, err := Compile(doc, Options{Role: "missing"})
	if err == nil || !strings.Contains(err.Error(), `role "missing" not found`) {
		t.Fatalf("Compile unknown role err = %v", err)
	}
}

func TestCompileAllowsUnscopedProjectionWithoutRoles(t *testing.T) {
	doc := baseDocument()
	projection, err := Compile(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if projection.Role != "" || len(projection.Mounts) != 2 || len(projection.DataBindings) != 2 {
		t.Fatalf("projection = %+v, want unscoped full projection", projection)
	}
}

func TestCompileOmitsOutOfRoleDataBindings(t *testing.T) {
	doc := baseDocument()
	doc.Roles = []manifest.Role{{
		ID:      "customers-only",
		Purpose: "Customer-facing workspace",
		Mounts:  []string{"handbook"},
		Skills:  []string{"acme:handbook"},
	}}
	projection, err := Compile(doc, Options{Role: "customers-only"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := projection.DataBindings["customers"]; !ok {
		t.Fatalf("customers binding missing: %+v", projection.DataBindings)
	}
	if _, ok := projection.DataBindings["fleet"]; ok {
		t.Fatalf("fleet binding should be omitted for partial role: %+v", projection.DataBindings)
	}
}

func TestCompileErrorsOnUnresolvedEmittedDataBinding(t *testing.T) {
	doc := baseDocument()
	doc.DataBindings["customers"] = manifest.DataBinding{Surface: "mount:missing"}
	_, err := Compile(doc, Options{})
	if err == nil || !strings.Contains(err.Error(), `data binding customers references unknown mount "missing"`) {
		t.Fatalf("Compile err = %v, want unresolved binding error", err)
	}
}

func TestCompileErrorsOnUnsatisfiedSkillRequirement(t *testing.T) {
	doc := baseDocument()
	doc.Skills = append(doc.Skills, manifest.Skill{
		ID:          "acme:crm",
		InstallSlug: "acme-crm",
		Path:        "skills/acme-crm",
		Requires:    []string{"service:crm"},
	})
	doc.Roles = []manifest.Role{{
		ID:      "bad-role",
		Purpose: "Missing CRM service",
		Mounts:  []string{"handbook"},
		Skills:  []string{"acme:crm"},
	}}
	_, err := Compile(doc, Options{Role: "bad-role"})
	if err == nil || !strings.Contains(err.Error(), `requires service "crm" outside selected role scope`) {
		t.Fatalf("Compile err = %v, want unsatisfied service requirement", err)
	}
}

func TestCompileErrorsOnToolSkillWhenSourceToolOutOfScope(t *testing.T) {
	doc := baseDocument()
	doc.Tools = append(doc.Tools, manifest.Tool{
		ID:      "clickup",
		Mode:    "optional",
		Purpose: "Project management CLI",
		SkillInstall: manifest.SkillInstall{
			Command: "clickup",
			Args:    []string{"skill", "install"},
		},
	})
	doc.Skills = append(doc.Skills, manifest.Skill{
		ID:          "acme:clickup",
		InstallSlug: "acme-clickup",
		Source: manifest.Source{
			Type: "tool",
			Tool: "clickup",
		},
	})
	doc.Roles = []manifest.Role{{
		ID:      "bad-role",
		Purpose: "Missing ClickUp tool",
		Skills:  []string{"acme:clickup"},
	}}
	_, err := Compile(doc, Options{Role: "bad-role"})
	if err == nil || !strings.Contains(err.Error(), `source tool "clickup" outside selected role scope`) {
		t.Fatalf("Compile err = %v, want source tool scope error", err)
	}
}

func TestCompileErrorsOnLocalMountURL(t *testing.T) {
	for _, gitURL := range []string{
		"/tmp/acme-handbook",
		"../acme-handbook",
		"acme-handbook",
		"~/acme-handbook",
		"file:///tmp/acme-handbook",
		`C:\Users\acme\handbook`,
	} {
		t.Run(gitURL, func(t *testing.T) {
			doc := baseDocument()
			doc.Mounts[0].GitURL = gitURL
			_, err := Compile(doc, Options{})
			if err == nil || !strings.Contains(err.Error(), "git_url") || !strings.Contains(err.Error(), "local") {
				t.Fatalf("Compile local mount err = %v", err)
			}
		})
	}
}

func TestCompileDedupesRoleGuidancePaths(t *testing.T) {
	doc := baseDocument()
	doc.AgentGuidance.Paths = []string{"agent-guidance/shared.md", "agent-guidance/shared.md"}
	doc.Roles = []manifest.Role{{
		ID:            "operator",
		Purpose:       "Operate",
		GuidancePaths: []string{"agent-guidance/shared.md", "agent-guidance/operator.md", "agent-guidance/operator.md"},
		Mounts:        []string{"handbook"},
		Skills:        []string{"acme:handbook"},
	}}
	projection, err := Compile(doc, Options{Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]int{}
	for _, ref := range projection.Guidance {
		paths[ref.Path]++
	}
	if paths["agent-guidance/shared.md"] != 1 {
		t.Fatalf("shared guidance count = %d, want 1: %#v", paths["agent-guidance/shared.md"], projection.Guidance)
	}
	if paths["agent-guidance/operator.md"] != 1 {
		t.Fatalf("operator guidance count = %d, want 1: %#v", paths["agent-guidance/operator.md"], projection.Guidance)
	}
}

func TestCompileProjectsApplicablePoliciesForRole(t *testing.T) {
	doc := baseDocument()
	doc.Roles = []manifest.Role{
		{ID: "operator", Purpose: "Operate", Mounts: []string{"handbook"}},
		{ID: "auditor", Purpose: "Audit", Mounts: []string{"fleet"}},
	}
	doc.Governance.Policies = []manifest.Policy{
		{
			ID: "workspace-policy", Title: "Workspace policy", Version: "1", SHA256: "sha256:" + strings.Repeat("a", 64),
			Mount: "handbook", Path: "policy/workspace.md", Summary: "Rules for shared work.", Topics: []string{"workspace changes"},
		},
		{
			ID: "operator-policy", Title: "Operator policy", Version: "2", SHA256: "sha256:" + strings.Repeat("b", 64),
			Mount: "handbook", Path: "policy/operator.md", Roles: []string{"operator"},
		},
		{
			ID: "auditor-policy", Title: "Auditor policy", Version: "3", SHA256: "sha256:" + strings.Repeat("c", 64),
			Mount: "fleet", Path: "policy/auditor.md", Roles: []string{"auditor"},
		},
	}

	projection, err := Compile(doc, Options{Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Policies) != 2 {
		t.Fatalf("projected policies = %#v, want universal and operator", projection.Policies)
	}
	if got := projection.Policies[0]; got.ID != "workspace-policy" || got.Title != "Workspace policy" || got.Version != "1" ||
		got.SHA256 != "sha256:"+strings.Repeat("a", 64) || got.Mount != "handbook" || got.Path != "policy/workspace.md" ||
		got.Summary != "Rules for shared work." || !reflect.DeepEqual(got.Topics, []string{"workspace changes"}) {
		t.Fatalf("universal policy projection = %#v", got)
	}
	if projection.Policies[1].ID != "operator-policy" {
		t.Fatalf("selected-role policy projection = %#v", projection.Policies[1])
	}
	for _, policy := range projection.Policies {
		if policy.ID == "auditor-policy" {
			t.Fatalf("projection leaked another role's policy: %#v", projection.Policies)
		}
	}
}

func TestCompileRejectsApplicablePolicyMountOutsideRole(t *testing.T) {
	doc := baseDocument()
	doc.Roles = []manifest.Role{{ID: "operator", Purpose: "Operate", Mounts: []string{"handbook"}}}
	doc.Governance.Policies = []manifest.Policy{{
		ID: "fleet-policy", Title: "Fleet policy", Version: "1", SHA256: "sha256:" + strings.Repeat("a", 64),
		Mount: "fleet", Path: "policy/fleet.md",
	}}
	_, err := Compile(doc, Options{Role: "operator"})
	if err == nil || !strings.Contains(err.Error(), `policy "fleet-policy" requires mount "fleet" outside selected role scope`) {
		t.Fatalf("Compile policy mount visibility err = %v", err)
	}
}

func TestCompilePolicyProjectionMatchesGuidanceScopeAndMetadata(t *testing.T) {
	doc := baseDocument()
	doc.AgentGuidance.Paths = nil
	doc.Roles = []manifest.Role{
		{ID: "operator", Purpose: "Operate", Mounts: []string{"handbook"}},
		{ID: "auditor", Purpose: "Audit", Mounts: []string{"fleet"}},
	}
	doc.Governance.Policies = []manifest.Policy{
		{
			ID: "workspace-policy", Title: "Workspace policy", Version: "1", SHA256: "sha256:" + strings.Repeat("a", 64),
			Mount: "handbook", Path: "policy/workspace.md", Summary: "Rules for shared work.", Topics: []string{"workspace changes"},
		},
		{
			ID: "auditor-policy", Title: "Auditor policy", Version: "1", SHA256: "sha256:" + strings.Repeat("b", 64),
			Mount: "fleet", Path: "policy/auditor.md", Roles: []string{"auditor"},
		},
	}
	projection, err := Compile(doc, Options{Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	composed, err := guidance.ComposeWithOptions(t.TempDir(), doc, guidance.Options{Role: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Policies) != 1 {
		t.Fatalf("projected policies = %#v", projection.Policies)
	}
	for _, want := range []string{"Workspace policy", "workspace-policy", "version `1`", "Rules for shared work.", "workspace changes"} {
		if !strings.Contains(string(composed), want) {
			t.Fatalf("guidance missing projected policy metadata %q:\n%s", want, composed)
		}
	}
	if strings.Contains(string(composed), "Auditor policy") {
		t.Fatalf("guidance leaked policy omitted by projection:\n%s", composed)
	}
}

func TestMarshalOmitsPoliciesForNonGovernedManifest(t *testing.T) {
	projection, err := Compile(baseDocument(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["policies"]; ok {
		t.Fatalf("non-governed projection emitted policies: %s", data)
	}
}

func baseDocument() manifest.Document {
	return manifest.Document{
		ManifestVersion: 1,
		Organization: manifest.Organization{
			ID:   "acme",
			Name: "Acme Example",
		},
		AgentGuidance: manifest.AgentGuidance{Paths: []string{"agent-guidance/acme.md"}},
		Mounts: []manifest.Mount{
			{
				ID:           "handbook",
				Kind:         "handbook",
				GitURL:       "https://github.com/example/acme-handbook.git",
				Mode:         "default",
				IncludePaths: []string{"customers", "meetings"},
			},
			{
				ID:           "fleet",
				Kind:         "fleet",
				GitURL:       "https://github.com/example/acme-fleet.git",
				Mode:         "default",
				IncludePaths: []string{"fleet"},
			},
		},
		DataBindings: map[string]manifest.DataBinding{
			"customers": {Surface: "mount:handbook"},
			"fleet":     {Surface: "mount:fleet"},
		},
		Services: []manifest.Service{
			{
				ID:          "crm",
				Kind:        "mcp",
				Purpose:     "CRM access",
				DescribeRef: "services/crm.server.json",
				AuthRef:     "env://CRM_TOKEN",
			},
		},
		Skills: []manifest.Skill{
			{
				ID:           "acme:handbook",
				InstallSlug:  "acme-handbook",
				Path:         "skills/acme-handbook",
				Capabilities: []string{"customers"},
				Requires:     []string{"workspace:handbook"},
			},
		},
	}
}

func fleetWorkContractBullets(markdown string) []string {
	lines := strings.Split(markdown, "\n")
	var bullets []string
	var current strings.Builder
	inSection := false
	flush := func() {
		if current.Len() == 0 {
			return
		}
		bullets = append(bullets, current.String())
		current.Reset()
	}
	for _, line := range lines {
		if !inSection {
			if strings.TrimSpace(line) == "Fleet work contract:" {
				inSection = true
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if current.Len() != 0 {
				flush()
			}
			if len(bullets) != 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "- ") {
			flush()
			current.WriteString(strings.TrimPrefix(line, "- "))
			continue
		}
		if strings.HasPrefix(line, "  ") && current.Len() != 0 {
			current.WriteString(" ")
			current.WriteString(trimmed)
		}
	}
	flush()
	return bullets
}
