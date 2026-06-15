package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
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
	for _, want := range []string{`"umbrella"`, `"skills"`, "launch-scoped"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("onboard stdout = %q, missing %q", stdout.String(), want)
		}
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("setup installed org skill globally: %v", err)
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

func TestSetupOpenCodeInstallsCompatibilityGlobalOrgSkills(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchProfileFixture(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "setup", "opencode", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatal(err)
	}
	globalSkillsDir := filepath.Join(home, ".config", "opencode", "skills")
	assertIndexedGlobalSkill(t, globalSkillsDir, "acme-handbook", "acme:handbook", compatibilityGlobalSkillScope)
	assertIndexedGlobalSkill(t, globalSkillsDir, "acme-calendar", "acme:calendar", compatibilityGlobalSkillScope)
	if _, err := os.Lstat(filepath.Join(umbrellaRoot, ".agents", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("setup should not materialize unread opencode launch skill: %v", err)
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

func TestSetupArgsForLaunchCarriesNoRefresh(t *testing.T) {
	args := setupArgsForLaunch("/home/example", "acme", "/home/example/acme", true, false)
	if !strings.Contains(strings.Join(args, " "), "--no-refresh") {
		t.Fatalf("setup args = %#v, want --no-refresh", args)
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

func TestSetupInteractiveRejectsMachineOutputModes(t *testing.T) {
	for _, flag := range []string{"--json", "--print"} {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{"our", "setup", "--interactive", flag})
		if err == nil || !strings.Contains(err.Error(), "--interactive and "+flag+" are mutually exclusive") {
			t.Fatalf("setup --interactive %s err = %v", flag, err)
		}
	}
}

func TestSetupInteractiveSelectsManifestAndErrorsOnAmbiguousEOF(t *testing.T) {
	home := t.TempDir()
	writeSimpleSetupManifest(t, home, "acme", "Acme Example")
	writeSimpleSetupManifest(t, home, "beta", "Beta Example")

	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		stdin:  bufio.NewReader(strings.NewReader("2\n")),
	}
	if err := a.run([]string{"our", "setup", "--interactive", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("setup --interactive: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	ws, err := umbrella.LoadWorkspace(filepath.Join(home, "beta"))
	if err != nil {
		t.Fatal(err)
	}
	if ws.ManifestRef != "beta" {
		t.Fatalf("manifest ref = %q", ws.ManifestRef)
	}

	stdout.Reset()
	stderr.Reset()
	a = app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader(""))}
	err = a.run([]string{"our", "setup", "--interactive", "--home", home})
	if err == nil || !strings.Contains(err.Error(), "pass --manifest") {
		t.Fatalf("setup ambiguous EOF err = %v", err)
	}
}

func TestSetupInteractiveRoleDefaultAndClear(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home, "--role", "operator", "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	a = app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader(""))}
	if err := a.run([]string{"our", "setup", "--interactive", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("setup --interactive default: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedRole != "operator" {
		t.Fatalf("selected role after EOF default = %q", state.SelectedRole)
	}

	stdout.Reset()
	stderr.Reset()
	a = app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader("none\n"))}
	if err := a.run([]string{"our", "setup", "--interactive", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("setup --interactive clear: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	state, err = umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedRole != "" {
		t.Fatalf("selected role after clear = %q", state.SelectedRole)
	}
}

func TestSetupInteractiveExplicitRoleSkipsRolePrompt(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader(""))}
	if err := a.run([]string{"our", "setup", "--interactive", "--manifest", "acme", "--home", home, "--role", "auditor", "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("setup --interactive --role: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "Role [") {
		t.Fatalf("explicit role should not prompt for role:\n%s", stdout.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedRole != "auditor" {
		t.Fatalf("selected role = %q", state.SelectedRole)
	}
}

func TestOnboardZeroManifestPrintsGuidanceAndDoesNotMark(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "onboard", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"our manifests add <name> <git-url>", "onboard\tunmarked"} {
		if !strings.Contains(out, want) {
			t.Fatalf("onboard zero stdout missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".our", "state.json")); !os.IsNotExist(err) {
		t.Fatalf("zero-manifest onboard should not write state: %v", err)
	}
}

func TestOnboardUnconfiguredSkipLeavesTourUnmarked(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader("n\n"))}
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if _, err := umbrella.LoadState(umbrellaRoot); !os.IsNotExist(err) {
		t.Fatalf("declined unconfigured onboard should not create state: %v", err)
	}
	if !strings.Contains(stdout.String(), "onboard\tunmarked") {
		t.Fatalf("stdout missing unmarked:\n%s", stdout.String())
	}
}

func TestOnboardFirstRunDelegatesSetupAndMarksTour(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		stdin:  bufio.NewReader(strings.NewReader("y\noperator\n")),
	}
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard first run: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedRole != "operator" {
		t.Fatalf("selected role = %q", state.SelectedRole)
	}
	if state.Tour == nil || state.Tour.CompletedAt == "" || state.Tour.Version != onboardingTourVersion {
		t.Fatalf("tour state = %#v", state.Tour)
	}
}

func TestOnboardConfiguredButNotTouredDeclinePreservesRoleAndLeavesTourUnmarked(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home, "--role", "auditor", "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()
	a = app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader("n\n"))}
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard configured: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedRole != "auditor" {
		t.Fatalf("selected role = %q", state.SelectedRole)
	}
	if state.Tour != nil {
		t.Fatalf("tour was marked unexpectedly: %#v", state.Tour)
	}
	if !strings.Contains(stdout.String(), "onboard\tunmarked") {
		t.Fatalf("stdout missing unmarked:\n%s", stdout.String())
	}
}

func TestOnboardAlreadyCompleteIsPureReview(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.SelectedRole = "operator"
	state.Tour = &umbrella.TourState{CompletedAt: "2026-06-14T00:00:00Z", Version: onboardingTourVersion}
	if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader("n\n"))}
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "onboard\tcomplete") || strings.Contains(out, "Review setup interactively") {
		t.Fatalf("already-complete onboard stdout:\n%s", out)
	}
	after, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if after.SelectedRole != "operator" || after.Tour.CompletedAt != "2026-06-14T00:00:00Z" {
		t.Fatalf("state mutated unexpectedly: %#v", after)
	}
}

func TestOnboardOldTourVersionSoftNotifiesWithoutRetour(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.Tour = &umbrella.TourState{CompletedAt: "2026-06-14T00:00:00Z", Version: 0}
	if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader(""))}
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "onboard\tupdated") || strings.Contains(out, "Run setup interactively now?") {
		t.Fatalf("old-version onboard stdout:\n%s", out)
	}
	after, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if after.Tour.Version != 0 {
		t.Fatalf("old tour marker should not be rewritten: %#v", after.Tour)
	}
}

func TestOnboardHelpAndNoVerbBloat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "onboard", "--help"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Usage of our onboard") {
		t.Fatalf("onboard help stderr:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"our", "configuration"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("configuration err = %v", err)
	}
}

func writeSimpleSetupManifest(t *testing.T, home, name, orgName string) {
	t.Helper()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", name)
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "`+name+`", "name": "`+orgName+`" },
  "umbrella": { "recommended_path": "~/`+name+`" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", name,
		"https://github.com/acme/" + name + "-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
}
