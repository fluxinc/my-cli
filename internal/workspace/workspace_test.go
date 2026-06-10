package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/manifest"
)

func TestListReadsSyncedManifest(t *testing.T) {
	home := t.TempDir()
	ref := writeRegisteredManifest(t, home, "acme", `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	if ref.Name != "acme" {
		t.Fatalf("ref = %#v", ref)
	}

	entries, err := List(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].LocalPath != filepath.Join(home, ".our", "workspaces", "handbook") {
		t.Fatalf("LocalPath = %q", entries[0].LocalPath)
	}
}

func TestSyncDryRunPlansCloneAndPull(t *testing.T) {
	home := t.TempDir()
	ref := writeRegisteredManifest(t, home, "acme", `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)

	results, err := Sync(home, "acme", []string{"handbook"}, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := results[0].Message; !strings.Contains(got, "git clone") {
		t.Fatalf("clone message = %q", got)
	}

	workspacePath := filepath.Join(home, ".our", "workspaces", "handbook")
	if err := os.MkdirAll(filepath.Join(workspacePath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	results, err = Sync(home, "acme", []string{"handbook"}, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := results[0].Message; !strings.Contains(got, "pull --ff-only") {
		t.Fatalf("pull message = %q", got)
	}
	if ref.LocalPath == "" {
		t.Fatal("registered manifest path was empty")
	}
}

func TestSyncRejectsExistingNonGitDirectory(t *testing.T) {
	home := t.TempDir()
	writeRegisteredManifest(t, home, "acme", `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	if err := os.MkdirAll(filepath.Join(home, ".our", "workspaces", "handbook"), 0o755); err != nil {
		t.Fatal(err)
	}

	results, err := Sync(home, "acme", []string{"handbook"}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "failed" || !strings.Contains(results[0].Error, "not a git repository") {
		t.Fatalf("results = %#v", results)
	}
}

func TestSyncChecksGitHubAuthBeforeClone(t *testing.T) {
	home := t.TempDir()
	writeRegisteredManifest(t, home, "acme", `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.our/workspaces/handbook"
    }
  ]
}`)
	var commands []string
	results, err := Sync(home, "acme", []string{"handbook"}, false, false, func(name string, args ...string) ([]byte, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if name == "gh" {
			return []byte("not logged in"), errors.New("exit 1")
		}
		return []byte("unexpected git"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != "failed" || !strings.Contains(results[0].Error, "gh auth login") {
		t.Fatalf("results = %#v", results)
	}
	if len(commands) != 1 || !strings.HasPrefix(commands[0], "gh auth status") {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestListMountsUsesUmbrellaRoot(t *testing.T) {
	home := t.TempDir()
	writeRegisteredManifest(t, home, "acme", `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required",
      "include_paths": ["meetings", "decisions"]
    }
  ]
}`)
	entries, err := ListMounts(home, "acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].LocalPath != filepath.Join(home, "acme", "handbook") {
		t.Fatalf("LocalPath = %q", entries[0].LocalPath)
	}
	if entries[0].Kind != "handbook" || entries[0].Mode != "required" || entries[0].SourceRef != "manifest:acme:handbook" {
		t.Fatalf("entry = %#v", entries[0])
	}
	if len(entries[0].IncludePaths) != 2 || entries[0].IncludePaths[0] != "meetings" || entries[0].IncludePaths[1] != "decisions" {
		t.Fatalf("IncludePaths = %#v", entries[0].IncludePaths)
	}
}

func TestListMountsResolvesSelfMountGitURL(t *testing.T) {
	home := t.TempDir()
	ref := writeRegisteredManifest(t, home, "acme", `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": ".",
      "mode": "default",
      "include_paths": ["meetings"]
    }
  ]
}`)
	entries, err := ListMounts(home, "acme", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].GitURL != ref.GitURL {
		t.Fatalf("GitURL = %q, want %q", entries[0].GitURL, ref.GitURL)
	}
}

func TestSyncMountsFiltersModes(t *testing.T) {
	home := t.TempDir()
	writeRegisteredManifest(t, home, "acme", `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "mode": "required"
    },
    {
      "id": "leadership",
      "kind": "meetings",
      "git_url": "https://github.com/acme/leadership.git",
      "mode": "optional"
    }
  ]
}`)
	results, err := SyncMounts(home, "acme", "", nil, false, []string{"required"}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Workspace != "handbook" {
		t.Fatalf("results = %#v", results)
	}
}

func TestSyncMountsUsesSparseCheckoutIncludePaths(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "workspace-source")
	writeFile(t, filepath.Join(source, "meetings", ".gitkeep"), "")
	writeFile(t, filepath.Join(source, "decisions", ".gitkeep"), "")
	writeFile(t, filepath.Join(source, "skills", "acme-handbook", "SKILL.md"), "# Acme Handbook\n")
	writeFile(t, filepath.Join(source, "manifest.json"), "{}\n")
	initGitRepo(t, source)
	writeRegisteredManifest(t, home, "acme", fmt.Sprintf(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": %q,
      "mode": "required",
      "include_paths": ["meetings", "decisions"]
    }
  ]
}`, source))

	results, err := SyncMounts(home, "acme", "", []string{"handbook"}, false, nil, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "synced" {
		t.Fatalf("results = %#v", results)
	}
	handbook := filepath.Join(home, "acme", "handbook")
	for _, want := range []string{
		filepath.Join(handbook, "meetings", ".gitkeep"),
		filepath.Join(handbook, "decisions", ".gitkeep"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("sparse mount missing %s: %v", want, err)
		}
	}
	for _, blocked := range []string{
		filepath.Join(handbook, "skills"),
		filepath.Join(handbook, "manifest.json"),
	} {
		if _, err := os.Stat(blocked); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sparse mount included %s: err=%v", blocked, err)
		}
	}
}

func writeRegisteredManifest(t *testing.T, home, name, body string) manifest.Ref {
	t.Helper()
	ref, err := manifest.Add(home, name, "https://github.com/acme/acme-ai-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ref.LocalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest.ManifestPath(ref), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return ref
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=our-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed workspace")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}
