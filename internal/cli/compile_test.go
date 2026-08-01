package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileCommandPrintsLaunchProjection(t *testing.T) {
	home := t.TempDir()
	writeCompileManifest(t, home)

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

	if err := a.run([]string{"my", "compile", "--manifest", "acme", "--role", "operator", "--home", home}); err != nil {
		t.Fatalf("compile: %v\nstderr:\n%s", err, stderr.String())
	}
	var projection struct {
		Target   string `json:"target"`
		Role     string `json:"role"`
		Policies []struct {
			ID      string   `json:"id"`
			Summary string   `json:"summary"`
			Topics  []string `json:"topics"`
		} `json:"policies"`
		DataBindings map[string]struct {
			Surface string `json:"surface"`
		} `json:"data_bindings"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &projection); err != nil {
		t.Fatalf("compile JSON: %v\n%s", err, stdout.String())
	}
	if projection.Target != "clawdapus" || projection.Role != "operator" {
		t.Fatalf("projection target/role = %q/%q", projection.Target, projection.Role)
	}
	if got := projection.DataBindings["customers"].Surface; got != "mount:handbook" {
		t.Fatalf("customers surface = %q", got)
	}
	if len(projection.Policies) != 1 || projection.Policies[0].ID != "workspace-policy" ||
		projection.Policies[0].Summary != "Rules for shared workspace changes." ||
		len(projection.Policies[0].Topics) != 1 || projection.Policies[0].Topics[0] != "workspace changes" {
		t.Fatalf("compile policy projection = %#v", projection.Policies)
	}
}

func TestCompileCommandRequiresRoleWhenRolesDeclared(t *testing.T) {
	home := t.TempDir()
	writeCompileManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "manifests", "add", "acme", "https://github.com/acme/acme-ai-manifest.git", "--home", home}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()

	err := a.run([]string{"my", "compile", "--manifest", "acme", "--home", home})
	if err == nil || !strings.Contains(err.Error(), "role is required") {
		t.Fatalf("compile without role err = %v", err)
	}
}

func TestLaunchAliasRemoved(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "launch", "--help"})
	if err == nil || !strings.Contains(err.Error(), `unknown command "launch"`) {
		t.Fatalf("launch alias err = %v", err)
	}
}

func writeCompileManifest(t *testing.T, home string) {
	t.Helper()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "agent_guidance": { "paths": ["agent-guidance/acme.md"] },
  "mounts": [
    {
      "id": "handbook",
      "kind": "handbook",
      "git_url": "https://github.com/acme/handbook.git",
      "mode": "default",
      "include_paths": ["customers", "meetings"]
    }
  ],
  "data_bindings": {
    "customers": { "surface": "mount:handbook" }
  },
	"governance": {
		"authorization": {
			"provider": "github",
			"manifest_repository": "acme/control",
			"admin_permission": "admin"
		},
		"policies": [
			{
				"id": "workspace-policy",
				"title": "Workspace policy",
				"mount": "handbook",
				"path": "policy/workspace.md",
				"version": "1",
				"sha256": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"acceptance": "optional",
				"summary": "Rules for shared workspace changes.",
				"topics": ["workspace changes"],
				"roles": ["operator"]
			}
		]
	},
  "skills": [
    {
      "id": "acme:handbook",
      "install_slug": "acme-handbook",
      "path": "skills/acme-handbook",
      "requires": ["workspace:handbook"]
    }
  ],
  "roles": [
    {
      "id": "operator",
      "purpose": "Operate the workspace",
      "guidance_paths": ["agent-guidance/operator.md"],
      "mounts": ["handbook"],
      "skills": ["acme:handbook"]
    }
  ]
}`)
}
