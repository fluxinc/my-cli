package recordindex

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/recordsvc"
)

// fakeService holds records and an append-only change feed. Each feed page
// holds pageSize changes; the cursor is the index of the next change.
type fakeService struct {
	records  map[string]recordsvc.Record
	feed     []recordsvc.Change
	pageSize int
	// duringList runs once, after the first listing page, to simulate a
	// change made while a rebuild is listing.
	duringList func(*fakeService)
	stuck      bool
}

func newFakeService() *fakeService {
	return &fakeService{records: map[string]recordsvc.Record{}, pageSize: 2}
}

func (f *fakeService) put(id, text string) {
	f.records[id] = recordsvc.Record{"id": id, "revision": 1, "text": text}
	f.feed = append(f.feed, recordsvc.Change{ID: id, Op: "upsert"})
}

func (f *fakeService) Get(_ context.Context, id string) (recordsvc.Record, error) {
	rec, ok := f.records[id]
	if !ok {
		return nil, &recordsvc.Error{ServiceID: "registry", Status: 404, Code: "not_found"}
	}
	return rec, nil
}

func (f *fakeService) ListAfter(_ context.Context, after string, _ bool) ([]recordsvc.Record, string, error) {
	ids := make([]string, 0, len(f.records))
	for id := range f.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var page []recordsvc.Record
	for _, id := range ids {
		if id > after && len(page) < 2 {
			page = append(page, f.records[id])
		}
	}
	next := ""
	if len(page) == 2 && page[1].ID() != ids[len(ids)-1] {
		next = page[1].ID()
	}
	if f.duringList != nil {
		hook := f.duringList
		f.duringList = nil
		hook(f)
	}
	return page, next, nil
}

func (f *fakeService) Changes(_ context.Context, cursor string) ([]recordsvc.Change, string, bool, error) {
	start := 0
	if cursor != "" {
		fmt.Sscan(cursor, &start)
	}
	if f.stuck {
		return nil, cursor, true, nil
	}
	end := start + f.pageSize
	if end > len(f.feed) {
		end = len(f.feed)
	}
	return f.feed[start:end], fmt.Sprint(end), end < len(f.feed), nil
}

// fakeQMD indexes the documents' text for substring queries.
type fakeQMD struct {
	builds, updates int
	failBuild       bool
	failUpdate      bool
}

func (q *fakeQMD) Build(genDir string) error {
	q.builds++
	if q.failBuild {
		return errors.New("embedding failed")
	}
	return nil
}

func (q *fakeQMD) Update(genDir string) error {
	q.updates++
	if q.failUpdate {
		return errors.New("embedding failed")
	}
	return nil
}

func (q *fakeQMD) Query(genDir, text string, limit int) ([]QueryResult, error) {
	entries, _ := os.ReadDir(filepath.Join(genDir, "docs"))
	var out []QueryResult
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(genDir, "docs", e.Name()))
		if strings.Contains(string(data), text) {
			out = append(out, QueryResult{File: e.Name(), Score: 0.9})
		}
	}
	return out, nil
}

func render(rec recordsvc.Record) string { return fmt.Sprint(rec["text"]) + "\n" }

func openTest(t *testing.T, root string, svc Service, q QMD, baseURL string) *Index {
	t.Helper()
	ix, err := Open(root, Source{Manifest: "acme", ServiceID: "registry", BaseURL: baseURL}, svc, q, render)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func hitIDs(hits []Hit) []string {
	var ids []string
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestRebuildCatchesChangesMadeDuringTheListing(t *testing.T) {
	svc := newFakeService()
	svc.put("a", "alpha clinic")
	svc.put("b", "bravo clinic")
	svc.put("c", "charlie clinic")
	// After the first listing page (a, b), someone renames a: the listing has
	// already passed it, so only the feed (from the cursor taken first) can
	// bring the new text in.
	svc.duringList = func(f *fakeService) { f.put("a", "alpha renamed hospital") }
	q := &fakeQMD{}
	ix := openTest(t, t.TempDir(), svc, q, "https://registry.example.test/api")

	res, err := ix.Sync(context.Background(), 0, false)
	if err != nil || !res.Rebuilt || q.builds != 1 {
		t.Fatalf("rebuild = %+v, %v (builds %d)", res, err, q.builds)
	}
	// Straight after the rebuild, before any later sync, a's document is
	// already the renamed one.
	st, _ := ix.load()
	want, _ := Fingerprint(svc.records["a"])
	if st.Docs["a"].Fingerprint != want || st.Cursor != fmt.Sprint(len(svc.feed)) {
		t.Fatalf("after rebuild: a = %+v, cursor %s; want fingerprint %s, cursor %d", st.Docs["a"], st.Cursor, want, len(svc.feed))
	}
	hits, _, err := ix.Search(context.Background(), "renamed hospital", 5, 10)
	if err != nil || strings.Join(hitIDs(hits), ",") != "a" {
		t.Fatalf("hits = %v, %v", hits, err)
	}
	if hits, _, _ := ix.Search(context.Background(), "alpha clinic", 5, 10); len(hits) != 0 {
		t.Fatalf("the stale text is still indexed: %v", hits)
	}
}

func TestFollowAppliesUpsertsTombstonesAndMissingRecords(t *testing.T) {
	svc := newFakeService()
	svc.put("a", "alpha")
	svc.put("b", "bravo")
	svc.put("c", "charlie")
	q := &fakeQMD{}
	ix := openTest(t, t.TempDir(), svc, q, "https://registry.example.test/api")
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}

	// A parent-only change: the record's own revision is unchanged, but what
	// it shows changed, so the fingerprint (over the whole record) moves.
	svc.records["a"] = recordsvc.Record{"id": "a", "revision": 1, "text": "alpha", "order": map[string]any{"partner": "newco"}}
	svc.feed = append(svc.feed, recordsvc.Change{ID: "a", Op: "upsert"}, recordsvc.Change{ID: "b", Op: "tombstone"})
	delete(svc.records, "c") // the feed says upsert, the record is gone: 404
	svc.feed = append(svc.feed, recordsvc.Change{ID: "c", Op: "upsert"})
	svc.put("d", "delta")

	res, err := ix.Sync(context.Background(), 0, false)
	if err != nil || res.Updated != 1 || res.Removed != 2 || res.Added != 1 || q.updates != 1 {
		t.Fatalf("follow = %+v, %v (updates %d)", res, err, q.updates)
	}
	if ids, _ := ix.Documents(); strings.Join(ids, ",") != "a,d" {
		t.Fatalf("documents = %v", ids)
	}

	// Nothing changed: no re-embed.
	if res, err := ix.Sync(context.Background(), 0, false); err != nil || res != (Result{}) || q.updates != 1 {
		t.Fatalf("idle sync = %+v, %v (updates %d)", res, err, q.updates)
	}
}

func TestABacklogIsNeverQueried(t *testing.T) {
	svc := newFakeService()
	svc.put("a", "alpha")
	ix := openTest(t, t.TempDir(), svc, &fakeQMD{}, "https://registry.example.test/api")
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ { // three pages of two
		svc.put(fmt.Sprintf("n%d", i), "new")
	}
	hits, res, err := ix.Search(context.Background(), "alpha", 1, 10)
	if err != nil || !res.Backlog || hits != nil {
		t.Fatalf("search with backlog = %v, %+v, %v", hits, res, err)
	}
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	if hits, res, err := ix.Search(context.Background(), "alpha", 1, 10); err != nil || res.Backlog || len(hits) != 1 {
		t.Fatalf("caught-up search = %v, %+v, %v", hits, res, err)
	}
}

func TestADifferentEndpointIsADifferentIndex(t *testing.T) {
	svc := newFakeService()
	svc.put("a", "alpha")
	root := t.TempDir()
	if _, err := openTest(t, root, svc, &fakeQMD{}, "https://registry.example.test/api").Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	other := openTest(t, root, svc, &fakeQMD{}, "https://registry.example.test/api-v2")
	if _, _, err := other.Search(context.Background(), "alpha", 5, 10); !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("another endpoint reused the index: %v", err)
	}
}

func TestAFailedRebuildLeavesThePreviousIndexUsable(t *testing.T) {
	svc := newFakeService()
	svc.put("a", "alpha")
	q := &fakeQMD{}
	root := t.TempDir()
	ix := openTest(t, root, svc, q, "https://registry.example.test/api")
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	q.failBuild = true
	if _, err := ix.Sync(context.Background(), 0, true); err == nil {
		t.Fatal("rebuild with a failing embed succeeded")
	}
	if hits, _, err := ix.Search(context.Background(), "alpha", 5, 10); err != nil || len(hits) != 1 {
		t.Fatalf("previous index after a failed rebuild = %v, %v", hits, err)
	}
	gens, _ := filepath.Glob(filepath.Join(ix.dir, "gen-*"))
	if len(gens) != 1 {
		t.Fatalf("leftover generations: %v", gens)
	}
}

func TestAStuckFeedIsAnErrorNotALoop(t *testing.T) {
	svc := newFakeService()
	svc.put("a", "alpha")
	ix := openTest(t, t.TempDir(), svc, &fakeQMD{}, "https://registry.example.test/api")
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	svc.stuck = true
	if _, err := ix.Sync(context.Background(), 0, false); err == nil || !strings.Contains(err.Error(), "not advancing") {
		t.Fatalf("stuck feed = %v", err)
	}
}

func TestAFailedEmbedIsRetriedBeforeTheNextQuery(t *testing.T) {
	svc := newFakeService()
	svc.put("a", "alpha")
	q := &fakeQMD{}
	ix := openTest(t, t.TempDir(), svc, q, "https://registry.example.test/api")
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	svc.put("b", "bravo")
	q.failUpdate = true
	if _, _, err := ix.Search(context.Background(), "bravo", 5, 10); err == nil {
		t.Fatal("a failed embed was queried anyway")
	}
	q.failUpdate = false
	hits, _, err := ix.Search(context.Background(), "bravo", 5, 10) // no new changes, still pending
	if err != nil || len(hits) != 1 || q.updates != 2 {
		t.Fatalf("retry = %v, %v (updates %d)", hits, err, q.updates)
	}
}

func TestIndexFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes")
	}
	svc := newFakeService()
	svc.put("a", "alpha")
	root := t.TempDir()
	ix := openTest(t, root, svc, &fakeQMD{}, "https://registry.example.test/api")
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(ix.dir, func(path string, d os.DirEntry, err error) error {
		info, _ := d.Info()
		want := os.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode %o, want %o", path, info.Mode().Perm(), want)
		}
		return nil
	})
}

// The real qmd command runs with HOME and INDEX_PATH inside the generation:
// the user's own qmd config and index are never touched.
func TestCommandQMDRunsIsolated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	userHome := t.TempDir()
	t.Setenv("HOME", userHome)
	t.Setenv("INDEX_PATH", filepath.Join(userHome, "shared.sqlite"))
	bin := t.TempDir()
	stub := "#!/bin/sh\nmkdir -p \"$HOME/.config/qmd\"\necho \"$@\" >> \"$HOME/.config/qmd/index.yml\"\necho \"$INDEX_PATH\" > \"$HOME/index-path\"\n" +
		"if [ \"$1\" = query ]; then echo '[{\"file\":\"qmd://records/abc.md\",\"score\":0.5}]'; fi\n"
	if err := os.WriteFile(filepath.Join(bin, "qmd"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	gen := t.TempDir()
	q := CommandQMD{ModelCache: filepath.Join(userHome, ".cache", "qmd", "models")}
	if err := q.Build(gen); err != nil {
		t.Fatal(err)
	}
	results, err := q.Query(gen, "anything", 5)
	if err != nil || len(results) != 1 || results[0].File != "abc.md" {
		t.Fatalf("query = %v, %v", results, err)
	}
	if _, err := os.Stat(filepath.Join(userHome, ".config", "qmd")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("qmd wrote the user's own config")
	}
	got, _ := os.ReadFile(filepath.Join(gen, "qmd-home", "index-path"))
	if strings.TrimSpace(string(got)) != filepath.Join(gen, "qmd-home", "index.sqlite") {
		t.Fatalf("INDEX_PATH = %q", got)
	}
	info, _ := os.Stat(filepath.Join(gen, "qmd-home", ".config", "qmd", "index.yml"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("qmd config mode %o", info.Mode().Perm())
	}
}

// The real qmd, when installed, runs entirely inside the generation: the
// user's own qmd config and default index stay byte-for-byte the same.
// Fake content only. Skipped where qmd is absent (CI).
func TestRealQMDNeverTouchesTheUsersIndex(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix only")
	}
	if _, err := exec.LookPath("qmd"); err != nil {
		t.Skip("qmd not installed")
	}
	if os.Getenv("MY_CLI_QMD_SMOKE") == "" {
		t.Skip("set MY_CLI_QMD_SMOKE=1 to run the real qmd (it loads embedding models)")
	}
	home := realHome
	watched := []string{filepath.Join(home, ".config", "qmd", "index.yml"), filepath.Join(home, ".cache", "qmd", "index.sqlite")}
	before := map[string]string{}
	for _, path := range watched {
		before[path] = snapshot(path)
	}

	svc := newFakeService()
	svc.put("box-1", "Fictional Springfield clinic routing box with a mammography unit")
	svc.put("box-2", "Sample Centerville hospital document printer")
	ix, err := Open(t.TempDir(), Source{Manifest: "acme", ServiceID: "registry", BaseURL: "https://registry.example.test/api"}, svc,
		CommandQMD{ModelCache: filepath.Join(home, ".cache", "qmd", "models")}, render)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ix.Sync(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	hits, _, err := ix.Search(context.Background(), "mammography clinic", 5, 5)
	if err != nil || len(hits) == 0 || hits[0].ID != "box-1" {
		t.Fatalf("real qmd hits = %v, %v", hits, err)
	}
	for _, path := range watched {
		if snapshot(path) != before[path] {
			t.Fatalf("%s changed", path)
		}
	}
	filepath.WalkDir(ix.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, _ := d.Info()
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s is readable by others (%o)", path, perm)
		}
		return nil
	})
}

// snapshot is a file's size, mode and content hash ("" when absent).
func snapshot(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	info, _ := os.Stat(path)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%d %o %x", len(data), info.Mode().Perm(), sum)
}
