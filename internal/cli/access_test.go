package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/access"
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
	a := testAccessMonitorApp(app{stdout: &stdout, stderr: &stderr, accessRunner: runner}, home)
	homeBefore := snapshotAccessTestTree(t, home)
	if err := a.run([]string{"my", "access", "check", "--dry-run", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if homeAfter := snapshotAccessTestTree(t, home); !reflect.DeepEqual(homeBefore, homeAfter) {
		t.Fatal("access dry run changed filesystem bytes or modes")
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

func TestAccessActivationPreflightsAllTargetsAndRecordsPerRepositoryBaselines(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, governedAccessTestManifest())
	targets := materializeAccessTargetsForTest(t, home)
	runner := governedAccessRunner(false)
	var stdout, stderr bytes.Buffer
	a := testAccessMonitorApp(app{stdout: &stdout, stderr: &stderr, accessRunner: runner}, home)
	if err := a.run([]string{"my", "access", "activate", "--yes", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	inventory, err := access.LoadInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Repositories) != len(targets) {
		t.Fatalf("activated repositories = %d, want %d", len(inventory.Repositories), len(targets))
	}
	for _, entry := range inventory.Repositories {
		if len(entry.Baselines) != 1 || entry.Baselines[0].Actor.ID != 17 || entry.Repository.NodeID == "" {
			t.Fatalf("entry = %#v", entry)
		}
	}
	if _, err := loadAccessMonitorDescriptor(home, "acme"); err != nil {
		t.Fatalf("activation did not install proactive monitor: %v", err)
	}
}

func TestAccessActivationFailureWritesNoPartialBaselines(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, governedAccessTestManifest())
	materializeAccessTargetsForTest(t, home)
	runner := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if joined == "api user" {
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		}
		if joined == "api -i repos/example/handbook" {
			return accessGitHubResponse(200, `{"id":30,"node_id":"R_handbook","full_name":"example/handbook","private":true,"permissions":{"pull":true}}`), nil
		}
		return accessGitHubResponse(503, `{"message":"provider unavailable"}`), fmt.Errorf("exit 1")
	}
	a := testAccessMonitorApp(app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: runner}, home)
	if err := a.run([]string{"my", "access", "activate", "--yes", "--manifest", "acme", "--home", home}); err == nil || !strings.Contains(err.Error(), "made no changes") {
		t.Fatalf("activation error = %v", err)
	}
	path, err := access.InventoryPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed activation wrote partial inventory: %v", err)
	}
}

func TestAccessEnforceQuarantinesOnlyOnSecondPersistedDenial(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, governedAccessTestManifest())
	targets := materializeAccessTargetsForTest(t, home)
	a := testAccessMonitorApp(app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAccessRunner(false)}, home)
	if err := a.run([]string{"my", "access", "activate", "--yes", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	a.accessRunner = governedAccessRunner(true)
	var first bytes.Buffer
	a.stdout = &first
	if err := a.run([]string{"my", "access", "enforce", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if _, err := os.Stat(target.Path); err != nil {
			t.Fatalf("first denial moved %s: %v", target.Path, err)
		}
	}
	time.Sleep(time.Millisecond)
	var second bytes.Buffer
	a.stdout = &second
	if err := a.run([]string{"my", "access", "enforce", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var report accessCheckReport
	if err := json.Unmarshal(second.Bytes(), &report); err != nil {
		t.Fatalf("parse report: %v\n%s", err, second.String())
	}
	for _, target := range report.Targets {
		if target.FutureAction != "quarantined" || target.JournalPath == "" {
			t.Fatalf("target = %#v", target)
		}
		if _, err := os.Stat(target.Path); !os.IsNotExist(err) {
			t.Fatalf("confirmed revocation left active path %s: %v", target.Path, err)
		}
	}
	inventory, err := access.LoadInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Repositories) != 0 || len(inventory.Quarantines) != len(targets) {
		t.Fatalf("inventory = %#v", inventory)
	}
	var statusOut bytes.Buffer
	a.stdout = &statusOut
	if err := a.run([]string{"my", "access", "status", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	var status accessStatusReport
	if err := json.Unmarshal(statusOut.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Error == "" || len(status.Quarantines) != len(targets) {
		t.Fatalf("post-quarantine status = %#v", status)
	}
}

func TestGovernedLaunchUsesFreshPositiveTTLWhenProviderIsOffline(t *testing.T) {
	body := strings.Replace(governedAccessTestManifest(), `"access": {`, `"access": { "positive_ttl": "1h",`, 1)
	home, root, _, _, _ := setupCLITrackedManifestBody(t, body)
	materializeAccessTargetsForTest(t, home)
	a := testAccessMonitorApp(app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAccessRunner(false)}, home)
	if err := a.run([]string{"my", "access", "activate", "--yes", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	doc, err := loadSingleRegisteredDoc(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.accessRunner = func(name string, args ...string) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("network unavailable")
	}
	if err := a.requireGovernedLaunchAccess(home, doc, root); err != nil {
		t.Fatalf("fresh positive TTL did not permit offline launch: %v", err)
	}
	if calls != 0 {
		t.Fatalf("fresh positive TTL made %d provider calls, want 0", calls)
	}
}

func TestGovernedLaunchBlocksUnknownAfterPositiveTTLExpires(t *testing.T) {
	body := strings.Replace(governedAccessTestManifest(), `"access": {`, `"access": { "positive_ttl": "1ns",`, 1)
	home, root, _, _, _ := setupCLITrackedManifestBody(t, body)
	materializeAccessTargetsForTest(t, home)
	a := testAccessMonitorApp(app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAccessRunner(false)}, home)
	if err := a.run([]string{"my", "access", "activate", "--yes", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	doc, err := loadSingleRegisteredDoc(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	a.accessRunner = func(name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("network unavailable")
	}
	if err := a.requireGovernedLaunchAccess(home, doc, root); err == nil || !strings.Contains(err.Error(), "governed launch blocked") {
		t.Fatalf("expired TTL offline gate error = %v", err)
	}
}

func TestGovernedLaunchBlocksNewerUnknownEvenWithinPositiveTTL(t *testing.T) {
	body := strings.Replace(governedAccessTestManifest(), `"access": {`, `"access": { "positive_ttl": "1h",`, 1)
	home, root, _, _, _ := setupCLITrackedManifestBody(t, body)
	targets := materializeAccessTargetsForTest(t, home)
	a := testAccessMonitorApp(app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAccessRunner(false)}, home)
	if err := a.run([]string{"my", "access", "activate", "--yes", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	inventory, err := access.LoadInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := managedInventoryEntry(inventory, targets[0].Path)
	if !ok {
		t.Fatal("activated target missing from inventory")
	}
	unknown := access.Decision{
		State: access.StateUnknown, ReasonCode: "network_unavailable", Actor: entry.Baselines[0].Actor,
	}
	if _, err := access.RecordObservation(access.ObservationInput{
		Home: home, Path: targets[0].Path, Decision: unknown, CheckedAt: time.Now().Add(time.Second),
		RequiredConfirmations: 2, ConfirmationInterval: time.Nanosecond,
	}); err != nil {
		t.Fatal(err)
	}
	doc, err := loadSingleRegisteredDoc(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.requireGovernedLaunchAccess(home, doc, root); err == nil || !strings.Contains(err.Error(), "newer non-allowed") {
		t.Fatalf("newer unknown gate error = %v", err)
	}
}

func governedAccessTestManifest() string {
	return `{
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
    },
    "access": {
      "revocation_confirmations": 2,
      "confirmation_interval": "1ns"
    }
  }
}`
}

func materializeAccessTargetsForTest(t *testing.T, home string) []accessTarget {
	t.Helper()
	doc, err := loadSingleRegisteredDoc(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := collectAccessTargets(home, doc, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if pathExists(target.Path) {
			continue
		}
		if err := os.MkdirAll(target.Path, 0o755); err != nil {
			t.Fatal(err)
		}
		writeCLITestFile(t, filepath.Join(target.Path, "README.md"), "# Managed test repository\n")
		initCLIGitRepo(t, target.Path)
	}
	return targets
}

func governedAccessRunner(deny bool) access.Runner {
	return func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if joined == "api user" {
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		}
		if strings.HasPrefix(joined, "api -i repositories/") {
			return accessGitHubResponse(404, `{"message":"Not Found"}`), fmt.Errorf("exit 1")
		}
		repo := strings.TrimPrefix(joined, "api -i repos/")
		if deny {
			return []byte("HTTP/2.0 404 Status\nX-OAuth-Scopes: repo\n\n{\"message\":\"Not Found\"}"), fmt.Errorf("exit 1")
		}
		id, node := 29, "R_control"
		if repo == "example/handbook" {
			id, node = 30, "R_handbook"
		}
		body := fmt.Sprintf(`{"id":%d,"node_id":%q,"full_name":%q,"private":true,"permissions":{"pull":true}}`, id, node, repo)
		return accessGitHubResponse(200, body), nil
	}
}

func accessGitHubResponse(status int, body string) []byte {
	return []byte(fmt.Sprintf("HTTP/2.0 %d Status\n\n%s", status, body))
}

func snapshotAccessTestTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ":" + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(":%x", sha256.Sum256(data))
		}
		snapshot[filepath.ToSlash(rel)] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func testAccessMonitorApp(a app, home string) app {
	a.accessPlatform = "linux"
	a.accessExecutable = filepath.Join(home, "bin", "my")
	a.accessMonitorRunner = func(name string, args ...string) ([]byte, error) { return []byte("active"), nil }
	return a
}
