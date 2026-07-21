package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/access"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

type policyTestFixture struct {
	home           string
	umbrellaRoot   string
	manifestCache  string
	manifestWriter string
	handbook       string
	content        string
	digest         string
}

func newPolicyTestFixture(t *testing.T) policyTestFixture {
	t.Helper()
	content := "# Release approval\n\nAn authorized operator approves every release.\n"
	sum := sha256.Sum256([]byte(content))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	body := fmt.Sprintf(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "sync": { "publish_policy": "pr", "pull_request_auto_merge": true },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "git@github.com:example/handbook.git", "mode": "required" }
  ],
  "roles": [
    { "id": "operator", "purpose": "Operate the example workspace", "mounts": ["handbook"] }
  ],
  "governance": {
    "authorization": {
      "provider": "github",
      "manifest_repository": "example/control",
      "admin_permission": "admin"
    },
    "policies": [
      {
        "id": "release-policy",
        "title": "Release approval policy",
        "mount": "handbook",
        "path": "policy/release.md",
        "version": "2026-07",
        "sha256": %q,
        "acceptance": "required",
        "roles": ["operator"]
      }
    ],
    "attestations": {
      "mount": "handbook",
      "path": "policy/attestations",
      "identity": "github"
    },
    "record_domains": [
      {"id":"decisions","title":"Decisions","mount":"handbook","path":"decisions","retention":"no-delete","admin_override":true,"review":"codeowner","publish":"auto-pr"}
    ],
    "protections": [
      { "mount": "handbook", "paths": ["policy/attestations"], "mode": "append-only" }
    ]
  }
}`, digest)
	home, umbrellaRoot, manifestCache, _, manifestWriter := setupCLITrackedManifestBody(t, body)
	handbook := filepath.Join(umbrellaRoot, "handbook")
	writeCLITestFile(t, filepath.Join(handbook, "policy", "release.md"), content)
	initCLIGitRepo(t, handbook)
	return policyTestFixture{
		home: home, umbrellaRoot: umbrellaRoot, manifestCache: manifestCache, manifestWriter: manifestWriter,
		handbook: handbook, content: content, digest: digest,
	}
}

func (f policyTestFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return f.runWithRunner(t, governedAccessRunner(false), args...)
}

func (f policyTestFixture) runWithRunner(t *testing.T, runner access.Runner, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: runner}
	command := append([]string{"my", "policy"}, args...)
	command = append(command, "--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot)
	err := a.run(command)
	return stdout.String(), err
}

func TestPolicyAcceptanceSurvivesGitHubLoginRename(t *testing.T) {
	f := newPolicyTestFixture(t)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}

	renamedRunner := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if joined == "api user" {
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator-renamed"}`), nil
		}
		body := `{"id":29,"node_id":"R_control","full_name":"example/control","private":true,"permissions":{"pull":true}}`
		return accessGitHubResponse(200, body), nil
	}
	status, err := f.runWithRunner(t, renamedRunner, "status", "release-policy")
	if err != nil || !strings.Contains(status, "accepted-locally") {
		t.Fatalf("status after login rename = %q, %v", status, err)
	}
	if _, err := f.runWithRunner(t, renamedRunner, "accept", "release-policy", "--yes"); err != nil {
		t.Fatalf("idempotent accept after login rename: %v", err)
	}
	after, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("login rename rewrote immutable acceptance evidence")
	}
}

func (f policyTestFixture) attestationPath() string {
	return filepath.Join(
		f.handbook, "policy", "attestations", "17", "release-policy",
		strings.TrimPrefix(f.digest, "sha256:")+".json",
	)
}

func (f policyTestFixture) configureGovernedOperator(t *testing.T) {
	t.Helper()
	_, state, err := umbrella.Ensure(f.umbrellaRoot, "acme", "acme")
	if err != nil {
		t.Fatal(err)
	}
	state.SelectedRole = "operator"
	if err := umbrella.SaveState(f.umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := testAccessMonitorApp(app{
		stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
	}, f.home)
	if err := a.run([]string{
		"my", "access", "activate", "--yes", "--manifest", "acme",
		"--home", f.home, "--umbrella", f.umbrellaRoot,
	}); err != nil {
		t.Fatalf("activate governed fixture: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
}

func TestPolicyListShowStatusAndAcceptanceLifecycle(t *testing.T) {
	f := newPolicyTestFixture(t)

	list, err := f.run(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list, "release-policy\t2026-07\trequired\t"+f.digest+"\tRelease approval policy") {
		t.Fatalf("list = %q", list)
	}

	// The reviewed bytes come from the committed blob, so platform checkout
	// line-ending conversion cannot change the document being accepted.
	writeCLITestFile(t, filepath.Join(f.handbook, "policy", "release.md"), strings.ReplaceAll(f.content, "\n", "\r\n"))
	shown, err := f.run(t, "show", "release-policy")
	if err != nil {
		t.Fatal(err)
	}
	if shown != f.content {
		t.Fatalf("show returned working-tree bytes\ngot:  %q\nwant: %q", shown, f.content)
	}
	runCLIGit(t, f.handbook, "restore", "--", "policy/release.md")

	status, err := f.run(t, "status", "release-policy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "release-policy\tmissing\t2026-07") {
		t.Fatalf("initial status = %q", status)
	}

	if _, err := f.run(t, "accept", "release-policy"); err == nil || !strings.Contains(err.Error(), "requires --yes") {
		t.Fatalf("accept without confirmation error = %v", err)
	}
	if _, err := os.Lstat(f.attestationPath()); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed acceptance wrote evidence: %v", err)
	}

	accepted, err := f.run(t, "accept", "release-policy", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(accepted, "release-policy\taccepted-locally") {
		t.Fatalf("accept = %q", accepted)
	}
	first, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || first[len(first)-1] != '\n' || bytes.Count(first, []byte("\n")) != 1 {
		t.Fatalf("attestation is not compact canonical JSON: %q", first)
	}
	var record policyAttestation
	if err := json.Unmarshal(first, &record); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(first, canonical) {
		t.Fatalf("attestation field order/encoding is not canonical\ngot:  %s\nwant: %s", first, canonical)
	}
	if record.SubjectID != 17 || record.SubjectLogin != "operator" || record.SubjectProvider != "github" ||
		record.PolicySHA256 != f.digest || record.Organization != "acme" || record.ManifestCommit == "" {
		t.Fatalf("attestation = %#v", record)
	}
	if status := gitCLIOutput(t, f.handbook, "status", "--porcelain"); !strings.Contains(status, "policy/attestations/17/release-policy/") {
		t.Fatalf("attestation was not marked intent-to-add: %q", status)
	}

	local, err := f.run(t, "status", "release-policy")
	if err != nil || !strings.Contains(local, "accepted-locally") {
		t.Fatalf("local status = %q, %v", local, err)
	}

	// An unrelated manifest commit does not rewrite or invalidate immutable
	// evidence for the same exact policy digest.
	writeCLITestFile(t, filepath.Join(f.manifestCache, "unrelated.txt"), "new manifest metadata\n")
	commitCLIGit(t, f.manifestCache, "unrelated manifest change")
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatalf("idempotent accept after manifest update: %v", err)
	}
	second, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("idempotent acceptance rewrote immutable evidence")
	}

	commitCLIGit(t, f.handbook, "publish policy acceptance")
	published, err := f.run(t, "status", "release-policy")
	if err != nil || !strings.Contains(published, "published") {
		t.Fatalf("published status = %q, %v", published, err)
	}
}

func TestPolicyDigestMismatchWritesNoAttestation(t *testing.T) {
	f := newPolicyTestFixture(t)
	writeCLITestFile(t, filepath.Join(f.handbook, "policy", "release.md"), f.content+"Changed after declaration.\n")
	commitCLIGit(t, f.handbook, "change policy without manifest digest")

	if _, err := f.run(t, "show", "release-policy"); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("show mismatch error = %v", err)
	}
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("accept mismatch error = %v", err)
	}
	if _, err := os.Lstat(f.attestationPath()); !os.IsNotExist(err) {
		t.Fatalf("digest mismatch wrote acceptance evidence: %v", err)
	}
}

func TestPolicyRevisionRequiresNewDigestAcceptance(t *testing.T) {
	f := newPolicyTestFixture(t)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	oldPath := f.attestationPath()

	revised := f.content + "\nTwo-person approval is required for production.\n"
	sum := sha256.Sum256([]byte(revised))
	revisedDigest := "sha256:" + hex.EncodeToString(sum[:])
	writeCLITestFile(t, filepath.Join(f.handbook, "policy", "release.md"), revised)
	commitCLIGit(t, f.handbook, "revise release policy")

	manifestPath := filepath.Join(f.manifestCache, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	revisedManifest := strings.ReplaceAll(string(manifestBytes), f.digest, revisedDigest)
	revisedManifest = strings.ReplaceAll(revisedManifest, `"version": "2026-07"`, `"version": "2026-08"`)
	writeCLITestFile(t, manifestPath, revisedManifest)
	commitCLIGit(t, f.manifestCache, "declare revised release policy")

	status, err := f.run(t, "status", "release-policy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "release-policy\tmissing\t2026-08") || !strings.Contains(status, strings.TrimPrefix(revisedDigest, "sha256:")) {
		t.Fatalf("revised status = %q", status)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("revision removed prior immutable evidence: %v", err)
	}
}

func TestPolicyAcceptanceRejectsSymlinkParentEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated Windows privileges")
	}
	f := newPolicyTestFixture(t)
	outside := t.TempDir()
	attestations := filepath.Join(f.handbook, "policy", "attestations")
	if err := os.Symlink(outside, attestations); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink escape error = %v", err)
	}
	outsidePath := filepath.Join(outside, "17", "release-policy", strings.TrimPrefix(f.digest, "sha256:")+".json")
	if _, err := os.Lstat(outsidePath); !os.IsNotExist(err) {
		t.Fatalf("acceptance escaped ledger mount: %v", err)
	}
}

func TestPolicyHasNoEditOrDeleteVerbs(t *testing.T) {
	f := newPolicyTestFixture(t)
	for _, verb := range []string{"edit", "delete"} {
		if _, err := f.run(t, verb, "release-policy"); err == nil || !strings.Contains(err.Error(), "unknown policy subcommand") {
			t.Fatalf("policy %s error = %v", verb, err)
		}
	}
}

func TestRequiredPolicyScopeUsesSelectedRoleAndEmptyMeansEveryone(t *testing.T) {
	doc := manifest.Document{Governance: manifest.Governance{Policies: []manifest.Policy{
		{ID: "everyone", Acceptance: "required"},
		{ID: "operators", Acceptance: "required", Roles: []string{"operator"}},
		{ID: "auditors", Acceptance: "required", Roles: []string{"auditor"}},
		{ID: "optional", Acceptance: "optional"},
	}}}
	for role, want := range map[string]string{
		"":         "everyone",
		"operator": "everyone,operators",
		"auditor":  "everyone,auditors",
	} {
		policies := requiredPoliciesForRole(doc, role)
		ids := make([]string, 0, len(policies))
		for _, policy := range policies {
			ids = append(ids, policy.ID)
		}
		if got := strings.Join(ids, ","); got != want {
			t.Fatalf("role %q policies = %q, want %q", role, got, want)
		}
	}
}

func TestGovernedPolicyGateUsesBaselineIdentityAndExactRemediation(t *testing.T) {
	f := newPolicyTestFixture(t)
	f.configureGovernedOperator(t)
	doc, err := loadSingleRegisteredDoc(f.home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	a := app{accessRunner: func(string, ...string) ([]byte, error) {
		t.Fatal("fresh positive access baseline should make policy gate offline")
		return nil, nil
	}}
	err = a.requireGovernedPolicyAcceptances(f.home, doc, f.umbrellaRoot)
	if err == nil {
		t.Fatal("missing required acceptance did not block")
	}
	for _, want := range []string{
		"GitHub actor 17", "my policy show release-policy --manifest acme",
		"my policy accept release-policy --yes --manifest acme",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("gate error missing %q: %v", want, err)
		}
	}
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	if err := a.requireGovernedPolicyAcceptances(f.home, doc, f.umbrellaRoot); err != nil {
		t.Fatalf("accepted policy still blocked: %v", err)
	}
}

func TestGovernedPolicyGateIsNotBypassedByTourMarker(t *testing.T) {
	f := newPolicyTestFixture(t)
	f.configureGovernedOperator(t)
	state, err := umbrella.LoadState(f.umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.Tour = &umbrella.TourState{
		CompletedAt: time.Now().UTC().Format(time.RFC3339), Version: onboardingTourVersion,
	}
	if err := umbrella.SaveState(f.umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	launched := false
	a := app{
		stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
		lookPath:    func(string) (string, error) { return "/bin/true", nil },
		execHarness: func(string, []string, string) error { launched = true; return nil },
	}
	err = a.run([]string{
		"my", "ai", "--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot,
		"--no-session", "--no-refresh", "--no-update-check", "codex",
	})
	if err == nil || !strings.Contains(err.Error(), "has not accepted") {
		t.Fatalf("launch error = %v", err)
	}
	if launched {
		t.Fatal("stale onboarding marker bypassed current policy acceptance")
	}
}

func TestNonInteractiveOnboardingPrintsPolicyCommandsAndDoesNotComplete(t *testing.T) {
	f := newPolicyTestFixture(t)
	f.configureGovernedOperator(t)
	statePath := filepath.Join(f.umbrellaRoot, umbrella.DirName, "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", f.home,
		"--umbrella", f.umbrellaRoot, "--no-refresh", "--no-update-check",
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"Required policy acceptance is incomplete",
		"my policy show release-policy",
		"my policy accept release-policy --yes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("non-interactive onboarding missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Onboarding complete") {
		t.Fatalf("non-interactive onboarding claimed completion:\n%s", out)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("non-interactive policy onboarding changed completion state")
	}
}

func TestInteractiveOnboardingDeclineThenAcceptExactPolicy(t *testing.T) {
	f := newPolicyTestFixture(t)
	f.configureGovernedOperator(t)
	state, err := umbrella.LoadState(f.umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	state.Tour = &umbrella.TourState{CompletedAt: "2026-07-01T00:00:00Z", Version: onboardingTourVersion}
	if err := umbrella.SaveState(f.umbrellaRoot, state); err != nil {
		t.Fatal(err)
	}

	var declinedOut, declinedErr bytes.Buffer
	decline := app{
		stdout: &declinedOut, stderr: &declinedErr, stdin: strings.NewReader("n\n"), interactive: true,
		accessRunner: governedAccessRunner(false),
	}
	if err := decline.run([]string{
		"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", f.home,
		"--umbrella", f.umbrellaRoot, "--no-refresh", "--no-update-check",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(declinedOut.String(), f.content) ||
		!strings.Contains(declinedOut.String(), "Policy onboarding incomplete (acceptance declined)") {
		t.Fatalf("declined onboarding output:\n%s", declinedOut.String())
	}
	if _, err := os.Lstat(f.attestationPath()); !os.IsNotExist(err) {
		t.Fatalf("declined onboarding wrote acceptance: %v", err)
	}

	var acceptedOut, acceptedErr bytes.Buffer
	accept := app{
		stdout: &acceptedOut, stderr: &acceptedErr, stdin: strings.NewReader("y\n"), interactive: true,
		accessRunner: governedAccessRunner(false),
	}
	if err := accept.run([]string{
		"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", f.home,
		"--umbrella", f.umbrellaRoot, "--no-refresh", "--no-update-check",
	}); err != nil {
		t.Fatalf("accept onboarding: %v\nstdout:\n%s\nstderr:\n%s", err, acceptedOut.String(), acceptedErr.String())
	}
	if !strings.Contains(acceptedOut.String(), "Onboarding complete - acme") {
		t.Fatalf("accepted onboarding output:\n%s", acceptedOut.String())
	}
	if _, err := os.Stat(f.attestationPath()); err != nil {
		t.Fatalf("accepted onboarding did not write evidence: %v", err)
	}
}

func TestInteractivePolicyOnboardingFailsClosedOnDigestOrIdentity(t *testing.T) {
	t.Run("digest mismatch", func(t *testing.T) {
		f := newPolicyTestFixture(t)
		f.configureGovernedOperator(t)
		writeCLITestFile(t, filepath.Join(f.handbook, "policy", "release.md"), f.content+"undeclared change\n")
		commitCLIGit(t, f.handbook, "undeclared policy change")
		var stdout, stderr bytes.Buffer
		a := app{
			stdout: &stdout, stderr: &stderr, stdin: strings.NewReader("y\n"), interactive: true,
			accessRunner: governedAccessRunner(false),
		}
		err := a.run([]string{
			"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", f.home,
			"--umbrella", f.umbrellaRoot, "--no-refresh", "--no-update-check",
		})
		if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("digest mismatch error = %v", err)
		}
		if _, err := os.Lstat(f.attestationPath()); !os.IsNotExist(err) {
			t.Fatalf("digest mismatch wrote acceptance: %v", err)
		}
	})

	t.Run("identity failure", func(t *testing.T) {
		f := newPolicyTestFixture(t)
		f.configureGovernedOperator(t)
		var stdout, stderr bytes.Buffer
		a := app{
			stdout: &stdout, stderr: &stderr, stdin: strings.NewReader("y\n"), interactive: true,
			accessRunner: func(string, ...string) ([]byte, error) { return []byte(`{}`), nil },
		}
		err := a.run([]string{
			"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", f.home,
			"--umbrella", f.umbrellaRoot, "--no-refresh", "--no-update-check",
		})
		if err == nil || !strings.Contains(err.Error(), "cannot establish immutable GitHub identity") {
			t.Fatalf("identity failure error = %v", err)
		}
		if _, err := os.Lstat(f.attestationPath()); !os.IsNotExist(err) {
			t.Fatalf("identity failure wrote acceptance: %v", err)
		}
	})
}

func TestGovernedLaunchRefreshesManifestEvenWithNoRefreshAndFindsNewPolicy(t *testing.T) {
	f := newPolicyTestFixture(t)
	f.configureGovernedOperator(t)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(f.manifestWriter, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc manifest.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	second := doc.Governance.Policies[0]
	second.ID = "new-required-policy"
	second.Title = "New required policy"
	doc.Governance.Policies = append(doc.Governance.Policies, second)
	updated, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, manifestPath, string(append(updated, '\n')))
	commitAndPushCLIGit(t, f.manifestWriter, "require a new policy")

	var stdout, stderr bytes.Buffer
	launched := false
	a := app{
		stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
		lookPath:    func(string) (string, error) { return "/bin/true", nil },
		execHarness: func(string, []string, string) error { launched = true; return nil },
	}
	err = a.run([]string{
		"my", "ai", "--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot,
		"--no-session", "--no-refresh", "--no-update-check", "codex",
	})
	if err == nil || !strings.Contains(err.Error(), "new-required-policy") {
		t.Fatalf("stale-manifest launch error = %v", err)
	}
	if launched {
		t.Fatal("--no-refresh hid a newly required policy")
	}
	refreshed, err := loadSingleRegisteredDoc(f.home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.doc.Governance.Policies) != 2 {
		t.Fatalf("manifest did not refresh: %#v", refreshed.doc.Governance.Policies)
	}
}

func TestGovernedLaunchBlocksWhenManifestFreshnessIsUnknownOrDirty(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, policyTestFixture)
		want    string
	}{
		{
			name: "remote unreachable",
			prepare: func(t *testing.T, f policyTestFixture) {
				runCLIGit(t, f.manifestCache, "remote", "set-url", "origin", filepath.Join(f.home, "missing-remote.git"))
			},
			want: "freshness could not be proven",
		},
		{
			name: "local manifest modification",
			prepare: func(t *testing.T, f policyTestFixture) {
				path := filepath.Join(f.manifestCache, "manifest.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeCLITestFile(t, path, string(data)+"\n")
			},
			want: "local changes or unpublished commits",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newPolicyTestFixture(t)
			f.configureGovernedOperator(t)
			if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
				t.Fatal(err)
			}
			test.prepare(t, f)
			var stdout, stderr bytes.Buffer
			launched := false
			a := app{
				stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
				lookPath:    func(string) (string, error) { return "/bin/true", nil },
				execHarness: func(string, []string, string) error { launched = true; return nil },
			}
			err := a.run([]string{
				"my", "ai", "--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot,
				"--no-session", "--no-refresh", "--no-update-check", "codex",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("launch error = %v, want %q", err, test.want)
			}
			if launched {
				t.Fatal("unproven manifest freshness launched harness")
			}
		})
	}
}

type governedPRRunnerState struct {
	remote      string
	created     bool
	merged      bool
	branch      string
	commit      string
	createCalls int
	proofActor  int64
	lostCreate  bool
}

func (s *governedPRRunnerState) run(name string, args ...string) ([]byte, error) {
	if name != "gh" {
		return nil, fmt.Errorf("unexpected command %s", name)
	}
	joined := strings.Join(args, " ")
	switch {
	case joined == "api user", joined == "api users/operator":
		return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
	case strings.Contains(joined, "/collaborators/operator/permission"):
		return []byte(`{"permission":"write","user":{"id":17,"node_id":"U_actor","login":"operator"}}`), nil
	case strings.HasPrefix(joined, "api repos/example/handbook/pulls?state=open"):
		if !s.created {
			return []byte(`[]`), nil
		}
		proofActor := s.proofActor
		if proofActor == 0 {
			proofActor = 17
		}
		return []byte(fmt.Sprintf(`[{"html_url":"https://github.com/example/handbook/pull/1","user":{"id":%d},"head":{"sha":%q}}]`, proofActor, s.commit)), nil
	case len(args) >= 2 && args[0] == "pr" && args[1] == "create":
		s.createCalls++
		for i := range args {
			if args[i] == "--head" && i+1 < len(args) {
				s.branch = args[i+1]
			}
		}
		if s.branch == "" {
			return nil, fmt.Errorf("missing PR head branch")
		}
		cmd := exec.Command("git", "--git-dir", s.remote, "rev-parse", "refs/heads/"+s.branch)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return out, err
		}
		s.commit = strings.TrimSpace(string(out))
		s.created = true
		if s.lostCreate {
			return nil, fmt.Errorf("simulated lost gh response after PR creation")
		}
		return []byte("https://github.com/example/handbook/pull/1\n"), nil
	case joined == "api repos/example/handbook/pulls/1":
		if !s.created {
			return nil, fmt.Errorf("PR was not created")
		}
		proofActor := s.proofActor
		if proofActor == 0 {
			proofActor = 17
		}
		return []byte(fmt.Sprintf(`{"html_url":"https://github.com/example/handbook/pull/1","user":{"id":%d},"head":{"sha":%q}}`, proofActor, s.commit)), nil
	case joined == "pr view https://github.com/example/handbook/pull/1 --json state,headRefOid,mergeCommit":
		state := "OPEN"
		mergeCommit := "null"
		if s.merged {
			state = "MERGED"
			mergeCommit = fmt.Sprintf(`{"oid":%q}`, s.commit)
		}
		return []byte(fmt.Sprintf(`{"state":%q,"headRefOid":%q,"mergeCommit":%s}`, state, s.commit, mergeCommit)), nil
	case len(args) >= 2 && args[0] == "pr" && args[1] == "merge":
		s.merged = true
		return []byte("auto-merge enabled\n"), nil
	default:
		return nil, fmt.Errorf("unexpected gh call: %s", joined)
	}
}

func preparePolicyFixtureRemote(t *testing.T, f policyTestFixture) (string, string) {
	t.Helper()
	writeCLITestFile(t, filepath.Join(f.handbook, ".gitignore"), "private.tmp\n")
	commitCLIGit(t, f.handbook, "add ignore policy")
	remote := filepath.Join(f.home, "handbook.git")
	runCLIGit(t, f.home, "init", "--bare", "-q", remote)
	runCLIGit(t, f.handbook, "remote", "add", "origin", remote)
	runCLIGit(t, f.handbook, "branch", "-M", "master")
	runCLIGit(t, f.handbook, "push", "-q", "-u", "origin", "master")
	return remote, strings.TrimSpace(gitCLIOutput(t, f.handbook, "rev-parse", "HEAD"))
}

func TestGovernedSyncPRDryRunAndApplyPreserveWorkingBytes(t *testing.T) {
	f := newPolicyTestFixture(t)
	remote, remoteMaster := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(f.handbook, "private.tmp"), "never publish or remove\n")
	attestationBefore, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}
	ignoredBefore, err := os.ReadFile(filepath.Join(f.handbook, "private.tmp"))
	if err != nil {
		t.Fatal(err)
	}

	state := &governedPRRunnerState{remote: remote}
	newApp := func(stdout, stderr *bytes.Buffer) app {
		return app{
			stdout: stdout, stderr: stderr, accessRunner: governedAccessRunner(false),
			publishRunner: state.run,
		}
	}
	args := []string{
		"my", "sync", "--push", "--manifest", "acme", "--home", f.home,
		"--umbrella", f.umbrellaRoot, "--message", "Accept release policy",
	}
	var dryOut, dryErr bytes.Buffer
	dry := newApp(&dryOut, &dryErr)
	if err := dry.run(append(append([]string{}, args...), "--print")); err != nil {
		t.Fatalf("PR dry run: %v\nstdout:\n%s\nstderr:\n%s", err, dryOut.String(), dryErr.String())
	}
	if state.createCalls != 0 || !strings.Contains(dryOut.String(), "governance pre-check passed") {
		t.Fatalf("dry-run state=%#v stdout=%s", state, dryOut.String())
	}
	if got := strings.TrimSpace(gitCLIOutput(t, f.handbook, "rev-parse", "HEAD")); got != remoteMaster {
		t.Fatalf("dry run changed HEAD: %s != %s", got, remoteMaster)
	}
	if branches := gitCLIOutput(t, f.handbook, "for-each-ref", "--format=%(refname)", "refs/heads/my/governed"); strings.TrimSpace(branches) != "" {
		t.Fatalf("dry run created branch: %s", branches)
	}

	var applyOut, applyErr bytes.Buffer
	apply := newApp(&applyOut, &applyErr)
	if err := apply.run(args); err != nil {
		t.Fatalf("PR apply: %v\nstdout:\n%s\nstderr:\n%s", err, applyOut.String(), applyErr.String())
	}
	if !state.created || !state.merged || state.commit == "" ||
		!strings.Contains(applyOut.String(), "pull request opened") ||
		!strings.Contains(applyOut.String(), "auto-merge requested") {
		t.Fatalf("state=%#v stdout=%s", state, applyOut.String())
	}
	after, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}
	ignoredAfter, err := os.ReadFile(filepath.Join(f.handbook, "private.tmp"))
	if err != nil {
		t.Fatalf("ignored local file was removed: %v", err)
	}
	if !bytes.Equal(attestationBefore, after) || !bytes.Equal(ignoredBefore, ignoredAfter) {
		t.Fatal("PR publication changed working-tree bytes")
	}
	if status := gitCLIOutput(t, f.handbook, "status", "--porcelain", "--", "policy/attestations"); strings.TrimSpace(status) != "" {
		t.Fatalf("published attestation remains dirty: %q", status)
	}
	if got := strings.TrimSpace(gitCLIOutput(t, f.handbook, "rev-parse", "HEAD")); got != state.commit {
		t.Fatalf("local HEAD = %s, PR commit = %s", got, state.commit)
	}
	if got := strings.TrimSpace(gitCLIOutput(t, f.handbook, "symbolic-ref", "--short", "HEAD")); got != state.branch {
		t.Fatalf("checkout stayed on protected base instead of PR branch: %q != %q", got, state.branch)
	}
	if got := strings.TrimSpace(gitCLIOutput(t, f.handbook, "rev-parse", "refs/heads/master")); got != remoteMaster {
		t.Fatalf("local protected base advanced to unmerged PR: %s != %s", got, remoteMaster)
	}
	if got := strings.TrimSpace(gitCLIOutput(t, f.handbook, "ls-remote", "origin", "refs/heads/master")); !strings.HasPrefix(got, remoteMaster) {
		t.Fatalf("PR publication changed protected base branch: %q", got)
	}
}

func TestGovernedSyncLocalPrecheckDeniesForgedAcceptanceAndDirectPush(t *testing.T) {
	f := newPolicyTestFixture(t)
	remote, _ := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(data), `"subject_id":17`, `"subject_id":99`, 1)
	forgedPath := filepath.Join(
		f.handbook, "policy", "attestations", "99", "release-policy",
		strings.TrimPrefix(f.digest, "sha256:")+".json",
	)
	writeCLITestFile(t, forgedPath, forged)

	state := &governedPRRunnerState{remote: remote}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
		publishRunner: state.run,
	}
	err = a.run([]string{
		"my", "sync", "--push", "--print", "--json", "--manifest", "acme", "--home", f.home,
		"--umbrella", f.umbrellaRoot, "--message", "Forge acceptance",
	})
	if err != nil {
		t.Fatalf("governance hold should be a structured non-mutating result: %v\n%s", err, stdout.String())
	}
	if state.createCalls != 0 || !strings.Contains(stdout.String(), `"reason_code": "governance_denied"`) ||
		!strings.Contains(stdout.String(), "attestation_subject_mismatch") || !strings.Contains(stdout.String(), "my governance check") {
		t.Fatalf("state=%#v report=%s", state, stdout.String())
	}

	stdout.Reset()
	err = a.run([]string{
		"my", "sync", "--publish", "direct", "--print", "--manifest", "acme", "--home", f.home,
		"--umbrella", f.umbrellaRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "refuse direct publish") {
		t.Fatalf("direct publish error = %v", err)
	}
}

func TestGovernedPRProofFailureLeavesAllLocalBytesAndChangesInPlace(t *testing.T) {
	f := newPolicyTestFixture(t)
	remote, baseHead := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}
	state := &governedPRRunnerState{remote: remote, proofActor: 99}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
		publishRunner: state.run,
	}
	err = a.run([]string{
		"my", "sync", "--push", "--manifest", "acme", "--home", f.home,
		"--umbrella", f.umbrellaRoot, "--message", "Proof failure test",
	})
	if err == nil || !strings.Contains(stdout.String(), "pull request author id 99") {
		t.Fatalf("proof failure err=%v stdout=%s", err, stdout.String())
	}
	after, readErr := os.ReadFile(f.attestationPath())
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("proof failure changed or removed local bytes: %v", readErr)
	}
	if got := strings.TrimSpace(gitCLIOutput(t, f.handbook, "rev-parse", "HEAD")); got != baseHead {
		t.Fatalf("proof failure advanced local base: %s != %s", got, baseHead)
	}
	if status := gitCLIOutput(t, f.handbook, "status", "--porcelain", "--", "policy/attestations"); strings.TrimSpace(status) == "" {
		t.Fatal("proof failure discarded the local uncommitted acceptance")
	}
	remoteProof := strings.TrimSpace(gitCLIOutput(t, f.handbook, "ls-remote", "origin", "refs/heads/"+state.branch))
	if state.branch == "" || !strings.HasPrefix(remoteProof, state.commit) {
		t.Fatalf("pushed recovery branch missing: state=%#v remote=%q", state, remoteProof)
	}
}

func TestGovernedPRRecoversIdempotentlyWhenCreateResponseIsLost(t *testing.T) {
	f := newPolicyTestFixture(t)
	remote, baseHead := preparePolicyFixtureRemote(t, f)
	f.configureGovernedOperator(t)
	if _, err := f.run(t, "accept", "release-policy", "--yes"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.attestationPath())
	if err != nil {
		t.Fatal(err)
	}
	state := &governedPRRunnerState{remote: remote, lostCreate: true}
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout, stderr: &stderr, accessRunner: governedAccessRunner(false),
		publishRunner: state.run,
	}
	if err := a.run([]string{
		"my", "sync", "--push", "--manifest", "acme", "--home", f.home,
		"--umbrella", f.umbrellaRoot, "--message", "Recover lost response",
	}); err != nil {
		t.Fatalf("lost response recovery: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	after, readErr := os.ReadFile(f.attestationPath())
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatalf("lost response recovery changed working bytes: %v", readErr)
	}
	if state.createCalls != 1 || !strings.Contains(stdout.String(), "pull request opened") {
		t.Fatalf("state=%#v stdout=%s", state, stdout.String())
	}
	if got := strings.TrimSpace(gitCLIOutput(t, f.handbook, "rev-parse", "refs/heads/master")); got != baseHead {
		t.Fatalf("lost response recovery advanced base: %s != %s", got, baseHead)
	}
}

func TestProspectiveCommitIsDeterministicAndReusesExistingAheadHead(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "content")
	writeCLITestFile(t, filepath.Join(repo, "policy", "record.md"), "one\n")
	initCLIGitRepo(t, repo)
	base := strings.TrimSpace(gitCLIOutput(t, repo, "rev-parse", "HEAD"))
	writeCLITestFile(t, filepath.Join(repo, "policy", "record.md"), "two\n")
	request := syncer.PRRequest{
		Entry:  syncer.Entry{LocalPath: repo, ContentPaths: []string{"policy"}},
		Branch: "master", Upstream: base, Head: base, Dirty: []string{"policy/record.md"},
		Message: "deterministic proposal",
	}
	first, err := buildProspectiveCommit(request)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Cleanup()
	request.DryRun = true
	second, err := buildProspectiveCommit(request)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Cleanup()
	if first.Commit != second.Commit {
		t.Fatalf("same bytes produced retry-unstable commits: %s != %s", first.Commit, second.Commit)
	}

	runCLIGit(t, repo, "add", "policy/record.md")
	runCLIGit(t, repo, "commit", "-q", "-m", "existing ahead commit")
	ahead := strings.TrimSpace(gitCLIOutput(t, repo, "rev-parse", "HEAD"))
	aheadRequest := syncer.PRRequest{
		Entry:  syncer.Entry{LocalPath: repo, ContentPaths: []string{"policy"}},
		Branch: "master", Upstream: base, Head: ahead, Changed: []string{"policy/record.md"},
		Message: "publish existing commit",
	}
	prospective, err := buildProspectiveCommit(aheadRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer prospective.Cleanup()
	if prospective.Commit != ahead {
		t.Fatalf("ahead-only publish added a synthetic empty commit: %s != %s", prospective.Commit, ahead)
	}
}
