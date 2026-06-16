package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestAdminRolesAddEditRemove(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, adminRoleServiceFixtureExtra())

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "admin", "roles", "add", "operator",
		"--manifest-dir", manifestDir,
		"--purpose", "Default operator",
		"--guidance", "guidance/operator.md",
		"--mount", "handbook",
		"--skill", "acme:handbook",
		"--tool", "qmd",
		"--service", "docs-search",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Roles) != 1 || doc.Roles[0].ID != "operator" || doc.Roles[0].Purpose != "Default operator" {
		t.Fatalf("roles after add = %#v", doc.Roles)
	}
	if strings.Join(doc.Roles[0].GuidancePaths, ",") != "guidance/operator.md" ||
		strings.Join(doc.Roles[0].Mounts, ",") != "handbook" ||
		strings.Join(doc.Roles[0].Skills, ",") != "acme:handbook" ||
		strings.Join(doc.Roles[0].Tools, ",") != "qmd" ||
		strings.Join(doc.Roles[0].Services, ",") != "docs-search" {
		t.Fatalf("role selections after add = %#v", doc.Roles[0])
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "admin", "roles", "edit", "operator",
		"--manifest-dir", manifestDir,
		"--purpose", "Updated operator",
		"--clear-skills",
		"--clear-tools",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err = manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Roles[0].Purpose != "Updated operator" || len(doc.Roles[0].Skills) != 0 || len(doc.Roles[0].Tools) != 0 {
		t.Fatalf("role after edit = %#v", doc.Roles[0])
	}
	if strings.Join(doc.Roles[0].Services, ",") != "docs-search" {
		t.Fatalf("edit should preserve absent list flags: %#v", doc.Roles[0])
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "admin", "roles", "remove", "operator",
		"--manifest-dir", manifestDir,
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err = manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Roles) != 0 {
		t.Fatalf("roles after remove = %#v", doc.Roles)
	}
}

func TestAdminRolesRejectsUnknownSelections(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, adminRoleServiceFixtureExtra())

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"my", "admin", "roles", "add", "operator",
		"--manifest-dir", manifestDir,
		"--purpose", "Default operator",
		"--mount", "missing-mount",
	})
	if err == nil || !strings.Contains(err.Error(), `unknown mount "missing-mount"`) {
		t.Fatalf("err = %v, want unknown mount validation", err)
	}
}

func TestAdminServicesAddEditAndRejectsLiteralConnectionSecrets(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "admin", "services", "add", "docs-search",
		"--manifest-dir", manifestDir,
		"--kind", "mcp",
		"--purpose", "Search docs",
		"--auth-ref", "env://DOCS_TOKEN",
		"--connection-type", "stdio",
		"--connection-command", "docs-mcp",
		"--connection-arg", "--stdio",
		"--connection-env", "DOCS_TOKEN=${DOCS_TOKEN}",
		"--connection-header", "Authorization=Bearer ${DOCS_AUTH_HEADER}",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Services) != 1 || doc.Services[0].ID != "docs-search" || doc.Services[0].Connection.Command != "docs-mcp" {
		t.Fatalf("services after add = %#v", doc.Services)
	}
	if doc.Services[0].Connection.Env["DOCS_TOKEN"] != "${DOCS_TOKEN}" ||
		doc.Services[0].Connection.Headers["Authorization"] != "Bearer ${DOCS_AUTH_HEADER}" {
		t.Fatalf("service connection refs = %#v", doc.Services[0].Connection)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "admin", "services", "edit", "docs-search",
		"--manifest-dir", manifestDir,
		"--purpose", "Search all docs",
		"--connection-env", "DOCS_TOKEN=${DOCS_TOKEN_V2}",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err = manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Services[0].Purpose != "Search all docs" || doc.Services[0].Connection.Env["DOCS_TOKEN"] != "${DOCS_TOKEN_V2}" {
		t.Fatalf("services after edit = %#v", doc.Services)
	}

	err = a.run([]string{
		"my", "admin", "services", "add", "bad-service",
		"--manifest-dir", manifestDir,
		"--kind", "mcp",
		"--purpose", "Bad",
		"--auth-ref", "none",
		"--connection-command", "bad-mcp",
		"--connection-env", "TOKEN=literal-secret",
	})
	if err == nil || !strings.Contains(err.Error(), "value must be ${VAR}") {
		t.Fatalf("err = %v, want literal secret rejection", err)
	}

	err = a.run([]string{
		"my", "admin", "services", "add", "bad-env-ref",
		"--manifest-dir", manifestDir,
		"--kind", "mcp",
		"--purpose", "Bad env ref",
		"--auth-ref", "none",
		"--connection-command", "bad-mcp",
		"--connection-env", "TOKEN=env://DOCS_TOKEN",
	})
	if err == nil || !strings.Contains(err.Error(), "value must be ${VAR}") {
		t.Fatalf("err = %v, want env:// connection env rejection", err)
	}

	err = a.run([]string{
		"my", "admin", "services", "add", "bad-header",
		"--manifest-dir", manifestDir,
		"--kind", "mcp",
		"--purpose", "Bad header",
		"--auth-ref", "none",
		"--connection-command", "bad-mcp",
		"--connection-header", "Authorization=env://DOCS_AUTH_HEADER",
	})
	if err == nil || !strings.Contains(err.Error(), "value must include ${VAR}") {
		t.Fatalf("err = %v, want env:// connection header rejection", err)
	}
}

func TestAdminServicesRemovePrunesRoles(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, adminRoleServiceFixtureExtra()+`,
  "roles": [
    {
      "id": "operator",
      "purpose": "Default operator",
      "services": ["docs-search"]
    }
  ]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"my", "admin", "services", "remove", "docs-search",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "--prune-roles") {
		t.Fatalf("err = %v, want prune roles blocker", err)
	}

	if err := a.run([]string{
		"my", "admin", "services", "remove", "docs-search",
		"--manifest-dir", manifestDir,
		"--prune-roles",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Services) != 0 {
		t.Fatalf("services after remove = %#v", doc.Services)
	}
	if len(doc.Roles) != 1 || len(doc.Roles[0].Services) != 0 {
		t.Fatalf("role services after prune = %#v", doc.Roles)
	}
}

func TestServiceEnvVarsIncludesConnectionHeadersAndEnvRefs(t *testing.T) {
	service := manifest.Service{
		AuthRef: "env://AUTH_TOKEN",
		Connection: manifest.ServiceConnection{
			Env:     map[string]string{"DOCS_TOKEN": "${DOCS_TOKEN}"},
			Headers: map[string]string{"Authorization": "Bearer ${HEADER_TOKEN}"},
		},
	}
	got := strings.Join(serviceEnvVars(service), ",")
	if got != "AUTH_TOKEN,DOCS_TOKEN,HEADER_TOKEN" {
		t.Fatalf("service env vars = %q", got)
	}
}

func adminRoleServiceFixtureExtra() string {
	return `,
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/handbook.git",
      "mode": "required"
    }
  ],
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "tools": [
    { "id": "qmd", "mode": "optional", "purpose": "Search markdown" }
  ],
  "services": [
    {
      "id": "docs-search",
      "kind": "mcp",
      "purpose": "Search docs",
      "auth_ref": "env://DOCS_TOKEN",
      "connection": {
        "command": "docs-mcp",
        "env": { "DOCS_TOKEN": "${DOCS_TOKEN}" }
      }
    }
  ]`
}
