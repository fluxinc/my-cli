// Package recordindex keeps a private, per-user semantic index of the records
// a bound service holds. It is a derived cache for finding records, never a
// source of values: every hit is re-read from the service before it is shown,
// and nothing here answers a get, list or set, or stands in when the service
// cannot answer.
//
// Layout under the state root (directories 0700, files 0600):
//
//	<source key>/lock          serializes sync, rebuild and query
//	<source key>/state.json    source, live generation, feed cursor, documents
//	<source key>/gen-<id>/     one generation: docs/*.md plus qmd's private home
//
// A rebuild fills a new generation and switches state.json to it only when
// it is complete, so a failed rebuild leaves the previous index usable.
package recordindex

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/recordsvc"
	"github.com/fluxinc/my-cli/internal/safefs"
)

// Format names how records are rendered into documents. Changing the
// rendering means changing this, which forces a rebuild.
const Format = "fleet-md/v1"

// Source identifies where an index's records come from. A different
// endpoint or format is a different source, with its own cursor and corpus.
type Source struct {
	Manifest  string `json:"manifest"`
	ServiceID string `json:"service_id"`
	BaseURL   string `json:"base_url"`
	Format    string `json:"format"`
}

// Key is the source's directory name.
func (s Source) Key() string {
	sum := sha256.Sum256([]byte(strings.Join([]string{s.Manifest, s.ServiceID, s.BaseURL, s.Format}, "\x00")))
	return hex.EncodeToString(sum[:12])
}

// Service is what the index needs from a records-http/v1 client.
type Service interface {
	Get(ctx context.Context, id string) (recordsvc.Record, error)
	ListAfter(ctx context.Context, after string, includeArchived bool) ([]recordsvc.Record, string, error)
	Changes(ctx context.Context, cursor string) ([]recordsvc.Change, string, bool, error)
}

// Index is one source's index.
type Index struct {
	dir     string
	source  Source
	service Service
	qmd     QMD
	render  func(recordsvc.Record) string
}

type document struct {
	File        string `json:"file"`
	Fingerprint string `json:"fingerprint"`
}

type state struct {
	Source     Source `json:"source"`
	Generation string `json:"generation"`
	Cursor     string `json:"cursor"`
	Complete   bool   `json:"complete"`
	// Pending is set when documents changed but qmd has not taken them yet;
	// the next sync re-runs the update before anything is queried.
	Pending bool                `json:"pending"`
	Docs    map[string]document `json:"docs"`
}

// Result says what a sync did.
type Result struct {
	Rebuilt bool `json:"rebuilt"`
	Added   int  `json:"added"`
	Updated int  `json:"updated"`
	Removed int  `json:"removed"`
	// Backlog is true when the change feed had more pages than the sync was
	// allowed to read; the index is behind and must not be queried.
	Backlog bool `json:"backlog"`
}

// Hit is one semantic match: a record id and its score. It carries no text
// from the index; callers show the record as the service returns it now.
type Hit struct {
	ID    string
	Score float64
}

// ErrNotBuilt means no complete index exists yet for this source.
var ErrNotBuilt = errors.New("the semantic index has not been built; run my fleet index")

// Open prepares the index directory for a source under root.
func Open(root string, source Source, service Service, q QMD, render func(recordsvc.Record) string) (*Index, error) {
	if source.Format == "" {
		source.Format = Format
	}
	dir := filepath.Join(root, source.Key())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	return &Index{dir: dir, source: source, service: service, qmd: q, render: render}, nil
}

// Sync brings the index up to date: a full rebuild when there is no
// complete index for this exact source (or rebuild is set), otherwise up to
// maxPages pages of the change feed (0 means all).
func (ix *Index) Sync(ctx context.Context, maxPages int, rebuild bool) (Result, error) {
	unlock, err := lockDir(ctx, ix.dir)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	return ix.syncLocked(ctx, maxPages, rebuild)
}

// Search syncs (bounded by maxPages) and queries in one locked step. With a
// backlog it returns no hits and Result.Backlog set; it never queries an
// index it knows is behind.
func (ix *Index) Search(ctx context.Context, text string, maxPages, limit int) ([]Hit, Result, error) {
	unlock, err := lockDir(ctx, ix.dir)
	if err != nil {
		return nil, Result{}, err
	}
	defer unlock()
	st, err := ix.load()
	if err != nil {
		return nil, Result{}, err
	}
	if st == nil || !st.Complete || st.Source != ix.source {
		return nil, Result{}, ErrNotBuilt
	}
	res, err := ix.syncLocked(ctx, maxPages, false)
	if err != nil || res.Backlog {
		return nil, res, err
	}
	if st, err = ix.load(); err != nil {
		return nil, res, err
	}
	hits, err := ix.query(ctx, st, text, limit)
	return hits, res, err
}

func (ix *Index) syncLocked(ctx context.Context, maxPages int, rebuild bool) (Result, error) {
	st, err := ix.load()
	if err != nil {
		return Result{}, err
	}
	if rebuild || st == nil || !st.Complete || st.Source != ix.source {
		return ix.rebuild(ctx)
	}
	return ix.follow(ctx, st, maxPages)
}

// rebuild fills a fresh generation: take the feed's cursor first, list every
// record by stable id, catch up from that cursor (so nothing that changed
// during the listing is missed), embed, and only then switch to it.
func (ix *Index) rebuild(ctx context.Context) (res Result, err error) {
	gen, err := ix.newGeneration()
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if err != nil {
			safefs.RemoveAll(filepath.Join(ix.dir, gen))
		}
	}()
	cursor, err := ix.drainCursor(ctx)
	if err != nil {
		return Result{}, err
	}
	st := &state{Source: ix.source, Generation: gen, Docs: map[string]document{}}
	after := ""
	for seen := map[string]bool{}; ; {
		records, next, err := ix.service.ListAfter(ctx, after, true)
		if err != nil {
			return Result{}, err
		}
		for _, rec := range records {
			if _, err := ix.store(st, rec); err != nil {
				return Result{}, err
			}
		}
		if next == "" {
			break
		}
		if seen[next] || next == after {
			return Result{}, fmt.Errorf("the service's record listing is not advancing (after %q)", next)
		}
		seen[next] = true
		after = next
	}
	st.Cursor = cursor
	catchUp, err := ix.applyFeed(ctx, st, 0)
	if err != nil {
		return Result{}, err
	}
	if err := ix.qmd.Build(ctx, ix.genDir(gen)); err != nil {
		return Result{}, fmt.Errorf("building the semantic index: %w", err)
	}
	st.Complete = true
	st.Pending = false
	if err := ix.save(st); err != nil {
		return Result{}, err
	}
	ix.removeOtherGenerations(gen)
	return Result{Rebuilt: true, Added: len(st.Docs), Updated: catchUp.Updated, Removed: catchUp.Removed}, nil
}

// follow applies up to maxPages of the change feed to the live generation
// and re-embeds if anything changed.
func (ix *Index) follow(ctx context.Context, st *state, maxPages int) (Result, error) {
	res, err := ix.applyFeed(ctx, st, maxPages)
	if err != nil {
		return res, err
	}
	if st.Pending {
		if err := ix.qmd.Update(ctx, ix.genDir(st.Generation)); err != nil {
			// The documents changed but the index didn't take them: say so
			// rather than answer from the old vectors. Pending stays set.
			return res, fmt.Errorf("updating the semantic index: %w", err)
		}
		st.Pending = false
		if err := ix.save(st); err != nil {
			return res, err
		}
	}
	return res, nil
}

// applyFeed reads change pages from st.Cursor. Each page is applied whole
// and the cursor saved after it, so an interrupted sync resumes there.
func (ix *Index) applyFeed(ctx context.Context, st *state, maxPages int) (Result, error) {
	var res Result
	for pages := 0; ; pages++ {
		if maxPages > 0 && pages == maxPages {
			res.Backlog = true
			return res, nil
		}
		changes, next, more, err := ix.service.Changes(ctx, st.Cursor)
		if err != nil {
			return res, err
		}
		before := res
		for _, change := range dedupe(changes) {
			if err := ix.applyChange(ctx, st, change, &res); err != nil {
				// Some documents of this page may already be rewritten. Record
				// them (their fingerprints now match the files) without moving
				// the cursor, so the retried page compares against what is on
				// disk and rewrites anything that has changed back since.
				if st.Complete {
					st.Pending = true
					if saveErr := ix.save(st); saveErr != nil {
						return res, errors.Join(err, saveErr)
					}
				}
				return res, err
			}
		}
		if res != before {
			st.Pending = true
		}
		if more && next == st.Cursor {
			return res, fmt.Errorf("the service's change feed is not advancing (cursor %q)", next)
		}
		st.Cursor = next
		if st.Complete {
			if err := ix.save(st); err != nil {
				return res, err
			}
		}
		if !more {
			return res, nil
		}
	}
}

func (ix *Index) applyChange(ctx context.Context, st *state, change recordsvc.Change, res *Result) error {
	if change.Op == "tombstone" {
		if ix.remove(st, change.ID) {
			res.Removed++
		}
		return nil
	}
	rec, err := ix.service.Get(ctx, change.ID)
	var apiErr *recordsvc.Error
	if errors.As(err, &apiErr) && apiErr.NotFound() {
		if ix.remove(st, change.ID) {
			res.Removed++
		}
		return nil
	}
	if err != nil {
		return err
	}
	if rec["archived"] == true {
		if ix.remove(st, change.ID) {
			res.Removed++
		}
		return nil
	}
	_, existed := st.Docs[change.ID]
	changed, err := ix.store(st, rec)
	if err != nil || !changed {
		return err
	}
	if existed {
		res.Updated++
	} else {
		res.Added++
	}
	return nil
}

// store writes a record's document when its fingerprint changed. Archived
// records are kept out of the index.
func (ix *Index) store(st *state, rec recordsvc.Record) (bool, error) {
	id := rec.ID()
	if id == "" {
		return false, errors.New("the service returned a record without an id")
	}
	if rec["archived"] == true {
		return ix.remove(st, id), nil
	}
	fingerprint, err := Fingerprint(rec)
	if err != nil {
		return false, err
	}
	if doc, ok := st.Docs[id]; ok && doc.Fingerprint == fingerprint {
		return false, nil
	}
	file := fileFor(id)
	if st.Complete {
		// Write ahead: record that this document is being rewritten (an
		// empty fingerprint matches nothing) before touching it, so if the
		// process dies after the rewrite but before the page's checkpoint,
		// the next run rewrites it from the service rather than trusting
		// the file.
		st.Docs[id] = document{File: file}
		st.Pending = true
		if err := ix.save(st); err != nil {
			return false, err
		}
	}
	body := "---\nrecord_id: " + quoteYAML(id) + "\n---\n\n" + ix.render(rec)
	if err := writePrivate(filepath.Join(ix.genDir(st.Generation), "docs", file), []byte(body)); err != nil {
		return false, err
	}
	st.Docs[id] = document{File: file, Fingerprint: fingerprint}
	return true, nil
}

func (ix *Index) remove(st *state, id string) bool {
	doc, ok := st.Docs[id]
	if !ok {
		return false
	}
	os.Remove(filepath.Join(ix.genDir(st.Generation), "docs", doc.File))
	delete(st.Docs, id)
	return true
}

func (ix *Index) query(ctx context.Context, st *state, text string, limit int) ([]Hit, error) {
	results, err := ix.qmd.Query(ctx, ix.genDir(st.Generation), text, limit)
	if err != nil {
		return nil, err
	}
	byFile := map[string]string{}
	for id, doc := range st.Docs {
		byFile[doc.File] = id
	}
	var hits []Hit
	seen := map[string]bool{}
	for _, r := range results {
		id, ok := byFile[r.File]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		hits = append(hits, Hit{ID: id, Score: r.Score})
	}
	return hits, nil
}

// drainCursor reads the feed to its end and returns the cursor there.
func (ix *Index) drainCursor(ctx context.Context) (string, error) {
	cursor := ""
	for {
		_, next, more, err := ix.service.Changes(ctx, cursor)
		if err != nil {
			return "", err
		}
		if more && next == cursor {
			return "", fmt.Errorf("the service's change feed is not advancing (cursor %q)", next)
		}
		cursor = next
		if !more {
			return cursor, nil
		}
	}
}

// Fingerprint is a hash of the whole record as the service returned it, so a
// change to anything it shows (a parent's field, a note) re-embeds it, not
// only a change to its own revision.
func Fingerprint(rec recordsvc.Record) (string, error) {
	canonical, err := json.Marshal(map[string]any(rec)) // map keys marshal sorted
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// fileFor names a record's document. Ids are opaque, so never a filename.
func fileFor(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16]) + ".md"
}

func dedupe(changes []recordsvc.Change) []recordsvc.Change {
	last := map[string]int{}
	for i, c := range changes {
		last[c.ID] = i
	}
	var out []recordsvc.Change
	for i, c := range changes {
		if last[c.ID] == i {
			out = append(out, c)
		}
	}
	return out
}

func quoteYAML(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func (ix *Index) genDir(gen string) string { return filepath.Join(ix.dir, gen) }

func (ix *Index) newGeneration() (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	gen := "gen-" + time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
	if err := os.MkdirAll(filepath.Join(ix.dir, gen, "docs"), 0o700); err != nil {
		return "", err
	}
	return gen, nil
}

func (ix *Index) removeOtherGenerations(keep string) {
	entries, _ := os.ReadDir(ix.dir)
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "gen-") && entry.Name() != keep {
			safefs.RemoveAll(filepath.Join(ix.dir, entry.Name()))
		}
	}
}

func (ix *Index) load() (*state, error) {
	data, err := os.ReadFile(filepath.Join(ix.dir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, nil // unreadable state: rebuild
	}
	if st.Docs == nil {
		st.Docs = map[string]document{}
	}
	return &st, nil
}

func (ix *Index) save(st *state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(ix.dir, "state.json"), data)
}

// writePrivate writes a 0600 file atomically (temp file, then rename).
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Documents lists the ids currently indexed (for tests and status).
func (ix *Index) Documents() ([]string, error) {
	st, err := ix.load()
	if err != nil || st == nil {
		return nil, err
	}
	ids := make([]string, 0, len(st.Docs))
	for id := range st.Docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
