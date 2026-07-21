package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestGovernanceCheckCommandReturnsStructuredDenial(t *testing.T) {
	home, _, manifestRepo, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "git@github.com:example/handbook.git", "mode": "required" }
  ],
  "governance": {
    "authorization": { "provider": "github", "manifest_repository": "example/control", "admin_permission": "admin" },
    "protections": [
      { "mount": "handbook", "paths": ["fleet"], "mode": "no-delete" }
    ]
  }
}`)
	content := filepath.Join(home, "handbook-check")
	writeCLITestFile(t, filepath.Join(content, "fleet", "asset.md"), "asset\n")
	initCLIGitRepo(t, content)
	base := strings.TrimSpace(gitCLIOutput(t, content, "rev-parse", "HEAD"))
	if err := os.Remove(filepath.Join(content, "fleet", "asset.md")); err != nil {
		t.Fatal(err)
	}
	commitCLIGit(t, content, "delete fleet record")
	head := strings.TrimSpace(gitCLIOutput(t, content, "rev-parse", "HEAD"))
	manifestBase := strings.TrimSpace(gitCLIOutput(t, manifestRepo, "rev-parse", "HEAD"))

	runner := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if name == "gh" {
			switch {
			case joined == "api users/operator":
				return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
			case strings.Contains(joined, "/collaborators/operator/permission"):
				return []byte(`{"permission":"write","user":{"id":17,"node_id":"U_actor","login":"operator"}}`), nil
			default:
				return nil, fmt.Errorf("unexpected gh call: %s", joined)
			}
		}
		cmd := exec.Command(name, args...)
		return cmd.CombinedOutput()
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, publishRunner: runner}
	err := a.run([]string{
		"my", "governance", "check", "--repo", content, "--repository", "example/handbook",
		"--base", base, "--head", head, "--manifest-repo", manifestRepo,
		"--manifest-base", manifestBase, "--mount", "handbook",
		"--actor-id", "17", "--actor-login", "operator", "--json",
	})
	if err == nil || !strings.Contains(err.Error(), "protected_path_deleted") {
		t.Fatalf("governance command error = %v", err)
	}
	var report struct {
		Allowed            bool `json:"allowed"`
		TrustedBasePolicy  bool `json:"trusted_base_policy"`
		CheckedParentEdges int  `json:"checked_parent_edges"`
		Violations         []struct {
			ReasonCode string `json:"reason_code"`
			Path       string `json:"path"`
		} `json:"violations"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse JSON: %v\n%s", err, stdout.String())
	}
	if report.Allowed || !report.TrustedBasePolicy || report.CheckedParentEdges == 0 ||
		len(report.Violations) == 0 || report.Violations[0].ReasonCode != "protected_path_deleted" {
		t.Fatalf("report = %#v", report)
	}
}

func TestGovernanceAuditRequiresRulesetsWorkflowIdentityAndNoBypass(t *testing.T) {
	f := newPolicyTestFixture(t)
	for _, test := range []struct {
		name       string
		bypass     bool
		wantOK     bool
		wantDetail string
	}{
		{name: "compliant", wantOK: true, wantDetail: `"compliant": true`},
		{name: "bypass actor", bypass: true, wantOK: false, wantDetail: `"id": "no-bypass-actors"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			a := app{stdout: &stdout, stderr: &stderr, publishRunner: governanceAuditRunner(test.bypass)}
			err := a.run([]string{"my", "governance", "audit", "--manifest", "acme", "--home", f.home, "--umbrella", f.umbrellaRoot, "--json"})
			if test.wantOK && err != nil {
				t.Fatalf("audit: %v\n%s", err, stdout.String())
			}
			if !test.wantOK && (err == nil || !strings.Contains(err.Error(), "missing GitHub enforcement")) {
				t.Fatalf("noncompliant audit error = %v", err)
			}
			if !strings.Contains(stdout.String(), test.wantDetail) {
				t.Fatalf("audit output missing %q:\n%s", test.wantDetail, stdout.String())
			}
			if !test.wantOK && !strings.Contains(stdout.String(), `"ok": false`) {
				t.Fatalf("bypass audit did not fail check:\n%s", stdout.String())
			}
		})
	}
}

func governanceAuditRunner(bypass bool) func(string, ...string) ([]byte, error) {
	workflow := `name: my-governance
on:
  pull_request_target:
jobs:
  check:
    steps:
      - uses: actions/checkout@0123456789abcdef0123456789abcdef01234567
      - uses: actions/setup-go@89abcdef0123456789abcdef0123456789abcdef
      - run: |
          repository: fluxinc/my-cli
          MY_CLI_COMMIT=0123456789abcdef0123456789abcdef01234567
          [[ "$MY_CLI_COMMIT" =~ ^[0-9a-f]{40}$ ]]
          git -C my-cli rev-parse HEAD^{commit}
          go build ./cmd/my
          "$RUNNER_TEMP/my-governance" governance check --actor-id ${{ github.event.pull_request.user.id }} --actor-login ${{ github.event.pull_request.user.login }} --manifest-base origin/main
`
	codeowners := "/.github/workflows/ @example/security-admins\n/.github/CODEOWNERS @example/security-admins\n/decisions/ @example/security-admins\n"
	content := func(value string) []byte {
		return []byte(fmt.Sprintf(`{"encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte(value))))
	}
	return func(name string, args ...string) ([]byte, error) {
		if name != "gh" || len(args) < 2 || args[0] != "api" {
			return nil, fmt.Errorf("unexpected audit command: %s %s", name, strings.Join(args, " "))
		}
		endpoint := args[1]
		switch {
		case endpoint == "repos/example/control", endpoint == "repos/example/handbook":
			return []byte(`{"default_branch":"main"}`), nil
		case strings.HasSuffix(endpoint, "/rulesets"):
			id := 1
			if strings.Contains(endpoint, "handbook") {
				id = 2
			}
			return []byte(fmt.Sprintf(`[{"id":%d}]`, id)), nil
		case strings.Contains(endpoint, "/rulesets/"):
			bypassJSON := "[]"
			if bypass && strings.Contains(endpoint, "handbook") {
				bypassJSON = `[{"actor_id":17,"actor_type":"RepositoryRole","bypass_mode":"always"}]`
			}
			return []byte(fmt.Sprintf(`{
  "id": 1,
  "name": "governed-main",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": %s,
  "conditions": {"ref_name":{"include":["~DEFAULT_BRANCH"],"exclude":[]}},
  "rules": [
    {"type":"pull_request","parameters":{"required_approving_review_count":1,"require_code_owner_review":true,"dismiss_stale_reviews_on_push":true,"required_review_thread_resolution":true}},
    {"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"my-governance"}]}},
    {"type":"deletion"},
    {"type":"non_fast_forward"}
  ]
}`, bypassJSON)), nil
		case strings.Contains(endpoint, "/contents/.github/workflows/my-governance.yml"):
			return content(workflow), nil
		case strings.Contains(endpoint, "/contents/.github/CODEOWNERS"):
			return content(codeowners), nil
		default:
			return nil, fmt.Errorf("unexpected audit endpoint: %s", endpoint)
		}
	}
}

func TestRecordDomainPolicyClassificationAndCodeownerCoverage(t *testing.T) {
	doc := manifest.Document{Governance: manifest.Governance{RecordDomains: []manifest.RecordDomain{
		{ID: "decisions", Mount: "handbook", Path: "decisions", Review: "codeowner", Publish: "auto-pr"},
		{ID: "bugs", Mount: "handbook", Path: "engineering/bugs", Review: "standard", Publish: "auto-pr"},
	}}}
	domains := changedRecordDomains(doc, "handbook", []string{"decisions/one.md", "engineering/bugs/two.md", "other.md"})
	if len(domains) != 2 || compatibleRecordDomainPolicies(domains) {
		t.Fatalf("domains=%#v compatible=%v", domains, compatibleRecordDomainPolicies(domains))
	}
	owners := "/.github/workflows/ @example/admins\n/decisions/ @example/admins\n"
	if !codeownersCoversDomain(owners, "decisions") || codeownersCoversDomain(owners, "engineering/bugs") {
		t.Fatalf("unexpected CODEOWNERS coverage")
	}
	if !codeownersCoversDomain("/investigations/ @example/admins\n", "investigations/customer-assets") {
		t.Fatal("parent-directory CODEOWNERS pattern should cover nested domain")
	}
}
