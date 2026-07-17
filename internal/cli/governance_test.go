package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
