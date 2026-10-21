package launchprofile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
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

func roleMountDocument() manifest.Document {
	doc := testDocument()
	doc.Mounts = append(doc.Mounts, manifest.Mount{ID: "ops-docs", Kind: "docs", Mode: manifest.MountModeRole, GitURL: "https://example.com/ops-docs.git"})
	doc.Skills = append(doc.Skills, manifest.Skill{ID: "acme:ops", InstallSlug: "acme-ops", Path: "skills/acme-ops", Requires: []string{"workspace:ops-docs"}})
	doc.Roles[0].Mounts = append(doc.Roles[0].Mounts, "ops-docs")
	doc.Roles[0].Skills = append(doc.Roles[0].Skills, "acme:ops")
	return doc
}

func TestAvailableScopeExcludesRoleOnlyMountWithoutRole(t *testing.T) {
	doc := roleMountDocument()
	mounts, _, _, err := availableScope(doc, Context{Target: TargetUmbrella}, manifest.Role{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !mounts["handbook"] || !mounts["fleet"] || mounts["ops-docs"] {
		t.Fatalf("unscoped mounts = %#v", mounts)
	}
	role, hasRole, err := selectedRole(doc, "operator")
	if err != nil {
		t.Fatal(err)
	}
	mounts, _, _, err = availableScope(doc, Context{Target: TargetUmbrella}, role, hasRole)
	if err != nil {
		t.Fatal(err)
	}
	if !mounts["ops-docs"] || !mounts["handbook"] || mounts["fleet"] {
		t.Fatalf("operator mounts = %#v", mounts)
	}
}

func TestComposeRoleOnlyMountSkillsFollowRoleScope(t *testing.T) {
	doc := roleMountDocument()
	profile, err := Compose(doc, Context{Target: TargetUmbrella}, Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := profile.SkillIDs(), []string{"acme:handbook", "acme:fleet", "acme:crm"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unscoped skill ids = %#v, want %#v", got, want)
	}
	if _, err := Compose(doc, Context{Target: TargetUmbrella}, Selector{Kind: SelectorExplicit, SkillRefs: []string{"acme:ops"}}); err == nil || !strings.Contains(err.Error(), `workspace "ops-docs" outside selected launch scope`) {
		t.Fatalf("explicit role-only skill without role err = %v", err)
	}
	profile, err = Compose(doc, Context{Target: TargetUmbrella, SelectedRole: "operator"}, Selector{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := profile.SkillIDs(), []string{"acme:handbook", "acme:crm", "acme:ops"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("operator skill ids = %#v, want %#v", got, want)
	}
}
