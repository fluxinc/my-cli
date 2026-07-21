package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const governancePolicyDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// TestGovernanceCITrustBoundary exercises direct-Git proposals through the
// real CLI binary. It deliberately bypasses all friendly local authoring
// commands: the committed manifest base and provider permission are the only
// authority available to the CI verifier.
func TestGovernanceCITrustBoundary(t *testing.T) {
	root := repoRoot(t)
	testRoot := t.TempDir()
	bin := filepath.Join(testRoot, "my")
	build := exec.Command("go", "build", "-o", bin, "./cmd/my")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}
	testPath := buildGovernanceGHStub(t, testRoot)

	manifestSource := filepath.Join(testRoot, "manifest-source")
	writeFile(t, filepath.Join(manifestSource, "manifest.json"), governedE2EManifest())
	initGitRepo(t, manifestSource)
	manifestBase := gitOutput(t, manifestSource, "rev-parse", "HEAD")
	manifestBare := filepath.Join(testRoot, "manifest.git")
	cloneBare(t, manifestSource, manifestBare)

	contentSource := filepath.Join(testRoot, "content-source")
	writeFile(t, filepath.Join(contentSource, "fleet", "asset.md"), "# Synthetic asset\n")
	writeFile(t, filepath.Join(contentSource, "policy", "attestations", "18", "release-policy", governancePolicyDigest+".json"), attestationE2EJSON(18, manifestBase))
	initGitRepo(t, contentSource)
	unacceptedBase := gitOutput(t, contentSource, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(contentSource, "policy", "attestations", "17", "release-policy", governancePolicyDigest+".json"), attestationE2EJSON(17, manifestBase))
	contentBase := commitAll(t, contentSource, "accept release policy")
	contentBare := filepath.Join(testRoot, "content.git")
	cloneBare(t, contentSource, contentBare)

	t.Run("non-admin protected record deletion is denied", func(t *testing.T) {
		repo := cloneWork(t, contentBare, filepath.Join(testRoot, "delete-record"))
		if err := os.Remove(filepath.Join(repo, "fleet", "asset.md")); err != nil {
			t.Fatal(err)
		}
		head := commitAll(t, repo, "delete protected record")
		runGovernanceCheck(t, bin, testPath, "write", false, "protected_path_deleted", repo, "example/records", contentBase, head, manifestSource, manifestBase, "handbook", contentSource, contentBase)
	})

	t.Run("administrator override permits protected record deletion", func(t *testing.T) {
		repo := cloneWork(t, contentBare, filepath.Join(testRoot, "admin-delete-record"))
		if err := os.Remove(filepath.Join(repo, "fleet", "asset.md")); err != nil {
			t.Fatal(err)
		}
		head := commitAll(t, repo, "administrator deletes protected record")
		runGovernanceCheck(t, bin, testPath, "admin", true, "", repo, "example/records", contentBase, head, manifestSource, manifestBase, "handbook", contentSource, contentBase)
	})

	t.Run("pull request author may append his own attestation", func(t *testing.T) {
		repo := cloneWork(t, contentBare, filepath.Join(testRoot, "own-attestation"))
		runGit(t, repo, "checkout", "-q", unacceptedBase)
		writeFile(t, filepath.Join(repo, "policy", "attestations", "17", "release-policy", governancePolicyDigest+".json"), attestationE2EJSON(17, manifestBase))
		head := commitAll(t, repo, "append own acceptance")
		runGovernanceCheck(t, bin, testPath, "write", true, "", repo, "example/records", unacceptedBase, head, manifestSource, manifestBase, "handbook", contentSource, contentBase)
	})

	t.Run("forged attestation subject is denied", func(t *testing.T) {
		repo := cloneWork(t, contentBare, filepath.Join(testRoot, "forged-attestation"))
		writeFile(t, filepath.Join(repo, "policy", "attestations", "99", "release-policy", governancePolicyDigest+".json"), attestationE2EJSON(99, manifestBase))
		head := commitAll(t, repo, "forge another subject")
		runGovernanceCheck(t, bin, testPath, "write", false, "attestation_subject_mismatch", repo, "example/records", contentBase, head, manifestSource, manifestBase, "handbook", contentSource, contentBase)
	})

	t.Run("even an administrator cannot rewrite acceptance evidence", func(t *testing.T) {
		repo := cloneWork(t, contentBare, filepath.Join(testRoot, "rewrite-attestation"))
		path := filepath.Join(repo, "policy", "attestations", "18", "release-policy", governancePolicyDigest+".json")
		writeFile(t, path, attestationE2EJSON(18, manifestBase)+"\n")
		head := commitAll(t, repo, "rewrite immutable acceptance")
		runGovernanceCheck(t, bin, testPath, "admin", false, "append_only_path_modified", repo, "example/records", contentBase, head, manifestSource, manifestBase, "handbook", contentSource, contentBase)
	})

	manifestHeadRepo := cloneWork(t, manifestBare, filepath.Join(testRoot, "manifest-change"))
	writeFile(t, filepath.Join(manifestHeadRepo, "README.md"), "proposed control-plane change\n")
	manifestHead := commitAll(t, manifestHeadRepo, "change manifest control plane")
	t.Run("non-admin manifest change is denied", func(t *testing.T) {
		runGovernanceCheck(t, bin, testPath, "write", false, "manifest_admin_required", manifestHeadRepo, "example/control", manifestBase, manifestHead, manifestSource, manifestBase, "@manifest", contentSource, contentBase)
	})
	t.Run("administrator manifest change is allowed", func(t *testing.T) {
		runGovernanceCheck(t, bin, testPath, "admin", true, "", manifestHeadRepo, "example/control", manifestBase, manifestHead, manifestSource, manifestBase, "@manifest", contentSource, contentBase)
	})

	t.Run("working-tree manifest cache edit cannot weaken CI", func(t *testing.T) {
		cache := cloneWork(t, manifestBare, filepath.Join(testRoot, "tampered-cache"))
		body := strings.Replace(governedE2EManifest(), `"protections": [`, `"protections_disabled": [`, 1)
		writeFile(t, filepath.Join(cache, "manifest.json"), body)
		repo := cloneWork(t, contentBare, filepath.Join(testRoot, "cache-bypass-delete"))
		if err := os.Remove(filepath.Join(repo, "fleet", "asset.md")); err != nil {
			t.Fatal(err)
		}
		head := commitAll(t, repo, "attempt cache bypass")
		runGovernanceCheck(t, bin, testPath, "write", false, "protected_path_deleted", repo, "example/records", contentBase, head, cache, manifestBase, "handbook", contentSource, contentBase)
	})
}

func governedE2EManifest() string {
	return fmt.Sprintf(`{
  "manifest_version": 1,
  "organization": {"id":"example","name":"Example"},
  "mounts": [
    {"id":"handbook","kind":"handbook","git_url":"https://github.com/example/records.git","mode":"required"}
  ],
  "governance": {
    "authorization": {"provider":"github","manifest_repository":"example/control","admin_permission":"admin"},
    "policies": [
      {"id":"release-policy","title":"Release policy","mount":"handbook","path":"policy/release.md","version":"2026-07","sha256":"sha256:%s","acceptance":"required"}
    ],
    "attestations": {"mount":"handbook","path":"policy/attestations","identity":"github"},
    "protections": [
      {"mount":"handbook","paths":["fleet"],"mode":"no-delete","admin_override":true},
      {"mount":"handbook","paths":["policy/attestations"],"mode":"append-only"}
    ]
  }
}
`, governancePolicyDigest)
}

func attestationE2EJSON(subjectID int64, manifestCommit string) string {
	return fmt.Sprintf(`{"schema_version":1,"organization":"example","policy_id":"release-policy","policy_version":"2026-07","policy_sha256":"sha256:%s","subject_provider":"github","subject_id":%d,"subject_login":"operator","accepted_at":"2026-07-17T03:00:00Z","manifest_commit":%q}`+"\n", governancePolicyDigest, subjectID, manifestCommit)
}

func buildGovernanceGHStub(t *testing.T, root string) string {
	t.Helper()
	binDir := filepath.Join(root, "test-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(gitPath, filepath.Join(binDir, "git")); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	stubSource := filepath.Join(root, "gh-stub.go")
	writeFile(t, stubSource, `package main
import (
  "fmt"
  "os"
  "strings"
)
func main() {
  args := strings.Join(os.Args[1:], " ")
  switch {
  case args == "api users/operator":
    fmt.Print("{\"id\":17,\"node_id\":\"U_example\",\"login\":\"operator\"}")
  case strings.Contains(args, "/collaborators/operator/permission"):
    permission := os.Getenv("MY_E2E_PERMISSION")
    fmt.Printf("{\"permission\":%q,\"user\":{\"id\":17,\"node_id\":\"U_example\",\"login\":\"operator\"}}", permission)
  default:
    fmt.Fprintln(os.Stderr, "unexpected gh call:", args)
    os.Exit(2)
  }
}
`)
	stub := filepath.Join(binDir, "gh")
	if runtimeExeSuffix() != "" {
		stub += runtimeExeSuffix()
	}
	cmd := exec.Command("go", "build", "-o", stub, stubSource)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gh stub: %v\n%s", err, out)
	}
	return binDir
}

func runtimeExeSuffix() string {
	if os.PathSeparator == '\\' {
		return ".exe"
	}
	return ""
}

func runGovernanceCheck(t *testing.T, bin, path, permission string, wantAllowed bool, reason, repo, repository, base, head, manifestRepo, manifestBase, mount, attestationRepo, attestationBase string) {
	t.Helper()
	cmd := exec.Command(bin,
		"governance", "check", "--repo", repo, "--repository", repository,
		"--base", base, "--head", head, "--manifest-repo", manifestRepo,
		"--manifest-base", manifestBase, "--mount", mount,
		"--attestation-repo", attestationRepo, "--attestation-repository", "example/records",
		"--attestation-base", attestationBase,
		"--actor-id", "17", "--actor-login", "operator", "--json",
	)
	cmd.Env = append(os.Environ(), "PATH="+path, "MY_E2E_PERMISSION="+permission)
	out, err := cmd.CombinedOutput()
	if wantAllowed {
		if err != nil || !strings.Contains(string(out), `"allowed": true`) {
			t.Fatalf("governance check should allow: %v\n%s", err, out)
		}
		return
	}
	if err == nil || !strings.Contains(string(out), `"allowed": false`) || !strings.Contains(string(out), reason) {
		t.Fatalf("governance check should deny with %q: %v\n%s", reason, err, out)
	}
}

func cloneBare(t *testing.T, source, target string) {
	t.Helper()
	cmd := exec.Command("git", "clone", "--bare", "-q", source, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone bare: %v\n%s", err, out)
	}
}

func cloneWork(t *testing.T, source, target string) string {
	t.Helper()
	cmd := exec.Command("git", "clone", "-q", source, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone work: %v\n%s", err, out)
	}
	return target
}

func commitAll(t *testing.T, repo, message string) string {
	t.Helper()
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "-c", "user.name=Example Test", "-c", "user.email=my-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", message)
	return gitOutput(t, repo, "rev-parse", "HEAD")
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
