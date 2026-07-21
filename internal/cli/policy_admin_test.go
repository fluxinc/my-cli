package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func TestAdminPolicyRegisteredPublishesCommittedBlobDigestAndPreservesCache(t *testing.T) {
	body := adminPolicyManifestBody()
	home, umbrellaRoot, cache, manifestRemote, _ := setupCLITrackedManifestBody(t, body)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	reg.Manifests[0].GitURL = "https://github.com/example/control.git"
	if err := manifest.SaveRegistry(home, reg); err != nil {
		t.Fatal(err)
	}

	committed := []byte("# Handling policy\n\nProtect the workspace.\n")
	dirty := []byte("# Uncommitted replacement\n")
	mount := filepath.Join(umbrellaRoot, "workspace")
	writeCLITestFile(t, filepath.Join(mount, "policy", "handling.md"), string(committed))
	initCLIGitRepo(t, mount)
	mountRemote := filepath.Join(home, "workspace.git")
	runCLIGit(t, home, "init", "--bare", "-q", mountRemote)
	runCLIGit(t, mount, "remote", "add", "origin", mountRemote)
	runCLIGit(t, mount, "branch", "-M", "master")
	runCLIGit(t, mount, "push", "-q", "-u", "origin", "master")
	if err := os.WriteFile(filepath.Join(mount, "policy", "handling.md"), dirty, 0o644); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(filepath.Join(cache, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	headBefore := strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD"))
	state := &governedPRRunnerState{remote: manifestRemote, permission: "admin", repository: "example/control"}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAdminAccessRunner(), publishRunner: state.run}
	if err := a.run([]string{
		"my", "admin", "policy", "add", "handling-policy",
		"--title", "Workspace handling policy", "--mount", "workspace", "--path", "policy/handling.md",
		"--version", "2026-07", "--acceptance", "required",
		"--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--json",
	}); err != nil {
		t.Fatalf("registered policy add: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	var result adminPolicyResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(committed))
	dirtyDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(dirty))
	if result.Action != "added" || result.Policy.SHA256 != wantDigest || result.Policy.SHA256 == dirtyDigest || result.Publication != "pull request opened" || result.PRURL == "" {
		t.Fatalf("result = %#v, want committed digest %s", result, wantDigest)
	}
	after, err := os.ReadFile(filepath.Join(cache, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || strings.TrimSpace(gitCLIOutput(t, cache, "status", "--porcelain")) != "" || strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD")) != headBefore {
		t.Fatal("registered policy authoring modified the sync-managed manifest cache")
	}
	proposal := gitCLIOutput(t, manifestRemote, "show", state.commit+":manifest.json")
	if !strings.Contains(proposal, `"id": "handling-policy"`) || !strings.Contains(proposal, wantDigest) || strings.Contains(proposal, dirtyDigest) {
		t.Fatalf("proposal does not bind committed policy bytes:\n%s", proposal)
	}
}

func TestAdminPolicyRegisteredRejectsDirtyManifestCache(t *testing.T) {
	home, umbrellaRoot, cache, _, _ := setupCLITrackedManifestBody(t, adminPolicyManifestBody())
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	reg.Manifests[0].GitURL = "https://github.com/example/control.git"
	if err := manifest.SaveRegistry(home, reg); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(cache, "operator-private.txt"), "preserve me\n")
	before, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAdminAccessRunner()}
	err = a.run([]string{
		"my", "admin", "policy", "add", "handling-policy",
		"--title", "Workspace handling policy", "--mount", "workspace", "--path", "policy/handling.md",
		"--version", "2026-07", "--acceptance", "required",
		"--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "registered manifest cache must remain clean") {
		t.Fatalf("dirty-cache error = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("dirty-cache rejection changed manifest bytes")
	}
}

func TestAdminPolicyRegisteredFailsClosedBeforePublication(t *testing.T) {
	home, umbrellaRoot, cache, _, _ := setupCLITrackedManifestBody(t, adminPolicyManifestBody())
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	reg.Manifests[0].GitURL = "https://github.com/example/control.git"
	if err := manifest.SaveRegistry(home, reg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	a := app{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminRunner("write"),
		publishRunner: func(name string, args ...string) ([]byte, error) {
			t.Fatal("publisher called before admin authorization")
			return nil, nil
		},
	}
	err = a.run([]string{
		"my", "admin", "policy", "add", "handling-policy",
		"--title", "Workspace handling policy", "--mount", "workspace", "--path", "policy/handling.md",
		"--version", "2026-07", "--acceptance", "required",
		"--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "governed manifest authoring denied") {
		t.Fatalf("authorization error = %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("denied registered policy authoring changed manifest bytes")
	}
}

func TestAdminPolicyRegisteredRejectsStaleCache(t *testing.T) {
	home, umbrellaRoot, cache, _, writer := setupCLITrackedManifestBody(t, adminPolicyManifestBody())
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	reg.Manifests[0].GitURL = "https://github.com/example/control.git"
	if err := manifest.SaveRegistry(home, reg); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "README.md"), "manifest advanced remotely\n")
	commitCLIGit(t, writer, "advance manifest remotely")
	runCLIGit(t, writer, "push", "-q", "origin", "HEAD")
	headBefore := strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD"))
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminAccessRunner()}
	err = a.run([]string{
		"my", "admin", "policy", "add", "handling-policy",
		"--title", "Workspace handling policy", "--mount", "workspace", "--path", "policy/handling.md",
		"--version", "2026-07", "--acceptance", "required",
		"--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot,
	})
	if err == nil || !strings.Contains(err.Error(), "not at its trusted upstream") {
		t.Fatalf("stale-cache error = %v", err)
	}
	if strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD")) != headBefore {
		t.Fatal("stale-cache rejection moved manifest HEAD")
	}
}

func TestAdminPolicyRegisteredRemovePublishesIsolatedPR(t *testing.T) {
	body := strings.Replace(
		adminPolicyManifestBody(),
		`"attestations":`,
		`"policies": [{"id":"legacy-policy","title":"Legacy","mount":"workspace","path":"policy/legacy.md","version":"1","sha256":"sha256:`+strings.Repeat("a", 64)+`","acceptance":"optional"}],
    "attestations":`,
		1,
	)
	home, umbrellaRoot, cache, manifestRemote, _ := setupCLITrackedManifestBody(t, body)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	reg.Manifests[0].GitURL = "https://github.com/example/control.git"
	if err := manifest.SaveRegistry(home, reg); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	state := &governedPRRunnerState{remote: manifestRemote, permission: "admin", repository: "example/control"}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAdminAccessRunner(), publishRunner: state.run}
	if err := a.run([]string{"my", "admin", "policy", "remove", "legacy-policy", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot, "--json"}); err != nil {
		t.Fatalf("registered remove: %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	var result adminPolicyResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Action != "removed" || result.Policy.ID != "legacy-policy" || result.Publication != "pull request opened" {
		t.Fatalf("result = %#v", result)
	}
	after, _ := os.ReadFile(filepath.Join(cache, "manifest.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("registered remove changed manifest cache")
	}
	proposal := gitCLIOutput(t, manifestRemote, "show", state.commit+":manifest.json")
	if strings.Contains(proposal, `"id": "legacy-policy"`) {
		t.Fatalf("removed policy remains in proposal:\n%s", proposal)
	}
}

func TestAdminPolicyExplicitAddAndRemove(t *testing.T) {
	dir := t.TempDir()
	writeCLITestFile(t, filepath.Join(dir, "manifest.json"), adminPolicyManifestBody())
	digest := "sha256:" + strings.Repeat("a", 64)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAdminAccessRunner()}
	if err := a.run([]string{
		"my", "admin", "policy", "add", "handling-policy",
		"--title", "Workspace handling policy", "--mount", "workspace", "--path", "policy/handling.md",
		"--version", "2026-07", "--acceptance", "required", "--role", "admin",
		"--manifest-dir", dir, "--sha256", digest, "--json",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Governance.Policies) != 1 || doc.Governance.Policies[0].ID != "handling-policy" || doc.Governance.Policies[0].SHA256 != digest || len(doc.Governance.Policies[0].Roles) != 1 {
		t.Fatalf("policies = %#v", doc.Governance.Policies)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "admin", "policy", "remove", "handling-policy", "--manifest-dir", dir, "--json"}); err != nil {
		t.Fatal(err)
	}
	doc, _, err = manifest.LoadDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Governance.Policies) != 0 {
		t.Fatalf("policies after remove = %#v", doc.Governance.Policies)
	}
}

func TestAdminPolicyExplicitRequiresValidDigest(t *testing.T) {
	dir := t.TempDir()
	writeCLITestFile(t, filepath.Join(dir, "manifest.json"), adminPolicyManifestBody())
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminAccessRunner()}
	base := []string{
		"my", "admin", "policy", "add", "handling-policy",
		"--title", "Workspace handling policy", "--mount", "workspace", "--path", "policy/handling.md",
		"--version", "2026-07", "--acceptance", "required", "--manifest-dir", dir,
	}
	if err := a.run(base); err == nil || !strings.Contains(err.Error(), "--sha256 is required") {
		t.Fatalf("missing digest error = %v", err)
	}
	if err := a.run(append(base, "--sha256", "sha256:not-a-digest")); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("invalid digest error = %v", err)
	}
}

func adminPolicyManifestBody() string {
	return `{
  "manifest_version": 1,
  "organization": {"id":"acme","name":"Acme Example"},
  "umbrella": {"recommended_path":"~/acme"},
  "mounts": [{"id":"workspace","kind":"handbook","git_url":"https://github.com/example/workspace.git","mode":"required"}],
  "roles": [{"id":"admin","purpose":"Administer the workspace","mounts":["workspace"]}],
  "governance": {
    "authorization": {"provider":"github","manifest_repository":"example/control","admin_permission":"admin"},
    "attestations": {"mount":"workspace","path":"compliance/attestations","identity":"github"},
    "protections": [{"mount":"workspace","paths":["compliance/attestations"],"mode":"append-only"}]
  }
}`
}
