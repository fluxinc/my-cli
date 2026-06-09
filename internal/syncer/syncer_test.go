package syncer

import (
	"errors"
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

func TestRunGnitDryRunPlansApprovedContentThroughGnit(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	gnitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		DryRun:     true,
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	if report.Backend != "gnit" {
		t.Fatalf("backend = %q, want gnit", report.Backend)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "dry-run" || result.Direction != "outbound" {
		t.Fatalf("result = %#v, want outbound dry-run", result)
	}
	for _, want := range []string{"gnit add", "gnit commit", "gnit push"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("message = %q, missing %q", result.Message, want)
		}
	}
}

func TestRunGnitPublishesWithWorkspaceRelativePathsAndCommit(t *testing.T) {
	remote, gnitRoot, content, _ := setupGnitWorkspaceWithDuplicateRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	var calls []string

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
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
	if !strings.Contains(calls[0], " gnit add handbook/meetings") || strings.Contains(calls[0], content) {
		t.Fatalf("add call = %q, want workspace-relative path", calls[0])
	}
	if !strings.Contains(calls[1], " gnit commit -m Add meeting note") {
		t.Fatalf("commit call = %q, want gnit commit", calls[1])
	}
	if !strings.Contains(calls[2], " gnit push") {
		t.Fatalf("push call = %q, want gnit push", calls[2])
	}
}

func TestRunGnitDirectHoldsDirtyNonContent(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	gnitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(content, "catalog", "customers.json"), "[]\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:  "gnit",
		GnitRoot: gnitRoot,
		Publish:  "direct",
		DryRun:   true,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" || !strings.Contains(result.Message, "dirty changes are outside declared content paths") {
		t.Fatalf("result = %#v, want dirty non-content hold", result)
	}
}

func TestRunGnitHoldsWhenWorkspaceIsNotInitialized(t *testing.T) {
	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: "https://github.com/acme/handbook.git", LocalPath: "/tmp/handbook"},
	}, Options{Backend: "gnit", GnitRoot: t.TempDir(), Publish: "auto"})

	if report.Backend != "gnit" || !strings.Contains(report.BackendMessage, "Gnit workspace not initialized") {
		t.Fatalf("report = %#v, want Gnit initialization hold", report)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "held back" || !strings.Contains(result.Message, "Gnit workspace not initialized") {
		t.Fatalf("result = %#v, want held back", result)
	}
}

func TestRunGnitAllowsCanonicalContentWithCleanDuplicateSibling(t *testing.T) {
	remote, gnitRoot, content, manifest := setupGnitWorkspaceWithDuplicateRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		DryRun:     true,
		Visibility: privateVisibility,
	})

	if report.BackendMessage != "" {
		t.Fatalf("backend message = %q, want clean duplicate to be tolerated", report.BackendMessage)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "dry-run" || result.Direction != "outbound" || !strings.Contains(result.Message, "gnit commit") {
		t.Fatalf("result = %#v, want canonical outbound dry-run", result)
	}
	sibling := findResult(t, report, "manifest")
	if sibling.Status != "already landed" {
		t.Fatalf("sibling = %#v, want already landed", sibling)
	}
}

func TestRunGnitHoldsWhenDuplicateSiblingHasPendingChanges(t *testing.T) {
	remote, gnitRoot, content, manifest := setupGnitWorkspaceWithDuplicateRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	writeFile(t, filepath.Join(manifest, "catalog", "customers.json"), "[]\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		DryRun:     true,
		Visibility: privateVisibility,
	})

	if !strings.Contains(report.BackendMessage, "unsafe duplicate checkouts") {
		t.Fatalf("backend message = %q, want unsafe duplicate warning", report.BackendMessage)
	}
	for _, id := range []string{"handbook", "manifest"} {
		result := findResult(t, report, id)
		if result.Status != "held back" || !strings.Contains(result.Message, "same remote sibling has pending changes") {
			t.Fatalf("%s result = %#v, want unsafe duplicate hold", id, result)
		}
	}
}

func TestRunGnitPullsCleanBehindRepo(t *testing.T) {
	remote, content, writer := setupTwoCheckoutRemote(t)
	gnitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(writer, "meetings", "2026-06-09-remote.md"), "remote\n")
	runGit(t, writer, "add", ".")
	runGit(t, writer, "commit", "-q", "-m", "remote meeting")
	runGit(t, writer, "push", "-q", "origin", "HEAD:master")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:  "gnit",
		GnitRoot: gnitRoot,
		Publish:  "auto",
	})

	result := findResult(t, report, "handbook")
	if result.Status != "pulled" || result.Direction != "inbound" {
		t.Fatalf("result = %#v, want inbound pull", result)
	}
	if got := gitOut(t, content, "rev-parse", "HEAD"); got != gitOut(t, writer, "rev-parse", "HEAD") {
		t.Fatalf("content did not fast-forward: content %s writer %s", got, gitOut(t, writer, "rev-parse", "HEAD"))
	}
}

func TestInspectNoFetchUsesLocalTrackingRefs(t *testing.T) {
	remote, content, writer := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(writer, "meetings", "2026-06-09-remote.md"), "remote\n")
	runGit(t, writer, "add", ".")
	runGit(t, writer, "commit", "-q", "-m", "remote meeting")
	runGit(t, writer, "push", "-q", "origin", "HEAD:master")

	entries := []Entry{{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content}}
	local := Inspect(entries, InspectOptions{Fetch: false})
	localResult := local[0]
	if localResult.Status != "already landed" || localResult.Behind != 0 {
		t.Fatalf("local result = %#v, want stale local tracking ref to look landed", localResult)
	}

	fetched := Inspect(entries, InspectOptions{Fetch: true})
	fetchedResult := fetched[0]
	if fetchedResult.Status != "pending" || fetchedResult.Behind != 1 {
		t.Fatalf("fetched result = %#v, want behind=1 pending", fetchedResult)
	}
}

func TestInspectReportsUnknownWhenFetchFails(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	results := Inspect([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content},
	}, InspectOptions{
		Fetch:  true,
		Runner: fetchFailingRunner,
	})

	result := results[0]
	if result.Status != "unknown" || !result.BehindUnknown {
		t.Fatalf("result = %#v, want unknown with behind_unknown", result)
	}
	if !strings.Contains(result.FetchError, "offline") || result.Error != "" {
		t.Fatalf("result = %#v, want fetch_error only", result)
	}
	if result.Branch == "" || result.Head == "" {
		t.Fatalf("result = %#v, want branch and head preserved", result)
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

func fetchFailingRunner(name string, args ...string) ([]byte, error) {
	if name == "git" && len(args) >= 3 && args[2] == "fetch" {
		return []byte("offline\n"), errors.New("offline")
	}
	return exec.Command(name, args...).CombinedOutput()
}

func setupGnitWorkspaceWithDuplicateRemote(t *testing.T) (string, string, string, string) {
	t.Helper()
	remote, content, manifest := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	gnitRoot := filepath.Join(root, "umbrella")
	gnitContent := filepath.Join(gnitRoot, "handbook")
	if err := os.MkdirAll(gnitRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(content, gnitContent); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: handbook\n")
	return remote, gnitRoot, gnitContent, manifest
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
	runGit(t, dir, "config", "user.name", "Our AI Test")
	runGit(t, dir, "config", "user.email", "our-test@example.com")
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
