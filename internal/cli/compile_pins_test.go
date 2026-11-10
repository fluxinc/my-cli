//go:build !windows

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/launchplan"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

const pinTestURL = "https://github.com/acme/handbook.git"

func pinGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func pinCommit(t *testing.T, dir, name string) string {
	t.Helper()
	writeCLITestFile(t, filepath.Join(dir, name), name+"\n")
	pinGit(t, dir, "add", name)
	pinGit(t, dir, "commit", "-q", "-m", "add "+name)
	return pinGit(t, dir, "rev-parse", "HEAD")
}

// pinFixture makes an origin repository and an umbrella whose handbook mount
// is a clone of it. The clone's remote URL is rewritten to the manifest's
// URL: probes never contact a remote, so the URL only has to identify it.
func pinFixture(t *testing.T) (root, origin, mount string, m manifest.Mount) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	origin = filepath.Join(base, "origin")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	pinGit(t, origin, "init", "-q", "-b", "main")
	pinCommit(t, origin, "a.md")
	root = filepath.Join(base, "acme")
	if _, _, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	mount = filepath.Join(root, "handbook")
	if out, err := exec.Command("git", "clone", "-q", origin, mount).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	pinGit(t, mount, "remote", "set-url", "origin", pinTestURL)
	return root, origin, mount, manifest.Mount{ID: "handbook", Kind: "handbook", GitURL: pinTestURL, Mode: "default"}
}

func wantUnavailable(t *testing.T, got launchplan.MountPin, reason string) {
	t.Helper()
	if got.Pin.Commit != "" || got.Pin.Source != "" || got.Pin.Unavailable != reason || got.Dirty {
		t.Fatalf("pin = %+v, want unavailable %q with no commit", got, reason)
	}
}

func TestMountPinObservedHead(t *testing.T) {
	root, _, mount, m := pinFixture(t)
	head := pinGit(t, mount, "rev-parse", "HEAD")
	got := observeMountPin(root, m)
	if got.Pin.Commit != head || len(got.Pin.Commit) != 40 || got.Pin.Source != "umbrella" || got.Pin.Unavailable != "" || got.Dirty {
		t.Fatalf("pin = %+v, want commit %s from umbrella", got, head)
	}
}

func TestMountPinDirtyIsDisclosedBesideTheCommit(t *testing.T) {
	root, _, mount, m := pinFixture(t)
	head := pinGit(t, mount, "rev-parse", "HEAD")
	writeCLITestFile(t, filepath.Join(mount, "untracked.md"), "wip\n")
	got := observeMountPin(root, m)
	if got.Pin.Commit != head || !got.Dirty {
		t.Fatalf("pin = %+v, want commit %s with dirty disclosed", got, head)
	}
}

func TestMountPinUnavailableReasons(t *testing.T) {
	t.Run("missing checkout", func(t *testing.T) {
		root, _, _, m := pinFixture(t)
		m.ID = "absent"
		wantUnavailable(t, observeMountPin(root, m), "missing_checkout")
	})
	t.Run("plain subdirectory of an ancestor repository", func(t *testing.T) {
		root, _, _, m := pinFixture(t)
		// The umbrella root is itself a repository; the mount is an ordinary
		// directory in it. Git must not answer with the ancestor's HEAD.
		pinGit(t, root, "init", "-q", "-b", "main")
		pinCommit(t, root, "root.md")
		m.ID = "plain"
		if err := os.MkdirAll(filepath.Join(root, "plain"), 0o755); err != nil {
			t.Fatal(err)
		}
		wantUnavailable(t, observeMountPin(root, m), "missing_checkout")
	})
	t.Run("ahead of the cached upstream", func(t *testing.T) {
		root, _, mount, m := pinFixture(t)
		pinCommit(t, mount, "local-only.md")
		wantUnavailable(t, observeMountPin(root, m), "ahead_of_upstream")
	})
	t.Run("no upstream", func(t *testing.T) {
		root, _, mount, m := pinFixture(t)
		pinGit(t, mount, "branch", "--unset-upstream")
		wantUnavailable(t, observeMountPin(root, m), "no_upstream")
	})
	t.Run("upstream is a local branch", func(t *testing.T) {
		root, _, mount, m := pinFixture(t)
		pinGit(t, mount, "branch", "other")
		pinGit(t, mount, "branch", "--set-upstream-to=other")
		wantUnavailable(t, observeMountPin(root, m), "no_upstream")
	})
	t.Run("detached head", func(t *testing.T) {
		root, _, mount, m := pinFixture(t)
		pinGit(t, mount, "checkout", "-q", "--detach")
		wantUnavailable(t, observeMountPin(root, m), "no_upstream")
	})
	t.Run("checkout of an unrelated repository", func(t *testing.T) {
		root, _, mount, m := pinFixture(t)
		pinGit(t, mount, "remote", "set-url", "origin", "https://github.com/acme/something-else.git")
		wantUnavailable(t, observeMountPin(root, m), "repository_mismatch")
	})
	t.Run("upstream on a different remote", func(t *testing.T) {
		root, origin, mount, m := pinFixture(t)
		pinGit(t, mount, "remote", "add", "fork", origin)
		pinGit(t, mount, "fetch", "-q", "fork")
		pinGit(t, mount, "branch", "--set-upstream-to=fork/main")
		wantUnavailable(t, observeMountPin(root, m), "repository_mismatch")
	})
	t.Run("same repository over another transport is a conservative mismatch", func(t *testing.T) {
		root, _, mount, m := pinFixture(t)
		pinGit(t, mount, "remote", "set-url", "origin", "git@github.com:acme/handbook.git")
		wantUnavailable(t, observeMountPin(root, m), "repository_mismatch")
	})
	t.Run("repository that cannot be inspected", func(t *testing.T) {
		root, _, _, m := pinFixture(t)
		m.ID = "broken"
		writeCLITestFile(t, filepath.Join(root, "broken", ".git"), "not a gitdir pointer\n")
		wantUnavailable(t, observeMountPin(root, m), "error")
	})
}

func TestMountPinNeverPrintsTheConfiguredRemote(t *testing.T) {
	root, _, mount, m := pinFixture(t)
	secret := "https://user:s3cret-token@example.invalid/acme/other.git"
	pinGit(t, mount, "remote", "set-url", "origin", secret)
	data, err := json.Marshal(observeMountPin(root, m))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "s3cret") || strings.Contains(string(data), "example.invalid") {
		t.Fatalf("pin output leaked the configured remote: %s", data)
	}
}

func TestMountPinSaysNothingAboutFetchability(t *testing.T) {
	root, origin, mount, m := pinFixture(t)
	head := pinGit(t, mount, "rev-parse", "HEAD")
	// The real remote moves on and even rewrites history; the clone's cached
	// upstream is now stale. The pin is unchanged, because it only ever
	// described this clone.
	pinGit(t, origin, "commit", "-q", "--amend", "-m", "rewritten")
	pinCommit(t, origin, "later.md")
	got := observeMountPin(root, m)
	if got.Pin.Commit != head || got.Pin.Unavailable != "" {
		t.Fatalf("pin = %+v, want the locally observed commit %s", got, head)
	}
	data, _ := json.Marshal(got)
	for _, word := range []string{"fetch", "published", "remote"} {
		if strings.Contains(strings.ToLower(string(data)), word) {
			t.Fatalf("pin output makes a claim about %q: %s", word, data)
		}
	}
}

func TestMountPinUsesTheCapturedHeadNotALaterOne(t *testing.T) {
	root, _, mount, m := pinFixture(t)
	head := pinGit(t, mount, "rev-parse", "HEAD")
	var calls [][]string
	run := func(dir string, args ...string) ([]byte, int, error) {
		calls = append(calls, args)
		return runPinGit(dir, args...)
	}
	got := observeMountPinWith(root, m, run)
	if got.Pin.Commit != head {
		t.Fatalf("pin = %+v", got)
	}
	headProbes := 0
	for _, args := range calls {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "HEAD^{commit}") {
			headProbes++
		}
		if strings.HasPrefix(joined, "merge-base --is-ancestor") && !strings.Contains(joined, head) {
			t.Fatalf("ancestry was checked against a symbolic ref, not the captured commit: %q", joined)
		}
	}
	if headProbes != 1 {
		t.Fatalf("HEAD was resolved %d times, want exactly once", headProbes)
	}
}

// partialCloneMissing turns the fixture's clone into a partial clone of a
// reachable promisor remote that lacks two commits the remote has: parent and
// tip. It holds tip's commit object only when withTip is set.
func partialCloneMissing(t *testing.T, origin, mount string, withTip bool) (parent, tip string) {
	t.Helper()
	pinGit(t, origin, "config", "uploadpack.allowFilter", "true")
	pinGit(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	parent = pinCommit(t, origin, "b.md")
	tip = pinCommit(t, origin, "c.md")
	if withTip {
		loose := filepath.Join(".git", "objects", tip[:2], tip[2:])
		data, err := os.ReadFile(filepath.Join(origin, loose))
		if err != nil {
			t.Fatalf("the tip commit is not a loose object in the origin: %v", err)
		}
		writeCLITestFile(t, filepath.Join(mount, loose), string(data))
	}
	pinGit(t, mount, "config", "remote.origin.promisor", "true")
	pinGit(t, mount, "config", "remote.origin.partialclonefilter", "blob:none")
	pinGit(t, mount, "config", "extensions.partialClone", "origin")
	pinGit(t, mount, "remote", "set-url", "origin", origin)
	return parent, tip
}

func pinHasObject(mount, id string) bool {
	cmd := exec.Command("git", "-C", mount, "cat-file", "-e", id)
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=1")
	return cmd.Run() == nil
}

// The probe runner itself must be unable to fetch. This asks it for an object
// that only the reachable promisor remote has, using a command Git does
// fetch on demand for, and then shows the same command fetching when run
// without the runner's protections.
func TestPinGitRunnerCannotFetch(t *testing.T) {
	_, origin, mount, _ := pinFixture(t)
	missing, _ := partialCloneMissing(t, origin, mount, false)
	// Repository config that tries to re-enable transports must not matter.
	pinGit(t, mount, "config", "protocol.allow", "always")
	pinGit(t, mount, "config", "protocol.file.allow", "always")
	if _, exit, err := runPinGit(mount, "cat-file", "-e", missing); err != nil || exit == 0 {
		t.Fatalf("the probe runner found an object the clone does not have: exit=%d err=%v", exit, err)
	}
	if pinHasObject(mount, missing) {
		t.Fatal("the probe runner fetched a missing object; compile must stay offline")
	}
	// Control: the trap is live.
	if err := exec.Command("git", "-C", mount, "cat-file", "-e", missing).Run(); err != nil || !pinHasObject(mount, missing) {
		t.Skipf("this Git did not lazy-fetch in the control run (err=%v), so the trap proves nothing here", err)
	}
}

// End to end: the cached upstream names a commit whose parent this clone does
// not have. The ancestry question cannot be answered offline, so there is no
// pin, and nothing was fetched to answer it.
func TestMountPinWithMissingHistoryIsAnErrorNotAFetch(t *testing.T) {
	root, origin, mount, m := pinFixture(t)
	parent, tip := partialCloneMissing(t, origin, mount, true)
	writeCLITestFile(t, filepath.Join(mount, ".git", "refs", "remotes", "origin", "main"), tip+"\n")
	m.GitURL = origin
	if !pinHasObject(mount, tip) || pinHasObject(mount, parent) {
		t.Fatalf("fixture is wrong: has tip=%v has parent=%v", pinHasObject(mount, tip), pinHasObject(mount, parent))
	}
	wantUnavailable(t, observeMountPin(root, m), "error")
	if pinHasObject(mount, parent) {
		t.Fatal("the probe fetched a missing commit; compile must stay offline")
	}
}

func TestCompileUmbrellaIdentity(t *testing.T) {
	root, _, _, _ := pinFixture(t)
	doc := manifest.Document{Organization: manifest.Organization{ID: "acme"}}
	if got, err := validateCompileUmbrella(root, doc); err != nil || got != root {
		t.Fatalf("valid umbrella: got %q err %v", got, err)
	}
	// A symlinked path resolves to the same root.
	link := filepath.Join(filepath.Dir(root), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if got, err := validateCompileUmbrella(link, doc); err != nil || got != root {
		t.Fatalf("symlinked umbrella: got %q err %v", got, err)
	}
	other := manifest.Document{Organization: manifest.Organization{ID: "globex"}}
	if _, err := validateCompileUmbrella(root, other); err == nil || !strings.Contains(err.Error(), "organization") {
		t.Fatalf("another organization's umbrella was accepted: %v", err)
	}
	// A session directory has no workspace identity of its own and must not
	// be relabelled by walking up to the umbrella.
	session := filepath.Join(root, "sessions", "2026-01-01-ab12")
	if err := os.MkdirAll(session, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCompileUmbrella(session, doc); err == nil {
		t.Fatal("a session directory was accepted as an umbrella")
	}
	if _, err := validateCompileUmbrella(filepath.Join(root, "nope"), doc); err == nil {
		t.Fatal("a missing directory was accepted as an umbrella")
	}
	// An identity that records no root, or another root, is not this umbrella.
	moved, err := umbrella.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, recorded := range []string{"", filepath.Join(filepath.Dir(root), "elsewhere")} {
		changed := moved
		changed.WorkspaceRoot = recorded
		if err := umbrella.SaveWorkspace(root, changed); err != nil {
			t.Fatal(err)
		}
		if _, err := validateCompileUmbrella(root, doc); err == nil || !strings.Contains(err.Error(), "different umbrella root") {
			t.Fatalf("an identity recording root %q was accepted: %v", recorded, err)
		}
	}
	if err := umbrella.SaveWorkspace(root, moved); err != nil {
		t.Fatal(err)
	}
	// A workspace file from a newer schema is refused, not guessed at.
	ws, err := umbrella.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	ws.SchemaVersion = umbrella.SchemaVersion + 1
	if err := umbrella.SaveWorkspace(root, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCompileUmbrella(root, doc); err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("an unknown workspace schema was accepted: %v", err)
	}
}

func compileForPins(t *testing.T, home string, extra ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	args := append([]string{"my", "compile", "--manifest", "acme", "--role", "operator", "--home", home}, extra...)
	if err := a.run(args); err != nil {
		t.Fatalf("compile %v: %v\nstderr:\n%s", extra, err, stderr.String())
	}
	return stdout.String()
}

func TestCompileWithoutUmbrellaIsByteForByteUnchanged(t *testing.T) {
	home := t.TempDir()
	writeCompileManifest(t, home)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "manifests", "add", "acme", "https://github.com/acme/acme-ai-manifest.git", "--home", home}); err != nil {
		t.Fatal(err)
	}
	plain := compileForPins(t, home)
	if strings.Contains(plain, `"pin"`) || strings.Contains(plain, `"dirty"`) {
		t.Fatalf("default compile output gained pin fields:\n%s", plain)
	}
	// The same projection with pins differs only by the added fields: strip
	// them and the bytes are identical.
	root, _, mount, _ := pinFixture(t)
	head := pinGit(t, mount, "rev-parse", "HEAD")
	pinned := compileForPins(t, home, "--umbrella", root)
	var doc map[string]any
	if err := json.Unmarshal([]byte(pinned), &doc); err != nil {
		t.Fatalf("pinned output is not JSON: %v\n%s", err, pinned)
	}
	mounts := doc["mounts"].([]any)
	if len(mounts) != 1 {
		t.Fatalf("mounts = %v", mounts)
	}
	entry := mounts[0].(map[string]any)
	pin, _ := entry["pin"].(map[string]any)
	if pin["commit"] != head || pin["source"] != "umbrella" {
		t.Fatalf("pin = %v, want commit %s from umbrella", pin, head)
	}
	if _, ok := entry["dirty"]; ok {
		t.Fatalf("a clean checkout disclosed dirty: %v", entry)
	}
	if doc["compile_version"].(float64) != 1 {
		t.Fatalf("compile_version changed: %v", doc["compile_version"])
	}
	delete(entry, "pin")
	var want map[string]any
	if err := json.Unmarshal([]byte(plain), &want); err != nil {
		t.Fatal(err)
	}
	gotBytes, _ := json.Marshal(doc)
	wantBytes, _ := json.Marshal(want)
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatalf("pinned output differs beyond the pin field\nplain:\n%s\npinned:\n%s", plain, pinned)
	}
	if again := compileForPins(t, home, "--umbrella", root); again != pinned {
		t.Fatal("pinned output is not deterministic")
	}
}

func TestCompileRejectsAnotherOrganizationsUmbrella(t *testing.T) {
	home := t.TempDir()
	writeCompileManifest(t, home)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "manifests", "add", "acme", "https://github.com/acme/acme-ai-manifest.git", "--home", home}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "globex")
	if _, _, err := umbrella.Ensure(root, "globex", "globex"); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	err := a.run([]string{"my", "compile", "--manifest", "acme", "--role", "operator", "--home", home, "--umbrella", root})
	if err == nil || stdout.Len() != 0 {
		t.Fatalf("compile against another organization's umbrella: err=%v output=%q", err, stdout.String())
	}
}
