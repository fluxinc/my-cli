package syncer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseGnitRosterAcceptsGeneratedVersionOne(t *testing.T) {
	roster, err := parseGnitRoster([]byte(`version: 1
mode: control
remote: git@github.com:acme/control.git
members:
- id: handbook
  path: workspace
  remote: https://github.com/acme/handbook.git
  required_excludes:
  - workspace
- id: app
  path: repos/app
  remote: git@github.com:acme/app.git
ignored:
- scratch
`))
	if err != nil {
		t.Fatal(err)
	}
	if roster.Version != 1 || roster.Mode != "control" || len(roster.Members) != 2 {
		t.Fatalf("roster = %#v", roster)
	}
	if got := roster.Members[1]; got.ID != "app" || got.Path != "repos/app" || got.Remote == "" {
		t.Fatalf("member = %#v", got)
	}
}

func TestParseGnitRosterAcceptsOptionalRemoteAndUnknownFields(t *testing.T) {
	roster, err := parseGnitRoster([]byte(`version: 1
mode: local
future: yes
members:
- id: local
  path: repos/local
  future_member_field: yes
  future_mapping:
    enabled: yes
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := roster.Members[0]; got.Remote != "" {
		t.Fatalf("remote = %q, want optional empty remote", got.Remote)
	}
}

func TestParseGnitRosterRejectsUnknownVersionAndMalformedMembers(t *testing.T) {
	for name, body := range map[string]string{
		"unknown version": "version: 2\nmode: control\nmembers: []\n",
		"inline members":  "version: 1\nmode: control\nmembers: [oops]\n",
		"missing path":    "version: 1\nmode: control\nmembers:\n- id: app\n",
		"escaping path":   "version: 1\nmode: control\nmembers:\n- id: app\n  path: ../app\n",
		"null members":    "version: 1\nmode: control\nmembers:\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGnitRoster([]byte(body)); err == nil {
				t.Fatalf("parse succeeded for %q", body)
			}
		})
	}
}

func TestClassifyGnitEntryRequiresExactCanonicalMemberPath(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	roster := gnitRoster{Version: 1, Mode: "control", Members: []gnitRosterMember{{ID: "handbook", Path: "content", Remote: remote}}}
	link := filepath.Join(root, "content-link")
	if err := os.Symlink(content, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	linked := classifyGnitEntry(root, roster, Entry{ID: "handbook", GitURL: remote, LocalPath: link}, execCommand)
	if linked.Backend != "gnit" || linked.Member == nil {
		t.Fatalf("linked route = %#v", linked)
	}
	descendant := classifyGnitEntry(root, roster, Entry{ID: "nested", GitURL: remote, LocalPath: filepath.Join(content, "meetings")}, execCommand)
	if descendant.Backend != "builtin" || descendant.Member != nil {
		t.Fatalf("descendant route = %#v", descendant)
	}
}

func TestCanonicalPathResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	a, err := canonicalPath(real)
	if err != nil {
		t.Fatal(err)
	}
	b, err := canonicalPath(link)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("canonical paths differ: %q != %q", a, b)
	}
}

func TestNormalizeGitRemoteMatchesHTTPSAndSSH(t *testing.T) {
	values := []string{
		"https://github.com/Acme/App.git",
		"git@github.com:acme/app.git",
		"ssh://git@github.com/acme/app.git",
	}
	for _, value := range values {
		if got := normalizeGitRemote(value); got != "github.com/acme/app" {
			t.Fatalf("normalizeGitRemote(%q) = %q", value, got)
		}
	}
}
