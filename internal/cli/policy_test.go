package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/access"
)

type policyTestFixture struct {
	home          string
	umbrellaRoot  string
	manifestCache string
	handbook      string
	content       string
	digest        string
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
    "protections": [
      { "mount": "handbook", "paths": ["policy/attestations"], "mode": "append-only" }
    ]
  }
}`, digest)
	home, umbrellaRoot, manifestCache, _, _ := setupCLITrackedManifestBody(t, body)
	handbook := filepath.Join(umbrellaRoot, "handbook")
	writeCLITestFile(t, filepath.Join(handbook, "policy", "release.md"), content)
	initCLIGitRepo(t, handbook)
	return policyTestFixture{
		home: home, umbrellaRoot: umbrellaRoot, manifestCache: manifestCache,
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
