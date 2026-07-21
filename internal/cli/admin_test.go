package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestGovernedAdminAuthoringRequiresLiveAdminPermission(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, `,
  "governance": {
    "authorization": {
      "provider": "github",
      "manifest_repository": "example/control",
      "admin_permission": "admin"
    }
  }`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, accessRunner: governedAdminRunner("write")}
	err := a.run([]string{"my", "admin", "contract", "add", "Require approval.", "-manifest-dir", manifestDir})
	if err == nil || !strings.Contains(err.Error(), "governed manifest authoring denied") || !strings.Contains(err.Error(), "admin permission is required") {
		t.Fatalf("err = %v, want governed admin denial", err)
	}
	doc, _, loadErr := manifest.LoadDocument(manifestDir)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(doc.Contract) != 0 {
		t.Fatalf("contract changed after denied authoring: %#v", doc.Contract)
	}

	stdout.Reset()
	stderr.Reset()
	a.accessRunner = governedAdminRunner("admin")
	if err := a.run([]string{"my", "admin", "contract", "add", "Require approval.", "--manifest-dir=" + manifestDir}); err != nil {
		t.Fatal(err)
	}
	doc, _, loadErr = manifest.LoadDocument(manifestDir)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(doc.Contract) != 1 || doc.Contract[0] != "Require approval." {
		t.Fatalf("contract = %#v", doc.Contract)
	}
}

func TestGovernedAdminAuthoringFailsClosedOnSSOError(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, `,
  "governance": {
    "authorization": {
      "provider": "github",
      "manifest_repository": "example/control",
      "admin_permission": "admin"
    }
  }`)
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: func(name string, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "api user" {
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		}
		return []byte("HTTP/2.0 403 Status\nX-GitHub-SSO: required\n\n{\"message\":\"Forbidden\"}"), errors.New("exit 1")
	}}
	err := a.run([]string{"my", "admin", "contract", "add", "Require approval.", "--manifest-dir", manifestDir})
	if err == nil || !strings.Contains(err.Error(), "sso") {
		t.Fatalf("err = %v, want fail-closed SSO denial", err)
	}
}

func TestGovernedAdminAuthoringUsesParserSelectedDuplicateManifestDir(t *testing.T) {
	ungovernedDir := t.TempDir()
	writeAdminManifest(t, ungovernedDir, "")
	governedDir := t.TempDir()
	writeAdminManifest(t, governedDir, `,
  "governance": {
    "authorization": {
      "provider": "github",
      "manifest_repository": "example/control",
      "admin_permission": "admin"
    }
  }`)
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminRunner("write")}
	err := a.run([]string{
		"my", "admin", "contract", "add", "Bypass attempt.",
		"--manifest-dir", ungovernedDir,
		"--manifest-dir", governedDir,
	})
	if err == nil || !strings.Contains(err.Error(), "governed manifest authoring denied") {
		t.Fatalf("err = %v, want authorization against parser-selected governed directory", err)
	}
	doc, _, loadErr := manifest.LoadDocument(governedDir)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(doc.Contract) != 0 {
		t.Fatalf("governed manifest changed through duplicate flag bypass: %#v", doc.Contract)
	}
}

func TestEveryGovernedManifestAuthoringGroupUsesParsedDirectoryGuardOnce(t *testing.T) {
	for _, dash := range []string{"--", "-"} {
		t.Run(dash, func(t *testing.T) {
			tests := []struct {
				name string
				args func(t *testing.T) []string
			}{
				{"contract", func(t *testing.T) []string {
					return []string{"my", "admin", "contract", "add", "Require approval."}
				}},
				{"policy", func(t *testing.T) []string {
					return []string{
						"my", "admin", "policy", "add", "handling-policy",
						"--title", "Handling policy", "--mount", "workspace", "--path", "policy/handling.md",
						"--version", "2026-07", "--acceptance", "required", "--sha256", "sha256:" + strings.Repeat("a", 64),
					}
				}},
				{"tools", func(t *testing.T) []string {
					return []string{"my", "admin", "tools", "add", "sample-tool", "--mode", "optional", "--purpose", "Test tool"}
				}},
				{"roles", func(t *testing.T) []string {
					return []string{"my", "admin", "roles", "add", "operator", "--purpose", "Test role"}
				}},
				{"services", func(t *testing.T) []string {
					return []string{"my", "admin", "services", "add", "status-api", "--kind", "http", "--purpose", "Test service", "--auth-ref", "none", "--describe-ref", "https://example.invalid/openapi.json"}
				}},
				{"skills", func(t *testing.T) []string {
					source := makeCLISkill(t, "demo-skill")
					return []string{"my", "admin", "skills", "add", filepath.Join(source, "demo-skill"), "--id", "acme:demo-skill", "--keep-original"}
				}},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					manifestDir := t.TempDir()
					writeAdminManifest(t, manifestDir, `,
  "governance": {
    "authorization": {
      "provider": "github",
      "manifest_repository": "example/control",
      "admin_permission": "admin"
    }
  }`)
					manifestPath := filepath.Join(manifestDir, "manifest.json")
					before, err := os.ReadFile(manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					calls := 0
					baseRunner := governedAdminRunner("write")
					a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: func(name string, args ...string) ([]byte, error) {
						calls++
						return baseRunner(name, args...)
					}}
					args := append(tt.args(t), dash+"manifest-dir", manifestDir, "--force")
					err = a.run(args)
					if err == nil || !strings.Contains(err.Error(), "governed manifest authoring denied") {
						t.Fatalf("err = %v, want denied manifest authoring", err)
					}
					if calls != 2 {
						t.Fatalf("provider calls = %d, want one actor and one repository check", calls)
					}
					after, err := os.ReadFile(manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(before, after) {
						t.Fatal("denied authoring changed manifest bytes")
					}
				})
			}
		})
	}
}

func TestGovernedPublishRequiresAdminBeforeManifestControlMutation(t *testing.T) {
	home, _, manifestDir, _, _ := setupCLITrackedManifestBody(t, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "governance": {
    "authorization": {
      "provider": "github",
      "manifest_repository": "example/control",
      "admin_permission": "admin"
    }
  }
}`)
	manifestPath := filepath.Join(manifestDir, "manifest.json")
	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	a := app{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		accessRunner: governedAdminRunner("write"),
		publishRunner: func(name string, args ...string) ([]byte, error) {
			t.Fatal("publish runner called before admin authorization")
			return nil, nil
		},
	}
	err = a.run([]string{"my", "publish", "--manifest", "acme", "--home", home})
	if err == nil || !strings.Contains(err.Error(), "governed manifest authoring denied") {
		t.Fatalf("err = %v, want governed publish denial", err)
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("denied publish changed manifest bytes")
	}
}

func governedAdminRunner(permission string) func(string, ...string) ([]byte, error) {
	return func(name string, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "api user":
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		case "api -i repos/example/control":
			admin := permission == "admin"
			push := admin || permission == "write"
			body := fmt.Sprintf(`{"id":29,"node_id":"R_repo","full_name":"example/control","private":true,"permissions":{"admin":%t,"push":%t,"pull":true}}`, admin, push)
			return []byte("HTTP/2.0 200 Status\n\n" + body), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, strings.Join(args, " "))
		}
	}
}

func TestAdminSkillsAddCopiesAndDeclares(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")
	sourceRoot := makeCLISkill(t, "demo-skill")
	skillDir := filepath.Join(sourceRoot, "demo-skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{
		"my", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "added\tacme:demo-skill\tdemo-skill") {
		t.Fatalf("admin add stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(manifestDir, "skills", "demo-skill", "SKILL.md")); err != nil {
		t.Fatalf("skill was not copied into manifest: %v", err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Skills) != 1 || doc.Skills[0].ID != "acme:demo-skill" || doc.Skills[0].Path != "skills/demo-skill" {
		t.Fatalf("manifest skills = %#v", doc.Skills)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("original skill should remain by default: %v", err)
	}
}

func TestAdminSkillsAddHarnessVisibleSourceRequiresChoice(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")
	skillDir := filepath.Join(t.TempDir(), ".claude", "skills", "demo-skill")
	writeCLITestFile(t, filepath.Join(skillDir, "SKILL.md"), "---\nname: demo-skill\n---\n")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"my", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "--keep-original") {
		t.Fatalf("admin add err = %v, want explicit keep/remove-original choice", err)
	}

	if err := a.run([]string{
		"my", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
		"--remove-original",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("source skill still exists after --remove-original, err=%v", err)
	}
}

func TestAdminSkillsRemoveBlocksThenPrunesRelatedProducts(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, `,
  "skills": [
    { "id": "acme:demo-skill", "install_slug": "demo-skill", "path": "skills/demo-skill" }
  ]`)
	writeCLITestFile(t, filepath.Join(manifestDir, "skills", "demo-skill", "SKILL.md"), "---\nname: demo-skill\n---\n")
	writeCLITestFile(t, filepath.Join(manifestDir, "catalog", "products.json"), `[
  {
    "id": "demo-product",
    "name": "Demo Product",
    "description": "Demo",
    "related_skills": ["acme:demo-skill", "acme:other-skill"]
  }
]`)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"my", "admin", "skills", "remove", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "related_skills") {
		t.Fatalf("remove err = %v, want related_skills blocker", err)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{
		"my", "admin", "skills", "remove", "demo-skill",
		"--manifest-dir", manifestDir,
		"--prune-related",
		"--delete-source",
	}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Skills) != 0 {
		t.Fatalf("manifest skills after remove = %#v", doc.Skills)
	}
	if _, err := os.Stat(filepath.Join(manifestDir, "skills", "demo-skill")); !os.IsNotExist(err) {
		t.Fatalf("skill source still exists after --delete-source, err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(manifestDir, "catalog", "products.json"))
	if err != nil {
		t.Fatal(err)
	}
	var products []manifest.Product
	if err := json.Unmarshal(data, &products); err != nil {
		t.Fatal(err)
	}
	if len(products) != 1 || strings.Join(products[0].RelatedSkills, ",") != "acme:other-skill" {
		t.Fatalf("products after prune = %#v", products)
	}
}

func TestAdminSkillsRemoveReportsAndPrunesOrphanDependencies(t *testing.T) {
	setup := func(t *testing.T) string {
		t.Helper()
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, `,
  "allowed_external_namespaces": ["spark"],
  "skills": [
    { "id": "acme:handbook", "install_slug": "acme-handbook", "path": "skills/acme-handbook" },
    {
      "id": "spark:use-spark",
      "install_slug": "use-spark",
      "source": { "type": "tool", "tool": "spark" },
      "requires": ["tool:spark"]
    }
  ],
  "tools": [
    {
      "id": "qmd",
      "mode": "optional",
      "purpose": "Search local markdown",
      "install": {
        "commands": ["npm install -g @tobilu/qmd"],
        "docs_url": "https://github.com/tobilu/qmd"
      }
    },
    {
      "id": "spark",
      "mode": "optional",
      "purpose": "Install Spark-provided skills",
      "skill_install": {
        "command": "spark",
        "args": ["skills", "install"]
      }
    }
  ]`)
		return manifestDir
	}

	t.Run("reports by default", func(t *testing.T) {
		manifestDir := setup(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"my", "admin", "skills", "remove", "spark:use-spark",
			"--manifest-dir", manifestDir,
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminSkillResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if strings.Join(result.OrphanedTools, ",") != "spark" || strings.Join(result.OrphanedNS, ",") != "spark" || result.SourcePath != "" {
			t.Fatalf("result = %#v", result)
		}
		doc, _, err := manifest.LoadDocument(manifestDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Tools) != 2 || !stringInSlice(doc.AllowedExternalNamespaces, "spark") {
			t.Fatalf("doc after default remove = %#v", doc)
		}
	})

	t.Run("prunes when requested", func(t *testing.T) {
		manifestDir := setup(t)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"my", "admin", "skills", "remove", "spark:use-spark",
			"--manifest-dir", manifestDir,
			"--prune-orphans",
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminSkillResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if strings.Join(result.PrunedTools, ",") != "spark" || strings.Join(result.PrunedNS, ",") != "spark" {
			t.Fatalf("result = %#v", result)
		}
		data, err := os.ReadFile(filepath.Join(manifestDir, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, unwanted := range []string{`"source":`, `"requires": null`, `"workspaces": null`, `"skill_install":`, `"spark"`} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("manifest contains %q after prune:\n%s", unwanted, text)
			}
		}
		if !strings.Contains(text, `"id": "qmd"`) {
			t.Fatalf("manifest lost remaining tool:\n%s", text)
		}
	})
}

func TestAdminSkillsDirtyCheckoutRequiresForce(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, "")
	initCLIGitRepo(t, manifestDir)
	writeCLITestFile(t, filepath.Join(manifestDir, "dirty.txt"), "dirty\n")
	sourceRoot := makeCLISkill(t, "demo-skill")
	skillDir := filepath.Join(sourceRoot, "demo-skill")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{
		"my", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
	})
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("dirty add err = %v, want dirty checkout refusal", err)
	}

	if err := a.run([]string{
		"my", "admin", "skills", "add", skillDir,
		"--id", "acme:demo-skill",
		"--manifest-dir", manifestDir,
		"--force",
	}); err != nil {
		t.Fatalf("force add failed: %v", err)
	}
}

func TestAdminToolsAddEditRemove(t *testing.T) {
	t.Run("add and edit tool", func(t *testing.T) {
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, "")
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"my", "admin", "tools", "add", "gnit",
			"--manifest-dir", manifestDir,
			"--mode", "required",
			"--purpose", "Multi-repo workspace publishing",
			"--install-command", "curl -fsSL https://raw.githubusercontent.com/mostlydev/gnit/master/install.sh | sh",
			"--docs-url", "https://github.com/mostlydev/gnit",
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminToolResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "added" || result.Tool.ID != "gnit" || result.Tool.Mode != "required" {
			t.Fatalf("result = %#v", result)
		}

		stdout.Reset()
		if err := a.run([]string{
			"my", "admin", "tools", "edit", "gnit",
			"--manifest-dir", manifestDir,
			"--purpose", "Gnit workspace publishing",
			"--clear-install-commands",
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		result = adminToolResult{}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "edited" || result.Tool.Purpose != "Gnit workspace publishing" || len(result.Tool.Install.Commands) != 0 {
			t.Fatalf("edited result = %#v", result)
		}
	})

	t.Run("remove blocks referenced tool", func(t *testing.T) {
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, `,
  "skills": [
    {
      "id": "acme:publisher",
      "install_slug": "publisher",
      "path": "skills/publisher",
      "requires": ["tool:gnit"]
    }
  ],
  "tools": [
    {
      "id": "gnit",
      "mode": "required",
      "purpose": "Multi-repo workspace publishing"
    }
  ]`)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{
			"my", "admin", "tools", "remove", "gnit",
			"--manifest-dir", manifestDir,
		})
		if err == nil || !strings.Contains(err.Error(), "referenced by skills") {
			t.Fatalf("remove err = %v, want referenced blocker", err)
		}
	})

	t.Run("remove unreferenced tool", func(t *testing.T) {
		manifestDir := t.TempDir()
		writeAdminManifest(t, manifestDir, `,
  "tools": [
    {
      "id": "gnit",
      "mode": "required",
      "purpose": "Multi-repo workspace publishing"
    }
  ]`)
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{
			"my", "admin", "tools", "remove", "gnit",
			"--manifest-dir", manifestDir,
			"--json",
		}); err != nil {
			t.Fatal(err)
		}
		var result adminToolResult
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Action != "removed" || result.Tool.ID != "gnit" {
			t.Fatalf("result = %#v", result)
		}
		doc, _, err := manifest.LoadDocument(manifestDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(doc.Tools) != 0 {
			t.Fatalf("tools after remove = %#v", doc.Tools)
		}
	})
}

func TestAdminCustomersRemoved(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	err := a.run([]string{"my", "admin", "customers", "add", "sampleco.example.com", "--manifest-dir", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), `unknown admin subcommand "customers"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestAdminRoutingDelegatesToTopLevelHandlers(t *testing.T) {
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "umbrella": { "recommended_path": "~/acme" },
  "mounts": [
    {
      "id": "leadership",
      "kind": "meetings",
      "git_url": "https://github.com/acme/leadership.git",
      "mode": "optional"
    }
  ],
  "workspaces": [
    {
      "id": "handbook",
      "git_url": "https://github.com/acme/acme-handbook.git",
      "local_path": "~/.my-cli/workspaces/handbook"
    }
  ]
}`)
	writeCLITestFile(t, filepath.Join(home, ".my-cli", "workspaces", "handbook", "meetings", ".keep"), "")

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	registerCLIManifest(t, a, home)

	t.Run("manifest add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{
			"my", "admin", "manifests", "add", "extra",
			"https://github.com/acme/extra-manifest.git",
			"--home", home,
		}); err != nil {
			t.Fatalf("my admin manifests add err = %v", err)
		}
		stdout.Reset()
		if err := a.run([]string{"my", "manifests", "list", "--home", home}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "extra-manifest") {
			t.Fatalf("manifest list stdout = %q", stdout.String())
		}
	})

	t.Run("mount add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{"my", "admin", "mounts", "add", "meetings:leadership", "--manifest", "acme", "--home", home, "--print"}); err != nil {
			t.Fatalf("my admin mounts add err = %v", err)
		}
		if !strings.Contains(stdout.String(), "leadership\tdry-run") {
			t.Fatalf("mount add stdout = %q", stdout.String())
		}
	})

	t.Run("meetings add alias", func(t *testing.T) {
		stdout.Reset()
		if err := a.run([]string{
			"my", "admin", "meetings", "add", "sampleco-followup",
			"--manifest", "acme",
			"--workspace", "handbook",
			"--home", home,
			"--date", "2026-05-13",
			"--customer", "sampleco",
			"--print",
		}); err != nil {
			t.Fatalf("my admin meetings add err = %v", err)
		}
		if !strings.Contains(stdout.String(), "2026-05-13-sampleco-followup") {
			t.Fatalf("meetings add stdout = %q", stdout.String())
		}
	})

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"my", "admin", "skills", "list"}, "use my skills list"},
		{[]string{"my", "admin", "manifests", "list"}, "use my manifests list"},
		{[]string{"my", "admin", "mounts", "list"}, "use my mounts list"},
		{[]string{"my", "admin", "meetings", "search", "cleanup"}, "use my meetings search"},
	} {
		t.Run(strings.Join(tc.args[2:], " "), func(t *testing.T) {
			err := a.run(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"my", "admin", "manifests"}, "missing admin manifests subcommand"},
		{[]string{"my", "admin", "mounts"}, "missing admin mounts subcommand"},
		{[]string{"my", "admin", "meetings"}, "missing admin meetings subcommand"},
	} {
		t.Run(strings.Join(tc.args[2:], " "), func(t *testing.T) {
			err := a.run(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s err = %v, want %q", strings.Join(tc.args, " "), err, tc.want)
			}
		})
	}

	t.Run("onboard", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{"my", "admin", "setup", "--home", t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "manifest") {
			t.Fatalf("my admin setup err = %v, want a manifest-related error", err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		err := a.run([]string{"my", "admin", "bogus"})
		if err == nil || !strings.Contains(err.Error(), "unknown admin subcommand") {
			t.Fatalf("my admin bogus err = %v, want unknown admin subcommand", err)
		}
	})

	t.Run("help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr}
		if err := a.run([]string{"my", "admin", "--help"}); err != nil {
			t.Fatalf("my admin --help err = %v", err)
		}
		for _, want := range []string{"my admin setup", "my admin manifests", "my admin mounts", "my admin meetings"} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("my admin --help missing %q in:\n%s", want, stdout.String())
			}
		}
	})
}
