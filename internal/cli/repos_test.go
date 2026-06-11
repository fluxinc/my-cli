package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/umbrella"
)

// setupCLIRepoCatalog builds a registered manifest with one catalog repo
// backed by a real local bare remote, plus an ensured umbrella.
func setupCLIRepoCatalog(t *testing.T) (home, umbrellaRoot, remote string) {
	t.Helper()
	home = t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	umbrellaRoot = filepath.Join(home, "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" }
}`)

	seed := filepath.Join(home, "seed")
	writeCLITestFile(t, filepath.Join(seed, "main.go"), "package main\n")
	initCLIGitRepo(t, seed)
	remote = filepath.Join(home, "sample-service.git")
	runCLIGit(t, home, "init", "--bare", "-q", "sample-service.git")
	runCLIGit(t, seed, "remote", "add", "origin", remote)
	runCLIGit(t, seed, "push", "-q", "origin", "HEAD:master")

	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "repos.json"), `[
  {
    "id": "sample-service",
    "git_url": "`+remote+`",
    "description": "Sample service source"
  }
]`)
	if _, _, err := umbrella.Ensure(umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	return home, umbrellaRoot, remote
}

func TestReposAddClonesAndRecordsSelection(t *testing.T) {
	home, umbrellaRoot, _ := setupCLIRepoCatalog(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}

	if err := a.run([]string{"our", "repos", "add", "sample-service", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatalf("repos add: %v\nstderr: %s", err, stderr.String())
	}
	clone := filepath.Join(umbrellaRoot, "repos", "sample-service")
	if _, err := os.Stat(filepath.Join(clone, "main.go")); err != nil {
		t.Fatalf("clone missing: %v", err)
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.SelectedRepos) != 1 || state.SelectedRepos[0] != "sample-service" {
		t.Fatalf("selected repos = %#v", state.SelectedRepos)
	}
	var mount *umbrella.MountStatus
	for i := range state.Mounts {
		if state.Mounts[i].ID == "repo:sample-service" {
			mount = &state.Mounts[i]
		}
	}
	if mount == nil || mount.Kind != "repo" {
		t.Fatalf("mounts = %#v", state.Mounts)
	}
}

func TestReposAddAdoptsExistingMatchingClone(t *testing.T) {
	home, umbrellaRoot, remote := setupCLIRepoCatalog(t)
	clone := filepath.Join(umbrellaRoot, "repos", "sample-service")
	runCLIGit(t, home, "clone", "-q", remote, clone)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "repos", "add", "sample-service", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatalf("repos add over existing clone: %v\nstderr: %s", err, stderr.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.SelectedRepos) != 1 {
		t.Fatalf("selected repos = %#v", state.SelectedRepos)
	}
}

func TestReposAddRejectsMismatchedRemote(t *testing.T) {
	home, umbrellaRoot, _ := setupCLIRepoCatalog(t)
	other := filepath.Join(home, "other.git")
	runCLIGit(t, home, "init", "--bare", "-q", "other.git")
	clone := filepath.Join(umbrellaRoot, "repos", "sample-service")
	writeCLITestFile(t, filepath.Join(clone, "README.md"), "other\n")
	initCLIGitRepo(t, clone)
	runCLIGit(t, clone, "remote", "add", "origin", other)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "repos", "add", "sample-service", "--home", home, "--umbrella", umbrellaRoot})
	if err == nil || !strings.Contains(err.Error(), "tracks") {
		t.Fatalf("err = %v, want remote mismatch", err)
	}
}

func TestReposListReportsCatalogAndCloneState(t *testing.T) {
	home, umbrellaRoot, _ := setupCLIRepoCatalog(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "repos", "add", "sample-service", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"our", "repos", "list", "--home", home, "--umbrella", umbrellaRoot, "--json"}); err != nil {
		t.Fatalf("repos list: %v\nstderr: %s", err, stderr.String())
	}
	var entries []struct {
		ID       string `json:"id"`
		GitURL   string `json:"git_url"`
		Selected bool   `json:"selected"`
		Cloned   bool   `json:"cloned"`
		Path     string `json:"path,omitempty"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatalf("parse JSON: %v\nstdout: %s", err, stdout.String())
	}
	if len(entries) != 1 || entries[0].ID != "sample-service" || !entries[0].Selected || !entries[0].Cloned || entries[0].Path == "" {
		t.Fatalf("entries = %#v", entries)
	}

	stdout.Reset()
	if err := a.run([]string{"our", "repos", "list", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "sample-service") {
		t.Fatalf("human output = %q", stdout.String())
	}
}

func TestReposRemoveDeselectsAndKeepsCloneWithoutForce(t *testing.T) {
	home, umbrellaRoot, _ := setupCLIRepoCatalog(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "repos", "add", "sample-service", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(umbrellaRoot, "repos", "sample-service")

	if err := a.run([]string{"our", "repos", "remove", "sample-service", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatalf("repos remove: %v\nstderr: %s", err, stderr.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.SelectedRepos) != 0 {
		t.Fatalf("selected repos = %#v", state.SelectedRepos)
	}
	if _, err := os.Stat(clone); err != nil {
		t.Fatalf("clone deleted without --force: %v", err)
	}

	if err := a.run([]string{"our", "repos", "remove", "sample-service", "--force", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatalf("repos remove --force: %v\nstderr: %s", err, stderr.String())
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatalf("clone remains after --force: %v", err)
	}
}

func TestSetupClonesDefaultRepos(t *testing.T) {
	home, umbrellaRoot, remote := setupCLIRepoCatalog(t)
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "catalog", "repos.json"), `[
  {
    "id": "sample-service",
    "git_url": "`+remote+`",
    "description": "Sample service source",
    "default": true
  }
]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatalf("setup: %v\nstderr: %s", err, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "repos", "sample-service", "main.go")); err != nil {
		t.Fatalf("default repo not cloned at setup: %v", err)
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.SelectedRepos) != 1 || state.SelectedRepos[0] != "sample-service" {
		t.Fatalf("selected repos = %#v", state.SelectedRepos)
	}
}

func TestRootProductFlagRemoved(t *testing.T) {
	home, umbrellaRoot, _ := setupCLIRepoCatalog(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"our", "root", "--product", "sample-service", "--home", home, "--umbrella", umbrellaRoot})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), "--product was removed") || !strings.Contains(stderr.String(), "--repo") {
		t.Fatalf("stderr = %q, want product-flag-removed remediation", stderr.String())
	}
}
