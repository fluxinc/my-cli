package worksession

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewIDUsesDateSlugAndSuffix(t *testing.T) {
	now := time.Date(2026, 6, 11, 3, 4, 5, 0, time.UTC)
	id, err := NewID(now, "fix-docs", bytes.NewReader([]byte{0xab, 0xcd}))
	if err != nil {
		t.Fatal(err)
	}
	if id != "2026-06-11-fix-docs-abcd" {
		t.Fatalf("id = %q, want 2026-06-11-fix-docs-abcd", id)
	}
}

func TestNewIDRejectsBadSlug(t *testing.T) {
	now := time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC)
	if _, err := NewID(now, "Bad Slug!", bytes.NewReader([]byte{1, 2})); err == nil {
		t.Fatal("want error for invalid slug")
	}
}

func TestStartCreatesWorktreeScratchGuidanceAndRegistry(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	baseHead := gitOut(t, repo, "rev-parse", "HEAD")

	session, err := Start(StartOptions{
		Root: root,
		Slug: "notes",
		Now:  time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC),
		Rand: bytes.NewReader([]byte{0x12, 0x34}),
		Mounts: []MountSpec{
			{ID: "handbook", Kind: "handbook", RepoPath: repo},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "2026-06-11-notes-1234" {
		t.Fatalf("session id = %q", session.ID)
	}
	if session.Status != StatusActive {
		t.Fatalf("status = %q, want %q", session.Status, StatusActive)
	}
	wantPath := filepath.Join(root, "sessions", session.ID)
	if session.Path != wantPath {
		t.Fatalf("path = %q, want %q", session.Path, wantPath)
	}
	worktree := filepath.Join(wantPath, "handbook")
	if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
		t.Fatalf("worktree missing seeded file: %v", err)
	}
	if got := gitOut(t, worktree, "rev-parse", "--abbrev-ref", "HEAD"); got != "my/session/"+session.ID {
		t.Fatalf("worktree branch = %q", got)
	}
	if got := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "master" {
		t.Fatalf("base checkout branch = %q, want master", got)
	}
	if _, err := os.Stat(filepath.Join(wantPath, "scratch")); err != nil {
		t.Fatalf("scratch dir missing: %v", err)
	}
	sessionDoc, err := os.ReadFile(filepath.Join(wantPath, "SESSION.md"))
	if err != nil {
		t.Fatalf("SESSION.md missing: %v", err)
	}
	for _, want := range []string{session.ID, "handbook", "my session finish"} {
		if !strings.Contains(string(sessionDoc), want) {
			t.Fatalf("SESSION.md missing %q:\n%s", want, sessionDoc)
		}
	}
	agentsDoc, err := os.ReadFile(filepath.Join(wantPath, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md missing: %v", err)
	}
	for _, want := range []string{session.ID, "handbook/", "scratch/", "SESSION.md", "my session finish"} {
		if !strings.Contains(string(agentsDoc), want) {
			t.Fatalf("AGENTS.md missing %q:\n%s", want, agentsDoc)
		}
	}
	claudeDoc, err := os.ReadFile(filepath.Join(wantPath, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("CLAUDE.md missing: %v", err)
	}
	if string(claudeDoc) != string(agentsDoc) {
		t.Fatal("CLAUDE.md does not match AGENTS.md")
	}

	loaded, err := Load(root, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != SchemaVersion || loaded.ID != session.ID || loaded.Slug != "notes" {
		t.Fatalf("loaded = %#v", loaded)
	}
	if len(loaded.Mounts) != 1 {
		t.Fatalf("mounts = %#v", loaded.Mounts)
	}
	m := loaded.Mounts[0]
	if m.ID != "handbook" || m.Kind != "handbook" || m.RepoPath != repo {
		t.Fatalf("mount = %#v", m)
	}
	if m.WorktreePath != worktree {
		t.Fatalf("worktree path = %q, want %q", m.WorktreePath, worktree)
	}
	if m.BaseBranch != "master" || m.BaseHead != baseHead || m.Branch != "my/session/"+session.ID {
		t.Fatalf("mount git fields = %#v (base head %s)", m, baseHead)
	}
}

func TestStartDefaultsToNounFreeID(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	session, err := Start(StartOptions{
		Root: root,
		Now:  time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC),
		Rand: bytes.NewReader([]byte{0x9a, 0xbc}),
		Mounts: []MountSpec{
			{ID: "handbook", Kind: "handbook", RepoPath: repo},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.Slug != "" || session.ID != "2026-06-11-9abc" {
		t.Fatalf("session = %q slug %q, want noun-free default id", session.ID, session.Slug)
	}
	loaded, err := Load(root, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Slug != "" {
		t.Fatalf("registry slug = %q, want empty default slug", loaded.Slug)
	}
}

func TestMigrateMovesActiveLegacySessionAndPreservesDirtyWork(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	id := "2026-06-11-legacy-abcd"
	legacyPath := filepath.Join(root, "work", id)
	legacyWorktree := filepath.Join(legacyPath, "handbook")
	legacyBranch := "my/work/" + id
	runGit(t, repo, "worktree", "add", "-b", legacyBranch, legacyWorktree)
	writeFile(t, filepath.Join(legacyPath, "SESSION.md"), "legacy session\n")
	writeFile(t, filepath.Join(legacyPath, "AGENTS.md"), "legacy agents\n")
	writeFile(t, filepath.Join(legacyPath, "scratch", "note.txt"), "scratch\n")
	writeFile(t, filepath.Join(legacyWorktree, "meetings", "draft.md"), "draft\n")
	session := Session{
		SchemaVersion: SchemaVersion,
		ID:            id,
		CreatedAt:     "2026-06-11T01:02:03Z",
		Status:        StatusActive,
		Path:          legacyPath,
		Mounts: []Mount{{
			ID:           "handbook",
			Kind:         "handbook",
			RepoPath:     repo,
			WorktreePath: legacyWorktree,
			BaseBranch:   "master",
			BaseHead:     gitOut(t, repo, "rev-parse", "HEAD"),
			Branch:       legacyBranch,
			ContentPaths: []string{"meetings"},
		}},
	}
	if err := Save(root, session); err != nil {
		t.Fatal(err)
	}

	report, err := Migrate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].Status != "fixed" {
		t.Fatalf("migration report = %#v", report)
	}
	newPath := filepath.Join(root, "sessions", id)
	newWorktree := filepath.Join(newPath, "handbook")
	if _, err := os.Stat(newWorktree); err != nil {
		t.Fatalf("migrated worktree missing: %v", err)
	}
	if got := gitOut(t, newWorktree, "rev-parse", "--abbrev-ref", "HEAD"); got != "my/session/"+id {
		t.Fatalf("branch = %q", got)
	}
	if got := gitOut(t, newWorktree, "status", "--porcelain", "--", "meetings/draft.md"); !strings.Contains(got, "?? meetings/draft.md") {
		t.Fatalf("dirty work not preserved: %q", got)
	}
	if _, err := os.Stat(filepath.Join(newPath, "scratch", "note.txt")); err != nil {
		t.Fatalf("scratch not moved: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy path remains: %v", err)
	}
	loaded, err := Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Path != newPath || loaded.Mounts[0].WorktreePath != newWorktree || loaded.Mounts[0].Branch != "my/session/"+id {
		t.Fatalf("loaded migrated session = %#v", loaded)
	}
	second, err := Migrate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Sessions) != 0 {
		t.Fatalf("second migration = %#v, want idempotent no-op", second)
	}
}

func TestMigrateReportsOrphanLegacyDirsWithoutMoving(t *testing.T) {
	root := t.TempDir()
	orphan := filepath.Join(root, "work", "2026-06-11-orphan-abcd")
	writeFile(t, filepath.Join(orphan, "note.txt"), "orphan\n")

	report, err := Migrate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Sessions) != 0 || len(report.Orphans) != 1 || report.Orphans[0] != orphan {
		t.Fatalf("migration report = %#v", report)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("orphan was moved or removed: %v", err)
	}
}

func TestMigrateSkipsAmbiguousMountWithoutMutating(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	id := "2026-06-11-ambig-abcd"
	legacyPath := filepath.Join(root, "work", id)
	legacyWorktree := filepath.Join(legacyPath, "handbook")
	legacyBranch := "my/work/" + id
	sessionBranch := "my/session/" + id
	runGit(t, repo, "worktree", "add", "-b", legacyBranch, legacyWorktree)
	// A pre-existing session branch makes the mount ambiguous: migration must
	// refuse to guess and skip without touching anything.
	runGit(t, repo, "branch", sessionBranch, "master")
	writeFile(t, filepath.Join(legacyPath, "SESSION.md"), "legacy session\n")
	session := Session{
		SchemaVersion: SchemaVersion,
		ID:            id,
		CreatedAt:     "2026-06-11T01:02:03Z",
		Status:        StatusActive,
		Path:          legacyPath,
		Mounts: []Mount{{
			ID:           "handbook",
			Kind:         "handbook",
			RepoPath:     repo,
			WorktreePath: legacyWorktree,
			BaseBranch:   "master",
			BaseHead:     gitOut(t, repo, "rev-parse", "HEAD"),
			Branch:       legacyBranch,
		}},
	}
	if err := Save(root, session); err != nil {
		t.Fatal(err)
	}

	report, err := Migrate(root)
	if err != nil {
		t.Fatalf("Migrate returned a fatal error for an ambiguous session: %v", err)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].Status != "skipped" {
		t.Fatalf("migration report = %#v, want one skipped session", report)
	}
	if !strings.Contains(report.Sessions[0].Message, "both") {
		t.Fatalf("skip message = %q, want mention of the conflicting branches", report.Sessions[0].Message)
	}
	if len(report.Orphans) != 0 {
		t.Fatalf("orphans = %#v, want none (session is tracked)", report.Orphans)
	}
	// Nothing was mutated: no target dir, legacy worktree/branch intact, record unchanged.
	if _, err := os.Stat(filepath.Join(root, "sessions", id)); !os.IsNotExist(err) {
		t.Fatalf("ambiguous session created a target dir: %v", err)
	}
	if got := gitOut(t, legacyWorktree, "rev-parse", "--abbrev-ref", "HEAD"); got != legacyBranch {
		t.Fatalf("legacy worktree branch = %q, want %q", got, legacyBranch)
	}
	loaded, err := Load(root, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Path != legacyPath || loaded.Mounts[0].Branch != legacyBranch || loaded.Mounts[0].WorktreePath != legacyWorktree {
		t.Fatalf("record changed for a skipped session: %#v", loaded)
	}
}

func TestStartFailsCleanlyOnNonGitMount(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	notRepo := filepath.Join(root, "docs")
	if err := os.MkdirAll(notRepo, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Start(StartOptions{
		Root: root,
		Now:  time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC),
		Rand: bytes.NewReader([]byte{0x12, 0x34}),
		Mounts: []MountSpec{
			{ID: "handbook", Kind: "handbook", RepoPath: repo},
			{ID: "docs", Kind: "docs", RepoPath: notRepo},
		},
	})
	if err == nil {
		t.Fatal("want error for non-git mount")
	}
	sessions, listErr := List(root)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(sessions) != 0 {
		t.Fatalf("registry not empty after failed start: %#v", sessions)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "sessions")); len(entries) != 0 {
		t.Fatalf("sessions dir not cleaned up: %v", entries)
	}
	if out := gitOut(t, repo, "worktree", "list", "--porcelain"); strings.Contains(out, "my/session/") {
		t.Fatalf("stale worktree left behind:\n%s", out)
	}
	if out := gitOut(t, repo, "branch", "--list", "my/session/*"); strings.TrimSpace(out) != "" {
		t.Fatalf("stale branch left behind: %q", out)
	}
}

func TestListReturnsSessionsSortedByID(t *testing.T) {
	root := t.TempDir()
	for _, id := range []string{"2026-06-12-b-aaaa", "2026-06-10-a-bbbb"} {
		if err := Save(root, Session{SchemaVersion: SchemaVersion, ID: id, Status: StatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 || sessions[0].ID != "2026-06-10-a-bbbb" || sessions[1].ID != "2026-06-12-b-aaaa" {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func TestListOnFreshRootIsEmpty(t *testing.T) {
	sessions, err := List(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %#v", sessions)
	}
}

func TestInspectReportsDirtyAndUnlanded(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	session, err := Start(StartOptions{
		Root: root,
		Now:  time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC),
		Rand: bytes.NewReader([]byte{0x56, 0x78}),
		Mounts: []MountSpec{
			{ID: "handbook", Kind: "handbook", RepoPath: repo},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	worktree := session.Mounts[0].WorktreePath

	status, err := Inspect(session, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Mounts) != 1 || len(status.Mounts[0].Dirty) != 0 || status.Mounts[0].Unlanded != 0 {
		t.Fatalf("fresh session status = %#v", status.Mounts)
	}

	writeFile(t, filepath.Join(worktree, "meetings", "draft.md"), "draft\n")
	writeFile(t, filepath.Join(worktree, "landed.md"), "landed\n")
	runGit(t, worktree, "add", "landed.md")
	runGit(t, worktree, "commit", "-q", "-m", "session commit")

	status, err = Inspect(session, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := status.Mounts[0]
	if m.Unlanded != 1 {
		t.Fatalf("unlanded = %d, want 1; status = %#v", m.Unlanded, m)
	}
	if len(m.Dirty) != 1 || !strings.Contains(m.Dirty[0], "meetings/draft.md") {
		t.Fatalf("dirty = %#v", m.Dirty)
	}
}

func TestLandCommitsDirtyContentMergesAndMarksFinished(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	session, err := Start(StartOptions{
		Root: root,
		Now:  time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC),
		Rand: bytes.NewReader([]byte{0xab, 0xcd}),
		Mounts: []MountSpec{
			{ID: "handbook", Kind: "handbook", RepoPath: repo, ContentPaths: []string{"meetings"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	worktree := session.Mounts[0].WorktreePath
	writeFile(t, filepath.Join(worktree, "meetings", "note.md"), "land me\n")
	runGit(t, worktree, "add", "-N", "meetings/note.md")

	result, err := Land(LandOptions{
		Root:    root,
		ID:      session.ID,
		Message: "Land session note",
		Now:     time.Date(2026, 6, 11, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Status != StatusFinished || result.Session.Outcome != OutcomeLanded || result.Session.FinishedAt == "" {
		t.Fatalf("finished session = %#v", result.Session)
	}
	if len(result.Mounts) != 1 || result.Mounts[0].Status != "landed" || result.Mounts[0].Commit == "" {
		t.Fatalf("mount results = %#v", result.Mounts)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(repo, "meetings", "note.md"))); got != "land me" {
		t.Fatalf("landed file = %q", got)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after land: %v", err)
	}
	if branches := runGit(t, repo, "branch", "--list", session.Mounts[0].Branch); strings.TrimSpace(branches) != "" {
		t.Fatalf("session branch remains: %q", branches)
	}
	if log := runGit(t, repo, "log", "--oneline", "-1"); !strings.Contains(log, "Land session note") {
		t.Fatalf("base log = %q", log)
	}
	loaded, err := Load(root, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusFinished || loaded.Outcome != OutcomeLanded {
		t.Fatalf("registry session = %#v", loaded)
	}
}

func TestLandHoldsUnadoptedUntrackedContent(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	session, err := Start(StartOptions{
		Root: root,
		Now:  time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC),
		Rand: bytes.NewReader([]byte{0xab, 0xce}),
		Mounts: []MountSpec{
			{ID: "handbook", Kind: "handbook", RepoPath: repo, ContentPaths: []string{"meetings"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	worktree := session.Mounts[0].WorktreePath
	writeFile(t, filepath.Join(worktree, "meetings", "draft.md"), "draft\n")

	_, err = Land(LandOptions{Root: root, ID: session.ID})
	if err == nil || !strings.Contains(err.Error(), "unadopted untracked content file meetings/draft.md") {
		t.Fatalf("err = %v, want unadopted hold", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "meetings", "draft.md")); !os.IsNotExist(statErr) {
		t.Fatalf("unadopted file landed in base: %v", statErr)
	}
	if _, statErr := os.Stat(worktree); statErr != nil {
		t.Fatalf("worktree removed despite hold: %v", statErr)
	}
	loaded, loadErr := Load(root, session.ID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.Status != StatusActive {
		t.Fatalf("session status = %q, want active", loaded.Status)
	}
}

func TestLandHoldsCommittedNonContentChanges(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	session, err := Start(StartOptions{
		Root: root,
		Now:  time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC),
		Rand: bytes.NewReader([]byte{0xab, 0xcf}),
		Mounts: []MountSpec{
			{ID: "handbook", Kind: "handbook", RepoPath: repo, ContentPaths: []string{"meetings"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	worktree := session.Mounts[0].WorktreePath
	writeFile(t, filepath.Join(worktree, "README.md"), "not content\n")
	runGit(t, worktree, "add", "README.md")
	runGit(t, worktree, "commit", "-q", "-m", "Change README")

	_, err = Land(LandOptions{Root: root, ID: session.ID})
	if err == nil || !strings.Contains(err.Error(), "committed changes outside declared content paths") {
		t.Fatalf("err = %v, want non-content hold", err)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(repo, "README.md"))); got != "seed" {
		t.Fatalf("base README = %q, want seed", got)
	}
}

func TestDiscardRemovesWorktreeBranchAndMarksDiscarded(t *testing.T) {
	root, repo := setupUmbrellaWithMount(t, "handbook")
	session, err := Start(StartOptions{
		Root: root,
		Now:  time.Date(2026, 6, 11, 1, 2, 3, 0, time.UTC),
		Rand: bytes.NewReader([]byte{0xde, 0xad}),
		Mounts: []MountSpec{
			{ID: "handbook", Kind: "handbook", RepoPath: repo},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(session.Mounts[0].WorktreePath, "meetings", "draft.md"), "draft\n")

	result, err := Discard(DiscardOptions{Root: root, ID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Status != StatusDiscarded || result.Session.Outcome != OutcomeDiscarded {
		t.Fatalf("discarded session = %#v", result.Session)
	}
	if _, err := os.Stat(session.Path); !os.IsNotExist(err) {
		t.Fatalf("session path remains after discard: %v", err)
	}
	if branches := runGit(t, repo, "branch", "--list", session.Mounts[0].Branch); strings.TrimSpace(branches) != "" {
		t.Fatalf("session branch remains: %q", branches)
	}
	loaded, err := Load(root, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != StatusDiscarded || loaded.Outcome != OutcomeDiscarded {
		t.Fatalf("registry session = %#v", loaded)
	}
}

func setupUmbrellaWithMount(t *testing.T, mountID string) (string, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, mountID)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "README.md"), "seed\n")
	runGit(t, repo, "init", "-q", "-b", "master")
	configGitUser(t, repo)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "seed")
	return root, repo
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

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return runGit(t, dir, args...)
}

func configGitUser(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
}
