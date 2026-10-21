package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRoleMountSetupManifest registers a manifest whose mounts cover every
// mode, each backed by a local bare remote so setup clones without network.
func writeRoleMountSetupManifest(t *testing.T, home string) string {
	t.Helper()
	isolateCLITestHome(t, home)
	remotes := filepath.Join(home, "remotes")
	if err := os.MkdirAll(remotes, 0o755); err != nil {
		t.Fatal(err)
	}
	urls := map[string]string{}
	for _, id := range []string{"handbook", "meetings", "leadership", "ops-docs"} {
		remote, _, _ := setupCLIRemoteRepo(t, remotes, id, map[string]string{"README.md": id + "\n"})
		urls[id] = remote
	}
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+urls["handbook"]+`", "mode": "required" },
    { "id": "meetings", "kind": "meetings", "git_url": "`+urls["meetings"]+`", "mode": "default" },
    { "id": "leadership", "kind": "meetings", "git_url": "`+urls["leadership"]+`", "mode": "optional" },
    { "id": "ops-docs", "kind": "docs", "git_url": "`+urls["ops-docs"]+`", "mode": "role" }
  ],
  "roles": [
    { "id": "operator", "purpose": "Operator", "mounts": ["handbook", "leadership", "ops-docs"] },
    { "id": "viewer", "purpose": "Viewer", "mounts": ["handbook"] }
  ]
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "manifests", "add", "acme", "https://github.com/acme/acme-ai-manifest.git", "--home", home}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, "acme")
}

func runRoleMountSetup(t *testing.T, home string, extra ...string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	args := append([]string{"my", "setup", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}, extra...)
	if err := a.run(args); err != nil {
		t.Fatalf("setup %v: %v\nstdout:\n%s\nstderr:\n%s", extra, err, stdout.String(), stderr.String())
	}
}

func assertMountCloned(t *testing.T, root, id string, want bool) {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, id, "README.md"))
	if got := err == nil; got != want {
		t.Fatalf("mount %s cloned = %v, want %v (err %v)", id, got, want, err)
	}
}

func TestSetupWithoutRoleSkipsRoleOnlyMount(t *testing.T) {
	home := t.TempDir()
	root := writeRoleMountSetupManifest(t, home)
	runRoleMountSetup(t, home)
	assertMountCloned(t, root, "handbook", true)
	assertMountCloned(t, root, "meetings", true)
	assertMountCloned(t, root, "leadership", false)
	assertMountCloned(t, root, "ops-docs", false)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "mounts", "list", "--home", home, "--umbrella", root, "--json"}); err != nil {
		t.Fatalf("mounts list: %v\n%s", err, stderr.String())
	}
	if strings.Contains(stdout.String(), "ops-docs") || !strings.Contains(stdout.String(), "leadership") {
		t.Fatalf("unscoped mounts list should hide only the role-only mount:\n%s", stdout.String())
	}
	err := a.run([]string{"my", "mounts", "add", "ops-docs", "--home", home, "--umbrella", root})
	if err == nil || !strings.Contains(err.Error(), "role-only") {
		t.Fatalf("mounts add role-only without role: err = %v", err)
	}
	assertMountCloned(t, root, "ops-docs", false)

	// An optional mount is still an explicit opt-in without a role.
	stdout.Reset()
	if err := a.run([]string{"my", "mounts", "add", "leadership", "--home", home, "--umbrella", root}); err != nil {
		t.Fatalf("mounts add optional: %v\n%s", err, stderr.String())
	}
	assertMountCloned(t, root, "leadership", true)
}

func TestSetupWithNonSelectingRoleSkipsRoleOnlyMount(t *testing.T) {
	home := t.TempDir()
	root := writeRoleMountSetupManifest(t, home)
	runRoleMountSetup(t, home, "--role", "viewer")
	assertMountCloned(t, root, "handbook", true)
	assertMountCloned(t, root, "meetings", true)
	assertMountCloned(t, root, "ops-docs", false)
}

func TestSetupWithSelectingRoleClonesRoleOnlyMount(t *testing.T) {
	home := t.TempDir()
	root := writeRoleMountSetupManifest(t, home)
	runRoleMountSetup(t, home, "--role", "operator")
	assertMountCloned(t, root, "handbook", true)
	assertMountCloned(t, root, "meetings", true)
	assertMountCloned(t, root, "ops-docs", true)
	// Role selection of an optional mount keeps the existing opt-in behavior.
	assertMountCloned(t, root, "leadership", false)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "mounts", "list", "--home", home, "--umbrella", root, "--json"}); err != nil {
		t.Fatalf("mounts list: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"ops-docs"`) {
		t.Fatalf("role-scoped mounts list should include ops-docs:\n%s", stdout.String())
	}
}
