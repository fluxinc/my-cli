// Package attempt records local evidence that a harness was launched.
//
// An attempt is an observation, not a claim. It says that a launcher process
// started a harness in a directory, what the nearby Git checkouts looked like
// before and after, and how the launcher's child ended if that was observed.
// It never says that work was finished, published or tracked by any service,
// and a clean Git tree says nothing about effects outside Git.
//
// Liveness is one kernel lock per attempt. The launcher holds an exclusive
// lock on the attempt's lock file for its whole life; the kernel releases it
// when the launcher dies. A reader that cannot take the lock knows the
// launcher is alive and leaves the record alone. A reader that takes it is
// the only writer.
package attempt

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SchemaVersion is the on-disk and JSON output version of Record.
const SchemaVersion = 1

// ErrUnsupported reports that attempt capture has no lock implementation on
// this platform. Callers launch normally and record nothing.
var ErrUnsupported = errors.New("attempt capture is not supported on this platform")

const (
	recordName = "record.json"
	lockName   = "lock"
	tempPrefix = recordName + ".tmp-"
)

// Closure reasons. A record with no closure reason has not been finalized.
const (
	ClosureExited       = "exited"
	ClosureStartFailed  = "start_failed"
	ClosureLauncherLost = "launcher_lost"
)

// Child states, set only when a launcher was found dead without an observed
// exit. Unknown is the answer whenever identity cannot be established.
const (
	ChildPossiblyRunning = "possibly_running"
	ChildNotFound        = "not_found"
	ChildUnknown         = "unknown"
)

// Entry states derived when listing.
const (
	StateStarting   = "starting"
	StateRunning    = "running"
	StateClosed     = "closed"
	StateUnclosed   = "unclosed"
	StateIncomplete = "incomplete"
	StateUnreadable = "unreadable"
)

// Record is one launch observation.
type Record struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	OSUser        string `json:"os_user,omitempty"`
	Host          string `json:"host,omitempty"`
	Harness       string `json:"harness"`
	Umbrella      string `json:"umbrella"`
	WorkDir       string `json:"work_dir"`
	SessionID     string `json:"session_id,omitempty"`
	// Goal is the opaque string given to --goal. It grants and binds nothing.
	Goal      string      `json:"goal,omitempty"`
	StartedAt string      `json:"started_at"`
	Launcher  ProcessRef  `json:"launcher"`
	Child     *ProcessRef `json:"child,omitempty"`

	Before *Observation `json:"before,omitempty"`
	// After is collected when the launcher saw its child end, or by a
	// reconciler that found the launcher gone (Kind says which).
	After *Observation `json:"after,omitempty"`

	// ObservedExit is set only when the launcher itself saw the child end.
	ObservedExit  *Exit  `json:"observed_exit,omitempty"`
	ClosureReason string `json:"closure_reason,omitempty"`
	StartError    string `json:"start_error,omitempty"`
	ReconciledAt  string `json:"reconciled_at,omitempty"`
	ChildState    string `json:"child_state,omitempty"`

	// LocalChangeObserved is true on positive evidence, false only when both
	// observations succeeded and showed none, and absent when unknown. It is
	// an observation of shared checkouts, not attribution to this launch, and
	// it is silent about effects outside Git.
	LocalChangeObserved *bool    `json:"local_change_observed,omitempty"`
	Errors              []string `json:"errors,omitempty"`
}

// ProcessRef identifies a process by pid and start time on the record's host.
type ProcessRef struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at,omitempty"`
}

// Exit is a child exit the launcher observed.
type Exit struct {
	Code int    `json:"code"`
	At   string `json:"at"`
}

// Entry is one attempt directory as seen by a reader.
type Entry struct {
	ID     string  `json:"id"`
	State  string  `json:"state"`
	Record *Record `json:"record,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// Dir returns the attempts directory for an umbrella root.
func Dir(root string) string {
	return filepath.Join(root, ".my-cli", "attempts")
}

// Handle is a live attempt owned by this launcher process.
type Handle struct {
	dir  string
	lock *os.File
	rec  Record
}

// Begin creates an attempt and publishes its initial record. The lock is
// taken before the record exists, so a reader never sees an unlocked record
// that its creator is still about to own.
func Begin(root string, rec Record) (*Handle, error) {
	if !lockSupported {
		return nil, ErrUnsupported
	}
	base := Dir(root)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	var dir string
	for range 5 {
		id, err := newID(time.Now())
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(base, id)
		err = os.Mkdir(dir, 0o700)
		if err == nil {
			rec.ID = id
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		dir = ""
	}
	if dir == "" {
		return nil, errors.New("could not allocate a unique attempt id")
	}
	lock, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	held, err := tryLock(lock)
	if err != nil || !held {
		lock.Close()
		if err == nil {
			err = errors.New("new attempt lock is already held")
		}
		return nil, err
	}
	rec.SchemaVersion = SchemaVersion
	h := &Handle{dir: dir, lock: lock, rec: rec}
	if err := writeRecord(dir, h.rec); err != nil {
		h.Close()
		return nil, err
	}
	return h, nil
}

// Record returns the handle's current record.
func (h *Handle) Record() Record { return h.rec }

// Update changes the record and replaces the file atomically. On a write
// failure the in-memory record keeps the change so a later Update retries it.
func (h *Handle) Update(change func(*Record)) error {
	change(&h.rec)
	return writeRecord(h.dir, h.rec)
}

// Close releases the launcher's lock. The lock file itself is never removed.
func (h *Handle) Close() {
	if h.lock != nil {
		unlock(h.lock)
		h.lock.Close()
		h.lock = nil
	}
}

// ListOptions controls reading and reconciliation.
type ListOptions struct {
	// Reconcile finalizes records whose launcher is gone and that have no
	// closure reason. It never touches a record whose lock is held.
	Reconcile bool
	// Observe collects the reconcile-time observation for the given repos.
	Observe func(repos []RepoTarget) *Observation
	// Probe reports a recorded child's state. Nil means unknown.
	Probe func(ref ProcessRef) string
	// Host is this machine's name; a record from another host is never probed.
	Host string
	Now  func() time.Time
	// IncompleteAfter is how old a record-less directory must be before it is
	// reported instead of skipped. Zero means one minute.
	IncompleteAfter time.Duration
}

// List reads every attempt under root, oldest first. A missing directory is
// an empty list. On platforms without lock support it returns ErrUnsupported.
func List(root string, opts ListOptions) ([]Entry, error) {
	if !lockSupported {
		return nil, ErrUnsupported
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.IncompleteAfter == 0 {
		opts.IncompleteAfter = time.Minute
	}
	items, err := os.ReadDir(Dir(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, item := range items {
		if !item.IsDir() {
			continue
		}
		if entry, ok := readEntry(filepath.Join(Dir(root), item.Name()), item.Name(), opts); ok {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

func readEntry(dir, id string, opts ListOptions) (Entry, bool) {
	entry := Entry{ID: id}
	// Open without O_CREATE: a reader never makes a lock for someone else.
	lock, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		// Its creator may be about to make the lock. Skip while it is new.
		return incomplete(dir, entry, opts)
	}
	if err != nil {
		entry.State, entry.Error = StateUnreadable, err.Error()
		return entry, true
	}
	defer lock.Close()
	held, err := tryLock(lock)
	if err != nil {
		entry.State, entry.Error = StateUnreadable, err.Error()
		return entry, true
	}
	if !held {
		// The launcher is alive. Read its last published record, change nothing.
		rec, err := readRecord(dir)
		switch {
		case errors.Is(err, os.ErrNotExist):
			entry.State = StateStarting
		case err != nil:
			entry.State, entry.Error = StateRunning, err.Error()
		default:
			entry.State, entry.Record = StateRunning, &rec
		}
		return entry, true
	}
	defer unlock(lock)
	// Read only now, under the lock, so a finalizer's last write is seen.
	rec, err := readRecord(dir)
	if errors.Is(err, os.ErrNotExist) {
		return incomplete(dir, entry, opts)
	}
	if err != nil {
		entry.State, entry.Error = StateUnreadable, err.Error()
		return entry, true
	}
	if rec.ClosureReason == "" && opts.Reconcile {
		reconcile(&rec, opts)
		if err := writeRecord(dir, rec); err != nil {
			entry.Error = "reconcile was not saved: " + err.Error()
			rec.ClosureReason, rec.ReconciledAt = "", ""
		}
	}
	entry.Record = &rec
	switch rec.ClosureReason {
	case "":
		entry.State = StateUnclosed
	case ClosureLauncherLost:
		entry.State = StateUnclosed
	default:
		entry.State = StateClosed
	}
	return entry, true
}

func incomplete(dir string, entry Entry, opts ListOptions) (Entry, bool) {
	info, err := os.Stat(dir)
	if err != nil || opts.Now().Sub(info.ModTime()) < opts.IncompleteAfter {
		return entry, false
	}
	entry.State = StateIncomplete
	entry.Error = "attempt directory has no record; its launcher stopped before publishing one"
	return entry, true
}

// reconcile finalizes a record whose launcher is gone. It never invents an
// exit: observed_exit stays as the launcher left it.
func reconcile(rec *Record, opts ListOptions) {
	rec.ClosureReason = ClosureLauncherLost
	rec.ReconciledAt = opts.Now().UTC().Format(time.RFC3339)
	if rec.ObservedExit == nil {
		rec.ChildState = ChildUnknown
		if rec.Child != nil && rec.Child.StartedAt != "" && rec.Host != "" && rec.Host == opts.Host && opts.Probe != nil {
			rec.ChildState = opts.Probe(*rec.Child)
		}
	}
	if rec.After == nil && opts.Observe != nil && rec.Before != nil && rec.Host == opts.Host {
		var targets []RepoTarget
		for _, repo := range rec.Before.Repos {
			targets = append(targets, RepoTarget{Label: repo.Label, Path: repo.Path})
		}
		if after := opts.Observe(targets); after != nil {
			after.Kind = KindAtReconcile
			rec.After = after
		}
	}
	rec.LocalChangeObserved = LocalChange(rec.Before, rec.After)
}

func readRecord(dir string) (Record, error) {
	var rec Record
	data, err := os.ReadFile(filepath.Join(dir, recordName))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, fmt.Errorf("record is not valid JSON: %w", err)
	}
	if rec.SchemaVersion > SchemaVersion {
		return rec, fmt.Errorf("record has schema_version %d; this my reads up to %d", rec.SchemaVersion, SchemaVersion)
	}
	return rec, nil
}

// writeRecord replaces record.json through a temp file in the same directory.
// A leftover temp file from an interrupted write is ignored by readers.
func writeRecord(dir string, rec Record) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	name := temp.Name()
	_, err = temp.Write(append(data, '\n'))
	if err == nil {
		err = temp.Chmod(0o600)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(dir, recordName))
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

func newID(now time.Time) (string, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return "", err
	}
	// Nanoseconds keep ids in launch order, so a listing sorted by id is
	// oldest first even for launches within the same second.
	now = now.UTC()
	return fmt.Sprintf("%s.%09dZ-%s", now.Format("20060102T150405"), now.Nanosecond(), hex.EncodeToString(suffix)), nil
}
