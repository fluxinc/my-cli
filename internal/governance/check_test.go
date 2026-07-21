package governance

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type checkFixture struct {
	root         string
	manifestRepo string
	contentRepo  string
	manifestBase string
	contentBase  string
}

func newCheckFixture(t *testing.T) checkFixture {
	t.Helper()
	root := t.TempDir()
	manifestRepo := filepath.Join(root, "manifest")
	contentRepo := filepath.Join(root, "handbook")
	manifestBody := `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "git@github.com:example/handbook.git", "mode": "required" }
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
        "title": "Release policy",
        "mount": "handbook",
        "path": "policy/release.md",
        "version": "2026-07",
        "sha256": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "acceptance": "optional"
      }
    ],
    "attestations": {
      "mount": "handbook",
      "path": "policy/attestations",
      "identity": "github"
    },
    "record_domains": [
      {"id":"decisions","title":"Decisions","mount":"handbook","path":"decisions","retention":"no-delete","review":"codeowner","publish":"auto-pr"}
    ],
    "protections": [
      { "mount": "handbook", "paths": ["fleet"], "mode": "no-delete", "admin_override": true },
      { "mount": "handbook", "paths": ["policy/attestations"], "mode": "append-only" }
    ]
  }
}`
	writeTestFile(t, filepath.Join(manifestRepo, "manifest.json"), manifestBody)
	initTestRepo(t, manifestRepo)
	manifestBase := gitOutput(t, manifestRepo, "rev-parse", "HEAD")

	writeTestFile(t, filepath.Join(contentRepo, "fleet", "asset.md"), "asset\n")
	writeTestFile(t, filepath.Join(contentRepo, "decisions", "one.md"), "decision\n")
	writeTestFile(t, filepath.Join(contentRepo, "policy", "attestations", "17", "release", strings.Repeat("a", 64)+".json"), `{"subject_id":17}`+"\n")
	initTestRepo(t, contentRepo)
	contentBase := gitOutput(t, contentRepo, "rev-parse", "HEAD")
	return checkFixture{root: root, manifestRepo: manifestRepo, contentRepo: contentRepo, manifestBase: manifestBase, contentBase: contentBase}
}

func TestCheckEnforcesRecordDomainImplicitRetention(t *testing.T) {
	f := newCheckFixture(t)
	if err := os.Remove(filepath.Join(f.contentRepo, "decisions", "one.md")); err != nil {
		t.Fatal(err)
	}
	head := commitTestRepo(t, f.contentRepo, "delete domain record")
	for _, permission := range []string{"write", "admin"} {
		report, err := Check(f.input(head, "handbook", "example/handbook", permission))
		if err != nil {
			t.Fatal(err)
		}
		if report.Allowed || !hasViolation(report, "protected_path_deleted", "decisions/one.md") {
			t.Fatalf("permission %s report = %#v", permission, report)
		}
	}
}

func (f checkFixture) input(head, mount, repository string, permission string) CheckInput {
	return CheckInput{
		Repo: f.contentRepo, Repository: repository, BaseRef: f.contentBase, HeadRef: head,
		ManifestRepo: f.manifestRepo, ManifestBaseRef: f.manifestBase, Mount: mount,
		ActorID: 17, ActorLogin: "operator", Runner: testGovernanceRunner(permission, 17),
	}
}

func TestCheckAllowsNewAttestationAndRejectsModificationEvenForAdmin(t *testing.T) {
	t.Run("new subject-bound attestation", func(t *testing.T) {
		f := newCheckFixture(t)
		path := filepath.Join(f.contentRepo, "policy", "attestations", "17", "release-policy", strings.Repeat("a", 64)+".json")
		writeTestFile(t, path, attestationJSON(17, f.manifestBase))
		head := commitTestRepo(t, f.contentRepo, "add attestation")
		report, err := Check(f.input(head, "handbook", "example/handbook", "write"))
		if err != nil {
			t.Fatal(err)
		}
		if !report.Allowed || len(report.Violations) != 0 || !report.TrustedBasePolicy {
			t.Fatalf("report = %#v", report)
		}
	})

	t.Run("existing attestation changed", func(t *testing.T) {
		f := newCheckFixture(t)
		path := filepath.Join(f.contentRepo, "policy", "attestations", "17", "release", strings.Repeat("a", 64)+".json")
		writeTestFile(t, path, "{\n  \"subject_id\": 17\n}\n")
		head := commitTestRepo(t, f.contentRepo, "rewrite attestation")
		for _, permission := range []string{"write", "admin"} {
			report, err := Check(f.input(head, "handbook", "example/handbook", permission))
			if err != nil {
				t.Fatal(err)
			}
			if report.Allowed || !hasViolation(report, "append_only_path_modified", "policy/attestations/") {
				t.Fatalf("permission %s report = %#v", permission, report)
			}
		}
	})
}

func TestCheckUsesTreeMembershipForRenameAwayAndAdminOverride(t *testing.T) {
	f := newCheckFixture(t)
	if err := os.MkdirAll(filepath.Join(f.contentRepo, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.contentRepo, "mv", "fleet/asset.md", "archive/asset.md")
	head := commitTestRepo(t, f.contentRepo, "rename protected record away")

	report, err := Check(f.input(head, "handbook", "example/handbook", "write"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Allowed || !hasViolation(report, "protected_path_deleted", "fleet/asset.md") {
		t.Fatalf("non-admin report = %#v", report)
	}
	admin, err := Check(f.input(head, "handbook", "example/handbook", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	if !admin.Allowed {
		t.Fatalf("trusted admin_override did not permit admin: %#v", admin)
	}
}

func TestCheckReadsManifestRulesOnlyFromTrustedBase(t *testing.T) {
	f := newCheckFixture(t)
	data, err := os.ReadFile(filepath.Join(f.manifestRepo, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "governance")
	weakened, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(f.manifestRepo, "manifest.json"), string(append(weakened, '\n')))
	head := commitTestRepo(t, f.manifestRepo, "delete governance from proposed head")
	input := CheckInput{
		Repo: f.manifestRepo, Repository: "example/control", BaseRef: f.manifestBase, HeadRef: head,
		ManifestRepo: f.manifestRepo, ManifestBaseRef: f.manifestBase, Mount: ManifestSurface,
		ActorID: 17, ActorLogin: "operator", Runner: testGovernanceRunner("write", 17),
	}
	report, err := Check(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.Allowed || !hasViolation(report, "manifest_admin_required", "manifest.json") || report.ManifestCommit != f.manifestBase {
		t.Fatalf("weakened-head report = %#v", report)
	}
	input.Runner = testGovernanceRunner("admin", 17)
	admin, err := Check(input)
	if err != nil {
		t.Fatal(err)
	}
	if !admin.Allowed {
		t.Fatalf("manifest repository admin denied: %#v", admin)
	}
}

func TestCheckInspectsSecondParentHistoryNotOnlyFinalTree(t *testing.T) {
	f := newCheckFixture(t)
	runGit(t, f.contentRepo, "checkout", "-q", "-b", "evil", f.contentBase)
	if err := os.Remove(filepath.Join(f.contentRepo, "fleet", "asset.md")); err != nil {
		t.Fatal(err)
	}
	evil := commitTestRepo(t, f.contentRepo, "delete protected record")
	runGit(t, f.contentRepo, "checkout", "-q", "-b", "good", f.contentBase)
	writeTestFile(t, filepath.Join(f.contentRepo, "safe.md"), "safe\n")
	_ = commitTestRepo(t, f.contentRepo, "safe change")
	runGit(t, f.contentRepo, "merge", "-q", "--no-ff", "-s", "ours", "evil", "-m", "merge second parent without its tree")
	head := gitOutput(t, f.contentRepo, "rev-parse", "HEAD")
	if _, err := os.Stat(filepath.Join(f.contentRepo, "fleet", "asset.md")); err != nil {
		t.Fatalf("final tree should retain protected record: %v", err)
	}
	report, err := Check(f.input(head, "handbook", "example/handbook", "write"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Allowed || !hasViolationAtCommit(report, "protected_path_deleted", evil) || report.CheckedParentEdges < 3 {
		t.Fatalf("merge-history report = %#v", report)
	}
}

func TestCheckBindsAddedAttestationToImmutablePRAuthorID(t *testing.T) {
	f := newCheckFixture(t)
	path := filepath.Join(f.contentRepo, "policy", "attestations", "99", "release", strings.Repeat("c", 64)+".json")
	writeTestFile(t, path, attestationJSON(99, f.manifestBase))
	head := commitTestRepo(t, f.contentRepo, "add another user's attestation")
	report, err := Check(f.input(head, "handbook", "example/handbook", "write"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Allowed || !hasViolation(report, "attestation_subject_mismatch", "policy/attestations/99/") {
		t.Fatalf("subject mismatch report = %#v", report)
	}

	input := f.input(head, "handbook", "example/handbook", "write")
	input.Runner = testGovernanceRunner("write", 99)
	if _, err := Check(input); err == nil || !strings.Contains(err.Error(), "not declared id 17") {
		t.Fatalf("login/id mismatch error = %v", err)
	}
}

func TestCheckTreatsAttestationManifestCommitAsProvenance(t *testing.T) {
	f := newCheckFixture(t)
	path := filepath.Join(f.contentRepo, "policy", "attestations", "17", "release-policy", strings.Repeat("a", 64)+".json")
	writeTestFile(t, path, attestationJSON(17, f.manifestBase))
	contentHead := commitTestRepo(t, f.contentRepo, "accept policy")

	writeTestFile(t, filepath.Join(f.manifestRepo, "README.md"), "manifest advances without changing policy\n")
	currentManifest := commitTestRepo(t, f.manifestRepo, "advance manifest")
	input := f.input(contentHead, "handbook", "example/handbook", "write")
	input.ManifestBaseRef = currentManifest
	report, err := Check(input)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Allowed || len(report.Violations) != 0 {
		t.Fatalf("manifest advance must not invalidate matching provenance: %#v", report)
	}
}

func TestCheckRejectsStaleAttestationAfterPolicyRevision(t *testing.T) {
	f := newCheckFixture(t)
	path := filepath.Join(f.contentRepo, "policy", "attestations", "17", "release-policy", strings.Repeat("a", 64)+".json")
	writeTestFile(t, path, attestationJSON(17, f.manifestBase))
	contentHead := commitTestRepo(t, f.contentRepo, "accept old policy version")

	manifestBody, err := os.ReadFile(filepath.Join(f.manifestRepo, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	revised := strings.ReplaceAll(string(manifestBody), strings.Repeat("a", 64), strings.Repeat("b", 64))
	revised = strings.ReplaceAll(revised, `"version": "2026-07"`, `"version": "2026-08"`)
	writeTestFile(t, filepath.Join(f.manifestRepo, "manifest.json"), revised)
	currentManifest := commitTestRepo(t, f.manifestRepo, "revise release policy")

	input := f.input(contentHead, "handbook", "example/handbook", "write")
	input.ManifestBaseRef = currentManifest
	report, err := Check(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.Allowed || len(report.Violations) != 1 || !strings.Contains(report.Violations[0].Message, "does not match a policy in the trusted base manifest") {
		t.Fatalf("stale attestation after policy revision must be rejected: %#v", report)
	}
}

func TestCheckRejectsMalformedAttestationManifestCommit(t *testing.T) {
	for _, manifestCommit := range []string{"", "deadbeef", strings.Repeat("g", 40), strings.Repeat("A", 40)} {
		t.Run(fmt.Sprintf("%q", manifestCommit), func(t *testing.T) {
			f := newCheckFixture(t)
			path := filepath.Join(f.contentRepo, "policy", "attestations", "17", "release-policy", strings.Repeat("a", 64)+".json")
			writeTestFile(t, path, attestationJSON(17, manifestCommit))
			head := commitTestRepo(t, f.contentRepo, "add malformed attestation provenance")
			report, err := Check(f.input(head, "handbook", "example/handbook", "write"))
			if err != nil {
				t.Fatal(err)
			}
			if report.Allowed || len(report.Violations) != 1 || !strings.Contains(report.Violations[0].Message, "full Git object ID") {
				t.Fatalf("malformed manifest_commit report = %#v", report)
			}
		})
	}
}

func attestationJSON(subjectID int64, manifestCommit string) string {
	return fmt.Sprintf(`{"schema_version":1,"organization":"acme","policy_id":"release-policy","policy_version":"2026-07","policy_sha256":"sha256:%s","subject_provider":"github","subject_id":%d,"subject_login":"operator","accepted_at":"2026-07-17T03:00:00Z","manifest_commit":%q}`+"\n", strings.Repeat("a", 64), subjectID, manifestCommit)
}

func hasViolation(report Report, code, pathPart string) bool {
	for _, violation := range report.Violations {
		if violation.ReasonCode == code && strings.Contains(violation.Path, pathPart) {
			return true
		}
	}
	return false
}

func hasViolationAtCommit(report Report, code, commit string) bool {
	for _, violation := range report.Violations {
		if violation.ReasonCode == code && violation.Commit == commit {
			return true
		}
	}
	return false
}

func testGovernanceRunner(permission string, identityID int64) Runner {
	return func(name string, args ...string) ([]byte, error) {
		if name == "gh" {
			joined := strings.Join(args, " ")
			switch {
			case joined == "api users/operator":
				return []byte(fmt.Sprintf(`{"id":%d,"node_id":"U_actor","login":"operator"}`, identityID)), nil
			case strings.Contains(joined, "/collaborators/operator/permission"):
				return []byte(fmt.Sprintf(`{"permission":%q,"user":{"id":%d,"node_id":"U_actor","login":"operator"}}`, permission, identityID)), nil
			default:
				return nil, fmt.Errorf("unexpected gh call: %s", joined)
			}
		}
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0")
		return cmd.CombinedOutput()
	}
}

func initTestRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "Governance Test")
	runGit(t, dir, "config", "user.email", "governance@example.com")
	_ = commitTestRepo(t, dir, "base")
}

func commitTestRepo(t *testing.T, dir, message string) string {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", message)
	return gitOutput(t, dir, "rev-parse", "HEAD")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
