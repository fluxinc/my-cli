package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func adminManifestMountsFixture() string {
	return adminRoleServiceFixtureExtra() + `,
  "roles": [
    { "id": "operator", "purpose": "Default operator", "mounts": ["handbook"] }
  ]`
}

func TestAdminManifestMountsAddRoleModeWithRoleAndRemoveRefusal(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, adminManifestMountsFixture())

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "admin", "manifest-mounts", "add", "ops-docs",
		"--manifest-dir", manifestDir,
		"--kind", "docs",
		"--git-url", "https://github.com/acme/ops-docs.git",
		"--mode", "role",
		"--include", "runbooks",
		"--role", "operator",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Mounts) != 2 || doc.Mounts[1].ID != "ops-docs" || doc.Mounts[1].Mode != "role" || doc.Mounts[1].Kind != "docs" || strings.Join(doc.Mounts[1].IncludePaths, ",") != "runbooks" {
		t.Fatalf("mounts after add = %#v", doc.Mounts)
	}
	if strings.Join(doc.Roles[0].Mounts, ",") != "handbook,ops-docs" {
		t.Fatalf("role mounts after add = %#v", doc.Roles[0].Mounts)
	}

	err = a.run([]string{"my", "admin", "manifest-mounts", "remove", "ops-docs", "--manifest-dir", manifestDir})
	if err == nil || !strings.Contains(err.Error(), "selected by roles: operator") || !strings.Contains(err.Error(), "--prune-roles") {
		t.Fatalf("err = %v, want selected-by-role refusal", err)
	}
	doc, _, err = manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Mounts) != 2 {
		t.Fatalf("refused remove changed mounts: %#v", doc.Mounts)
	}

	if err := a.run([]string{"my", "admin", "roles", "edit", "operator", "--manifest-dir", manifestDir, "--mount", "handbook"}); err == nil || !strings.Contains(err.Error(), "not selected by any role") {
		t.Fatalf("err = %v, want dangling role mount validation when deselecting", err)
	}
}

func TestAdminManifestMountsRemoveUnselectedMount(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, adminManifestMountsFixture())

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "admin", "manifest-mounts", "add", "archive",
		"--manifest-dir", manifestDir,
		"--kind", "docs",
		"--git-url", "https://github.com/acme/archive.git",
		"--mode", "optional",
		"--json",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"action": "added"`) {
		t.Fatalf("json output = %s", stdout.String())
	}
	if err := a.run([]string{"my", "admin", "manifest-mounts", "remove", "archive", "--manifest-dir", manifestDir}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Mounts) != 1 || doc.Mounts[0].ID != "handbook" {
		t.Fatalf("mounts after remove = %#v", doc.Mounts)
	}
}

func TestAdminManifestMountsAddRejectsInvalidDeclarations(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, adminManifestMountsFixture())
	before, err := os.ReadFile(filepath.Join(manifestDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		id   string
		args []string
		want string
	}{
		{"dangling role mount", "ops-docs", []string{"--mode", "role"}, "not selected by any role"},
		{"unknown mode", "ops-docs", []string{"--mode", "sometimes"}, `mode "sometimes" is unsupported`},
		{"unknown role", "ops-docs", []string{"--mode", "role", "--role", "ghost"}, `role "ghost" is not declared`},
		{"duplicate", "handbook", []string{"--mode", "optional"}, "already exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			a := app{stdout: &stdout, stderr: &stderr}
			err := a.run(append([]string{
				"my", "admin", "manifest-mounts", "add", tc.id,
				"--manifest-dir", manifestDir,
				"--kind", "docs",
				"--git-url", "https://github.com/acme/ops-docs.git",
			}, tc.args...))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	after, err := os.ReadFile(filepath.Join(manifestDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("rejected adds changed manifest:\n%s", after)
	}
}

func TestAdminMountsStillAliasesUmbrellaMounts(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "admin", "mounts", "list"})
	if err == nil || !strings.Contains(err.Error(), "use my mounts list") {
		t.Fatalf("err = %v, want operational-read alias error", err)
	}
}

func writeRoleMountPruneManifest(t *testing.T, dir, extraSkills string) {
	t.Helper()
	writeAdminManifest(t, dir, `,
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "https://github.com/acme/handbook.git", "mode": "required" },
    { "id": "ops-docs", "kind": "docs", "git_url": "https://github.com/acme/ops-docs.git", "mode": "role" }
  ],
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }`+extraSkills+`
  ],
  "roles": [
    { "id": "operator", "purpose": "Operator", "mounts": ["handbook", "ops-docs"] },
    { "id": "oncall", "purpose": "On call", "mounts": ["ops-docs"] },
    { "id": "viewer", "purpose": "Viewer", "mounts": ["handbook"] }
  ]`)
}

func TestAdminManifestMountsRemovePruneRolesRemovesRoleMount(t *testing.T) {
	manifestDir := t.TempDir()
	writeRoleMountPruneManifest(t, manifestDir, "")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "admin", "manifest-mounts", "remove", "ops-docs", "--manifest-dir", manifestDir})
	if err == nil || !strings.Contains(err.Error(), "selected by roles: operator, oncall") {
		t.Fatalf("err = %v, want refusal without --prune-roles", err)
	}
	if err := a.run([]string{"my", "admin", "manifest-mounts", "remove", "ops-docs", "--manifest-dir", manifestDir, "--prune-roles", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"pruned_roles": [`) || !strings.Contains(stdout.String(), `"oncall"`) {
		t.Fatalf("json output = %s", stdout.String())
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Mounts) != 1 || doc.Mounts[0].ID != "handbook" {
		t.Fatalf("mounts after prune = %#v", doc.Mounts)
	}
	got := map[string]string{}
	for _, role := range doc.Roles {
		got[role.ID] = strings.Join(role.Mounts, ",")
	}
	if len(doc.Roles) != 3 || got["operator"] != "handbook" || got["oncall"] != "" || got["viewer"] != "handbook" {
		t.Fatalf("roles after prune = %#v", doc.Roles)
	}
	if result := manifest.ValidateDocument(manifestDir, doc); len(result.Errors) != 0 {
		t.Fatalf("pruned manifest invalid: %#v", result.Errors)
	}
}

func TestAdminManifestMountsRemovePruneRolesLastSelectingRole(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, adminManifestMountsFixture())
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "admin", "manifest-mounts", "add", "ops-docs",
		"--manifest-dir", manifestDir, "--kind", "docs",
		"--git-url", "https://github.com/acme/ops-docs.git",
		"--mode", "role", "--role", "operator",
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.run([]string{"my", "admin", "manifest-mounts", "remove", "ops-docs", "--manifest-dir", manifestDir, "--prune-roles"}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Mounts) != 1 || strings.Join(doc.Roles[0].Mounts, ",") != "handbook" {
		t.Fatalf("after prune mounts = %#v roles = %#v", doc.Mounts, doc.Roles)
	}
}

func TestAdminManifestMountsRemovePruneRolesValidationFailureLeavesManifest(t *testing.T) {
	manifestDir := t.TempDir()
	// A skill still requires the mount, so the pruned manifest is invalid.
	writeRoleMountPruneManifest(t, manifestDir, `,
    { "id": "acme:ops", "install_slug": "acme-ops", "path": "skills/acme-ops", "requires": ["workspace:ops-docs"] }`)
	path := filepath.Join(manifestDir, "manifest.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err = a.run([]string{"my", "admin", "manifest-mounts", "remove", "ops-docs", "--manifest-dir", manifestDir, "--prune-roles"})
	if err == nil || !strings.Contains(err.Error(), "updated manifest is invalid") || !strings.Contains(err.Error(), `requires unknown workspace or mount "ops-docs"`) {
		t.Fatalf("err = %v, want skill-requirement validation failure", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("failed prune changed manifest:\n%s", after)
	}
}
