package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/umbrella"
	"github.com/fluxinc/our-ai/internal/workspace"
)

func TestSyncContentPathsIncludesSupport(t *testing.T) {
	tests := []struct {
		name  string
		entry workspace.Entry
		want  []string
	}{
		{
			name:  "handbook default",
			entry: workspace.Entry{Kind: "handbook"},
			want:  []string{"meetings", "support", "decisions", "projects", "policy", "people"},
		},
		{
			name:  "support default",
			entry: workspace.Entry{Kind: "support"},
			want:  []string{"support"},
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

func TestSyncHoldsManifestWithLocalMountURL(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "init", "acme", "--home", home}); err != nil {
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
		"our", "sync",
		"--backend", "builtin",
		"--publish", "direct",
		"--scope", "manifest",
		"--manifest", "acme",
		"--home", home,
	}); err != nil {
		t.Fatalf("sync: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"manifest\theld back", "local mount URL", "run our publish --manifest acme"} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync stdout = %q, missing %q", out, want)
		}
	}
	remoteHead := strings.TrimSpace(gitCLIOutput(t, manifestRepo, "rev-parse", "origin/master"))
	if remoteHead == localHead {
		t.Fatalf("sync pushed manifest despite local mount URL guard")
	}
}

func TestSyncExplicitGnitBackendReportsMissingWorkspace(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "our", "manifests", "acme")
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
		"our", "manifests", "add", "acme",
		"https://github.com/acme/acme-ai-manifest.git",
		"--home", home,
	}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := a.run([]string{"our", "sync", "--backend", "gnit", "--manifest", "acme", "--home", home, "--print", "--json"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		`"backend": "gnit"`,
		"Gnit workspace not initialized",
		`"status": "held back"`,
		`"id": "handbook"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("sync stdout = %q, missing %q", out, want)
		}
	}
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
		"our", "sync", "--print", "--json",
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
		"our", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--manifest", "acme",
		"--home", home,
		"--umbrella", umbrellaRoot,
	}); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(umbrellaRoot, ".our", "last-sync.json")
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
	if err := a.run([]string{"our", "doctor", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
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
		"our", "sync",
		"--backend", "builtin",
		"--publish", "never",
		"--scope", "manifest",
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
		"our", "sync",
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
		"our", "sync",
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
	if err := a.run([]string{"our", "sync", "--backend", "builtin", "--scope", "repos", "--print", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatalf("sync --scope repos failed: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	err := a.run([]string{"our", "sync", "--backend", "builtin", "--scope", "products", "--print", "--manifest", "acme", "--home", home, "--json"})
	if err == nil || !strings.Contains(err.Error(), "--scope must be one of") {
		t.Fatalf("err = %v, want products scope rejected", err)
	}
}

func TestSyncUsesManifestPublishPolicyAndCLIOverride(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "sync": { "publish_policy": "never" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "sync", "--backend", "builtin", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "never"`) {
		t.Fatalf("sync stdout = %q, want manifest publish policy", stdout.String())
	}

	stdout.Reset()
	if err := a.run([]string{"our", "sync", "--backend", "builtin", "--publish", "direct", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "direct"`) {
		t.Fatalf("sync stdout = %q, want CLI override", stdout.String())
	}
}

func TestSyncUsesManifestPRPublishPolicy(t *testing.T) {
	home, _, _, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "sync": { "publish_policy": "pr" },
  "umbrella": { "recommended_path": "~/acme" }
}`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "sync", "--backend", "builtin", "--manifest", "acme", "--home", home, "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"publish": "pr"`) {
		t.Fatalf("sync stdout = %q, want manifest PR policy", stdout.String())
	}
}
