package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredManifestAdoptionSmoke(t *testing.T) {
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "flux")
	build := exec.Command("go", "build", "-o", bin, "./cmd/flux")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	home := t.TempDir()
	manifestRoot := filepath.Join(home, ".local", "share", "flux", "manifests", "acme")
	umbrellaRoot := filepath.Join(home, "acme")
	workspaceRoot := filepath.Join(umbrellaRoot, "handbook")
	workspaceSource := filepath.Join(home, "workspace-source")
	productSource := filepath.Join(home, "product-source")
	writeFile(t, filepath.Join(manifestRoot, "skills", "acme-handbook", "SKILL.md"), `---
name: acme-handbook
description: Acme handbook
---

# Acme Handbook
`)
	writeFile(t, filepath.Join(home, "test-bin", "mock-skill-tool"), `#!/bin/sh
set -eu
/bin/mkdir -p "$1/mock-mail"
/bin/cat > "$1/mock-mail/SKILL.md" <<'EOF'
---
name: mock-mail
description: Mock mail skill
---

# Mock Mail
EOF
`)
	if err := os.Chmod(filepath.Join(home, "test-bin", "mock-skill-tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(productSource, "README.md"), "# Sample Product\n")
	initGitRepo(t, productSource)
	writeFile(t, filepath.Join(manifestRoot, "catalog", "products.json"), fmt.Sprintf(`[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "git_url": %q,
    "description": "Sample service",
    "purpose": "Synthetic product source for public fixture tests",
    "related_skills": ["acme:handbook"]
  }
]`, productSource))
	writeFile(t, filepath.Join(manifestRoot, "manifest.json"), fmt.Sprintf(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "allowed_external_namespaces": ["mock"],
  "umbrella": { "recommended_path": "~/acme" },
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" },
    {
      "id": "mock:mail",
      "install_slug": "mock-mail",
      "source": { "type": "tool", "tool": "mock-skill-tool" },
      "requires": ["tool:mock-skill-tool"]
    }
  ],
  "mounts": [
    {
      "id": "handbook",
      "git_url": %q,
      "kind": "handbook",
      "mode": "required",
      "include_paths": ["meetings"]
    }
  ],
  "tools": [
    {
      "id": "mock-skill-tool",
      "mode": "optional",
      "purpose": "Synthetic local tool for test-provided agent skills",
      "skill_install": { "command": "mock-skill-tool", "args": ["{{ skills_root }}"] }
    }
  ]
}`, workspaceSource))
	writeFile(t, filepath.Join(workspaceSource, "meetings", "2026-03-12-sampleco-implementation.md"), `---
id: 2026-03-12-sampleco-implementation
date: 2026-03-12
title: "SampleCo implementation"
customer: sampleco
product: sample-product
status: finalized
---

# SampleCo implementation

## Promises

- June 1 review of the interface plan.
- data cleanup commitment for modality-specific cleanup.
`)
	writeFile(t, filepath.Join(workspaceSource, "skills", "stale", "SKILL.md"), "# Stale skill source\n")
	writeFile(t, filepath.Join(workspaceSource, "manifest.json"), "{}\n")
	initGitRepo(t, workspaceSource)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	runFlux(t, bin, home, "manifest", "add", "acme", "https://github.com/acme/acme-ai-manifest.git", "--home", home)
	installOut := runFlux(t, bin, home, "onboard", "--home", home)
	for _, want := range []string{"acme-handbook", "acme:handbook", "mock-mail", "mock:mail", "installed"} {
		if !strings.Contains(installOut, want) {
			t.Fatalf("skills install output = %q, missing %q", installOut, want)
		}
	}
	target := filepath.Join(home, ".claude", "skills", "acme-handbook")
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("installed target is not a symlink: info=%v err=%v", info, err)
	}
	toolTarget := filepath.Join(home, ".claude", "skills", "mock-mail")
	if info, err := os.Lstat(toolTarget); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("tool-provided skill target is not a symlink: info=%v err=%v\n%s", info, err, installOut)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, ".git")); err != nil {
		t.Fatalf("workspace was not cloned during skills install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "meetings", "2026-03-12-sampleco-implementation.md")); err != nil {
		t.Fatalf("workspace sparse checkout missed meetings content: %v", err)
	}
	for _, blocked := range []string{
		filepath.Join(workspaceRoot, "skills"),
		filepath.Join(workspaceRoot, "manifest.json"),
	} {
		if _, err := os.Stat(blocked); !os.IsNotExist(err) {
			t.Fatalf("workspace sparse checkout included %s: err=%v", blocked, err)
		}
	}
	for _, path := range []string{
		filepath.Join(umbrellaRoot, ".flux", "workspace.json"),
		filepath.Join(umbrellaRoot, ".flux", "state.json"),
		filepath.Join(umbrellaRoot, "personal"),
		filepath.Join(umbrellaRoot, "products"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("onboard did not create %s: %v", path, err)
		}
	}

	searchOut := runFluxDir(t, bin, home, umbrellaRoot, "meetings", "search", "SampleCo", "--home", home, "--json")
	for _, want := range []string{"2026-03-12-sampleco-implementation", "# SampleCo implementation"} {
		if !strings.Contains(searchOut, want) {
			t.Fatalf("meetings search output = %q, missing %q", searchOut, want)
		}
	}
	getOut := runFluxDir(t, bin, home, umbrellaRoot, "meetings", "get", "2026-03-12-sampleco-implementation", "--home", home, "--json")
	for _, want := range []string{"June 1 review", "data cleanup commitment"} {
		if !strings.Contains(getOut, want) {
			t.Fatalf("meetings get output = %q, missing %q", getOut, want)
		}
	}

	addProductOut := runFluxDir(t, bin, home, umbrellaRoot, "mount", "add", "product:sample-product", "--home", home, "--json")
	for _, want := range []string{"product:sample-product", "synced"} {
		if !strings.Contains(addProductOut, want) {
			t.Fatalf("mount add product output = %q, missing %q", addProductOut, want)
		}
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "products", "sample-product", ".git")); err != nil {
		t.Fatalf("product was not cloned: %v", err)
	}
	mountListOut := runFluxDir(t, bin, home, umbrellaRoot, "mount", "list", "--home", home, "--json")
	if !strings.Contains(mountListOut, "product:sample-product") || !strings.Contains(mountListOut, `"kind": "product"`) {
		t.Fatalf("mount list output = %q", mountListOut)
	}
	state, err := os.ReadFile(filepath.Join(umbrellaRoot, ".flux", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"sample-product"`, `"product:sample-product"`} {
		if !strings.Contains(string(state), want) {
			t.Fatalf("state = %s, missing %q", state, want)
		}
	}
	runFluxDir(t, bin, home, umbrellaRoot, "mount", "add", "product:sample-product", "--home", home)
	runFluxDir(t, bin, home, umbrellaRoot, "mount", "sync", "product:sample-product", "--home", home)
}

func runFlux(t *testing.T, bin, home string, args ...string) string {
	return runFluxDir(t, bin, home, "", args...)
}

func runFluxDir(t *testing.T, bin, home, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+isolatedPathWithGit(t, home))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("flux %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=flux-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed workspace")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func isolatedPathWithGit(t *testing.T, home string) string {
	t.Helper()
	pathDir := filepath.Join(home, "test-bin")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(pathDir, "git")
	if err := os.Symlink(gitPath, linkPath); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	return pathDir
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.Contains(string(data), "module github.com/fluxinc/flux") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
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
