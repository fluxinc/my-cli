package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/guidance"
	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/mcpconfig"
	"github.com/fluxinc/our-ai/internal/umbrella"
)

func TestOnboardJSONAndDoctorUmbrella(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	if err := os.MkdirAll(filepath.Join(manifestCache, "skills", "acme-handbook"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "skills", "acme-handbook", "SKILL.md"), []byte("---\nname: acme-handbook\ndescription: Acme handbook\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestCache, "manifest.json"), []byte(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "setup", "claude-code", "--copy", "--json", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"umbrella"`, `"skills"`, `"acme-handbook"`} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("onboard stdout = %q, missing %q", stdout.String(), want)
		}
	}

	stdout.Reset()
	if err := a.run([]string{"our", "doctor", "--umbrella", filepath.Join(home, "acme"), "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "umbrella\tacme\tok") {
		t.Fatalf("doctor stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "umbrella\tguidance\tok") {
		t.Fatalf("doctor stdout = %q, want guidance ok", stdout.String())
	}
}

func TestOnboardAutoRefreshesManifestBeforeGuidance(t *testing.T) {
	run := func(t *testing.T, noRefresh bool, wantFresh bool) {
		t.Helper()
		home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
		writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["guidance/fresh.md"] }
}`)
		writeCLITestFile(t, filepath.Join(writer, "guidance", "fresh.md"), "fresh guidance from manifest\n")
		commitAndPushCLIGit(t, writer, "add guidance")

		args := []string{"our", "setup", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}
		if noRefresh {
			args = append(args, "--no-refresh")
		}
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run(args); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(umbrellaRoot, "AGENTS.md"))
		if err != nil {
			t.Fatal(err)
		}
		if gotFresh := strings.Contains(string(data), "fresh guidance from manifest"); gotFresh != wantFresh {
			t.Fatalf("AGENTS.md fresh=%v, want %v\n%s", gotFresh, wantFresh, data)
		}
	}

	t.Run("refreshes", func(t *testing.T) {
		run(t, false, true)
	})
	t.Run("opt out", func(t *testing.T) {
		run(t, true, false)
	})
}

func TestOnboardArgsForLaunchCarriesNoRefresh(t *testing.T) {
	args := onboardArgsForLaunch("/home/example", "acme", "/home/example/acme", true, false)
	if !strings.Contains(strings.Join(args, " "), "--no-refresh") {
		t.Fatalf("onboard args = %#v, want --no-refresh", args)
	}
}

func TestServicesListAndGet(t *testing.T) {
	home := t.TempDir()
	writeServicesRolesManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "services", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"docs-search", "Search the handbook", "status-api", "mcp", "http"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("services list stdout missing %q in:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	if err := a.run([]string{"our", "services", "get", "docs-search", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var service struct {
		ID         string `json:"id"`
		Kind       string `json:"kind"`
		AuthRef    string `json:"auth_ref"`
		Connection struct {
			Command string `json:"command"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &service); err != nil {
		t.Fatalf("services get --json: %v in:\n%s", err, stdout.String())
	}
	if service.ID != "docs-search" || service.Kind != "mcp" || service.AuthRef != "env://ACME_DOCS_TOKEN" || service.Connection.Command != "acme-docs-mcp" {
		t.Fatalf("services get --json = %+v", service)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "services", "get", "nope", "--manifest", "acme", "--home", home}); err == nil {
		t.Fatal("services get nope should fail")
	} else if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("services get nope error = %v", err)
	}
}

func TestRolesListAndGet(t *testing.T) {
	home := t.TempDir()
	writeServicesRolesManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "roles", "list", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"operator", "Default operator role", "docs-search"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("roles list stdout missing %q in:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	if err := a.run([]string{"our", "roles", "get", "operator", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var role struct {
		ID       string   `json:"id"`
		Services []string `json:"services"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &role); err != nil {
		t.Fatalf("roles get --json: %v in:\n%s", err, stdout.String())
	}
	if role.ID != "operator" || len(role.Services) != 1 || role.Services[0] != "docs-search" {
		t.Fatalf("roles get --json = %+v", role)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "roles", "get", "nope", "--manifest", "acme", "--home", home}); err == nil {
		t.Fatal("roles get nope should fail")
	}
}

func TestSetupRolePersistsStateAndFiltersGuidance(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "setup",
		"--manifest", "acme",
		"--home", home,
		"--role", "operator",
		"--no-refresh",
		"--no-update-check",
	}); err != nil {
		t.Fatalf("setup --role: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedRole != "operator" {
		t.Fatalf("selected role = %q", state.SelectedRole)
	}
	data, err := os.ReadFile(filepath.Join(umbrellaRoot, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	guidance := string(data)
	for _, want := range []string{"base guidance", "operator role guidance"} {
		if !strings.Contains(guidance, want) {
			t.Fatalf("AGENTS.md missing %q:\n%s", want, guidance)
		}
	}
	if strings.Contains(guidance, "auditor role guidance") {
		t.Fatalf("AGENTS.md included unselected role guidance:\n%s", guidance)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"our", "doctor",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--no-fetch",
	}); err != nil {
		t.Fatalf("doctor: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "umbrella\tguidance\tok") {
		t.Fatalf("doctor stdout should report guidance ok:\n%s", stdout.String())
	}
}

func TestSetupRoleRejectsUnknownRole(t *testing.T) {
	home := t.TempDir()
	writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"our", "setup",
		"--manifest", "acme",
		"--home", home,
		"--role", "missing",
		"--no-refresh",
		"--no-update-check",
	})
	if err == nil || !strings.Contains(err.Error(), `role "missing" not found; run our roles list`) {
		t.Fatalf("setup --role missing err = %v", err)
	}
}

func TestVisibleServicesHonorsSelectedRole(t *testing.T) {
	doc := manifest.Document{
		Services: []manifest.Service{
			{ID: "docs-search", Kind: "mcp", Purpose: "Search docs", AuthRef: "none"},
			{ID: "status-api", Kind: "http", Purpose: "Status API", AuthRef: "none"},
		},
		Roles: []manifest.Role{
			{ID: "operator", Purpose: "Operator", Services: []string{"docs-search"}},
		},
	}
	all, err := visibleServices(doc, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("visibleServices without role = %#v", all)
	}
	filtered, err := visibleServices(doc, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != "docs-search" {
		t.Fatalf("visibleServices operator = %#v", filtered)
	}
	if _, err := visibleServices(doc, "missing"); err == nil || !strings.Contains(err.Error(), "our roles list") {
		t.Fatalf("visibleServices missing err = %v", err)
	}
}

func TestDerivedReconcileFailedIncludesBlockedMCPConfig(t *testing.T) {
	report := derivedReconcileReport{
		Guidance: guidance.Result{Status: "ok"},
		MCP:      mcpconfig.Result{Status: "blocked"},
	}
	if !derivedReconcileFailed(report) {
		t.Fatal("blocked MCP config should fail derived reconcile")
	}
}

func TestSetupMaterializesMCPConfigForSelectedRole(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "setup",
		"--manifest", "acme",
		"--home", home,
		"--role", "operator",
		"--no-refresh",
		"--no-update-check",
	}); err != nil {
		t.Fatalf("setup --role: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(umbrellaRoot, ".mcp.json"))
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	var mcp struct {
		Servers map[string]struct {
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &mcp); err != nil {
		t.Fatalf("parse .mcp.json: %v in:\n%s", err, data)
	}
	if entry, ok := mcp.Servers["docs-search"]; !ok || entry.Command != "acme-docs-mcp" {
		t.Fatalf(".mcp.json servers = %v", mcp.Servers)
	}
}

func TestSetupSkipsMCPConfigWhenRoleGrantsNoMCPServices(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "setup",
		"--manifest", "acme",
		"--home", home,
		"--role", "auditor",
		"--no-refresh",
		"--no-update-check",
	}); err != nil {
		t.Fatalf("setup --role auditor: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, ".mcp.json")); !os.IsNotExist(err) {
		t.Fatalf(".mcp.json should not exist for auditor role: %v", err)
	}
}
