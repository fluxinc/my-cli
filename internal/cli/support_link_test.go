package cli

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/recordsvc"
)

const supportLinkRecord = "2026-10-02-network"

// setupSupportLink is setupServiceFleet with the handbook workspace a real
// checkout whose canonical remote is a local bare repository, the support
// record published on its default branch.
func setupSupportLink(t *testing.T) (*fakeRegistry, string, app, *bytes.Buffer, string) {
	t.Helper()
	registry := newFakeRegistry()
	server := httptest.NewServer(registry)
	t.Cleanup(server.Close)
	home, a, stdout, _ := setupServiceFleet(t, server.URL)
	workspace := filepath.Join(home, ".my-cli", "workspaces", "handbook")
	remote := filepath.Join(t.TempDir(), "handbook.git")
	git(t, "", "init", "--quiet", "--bare", "--initial-branch=main", remote)
	git(t, workspace, "init", "--quiet", "--initial-branch=main")
	git(t, workspace, "add", ".")
	git(t, workspace, "commit", "--quiet", "-m", "Handbook")
	git(t, workspace, "remote", "add", "origin", remote)
	git(t, workspace, "push", "--quiet", "origin", "main")

	cached := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme", "manifest.json")
	data, err := os.ReadFile(cached)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("https://github.com/acme/acme-handbook.git"), []byte(remote), 1)
	writeCLITestFile(t, cached, string(data))
	return registry, home, a, stdout, workspace
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.test", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func linkSupport(a app, home string, extra ...string) error {
	args := append([]string{"my", "support", "link", supportLinkRecord, "--home", home, "--manifest", "acme"}, extra...)
	return a.run(args)
}

func TestSupportLinkNotesAPublishedRecordOnceOnEachNamedRecord(t *testing.T) {
	registry, home, a, stdout, _ := setupSupportLink(t)

	if err := linkSupport(a, home, "--record", "reg:box:1", "--record", "reg:box:2", "--record", "reg:box:1", "--json"); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Reference recordsvc.Reference `json:"reference"`
		Links     []supportLinkResult `json:"links"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	if out.Reference != (recordsvc.Reference{Namespace: "acme.handbook", System: "support", ID: supportLinkRecord}) {
		t.Fatalf("reference: %+v", out.Reference)
	}
	if len(out.Links) != 2 || !out.Links[0].Created || !out.Links[1].Created || registry.linkPosts != 2 {
		t.Fatalf("links %+v after %d posts; a repeated --record links once", out.Links, registry.linkPosts)
	}
	for key, body := range registry.links {
		if body != "Support record 2026-10-02-network" {
			t.Fatalf("%s: body %q carries more than the id", key, body)
		}
	}

	stdout.Reset()
	if err := linkSupport(a, home, "--record", "reg:box:1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "reg:box:1: already linked "+supportLinkRecord) || len(registry.links) != 2 {
		t.Fatalf("rerun: %s", stdout)
	}
	if registry.linkKeys[0] != registry.linkKeys[2] || registry.linkKeys[0] == registry.linkKeys[1] {
		t.Fatalf("keys must repeat per record and differ across records: %v", registry.linkKeys)
	}
}

func TestSupportLinkRetriesOnceWhenTheRecordMovedOn(t *testing.T) {
	registry, home, a, stdout, _ := setupSupportLink(t)
	registry.staleLinks = 1
	if err := linkSupport(a, home, "--record", "reg:box:1"); err != nil {
		t.Fatal(err)
	}
	if registry.linkPosts != 2 || len(registry.links) != 1 || !strings.Contains(stdout.String(), "reg:box:1: linked") {
		t.Fatalf("posts %d, links %v: %s", registry.linkPosts, registry.links, stdout)
	}
}

func TestSupportLinkRefusesUnpublishedRecordsWithoutWriting(t *testing.T) {
	path := filepath.Join("support", supportLinkRecord+".md")
	for name, unpublish := range map[string]func(t *testing.T, workspace string){
		"dirty": func(t *testing.T, workspace string) {
			appendFile(t, filepath.Join(workspace, path), "\nLocal edit.\n")
		},
		"committed, not pushed": func(t *testing.T, workspace string) {
			appendFile(t, filepath.Join(workspace, path), "\nLocal edit.\n")
			git(t, workspace, "commit", "--quiet", "-am", "Edit")
		},
		"pushed to a topic branch only": func(t *testing.T, workspace string) {
			git(t, workspace, "checkout", "--quiet", "-b", "topic")
			appendFile(t, filepath.Join(workspace, path), "\nLocal edit.\n")
			git(t, workspace, "commit", "--quiet", "-am", "Edit")
			git(t, workspace, "push", "--quiet", "-u", "origin", "topic")
		},
		"untracked": func(t *testing.T, workspace string) {
			git(t, workspace, "rm", "--quiet", "--cached", path)
			git(t, workspace, "commit", "--quiet", "-m", "Untrack")
			git(t, workspace, "push", "--quiet", "origin", "main")
		},
	} {
		t.Run(name, func(t *testing.T) {
			registry, home, a, _, workspace := setupSupportLink(t)
			unpublish(t, workspace)
			err := linkSupport(a, home, "--record", "reg:box:1")
			if err == nil || !strings.Contains(err.Error(), "is not published") {
				t.Fatalf("expected refusal, got %v", err)
			}
			if registry.linkPosts != 0 {
				t.Fatalf("%d link requests sent for an unpublished record", registry.linkPosts)
			}
		})
	}
}

func TestSupportLinkNeedsExactStableIDs(t *testing.T) {
	registry, home, a, _, _ := setupSupportLink(t)
	// A label resolves elsewhere, but link never resolves: it names records.
	err := linkSupport(a, home, "--record", "reg:box:1", "--record", "acme-box-1")
	if err == nil || registry.linkPosts != 0 {
		t.Fatalf("expected refusal before any write, got %v after %d posts", err, registry.linkPosts)
	}
	if err := linkSupport(a, home); err == nil || !strings.Contains(err.Error(), "--record") {
		t.Fatalf("expected usage, got %v", err)
	}
}

func TestSupportLinkRerunAfterAPublishedTitleCorrectionReplays(t *testing.T) {
	registry, home, a, stdout, workspace := setupSupportLink(t)
	if err := linkSupport(a, home, "--record", "reg:box:1"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "support", supportLinkRecord+".md")
	data, _ := os.ReadFile(path)
	writeCLITestFile(t, path, strings.Replace(string(data), `title: "Site network"`, `title: "Site network outage"`, 1))
	git(t, workspace, "commit", "--quiet", "-am", "Correct title")
	git(t, workspace, "push", "--quiet", "origin", "main")
	stdout.Reset()
	if err := linkSupport(a, home, "--record", "reg:box:1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "already linked") || len(registry.links) != 1 {
		t.Fatalf("rerun after a title correction: %s", stdout)
	}
}

func TestSupportLinkReportsCompletedLinksInJSONWhenALaterOneFails(t *testing.T) {
	registry, home, a, stdout, _ := setupSupportLink(t)
	// reg:box:2 already holds this reference with another body, so its link
	// is a conflict after reg:box:1 succeeds.
	registry.links = map[string]string{`reg:box:2 {"id":"2026-10-02-network","namespace":"acme.handbook","system":"support"}`: "other"}
	err := linkSupport(a, home, "--record", "reg:box:1", "--record", "reg:box:2", "--json")
	if err == nil {
		t.Fatal("expected failure")
	}
	var out struct {
		Error string              `json:"error"`
		Links []supportLinkResult `json:"links"`
	}
	if jerr := json.Unmarshal(stdout.Bytes(), &out); jerr != nil {
		t.Fatalf("%v: %s", jerr, stdout)
	}
	if out.Error != "link_failed" || len(out.Links) != 1 || out.Links[0].Record != "reg:box:1" || !out.Links[0].Created {
		t.Fatalf("partial result: %+v", out)
	}
}

func TestSupportLinkRefusesARefreshThatNamesAnotherRecord(t *testing.T) {
	registry, home, a, _, _ := setupSupportLink(t)
	registry.staleLinks = 1
	registry.redirectOnRefresh = "reg:box:2"
	err := linkSupport(a, home, "--record", "reg:box:1")
	if err == nil || !strings.Contains(err.Error(), "now reads back as reg:box:2") || len(registry.links) != 0 {
		t.Fatalf("expected refusal, got %v with links %v", err, registry.links)
	}
}

func TestSupportLinkKeyIsFramed(t *testing.T) {
	a := supportLinkKey("svc", "r:1", recordsvc.Reference{Namespace: "x.y", System: "support", ID: "z"})
	b := supportLinkKey("svc", "r:1", recordsvc.Reference{Namespace: "x", System: "support", ID: "y.z"})
	if a == b || len(a) != 64 || a != supportLinkKey("svc", "r:1", recordsvc.Reference{Namespace: "x.y", System: "support", ID: "z"}) {
		t.Fatalf("keys: %s %s", a, b)
	}
}

func appendFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
