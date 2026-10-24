package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestContractMigrationRejectsDestinationCollisions(t *testing.T) {
	for _, kind := range []string{"directory", "file-symlink", "parent-symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			writeAdminManifest(t, dir, `, "contract": ["Keep this rule."]`)
			before, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
			destination := filepath.Join(dir, "guidance", "contract.md")
			if kind == "parent-symlink" {
				if err := os.Symlink(outside, filepath.Join(dir, "guidance")); err != nil {
					t.Skip(err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.Mkdir(destination, 0755); err != nil {
						t.Fatal(err)
					}
				} else {
					writeCLITestFile(t, filepath.Join(outside, "target.md"), "- External rule.\n")
					if err := os.Symlink(filepath.Join(outside, "target.md"), destination); err != nil {
						t.Skip(err)
					}
				}
			}
			a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
			if _, err := runAdminContractJSON(t, a, "migrate", "--manifest-dir", dir, "--force"); err == nil {
				t.Fatal("migration accepted a collision")
			}
			after, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("refused migration changed manifest")
			}
			if _, err := os.Stat(filepath.Join(outside, "contract.md")); !os.IsNotExist(err) {
				t.Fatalf("migration wrote outside checkout: %v", err)
			}
			if kind == "file-symlink" {
				got, _ := os.ReadFile(filepath.Join(outside, "target.md"))
				if string(got) != "- External rule.\n" {
					t.Fatal("migration changed symlink target")
				}
			}
		})
	}
}

func TestContractMigrationRollsBackAfterManifestWriteFailure(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `, "contract": ["Keep this rule."]`)
	doc, manifestPath, err := manifest.LoadDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(manifestPath)
	edit, err := planContractEdit("migrate", nil, adminContractOpts{}, dir, manifestPath, doc)
	if err != nil {
		t.Fatal(err)
	}
	// Preserve identical readable bytes but make the destination non-regular.
	// This deterministically fails the final manifest replacement on all hosts.
	saved := filepath.Join(dir, "saved-manifest.json")
	if err := os.Rename(manifestPath, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(saved, manifestPath); err != nil {
		t.Skip(err)
	}
	if err := writeContractEdit(dir, edit); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("write error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "guidance", "contract.md")); !os.IsNotExist(err) {
		t.Fatalf("new contract left after failed migration: %v", err)
	}
	after, _ := os.ReadFile(manifestPath)
	if !bytes.Equal(before, after) {
		t.Fatal("failed migration changed inline contract")
	}
}

func TestContractNewAuthoringVerbsRequireProviderAdmin(t *testing.T) {
	for _, args := range [][]string{{"migrate"}, {"replace", "1", "Changed rule."}, {"add", "New rule."}, {"remove", "1"}} {
		t.Run(args[0], func(t *testing.T) {
			dir := t.TempDir()
			writeCLITestFile(t, filepath.Join(dir, "manifest.json"), governedContractBody)
			before, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
			a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminRunner("write")}
			command := append(append([]string{}, args...), "--manifest-dir="+dir, "--force")
			if _, err := runAdminContractJSON(t, a, command...); err == nil || !strings.Contains(err.Error(), "governed manifest authoring denied") {
				t.Fatalf("authorization error = %v", err)
			}
			after, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("denied authoring changed manifest")
			}
			if _, err := os.Stat(filepath.Join(dir, "guidance", "contract.md")); !os.IsNotExist(err) {
				t.Fatalf("denied migration created file: %v", err)
			}
		})
	}
}

func TestContractLocalEditRefusesChangedCommitWithSameBytes(t *testing.T) {
	_, _, cache, _, _ := setupCLITrackedManifestBody(t, `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":["Original rule."]}`)
	doc, manifestPath, err := manifest.LoadDocument(cache)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(manifestPath)
	edit, err := planContractEdit("replace", []string{"1", "Changed rule."}, adminContractOpts{}, cache, manifestPath, doc)
	if err != nil {
		t.Fatal(err)
	}
	edit.result.BaseCommit = strings.TrimSpace(gitCLIOutput(t, cache, "rev-parse", "HEAD"))
	runCLIGit(t, cache, "commit", "--allow-empty", "-m", "Move base without changing contract")
	if err := writeContractEdit(cache, edit); err == nil {
		t.Fatal("edit accepted a changed base commit")
	}
	after, _ := os.ReadFile(manifestPath)
	if !bytes.Equal(before, after) {
		t.Fatal("stale edit overwrote manifest")
	}
}

func TestContractMigrationOutsideGitHasNoBaseCommit(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `, "contract": ["Keep this rule."]`)
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	result, err := runAdminContractJSON(t, a, "migrate", "--manifest-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if result.BaseCommit != "" {
		t.Fatalf("non-Git base commit = %q", result.BaseCommit)
	}
}

func TestCompileContractOutsideGitHasNoDiagnosticRevision(t *testing.T) {
	home, _, cache, _, _ := setupCLITrackedManifestBody(t, `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract_file":"guidance/contract.md"}`)
	writeCLITestFile(t, filepath.Join(cache, "guidance", "contract.md"), "- Keep this rule.\n")
	if err := os.Rename(filepath.Join(cache, ".git"), filepath.Join(t.TempDir(), "saved-git")); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}}
	if err := a.runCompile([]string{"--home", home, "--manifest", "acme"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "manifest_commit") || strings.Contains(out.String(), "fatal:") {
		t.Fatalf("compile serialized Git diagnostic as revision: %s", out)
	}
	if !strings.Contains(out.String(), "Keep this rule.") {
		t.Fatal("compile lost contract")
	}
}
