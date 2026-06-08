package syncer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAutoPushesPrivateContentAndPullsCleanSibling(t *testing.T) {
	remote, content, manifest := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	contentResult := findResult(t, report, "handbook")
	if contentResult.Status != "pushed" {
		t.Fatalf("content status = %q, want pushed; report = %#v", contentResult.Status, report)
	}
	manifestResult := findResult(t, report, "manifest")
	if manifestResult.Status != "pulled" {
		t.Fatalf("manifest status = %q, want pulled; report = %#v", manifestResult.Status, report)
	}
	if got := gitOut(t, manifest, "rev-parse", "HEAD"); got != gitOut(t, content, "rev-parse", "HEAD") {
		t.Fatalf("sibling checkout did not fast-forward: manifest %s content %s", got, gitOut(t, content, "rev-parse", "HEAD"))
	}
	if _, err := os.Stat(filepath.Join(manifest, "meetings", "2026-06-08-sync.md")); err != nil {
		t.Fatalf("manifest checkout missed pushed content: %v", err)
	}
}

func TestRunHoldsDuplicateRemoteWhenBothCheckoutsHavePendingChanges(t *testing.T) {
	remote, content, manifest := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	writeFile(t, filepath.Join(manifest, "catalog", "customers.json"), "[]\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	for _, id := range []string{"handbook", "manifest"} {
		result := findResult(t, report, id)
		if result.Status != "held back" || !strings.Contains(result.Message, "another checkout of the same remote") {
			t.Fatalf("%s result = %#v, want duplicate-remote hold", id, result)
		}
	}
	if log := gitOut(t, content, "log", "--oneline", "--all"); strings.Contains(log, "Add meeting note") {
		t.Fatalf("content change was committed despite duplicate hold:\n%s", log)
	}
}

func TestRunNitDryRunPlansApprovedContentThroughNit(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	nitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(nitRoot, ".nit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "nit",
		NitRoot:    nitRoot,
		Publish:    "auto",
		DryRun:     true,
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	if report.Backend != "nit" {
		t.Fatalf("backend = %q, want nit", report.Backend)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "dry-run" || result.Direction != "outbound" {
		t.Fatalf("result = %#v, want outbound dry-run", result)
	}
	for _, want := range []string{"nit add", "nit commit", "nit push"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("message = %q, missing %q", result.Message, want)
		}
	}
}

func TestRunNitPublishesWithWorkspaceRelativePathsAndCommit(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	nitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(nitRoot, ".nit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	var calls []string

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "nit",
		NitRoot:    nitRoot,
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
		DirRunner: func(dir, name string, args ...string) ([]byte, error) {
			calls = append(calls, dir+" "+name+" "+strings.Join(args, " "))
			return []byte("ok\n"), nil
		},
	})

	result := findResult(t, report, "handbook")
	if result.Status != "pushed" {
		t.Fatalf("result = %#v, want pushed", result)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %#v, want add/commit/push", calls)
	}
	if !strings.Contains(calls[0], " nit add content/meetings") || strings.Contains(calls[0], content) {
		t.Fatalf("add call = %q, want workspace-relative path", calls[0])
	}
	if !strings.Contains(calls[1], " nit commit -m Add meeting note") {
		t.Fatalf("commit call = %q, want nit commit", calls[1])
	}
	if !strings.Contains(calls[2], " nit push") {
		t.Fatalf("push call = %q, want nit push", calls[2])
	}
}

func TestRunNitDirectHoldsDirtyNonContent(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	nitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(nitRoot, ".nit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(content, "catalog", "customers.json"), "[]\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend: "nit",
		NitRoot: nitRoot,
		Publish: "direct",
		DryRun:  true,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" || !strings.Contains(result.Message, "dirty changes are outside declared content paths") {
		t.Fatalf("result = %#v, want dirty non-content hold", result)
	}
}

func TestRunNitHoldsWhenWorkspaceIsNotInitialized(t *testing.T) {
	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: "https://github.com/acme/handbook.git", LocalPath: "/tmp/handbook"},
	}, Options{Backend: "nit", NitRoot: t.TempDir(), Publish: "auto"})

	if report.Backend != "nit" || !strings.Contains(report.BackendMessage, "Nit workspace not initialized") {
		t.Fatalf("report = %#v, want Nit initialization hold", report)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "held back" || !strings.Contains(result.Message, "Nit workspace not initialized") {
		t.Fatalf("result = %#v, want held back", result)
	}
}

func TestRunNitHoldsDuplicateRemoteBeforeDelegation(t *testing.T) {
	remote, content, manifest := setupTwoCheckoutRemote(t)
	nitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(nitRoot, ".nit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Backend:    "nit",
		NitRoot:    nitRoot,
		Publish:    "auto",
		DryRun:     true,
		Visibility: privateVisibility,
	})

	if !strings.Contains(report.BackendMessage, "canonicalize duplicate checkouts") {
		t.Fatalf("backend message = %q, want duplicate canonicalization hold", report.BackendMessage)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "held back" || !strings.Contains(result.Message, "collapse to one canonical checkout") {
		t.Fatalf("result = %#v, want duplicate-remote hold", result)
	}
}

func setupTwoCheckoutRemote(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	remote := filepath.Join(root, "remote.git")
	content := filepath.Join(root, "content")
	manifest := filepath.Join(root, "manifest")
	if err := os.MkdirAll(filepath.Join(seed, "meetings"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(seed, "README.md"), "seed\n")
	runGit(t, seed, "init", "-q")
	configGitUser(t, seed)
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-q", "-m", "seed")
	runGit(t, root, "init", "--bare", "-q", remote)
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-q", "origin", "HEAD:master")
	runGit(t, root, "clone", "-q", remote, content)
	runGit(t, root, "clone", "-q", remote, manifest)
	configGitUser(t, content)
	configGitUser(t, manifest)
	return remote, content, manifest
}

func privateVisibility(string) (string, error) {
	return "PRIVATE", nil
}

func findResult(t *testing.T, report Report, id string) Result {
	t.Helper()
	for _, result := range report.Results {
		if result.ID == id {
			return result
		}
	}
	t.Fatalf("missing result %q in %#v", id, report)
	return Result{}
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

func configGitUser(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "config", "user.name", "Flux Test")
	runGit(t, dir, "config", "user.email", "flux-test@example.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out := runGit(t, dir, args...)
	return strings.TrimSpace(out)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}
