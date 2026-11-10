//go:build !windows

package attempt

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func repoWithCommit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func observeOne(t *testing.T, dir string) RepoObs {
	t.Helper()
	obs := Observe(KindAfter, []RepoTarget{{Label: "x", Path: dir}}, RunGit, time.Unix(0, 0))
	if len(obs.Repos) != 1 {
		t.Fatalf("repos = %+v", obs.Repos)
	}
	return obs.Repos[0]
}

func TestObserveNoUpstreamIsUnknownPublication(t *testing.T) {
	repo := observeOne(t, repoWithCommit(t))
	if repo.Error != "" || repo.Branch != "main" || repo.Head == "" {
		t.Fatalf("repo = %+v", repo)
	}
	if repo.Dirty == nil || *repo.Dirty {
		t.Fatalf("dirty = %v, want false", repo.Dirty)
	}
	if repo.HasUpstream == nil || *repo.HasUpstream {
		t.Fatal("has_upstream should be false")
	}
	if repo.Ahead != nil {
		t.Fatalf("ahead must be unknown without an upstream, got %d", *repo.Ahead)
	}
}

func TestObserveUntrackedFilesAreDirtyAndNamesAreNotCounted(t *testing.T) {
	dir := repoWithCommit(t)
	// A name with a newline would break line counting; presence is all we record.
	if err := os.WriteFile(filepath.Join(dir, "new\nfile.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := observeOne(t, dir)
	if repo.Dirty == nil || !*repo.Dirty {
		t.Fatalf("an untracked file was not dirty: %+v", repo)
	}
}

func TestObserveAheadOfCachedUpstream(t *testing.T) {
	origin := repoWithCommit(t)
	clone := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", "-q", origin, clone).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	repo := observeOne(t, clone)
	if repo.HasUpstream == nil || !*repo.HasUpstream || repo.Ahead == nil || *repo.Ahead != 0 {
		t.Fatalf("fresh clone = %+v", repo)
	}
	if err := os.WriteFile(filepath.Join(clone, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, clone, "add", "b.txt")
	git(t, clone, "commit", "-q", "-m", "second")
	repo = observeOne(t, clone)
	if repo.Ahead == nil || *repo.Ahead != 1 || *repo.Dirty {
		t.Fatalf("after a local commit = %+v", repo)
	}
}

func TestObserveSurfacesRepositoryErrors(t *testing.T) {
	repo := observeOne(t, t.TempDir()) // not a repository
	if repo.Error == "" || repo.Dirty != nil || repo.Ahead != nil {
		t.Fatalf("a failed observation must carry an error and no values: %+v", repo)
	}
	missing := observeOne(t, filepath.Join(t.TempDir(), "gone"))
	if missing.Error == "" {
		t.Fatal("a missing checkout must surface an error")
	}
}

func TestLocalChange(t *testing.T) {
	yes, no := true, false
	zero, two := 0, 2
	clean := RepoObs{Path: "/r", Branch: "main", Head: "aaa", Dirty: &no, HasUpstream: &yes, Ahead: &zero}
	with := func(change func(*RepoObs)) *Observation {
		repo := clean
		change(&repo)
		return &Observation{Repos: []RepoObs{repo}}
	}
	same := with(func(*RepoObs) {})
	cases := []struct {
		name          string
		before, after *Observation
		want          *bool
	}{
		{"clean before and after", same, same, &no},
		{"dirty at the end", same, with(func(r *RepoObs) { r.Dirty = &yes }), &yes},
		{"ahead at the end", same, with(func(r *RepoObs) { r.Ahead = &two }), &yes},
		{"head moved", same, with(func(r *RepoObs) { r.Head = "bbb" }), &yes},
		{"branch switched", same, with(func(r *RepoObs) { r.Branch = "topic" }), &yes},
		{"no after observation", same, nil, nil},
		{"no checkouts observed is unknown, not unchanged", &Observation{}, &Observation{}, nil},
		{"no before observation", nil, same, nil},
		{"dirty with no before is still positive", nil, with(func(r *RepoObs) { r.Dirty = &yes }), &yes},
		{"after failed", same, with(func(r *RepoObs) { r.Error, r.Dirty, r.Ahead = "boom", nil, nil }), nil},
		{"before failed", with(func(r *RepoObs) { r.Error = "boom" }), same, nil},
		{"no upstream is unknown, not unchanged", same, with(func(r *RepoObs) { r.HasUpstream, r.Ahead = &no, nil }), nil},
	}
	for _, tc := range cases {
		got := LocalChange(tc.before, tc.after)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%s: got %v, want unknown", tc.name, *got)
		case tc.want != nil && got == nil:
			t.Errorf("%s: got unknown, want %v", tc.name, *tc.want)
		case tc.want != nil && *got != *tc.want:
			t.Errorf("%s: got %v, want %v", tc.name, *got, *tc.want)
		}
	}
}

func TestObserveIgnoresMyOwnStateDirectory(t *testing.T) {
	dir := repoWithCommit(t)
	state := filepath.Join(dir, ".my-cli", "attempts", "x")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "record.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if repo := observeOne(t, dir); repo.Error != "" || repo.Dirty == nil || *repo.Dirty {
		t.Fatalf("my's own launch records made the checkout look dirty: %+v", repo)
	}
}
