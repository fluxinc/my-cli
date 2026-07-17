package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccessCheckDryRunUsesLiveRightsAndWritesNothing(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "git@github.com:example/handbook.git", "mode": "required" }
  ],
  "governance": {
    "authorization": {
      "provider": "github",
      "manifest_repository": "example/control",
      "admin_permission": "admin"
    }
  }
}`)
	var calls []string
	runner := func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		joined := strings.Join(args, " ")
		if joined == "api user" {
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		}
		repo := strings.TrimPrefix(joined, "api -i repos/")
		id := int64(29)
		node := "R_control"
		if repo == "example/handbook" {
			id = 30
			node = "R_handbook"
		}
		body := fmt.Sprintf(`{"id":%d,"node_id":%q,"full_name":%q,"private":true,"permissions":{"admin":false,"push":true,"pull":true}}`, id, node, repo)
		return []byte("HTTP/2.0 200 Status\n\n" + body), nil
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: runner}
	if err := a.run([]string{"my", "access", "check", "--dry-run", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var report accessCheckReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse report: %v\n%s", err, stdout.String())
	}
	if !report.DryRun || report.Writes || len(report.Targets) != 2 {
		t.Fatalf("report = %#v", report)
	}
	for _, target := range report.Targets {
		if target.Decision.State != "allowed" || target.FutureAction == "revocation-pending-confirmation" {
			t.Fatalf("target = %#v", target)
		}
	}
	inventory := filepath.Join(home, ".local", "state", "my-cli", "access", "inventory.json")
	if _, err := os.Stat(inventory); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote inventory: %v", err)
	}
	if len(calls) != 4 {
		t.Fatalf("calls = %#v, want actor+repository for two targets", calls)
	}
}

func TestAccessCheckRequiresExplicitDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "access", "check", "--home", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "requires --dry-run") {
		t.Fatalf("err = %v", err)
	}
}
