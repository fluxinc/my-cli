package launchprofile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/manifest"
)

func TestComposeUmbrellaDefaultSelectsAllOrgSkills(t *testing.T) {
	profile, err := Compose(testDocument(), Context{Target: TargetUmbrella}, Selector{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acme:handbook", "acme:fleet", "acme:crm"}
	if got := profile.SkillIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill ids = %#v, want %#v", got, want)
	}
}

func TestComposeRoleDefaultNarrowsToRoleSkills(t *testing.T) {
	profile, err := Compose(testDocument(), Context{Target: TargetUmbrella, SelectedRole: "operator"}, Selector{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acme:handbook", "acme:crm"}
	if got := profile.SkillIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill ids = %#v, want %#v", got, want)
	}
}

func TestComposeSessionDefaultUsesSatisfiedWorkspaceRequirementsAndRoleSkills(t *testing.T) {
	profile, err := Compose(testDocument(), Context{Target: TargetSession, Mounts: []string{"handbook"}}, Selector{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acme:handbook", "acme:crm"}
	if got := profile.SkillIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill ids = %#v, want %#v", got, want)
	}
}

func TestComposeExplicitAndProfileSelectors(t *testing.T) {
	doc := testDocument()
	profile, err := Compose(doc, Context{Target: TargetUmbrella}, Selector{Kind: SelectorExplicit, SkillRefs: []string{"acme:fleet", "acme:handbook"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"acme:handbook", "acme:fleet"}
	if got := profile.SkillIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit skill ids = %#v, want manifest order %#v", got, want)
	}

	profile, err = Compose(doc, Context{Target: TargetUmbrella}, Selector{Kind: SelectorProfile, ProfileID: "support"})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"acme:crm"}
	if got := profile.SkillIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("profile skill ids = %#v, want %#v", got, want)
	}
}

func TestComposeFailsOnUnsatisfiedClosure(t *testing.T) {
	_, err := Compose(testDocument(), Context{Target: TargetUmbrella, SelectedRole: "auditor"}, Selector{})
	if err == nil || !strings.Contains(err.Error(), `requires service "crm" outside selected launch scope`) {
		t.Fatalf("err = %v, want service closure failure", err)
	}
}

func TestComposeRepoRejectsExplicitOrgSkillSelectors(t *testing.T) {
	_, err := Compose(testDocument(), Context{Target: TargetRepo}, Selector{Kind: SelectorAll})
	if err == nil || !strings.Contains(err.Error(), "repo-scoped skill profiles are not supported yet") {
		t.Fatalf("err = %v, want repo selector error", err)
	}
}

func testDocument() manifest.Document {
	return manifest.Document{
		ManifestVersion: 1,
		Organization:    manifest.Organization{ID: "acme", Name: "Acme"},
		Mounts: []manifest.Mount{
			{ID: "handbook", Kind: "handbook", Mode: "default", GitURL: "https://example.com/handbook.git"},
			{ID: "fleet", Kind: "fleet", Mode: "default", GitURL: "https://example.com/fleet.git"},
		},
		Services: []manifest.Service{
			{ID: "crm", Kind: "mcp", Purpose: "CRM", AuthRef: "env://CRM_TOKEN"},
		},
		Skills: []manifest.Skill{
			{ID: "acme:handbook", InstallSlug: "acme-handbook", Path: "skills/acme-handbook", Requires: []string{"workspace:handbook"}},
			{ID: "acme:fleet", InstallSlug: "acme-fleet", Path: "skills/acme-fleet", Requires: []string{"workspace:fleet"}},
			{ID: "acme:crm", InstallSlug: "acme-crm", Path: "skills/acme-crm", Requires: []string{"service:crm"}},
		},
		Roles: []manifest.Role{
			{ID: "operator", Purpose: "Operator", Mounts: []string{"handbook"}, Services: []string{"crm"}, Skills: []string{"acme:handbook", "acme:crm"}},
			{ID: "auditor", Purpose: "Auditor", Mounts: []string{"handbook"}, Skills: []string{"acme:crm"}},
		},
		Profiles: []manifest.Profile{
			{ID: "support", Skills: []string{"acme:crm"}},
		},
	}
}
