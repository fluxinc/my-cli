package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/workspace"
)

func TestSyncHelpMentionsManifestControlPublishPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "sync", "--help"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	for _, want := range []string{
		"my publish --manifest NAME",
		"my sync --publish direct --scope manifest",
		"Unrelated dirty non-content files are still held",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("sync help stderr = %q, missing %q", stderr.String(), want)
		}
	}
	if stdout.String() != "" {
		t.Fatalf("sync help stdout = %q, want stderr-only usage", stdout.String())
	}
}

func TestSyncContentPathsIncludesRecordDefaults(t *testing.T) {
	tests := []struct {
		name  string
		entry workspace.Entry
		want  []string
	}{
		{
			name:  "handbook default",
			entry: workspace.Entry{Kind: "handbook"},
			want:  []string{"customers", "meetings", "support", "fleet", "decisions", "projects", "policy", "people"},
		},
		{
			name:  "support default",
			entry: workspace.Entry{Kind: "support"},
			want:  []string{"support"},
		},
		{
			name:  "customers default",
			entry: workspace.Entry{Kind: "customers"},
			want:  []string{"customers"},
		},
		{
			name:  "fleet default",
			entry: workspace.Entry{Kind: "fleet"},
			want:  []string{"fleet"},
		},
		{
			name:  "include paths override",
			entry: workspace.Entry{Kind: "support", IncludePaths: []string{"support/resolved"}},
			want:  []string{"support/resolved"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := syncContentPaths(tt.entry)
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("syncContentPaths() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func setupCLISyncContentMount(t *testing.T, name string) (home, umbrellaRoot, mountPath, remote, writer string) {
	t.Helper()
	root := t.TempDir()
	remote, clone, writer := setupCLIRemoteRepo(t, root, name, map[string]string{
		"README.md": "seed\n",
	})
	home, umbrellaRoot, _, _, _ = setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	mountPath = filepath.Join(umbrellaRoot, "handbook")
	if err := os.MkdirAll(umbrellaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	if _, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	} else {
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        "handbook",
			Kind:      "handbook",
			SourceRef: "manifest:acme:handbook",
			Status:    "synced",
		})
		if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
			t.Fatal(err)
		}
	}
	return home, umbrellaRoot, mountPath, remote, writer
}

func TestSyncDirectPublishesFleetFromHandbookDefault(t *testing.T) {
	root := t.TempDir()
	remote, clone, _ := setupCLIRemoteRepo(t, root, "handbook", map[string]string{
		"README.md": "seed\n",
	})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.MkdirAll(umbrellaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	if _, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	} else {
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        "handbook",
			Kind:      "handbook",
			SourceRef: "manifest:acme:handbook",
			Status:    "synced",
		})
		if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
			t.Fatal(err)
		}
	}
	writeCLITestFile(t, filepath.Join(mountPath, "fleet", "example-device-4.md"), "fleet\n")
	runCLIGit(t, mountPath, "add", "-N", "fleet/example-device-4.md")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status": "pushed"`) {
		t.Fatalf("sync --publish direct stdout = %q, want direct push", stdout.String())
	}
	if got := strings.TrimSpace(gitCLIOutput(t, remote, "show", "master:fleet/example-device-4.md")); got != "fleet" {
		t.Fatalf("remote fleet/example-device-4.md = %q", got)
	}
}

func TestSyncDirectPublishesCustomerFromHandbookDefault(t *testing.T) {
	root := t.TempDir()
	remote, clone, _ := setupCLIRemoteRepo(t, root, "handbook", map[string]string{
		"README.md": "seed\n",
	})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.MkdirAll(umbrellaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	if _, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	} else {
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        "handbook",
			Kind:      "handbook",
			SourceRef: "manifest:acme:handbook",
			Status:    "synced",
		})
		if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "customers", "add", "sampleco.example.com",
		"--manifest", "acme",
		"--workspace", "handbook",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--name", "SampleCo",
	}); err != nil {
		t.Fatalf("customers add: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "outside declared publish paths") {
		t.Fatalf("customers add stderr = %q, want no publish-path warning", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status": "pushed"`) {
		t.Fatalf("sync --publish direct stdout = %q, want direct push", stdout.String())
	}
	if got := gitCLIOutput(t, remote, "show", "master:customers/sampleco.example.com.md"); !strings.Contains(got, "name: SampleCo") {
		t.Fatalf("remote customer record = %q", got)
	}
}

func TestSyncDirectPublishesCustomerFromCustomersDefault(t *testing.T) {
	root := t.TempDir()
	remote, clone, _ := setupCLIRemoteRepo(t, root, "customers", map[string]string{
		"README.md": "seed\n",
	})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "customers", "kind": "customers", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	mountPath := filepath.Join(umbrellaRoot, "customers")
	if err := os.MkdirAll(umbrellaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	if _, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	} else {
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        "customers",
			Kind:      "customers",
			SourceRef: "manifest:acme:customers",
			Status:    "synced",
		})
		if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
			t.Fatal(err)
		}
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "customers", "add", "sampleco.example.com",
		"--manifest", "acme",
		"--workspace", "customers",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--name", "SampleCo",
	}); err != nil {
		t.Fatalf("customers add: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "outside declared publish paths") {
		t.Fatalf("customers add stderr = %q, want no publish-path warning", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status": "pushed"`) {
		t.Fatalf("sync --publish direct stdout = %q, want direct push", stdout.String())
	}
	if got := gitCLIOutput(t, remote, "show", "master:customers/sampleco.example.com.md"); !strings.Contains(got, "name: SampleCo") {
		t.Fatalf("remote customer record = %q", got)
	}
}

func TestSyncHoldsManifestWithLocalMountURL(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "init", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	manifestRepo, err := manifest.DefaultCachePath(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(t.TempDir(), "acme-manifest.git")
	runCLIGit(t, home, "init", "--bare", "-q", remote)
	runCLIGit(t, manifestRepo, "remote", "add", "origin", remote)
	runCLIGit(t, manifestRepo, "push", "-q", "-u", "origin", "master")
	writeCLITestFile(t, filepath.Join(manifestRepo, "catalog", "products.json"), "[{\"id\":\"local\",\"name\":\"Local\",\"description\":\"Local product\"}]\n")
	commitCLIGit(t, manifestRepo, "Edit catalog locally")
	localHead := strings.TrimSpace(gitCLIOutput(t, manifestRepo, "rev-parse", "HEAD"))

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
	}); err != nil {
		t.Fatalf("sync: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"manifest\theld back", "local mount URL", "run my publish --manifest acme", "next=my publish --manifest acme"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync stdout = %q, missing %q", out, want)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("sync json: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"reason_code": "local_mount_urls"`) {
		t.Fatalf("sync json stdout = %q, want local_mount_urls reason_code", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"next_command": "my publish --manifest acme"`) {
		t.Fatalf("sync json stdout = %q, want local mount next_command", stdout.String())
	}
	remoteHead := strings.TrimSpace(gitCLIOutput(t, manifestRepo, "rev-parse", "origin/master"))
	if remoteHead == localHead {
		t.Fatalf("sync pushed manifest despite local mount URL guard")
	}
}

func TestSyncPushHoldsManifestControlChangesInAutoMode(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  { "id": "demo", "name": "Demo", "description": "Demo product" }
]
`)
	commitAndPushCLIGit(t, manifestCache, "Seed product catalog")
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  { "id": "demo", "name": "Demo", "description": "Updated demo product" }
]
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--push",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("sync --push: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"role": "manifest"`, `"status": "held back"`, `"reason_code": "auto_non_content"`, `"next_command": "my publish --manifest acme"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync --push stdout = %q, missing %q", out, want)
		}
	}
	if got := gitCLIOutput(t, remote, "show", "master:catalog/products.json"); strings.Contains(got, "Updated demo product") {
		t.Fatalf("sync --push updated remote catalog in auto mode: %q", got)
	}
}

func TestSyncDirectPublishesManifestControlChanges(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  { "id": "demo", "name": "Demo", "description": "Demo product" }
]
`)
	commitAndPushCLIGit(t, manifestCache, "Seed product catalog")
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  { "id": "demo", "name": "Demo", "description": "Updated demo product" }
]
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"role": "manifest"`, `"status": "pushed"`, `"direction": "outbound"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync --publish direct stdout = %q, missing %q", out, want)
		}
	}
	if got := gitCLIOutput(t, remote, "show", "master:catalog/products.json"); !strings.Contains(got, "Updated demo product") {
		t.Fatalf("remote catalog = %q, want published update", got)
	}
}

func TestSyncDirectPublishesUntrackedManifestControlChanges(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "products.json"), `[
  { "id": "demo", "name": "Demo", "description": "Demo product" }
]
`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"role": "manifest"`, `"status": "pushed"`, `"direction": "outbound"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync --publish direct stdout = %q, missing %q", out, want)
		}
	}
	if got := gitCLIOutput(t, remote, "show", "master:catalog/products.json"); !strings.Contains(got, "Demo product") {
		t.Fatalf("remote catalog = %q, want published new catalog", got)
	}
}

func TestSyncDirectPublishesManifestControlRename(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "old.json"), `{
  "id": "old",
  "description": "Old catalog entry"
}
`)
	commitAndPushCLIGit(t, manifestCache, "Seed old catalog entry")
	runCLIGit(t, manifestCache, "mv", "catalog/old.json", "catalog/new.json")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"role": "manifest"`, `"status": "pushed"`, `"direction": "outbound"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync --publish direct stdout = %q, missing %q", out, want)
		}
	}
	if got := gitCLIOutput(t, remote, "show", "master:catalog/new.json"); !strings.Contains(got, "Old catalog entry") {
		t.Fatalf("remote renamed catalog = %q, want published rename", got)
	}
	cmd := exec.Command("git", "-C", remote, "cat-file", "-e", "master:catalog/old.json")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("remote still has old catalog path after rename\n%s", out)
	}
	if status := gitCLIOutput(t, manifestCache, "status", "--short"); strings.TrimSpace(status) != "" {
		t.Fatalf("manifest checkout dirty after sync direct rename:\n%s", status)
	}
}

func TestSyncDirectHoldsManifestRenameFromOutsideControlPaths(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "README.md"), "seed readme\n")
	commitAndPushCLIGit(t, manifestCache, "Seed manifest readme")
	if err := os.MkdirAll(filepath.Join(manifestCache, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	runCLIGit(t, manifestCache, "mv", "README.md", "catalog/readme.md")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"role": "manifest"`, `"status": "held back"`, `"reason_code": "outside_content_paths"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync --publish direct stdout = %q, missing %q", out, want)
		}
	}
	if got := gitCLIOutput(t, remote, "show", "master:README.md"); !strings.Contains(got, "seed readme") {
		t.Fatalf("remote README = %q, want original file preserved", got)
	}
	cmd := exec.Command("git", "-C", remote, "cat-file", "-e", "master:catalog/readme.md")
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("sync --publish direct pushed cross-boundary rename\n%s", out)
	}
}

func TestSyncDirectHoldsManifestFilesOutsideControlPaths(t *testing.T) {
	home, _, manifestCache, remote, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	writeCLITestFile(t, filepath.Join(manifestCache, "README.md"), "seed\n")
	commitAndPushCLIGit(t, manifestCache, "Seed manifest readme")
	writeCLITestFile(t, filepath.Join(manifestCache, "README.md"), "local scratch\n")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{`"role": "manifest"`, `"status": "held back"`, `"reason_code": "outside_content_paths"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync --publish direct stdout = %q, missing %q", out, want)
		}
	}
	if got := gitCLIOutput(t, remote, "show", "master:README.md"); strings.Contains(got, "local scratch") {
		t.Fatalf("sync --publish direct pushed README outside manifest control paths: %q", got)
	}
}

func TestSyncExplicitGnitBackendReportsMissingWorkspace(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required"
    }
  ]
}`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "sync", "--backend", "gnit", "--manifest", "acme", "--home", home, "--print", "--json"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"backend": "gnit"`,
		"umbrella is not a Gnit control workspace",
		`"status": "held back"`,
		`"id": "handbook"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync stdout = %q, missing %q", out, want)
		}
	}
}

func TestSyncAutoUsesBuiltinForUnrosteredContentInGnitUmbrella(t *testing.T) {
	home, root, content, _ := setupCLITrackedContentWorkspace(t, "auto")
	writeCLITestFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers: []\n")
	writeCLITestFile(t, filepath.Join(content, "meetings", "target-aware.md"), "target aware\n")
	runCLIGit(t, content, "add", "-N", "meetings/target-aware.md")
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "sync", "--publish", "direct", "--scope", "content", "--print", "--json", "--home", home, "--umbrella", root}); err != nil {
		t.Fatalf("sync: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	var report syncCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	result := syncResultByID(t, report, "handbook")
	if report.Backend != "auto" || result.Backend != "builtin" || result.Status != "dry-run" {
		t.Fatalf("report = %#v result=%#v", report, result)
	}
}

func TestSyncBareDefaultPullOnlyAndExplicitPublish(t *testing.T) {
	root := t.TempDir()
	remote, clone, _ := setupCLIRemoteRepo(t, root, "handbook", map[string]string{
		"README.md": "seed\n",
	})
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "`+remote+`", "mode": "required" }
  ]
}`)
	mountPath := filepath.Join(umbrellaRoot, "handbook")
	if err := os.MkdirAll(umbrellaRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(clone, mountPath); err != nil {
		t.Fatal(err)
	}
	if _, state, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	} else {
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        "handbook",
			Kind:      "handbook",
			SourceRef: "manifest:acme:handbook",
			Status:    "synced",
		})
		if err := umbrella.SaveState(umbrellaRoot, state); err != nil {
			t.Fatal(err)
		}
	}
	writeCLITestFile(t, filepath.Join(mountPath, "meetings", "publish.md"), "publish\n")
	runCLIGit(t, mountPath, "add", "-N", "meetings/publish.md")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--json",
	}); err != nil {
		t.Fatalf("bare sync: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"publish": "never"`) ||
		!strings.Contains(stdout.String(), "publish disabled") {
		t.Fatalf("bare sync stdout = %q, want pull-only publish disabled hold", stdout.String())
	}
	if out, err := exec.Command("git", "-C", remote, "show", "master:meetings/publish.md").CombinedOutput(); err == nil {
		t.Fatalf("bare sync published unexpectedly: %s", out)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--push",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--json",
	}); err != nil {
		t.Fatalf("sync --push: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"publish": "auto"`) {
		t.Fatalf("sync --push stdout = %q, want auto policy", stdout.String())
	}
	if out, err := exec.Command("git", "-C", remote, "show", "master:meetings/publish.md").CombinedOutput(); err == nil {
		t.Fatalf("sync --push without private visibility published unexpectedly: %s", out)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--print",
		"--message", "Ship publish",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatalf("sync --publish direct --print: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "next\tapply\tmy sync --publish direct --message 'Ship publish'") {
		t.Fatalf("sync --publish direct --print stdout = %q, want explicit publish apply hint", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
		"--json",
	}); err != nil {
		t.Fatalf("sync --publish direct: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"publish": "direct"`) ||
		!strings.Contains(stdout.String(), `"status": "pushed"`) {
		t.Fatalf("sync --publish direct stdout = %q, want direct push", stdout.String())
	}
	if got := strings.TrimSpace(gitCLIOutput(t, remote, "show", "master:meetings/publish.md")); got != "publish" {
		t.Fatalf("remote publish.md = %q", got)
	}
}

func TestSyncReportsDirtyBehindAndDivergedNextCommands(t *testing.T) {
	t.Run("dirty behind text and JSON", func(t *testing.T) {
		home, umbrellaRoot, mountPath, _, writer := setupCLISyncContentMount(t, "dirty-behind")
		writeCLITestFile(t, filepath.Join(writer, "meetings", "remote.md"), "remote\n")
		commitAndPushCLIGit(t, writer, "remote meeting")
		writeCLITestFile(t, filepath.Join(mountPath, "meetings", "local.md"), "local\n")
		runCLIGit(t, mountPath, "add", "-N", "meetings/local.md")

		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"my", "sync",
			"--backend", "builtin",
			"--publish", "direct",
			"--manifest", "acme",
			"--home", home,
			"--umbrella", umbrellaRoot,
		}); err != nil {
			t.Fatalf("sync dirty-behind: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}
		wantNext := "next=git -C " + shellQuote(mountPath) + " status --short"
		for _, want := range []string{"remote has new commits and checkout has uncommitted files", wantNext} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("sync stdout = %q, missing %q", stdout.String(), want)
			}
		}

		stdout.Reset()
		stderr.Reset()
		if err := a.run([]string{
			"my", "sync",
			"--backend", "builtin",
			"--publish", "direct",
			"--manifest", "acme",
			"--home", home,
			"--umbrella", umbrellaRoot,
			"--json",
		}); err != nil {
			t.Fatalf("sync dirty-behind json: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}
		var report syncCommandReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("decode sync JSON: %v\n%s", err, stdout.String())
		}
		result := syncResultByID(t, report, "handbook")
		if result.ReasonCode != "dirty_behind" {
			t.Fatalf("reason code = %q, want dirty_behind; result=%#v", result.ReasonCode, result)
		}
		wantCommand := "git -C " + shellQuote(mountPath) + " status --short"
		if result.NextCommand != wantCommand {
			t.Fatalf("next command = %q, want %q", result.NextCommand, wantCommand)
		}
	})

	t.Run("diverged JSON", func(t *testing.T) {
		home, umbrellaRoot, mountPath, _, writer := setupCLISyncContentMount(t, "diverged")
		writeCLITestFile(t, filepath.Join(mountPath, "meetings", "local.md"), "local\n")
		commitCLIGit(t, mountPath, "local meeting")
		writeCLITestFile(t, filepath.Join(writer, "meetings", "remote.md"), "remote\n")
		commitAndPushCLIGit(t, writer, "remote meeting")

		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"my", "sync",
			"--backend", "builtin",
			"--publish", "direct",
			"--manifest", "acme",
			"--home", home,
			"--umbrella", umbrellaRoot,
			"--json",
		}); err != nil {
			t.Fatalf("sync diverged json: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
		}
		var report syncCommandReport
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("decode sync JSON: %v\n%s", err, stdout.String())
		}
		result := syncResultByID(t, report, "handbook")
		if result.ReasonCode != "diverged" {
			t.Fatalf("reason code = %q, want diverged; result=%#v", result.ReasonCode, result)
		}
		if result.NextCommand != "my doctor" {
			t.Fatalf("next command = %q, want my doctor", result.NextCommand)
		}
	})
}

func syncResultByID(t *testing.T, report syncCommandReport, id string) syncer.Result {
	t.Helper()
	for _, result := range report.Results {
		if result.ID == id {
			return result
		}
	}
	t.Fatalf("missing sync result %q in %#v", id, report.Results)
	return syncer.Result{}
}

func TestSyncEmitsSeparateManifestAndContentEntries(t *testing.T) {
	home, umbrellaRoot, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    { "id": "handbook", "kind": "handbook", "git_url": "https://github.com/acme/acme-handbook.git", "mode": "required" }
  ]
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync", "--print", "--json",
		"--backend", "builtin",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	var report syncCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode sync JSON: %v\n%s", err, stdout.String())
	}
	var roles []string
	for _, result := range report.Results {
		roles = append(roles, result.Role+":"+result.ID)
	}
	if strings.Join(roles, ",") != "manifest:acme,content:handbook" {
		t.Fatalf("results = %#v, want separate manifest and content entries", report.Results)
	}
}

func TestSyncPersistsLastSyncAuditAndDoctorReportsIt(t *testing.T) {
	home, umbrellaRoot, _, _ := setupCLITrackedManifest(t)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(umbrellaRoot, ".my-cli", "last-sync.json")
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var audit lastSyncAudit
	if err := json.Unmarshal(data, &audit); err != nil {
		t.Fatal(err)
	}
	if audit.Report.Publish != "never" || len(audit.Report.Results) != 1 || audit.Report.Results[0].Head == "" {
		t.Fatalf("audit = %#v, want publish/report/head", audit)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "doctor", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "last-sync\tlast publish\tok") ||
		!strings.Contains(out, "publish=never") ||
		!strings.Contains(out, "already_landed=1") {
		t.Fatalf("doctor stdout = %q", out)
	}
}

func TestSyncReconcilesDerivedAfterManifestPull(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(writer, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	commitAndPushCLIGit(t, writer, "add handbook skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--scope", "manifest",
		"--verbose",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "acme\tacme\tmanifest\tpulled") ||
		!strings.Contains(out, "derived-skill\tclaude-code\t*\tskipped") ||
		!strings.Contains(out, "launch-scoped") {
		t.Fatalf("sync stdout = %q", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("sync installed org skill globally: %v", err)
	}
	assertIndexedGlobalSkill(t, filepath.Join(home, ".config", "opencode", "skills"), "acme-handbook", "acme:handbook", compatibilityGlobalSkillScope)
}

func TestSyncNoDerivedSkipsDerivedReconcile(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(writer, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	commitAndPushCLIGit(t, writer, "add handbook skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--scope", "manifest",
		"--no-derived",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "derived-skill") {
		t.Fatalf("sync stdout = %q, want derived reconcile skipped", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("skill installed despite --no-derived: %v", err)
	}
}

func TestSyncContentScopeDoesNotReconcileDerived(t *testing.T) {
	home, umbrellaRoot, _, _, writer := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(writer, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ]
}`)
	writeCLITestFile(t, filepath.Join(writer, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---
`)
	commitAndPushCLIGit(t, writer, "add handbook skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--scope", "content",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if strings.Contains(out, "derived-skill") {
		t.Fatalf("sync stdout = %q, want content scope to skip derived reconcile", out)
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "acme-handbook")); !os.IsNotExist(err) {
		t.Fatalf("skill installed despite content scope: %v", err)
	}
}

func TestSyncScopeReposAcceptedAndProductsRejected(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "sync", "--backend", "builtin", "--scope", "repos", "--print", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatalf("sync --scope repos failed: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	err := a.run([]string{"my", "sync", "--backend", "builtin", "--scope", "products", "--print", "--manifest", "acme", "--home", home, "--json"})
	if err == nil || !strings.Contains(err.Error(), "--scope must be one of") {
		t.Fatalf("err = %v, want products scope rejected", err)
	}
}

func TestSyncPushUsesManifestPublishPolicyAndCLIOverride(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "sync": { "publish_policy": "never" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "sync", "--backend", "builtin", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "never"`) {
		t.Fatalf("sync stdout = %q, want bare sync publish never", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "sync", "--backend", "builtin", "--push", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "never"`) {
		t.Fatalf("sync --push stdout = %q, want manifest publish policy", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "sync", "--backend", "builtin", "--publish", "direct", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "direct"`) {
		t.Fatalf("sync stdout = %q, want CLI override", stdout.String())
	}
}

func TestSyncPushUsesManifestPRPublishPolicy(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "sync": { "publish_policy": "pr" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "sync", "--backend", "builtin", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "never"`) {
		t.Fatalf("sync stdout = %q, want bare sync publish never", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"my", "sync", "--backend", "builtin", "--push", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "pr"`) {
		t.Fatalf("sync stdout = %q, want manifest PR policy", stdout.String())
	}
}

func TestSyncRejectsPushWithPublishMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "sync", "--push", "--publish", "auto", "--home", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "--push and --publish cannot be combined") {
		t.Fatalf("err = %v, want --push/--publish conflict", err)
	}
}

func TestSyncHumanOutputConciseAndVerbose(t *testing.T) {
	home, umbrellaRoot, _, _ := setupCLITrackedManifest(t)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--print",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "up to date" {
		t.Fatalf("concise sync stdout = %q, want up to date", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{
		"my", "sync",
		"--backend", "builtin",
		"--print",
		"--verbose",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	if !strings.Contains(out, "# backend: builtin") || !strings.Contains(out, "already landed") {
		t.Fatalf("verbose sync stdout = %q, want backend and clean row", out)
	}
}
