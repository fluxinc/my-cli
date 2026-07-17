package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRegisteredManifestAdoptionSmoke(t *testing.T) {
	root := repoRoot(t)
	bin := filepath.Join(t.TempDir(), "my")
	build := exec.Command("go", "build", "-o", bin, "./cmd/my")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	home := t.TempDir()
	manifestRoot := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
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
	writeFile(t, filepath.Join(manifestRoot, "catalog", "repos.json"), fmt.Sprintf(`[
  {
    "id": "sample-service",
    "git_url": %q,
    "description": "Sample service source"
  }
]`, productSource))
	writeFile(t, filepath.Join(manifestRoot, "catalog", "products.json"), `[
  {
    "id": "sample-product",
    "name": "Sample Product",
    "description": "Sample service",
    "purpose": "Synthetic product source for public fixture tests",
    "repos": ["sample-service"],
    "related_skills": ["acme:handbook"]
  }
]`)
	writeFile(t, filepath.Join(manifestRoot, "agent-guidance", "acme.md"), `# Acme Agent Defaults

Acme operators should check the local handbook before answering operational questions.
`)
	writeFile(t, filepath.Join(manifestRoot, "manifest.json"), fmt.Sprintf(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "allowed_external_namespaces": ["mock"],
  "umbrella": { "recommended_path": "~/acme" },
  "agent_guidance": { "paths": ["agent-guidance/acme.md"] },
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
      "include_paths": ["customers", "meetings"]
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
	writeFile(t, filepath.Join(workspaceSource, "customers", "sampleco.example.com.md"), `---
id: sampleco.example.com
name: SampleCo
domain: sampleco.example.com
domain_confirmed: true
aliases:
  - sampleco
---

# SampleCo
`)
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

	runMy(t, bin, home, "manifests", "add", "acme", "https://github.com/acme/acme-ai-manifest.git", "--home", home)
	installOut := runMy(t, bin, home, "setup", "--home", home)
	// ADR 0001: organization and tool skills are no longer installed user-global.
	// Concise setup output hides launch-scoped org/tool skill rows; only the
	// bundled self-skill is ensured in the harness dir.
	for _, want := range []string{"my:self", "installed"} {
		if !strings.Contains(installOut, want) {
			t.Fatalf("setup output = %q, missing %q", installOut, want)
		}
	}
	if strings.Contains(installOut, "launch-scoped") {
		t.Fatalf("setup output = %q, want concise default without launch-scoped rows", installOut)
	}
	selfTarget := filepath.Join(home, ".claude", "skills", "my-cli")
	if _, err := os.Lstat(selfTarget); err != nil {
		t.Fatalf("self-skill was not ensured in the harness dir: err=%v\n%s", err, installOut)
	}
	// Org and tool skills must NOT be materialized into the user config dir.
	for _, slug := range []string{"acme-handbook", "mock-mail"} {
		if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", slug)); !os.IsNotExist(err) {
			t.Fatalf("org skill %q must not be installed user-global; err=%v\n%s", slug, err, installOut)
		}
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, ".git")); err != nil {
		t.Fatalf("workspace was not cloned during skills install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "meetings", "2026-03-12-sampleco-implementation.md")); err != nil {
		t.Fatalf("workspace sparse checkout missed meetings content: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "customers", "sampleco.example.com.md")); err != nil {
		t.Fatalf("workspace sparse checkout missed customer content: %v", err)
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
		filepath.Join(umbrellaRoot, ".my-cli", "workspace.json"),
		filepath.Join(umbrellaRoot, ".my-cli", "state.json"),
		filepath.Join(umbrellaRoot, "personal"),
		filepath.Join(umbrellaRoot, "repos"),
		filepath.Join(umbrellaRoot, "AGENTS.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("onboard did not create %s: %v", path, err)
		}
	}
	agents, err := os.ReadFile(filepath.Join(umbrellaRoot, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"My AI Workspace", "Acme Agent Defaults", "my customers list", "my meetings search <text>"} {
		if !strings.Contains(string(agents), want) {
			t.Fatalf("AGENTS.md = %s, missing %q", agents, want)
		}
	}
	if target, err := os.Readlink(filepath.Join(umbrellaRoot, "CLAUDE.md")); err != nil || target != "AGENTS.md" {
		t.Fatalf("CLAUDE.md is not a symlink to AGENTS.md: target=%q err=%v", target, err)
	}

	searchOut := runMyDir(t, bin, home, umbrellaRoot, "meetings", "search", "SampleCo", "--home", home, "--json")
	for _, want := range []string{"2026-03-12-sampleco-implementation", "# SampleCo implementation"} {
		if !strings.Contains(searchOut, want) {
			t.Fatalf("meetings search output = %q, missing %q", searchOut, want)
		}
	}
	getOut := runMyDir(t, bin, home, umbrellaRoot, "meetings", "get", "2026-03-12-sampleco-implementation", "--home", home, "--json")
	for _, want := range []string{"June 1 review", "data cleanup commitment"} {
		if !strings.Contains(getOut, want) {
			t.Fatalf("meetings get output = %q, missing %q", getOut, want)
		}
	}

	addRepoOut := runMyDir(t, bin, home, umbrellaRoot, "repos", "add", "sample-service", "--home", home, "--json")
	for _, want := range []string{"repo:sample-service", "synced"} {
		if !strings.Contains(addRepoOut, want) {
			t.Fatalf("repos add output = %q, missing %q", addRepoOut, want)
		}
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "repos", "sample-service", ".git")); err != nil {
		t.Fatalf("repo was not cloned: %v", err)
	}
	reposListOut := runMyDir(t, bin, home, umbrellaRoot, "repos", "list", "--home", home, "--json")
	if !strings.Contains(reposListOut, `"id": "sample-service"`) || !strings.Contains(reposListOut, `"cloned": true`) {
		t.Fatalf("repos list output = %q", reposListOut)
	}
	state, err := os.ReadFile(filepath.Join(umbrellaRoot, ".my-cli", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"sample-service"`, `"repo:sample-service"`} {
		if !strings.Contains(string(state), want) {
			t.Fatalf("state = %s, missing %q", state, want)
		}
	}
	runMyDir(t, bin, home, umbrellaRoot, "repos", "add", "sample-service", "--home", home)
	runMyDir(t, bin, home, umbrellaRoot, "mounts", "sync", "repo:sample-service", "--home", home)
}

func runMy(t *testing.T, bin, home string, args ...string) string {
	return runMyDir(t, bin, home, "", args...)
}

func runMyDir(t *testing.T, bin, home, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+isolatedPathWithGit(t, home))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("my %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.name=Example Test", "-c", "user.email=my-test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "seed workspace")
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
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve e2e test source path")
	}
	dir := filepath.Dir(source)
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && strings.Contains(string(data), "module github.com/fluxinc/my-cli") {
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
