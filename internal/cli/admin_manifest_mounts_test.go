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
	if err == nil || !strings.Contains(err.Error(), "selected by role(s) operator") {
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
