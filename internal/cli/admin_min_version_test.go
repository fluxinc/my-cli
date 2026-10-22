package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const minVersionRoleMountFixture = `,
  "future_field": { "keep": true },
  "mounts": [
    { "id": "ops-docs", "kind": "docs", "git_url": "https://github.com/acme/ops-docs.git", "mode": "role" }
  ],
  "roles": [
    { "id": "operator", "purpose": "Operator", "mounts": ["ops-docs"] }
  ]`

func runAdminMinVersion(t *testing.T, args ...string) (adminMinVersionResult, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run(append([]string{"my", "admin", "min-version", "--json"}, args...))
	var result adminMinVersionResult
	if err == nil {
		if jerr := json.Unmarshal(stdout.Bytes(), &result); jerr != nil {
			t.Fatalf("parse JSON: %v\n%s", jerr, stdout.String())
		}
	}
	return result, err
}

func readManifestText(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAdminMinVersionRaisesToFeatureFloorAndKeepsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, minVersionRoleMountFixture)
	result, err := runAdminMinVersion(t, "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "set" || result.MinMyVersion != "0.45.0" || result.FeatureFloor != "0.45.0" || result.Previous != "" {
		t.Fatalf("result = %#v", result)
	}
	text := readManifestText(t, dir)
	if !strings.Contains(text, `"min_my_version": "0.45.0"`) || !strings.Contains(text, `"future_field"`) {
		t.Fatalf("manifest =\n%s", text)
	}

	again, err := runAdminMinVersion(t, "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Action != "unchanged" || again.MinMyVersion != "0.45.0" {
		t.Fatalf("rerun = %#v, want idempotent", again)
	}
	if readManifestText(t, dir) != text {
		t.Fatal("idempotent rerun rewrote the manifest")
	}
}

func TestAdminMinVersionNeverLowersDeclaredVersion(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `,
  "min_my_version": "0.50.0"`+minVersionRoleMountFixture)
	before := readManifestText(t, dir)
	result, err := runAdminMinVersion(t, "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "unchanged" || result.MinMyVersion != "0.50.0" || readManifestText(t, dir) != before {
		t.Fatalf("result = %#v, want declared higher version kept", result)
	}
}

func TestAdminMinVersionSetValidatesAgainstFloor(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, minVersionRoleMountFixture)
	before := readManifestText(t, dir)
	if _, err := runAdminMinVersion(t, "--manifest-dir", dir, "--set", "0.44.0"); err == nil || !strings.Contains(err.Error(), `older than v0.45.0, which mount mode "role" needs`) {
		t.Fatalf("err = %v, want floor refusal", err)
	}
	if _, err := runAdminMinVersion(t, "--manifest-dir", dir, "--set", "0.46.0-rc1"); err == nil || !strings.Contains(err.Error(), "must be a release version") {
		t.Fatalf("err = %v, want grammar refusal", err)
	}
	if readManifestText(t, dir) != before {
		t.Fatal("refused --set changed the manifest")
	}
	result, err := runAdminMinVersion(t, "--manifest-dir", dir, "--set", "v0.46.0")
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "set" || result.MinMyVersion != "0.46.0" {
		t.Fatalf("result = %#v", result)
	}
}

func TestAdminMinVersionWithoutGatedFeatures(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, "")
	result, err := runAdminMinVersion(t, "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != "unchanged" || result.MinMyVersion != "" || !strings.Contains(result.Message, "no features") {
		t.Fatalf("result = %#v", result)
	}
}

func writeFutureModeManifest(t *testing.T, home string) {
	t.Helper()
	writeSimpleSetupManifest(t, home, "acme", "Acme Example")
	writeCLITestFile(t, filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme", "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [{ "id": "ops", "kind": "docs", "git_url": "https://github.com/acme/ops.git", "mode": "future" }]
}`)
}

func TestLaunchInvalidManifestSuggestsAvailableUpdate(t *testing.T) {
	home := t.TempDir()
	writeFutureModeManifest(t, home)
	server := newCLILatestServer(t, "0.2.0")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, updateCurrentVersion: "0.1.0", updateSource: server.source()}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--print", "claude"})
	if err == nil || !strings.Contains(err.Error(), `manifest "acme" is invalid: mount "ops" mode "future" is unsupported`) ||
		!strings.Contains(err.Error(), "a newer my (v0.2.0) is available and may support this manifest: run `my update`") {
		t.Fatalf("err = %v, want invalid manifest with update hint", err)
	}
}

func TestLaunchInvalidManifestHonorsNoUpdateCheck(t *testing.T) {
	home := t.TempDir()
	writeFutureModeManifest(t, home)
	server := newCLILatestServer(t, "0.2.0")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, updateCurrentVersion: "0.1.0", updateSource: server.source()}
	err := a.run([]string{"my", "ai", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check", "--print", "claude"})
	if err == nil || !strings.Contains(err.Error(), "is invalid") || strings.Contains(err.Error(), "newer my") {
		t.Fatalf("err = %v, want invalid manifest without update hint", err)
	}
	if server.latestRequests != 0 {
		t.Fatalf("latest requests = %d, want 0", server.latestRequests)
	}
}

func TestRootInvalidManifestSuggestsAvailableUpdate(t *testing.T) {
	home := t.TempDir()
	writeFutureModeManifest(t, home)
	server := newCLILatestServer(t, "0.2.0")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, updateCurrentVersion: "0.1.0", updateSource: server.source()}
	err := a.run([]string{"my", "root", "--manifest", "acme", "--home", home, "--umbrella", filepath.Join(home, "acme"), "--no-refresh"})
	if err == nil || !strings.Contains(err.Error(), "may support this manifest") {
		t.Fatalf("err = %v, want update hint", err)
	}
}
