//go:build !windows

package attempt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as a helper process so lock tests use competing
// processes: a flock is per open file description, and goroutines in one
// process would not prove anything about a launcher that dies.
func TestMain(m *testing.M) {
	if mode := os.Getenv("ATTEMPT_HELPER"); mode != "" {
		os.Exit(helperMain(mode, os.Getenv("ATTEMPT_ROOT")))
	}
	os.Exit(m.Run())
}

func helperMain(mode, root string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "helper:", err)
		return 2
	}
	block := func() int {
		bufio.NewReader(os.Stdin).ReadString('\n') // until killed or stdin closes
		return 0
	}
	switch mode {
	case "hold", "hold-live-child", "hold-dead-child", "hold-unidentified-child":
		host, _ := os.Hostname()
		h, err := Begin(root, Record{Harness: "codex", Umbrella: root, WorkDir: root, Host: host, StartedAt: "2026-01-01T00:00:00Z", Launcher: ProcessRef{PID: os.Getpid()}})
		if err != nil {
			return fail(err)
		}
		childPID := 0
		if mode != "hold" {
			child := exec.Command("sleep", "300")
			if err := child.Start(); err != nil {
				return fail(err)
			}
			childPID = child.Process.Pid
			start, found, err := ProcessStart(childPID)
			if err != nil || !found {
				return fail(fmt.Errorf("child start: found=%v err=%v", found, err))
			}
			if mode == "hold-dead-child" {
				child.Process.Kill()
				child.Wait()
			}
			if mode == "hold-unidentified-child" {
				start = "" // the Start -> persist identity gap
			}
			if err := h.Update(func(r *Record) { r.Child = &ProcessRef{PID: childPID, StartedAt: start} }); err != nil {
				return fail(err)
			}
		}
		fmt.Printf("ready %s %d\n", h.Record().ID, childPID)
		return block()
	case "lock-only":
		dir := filepath.Join(Dir(root), "20260101T000000Z-lockonly")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fail(err)
		}
		lock, err := os.OpenFile(filepath.Join(dir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return fail(err)
		}
		if held, err := tryLock(lock); err != nil || !held {
			return fail(fmt.Errorf("lock: held=%v err=%v", held, err))
		}
		fmt.Println("ready lockonly 0")
		return block()
	case "reconcile":
		host, _ := os.Hostname()
		entries, err := List(root, ListOptions{Reconcile: true, Probe: ProbeChild, Host: host})
		if err != nil {
			return fail(err)
		}
		json.NewEncoder(os.Stdout).Encode(entries)
		return 0
	}
	return fail(fmt.Errorf("unknown mode %q", mode))
}

type helper struct {
	cmd   *exec.Cmd
	id    string
	child int
}

func startHelper(t *testing.T, mode, root string) *helper {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ATTEMPT_HELPER="+mode, "ATTEMPT_ROOT="+root)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &helper{cmd: cmd}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
		if h.child > 0 {
			syscall.Kill(h.child, syscall.SIGKILL)
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("helper %s did not report ready: %v", mode, err)
	}
	parts := strings.Fields(line)
	if len(parts) != 3 || parts[0] != "ready" {
		t.Fatalf("helper %s said %q", mode, line)
	}
	h.id = parts[1]
	h.child, _ = strconv.Atoi(parts[2])
	return h
}

// kill ends the helper the way a crashed launcher ends: no cleanup runs.
func (h *helper) kill(t *testing.T) {
	t.Helper()
	if err := h.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	h.cmd.Wait()
}

func listHere(t *testing.T, root string, reconcile bool) []Entry {
	t.Helper()
	host, _ := os.Hostname()
	entries, err := List(root, ListOptions{Reconcile: reconcile, Probe: ProbeChild, Host: host})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func only(t *testing.T, entries []Entry) Entry {
	t.Helper()
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(entries), entries)
	}
	return entries[0]
}

func TestBeginPublishesPrivateRecordUnderLock(t *testing.T) {
	root := t.TempDir()
	h, err := Begin(root, Record{Harness: "codex", Umbrella: root, WorkDir: root, StartedAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	dir := filepath.Join(Dir(root), h.Record().ID)
	for path, want := range map[string]os.FileMode{Dir(root): 0o700, dir: 0o700, filepath.Join(dir, lockName): 0o600, filepath.Join(dir, recordName): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
	rec, err := readRecord(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rec.SchemaVersion != SchemaVersion || rec.ID != h.Record().ID || rec.ClosureReason != "" {
		t.Fatalf("initial record = %+v", rec)
	}
}

func TestListLeavesLiveLaunchesAloneAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	first := startHelper(t, "hold", root)
	second := startHelper(t, "hold", root)
	before, _ := os.ReadFile(filepath.Join(Dir(root), first.id, recordName))

	entries := listHere(t, root, true)
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %+v", entries)
	}
	for _, entry := range entries {
		if entry.State != StateRunning || entry.Record == nil || entry.Record.ClosureReason != "" || entry.Record.ReconciledAt != "" {
			t.Fatalf("live launch was not left running: %+v", entry)
		}
	}
	after, _ := os.ReadFile(filepath.Join(Dir(root), first.id, recordName))
	if string(before) != string(after) {
		t.Fatal("a reader rewrote a live launch's record")
	}

	// Only the launcher that died is reconciled; the other stays running.
	first.kill(t)
	states := map[string]string{}
	for _, entry := range listHere(t, root, true) {
		states[entry.ID] = entry.State
	}
	if states[first.id] != StateUnclosed || states[second.id] != StateRunning {
		t.Fatalf("states = %v", states)
	}
}

func TestReconcileLostLauncherNeverInventsAnExit(t *testing.T) {
	root := t.TempDir()
	h := startHelper(t, "hold", root)
	h.kill(t)

	if got := only(t, listHere(t, root, false)); got.State != StateUnclosed || got.Record.ClosureReason != "" {
		t.Fatalf("a read without reconcile changed the record: %+v", got.Record)
	}
	rec := only(t, listHere(t, root, true)).Record
	if rec.ClosureReason != ClosureLauncherLost || rec.ReconciledAt == "" {
		t.Fatalf("not reconciled: %+v", rec)
	}
	if rec.ObservedExit != nil {
		t.Fatalf("reconcile invented an exit: %+v", rec.ObservedExit)
	}
	if rec.ChildState != ChildUnknown {
		t.Fatalf("no child was recorded, so child_state must be unknown, got %q", rec.ChildState)
	}
	if rec.LocalChangeObserved != nil {
		t.Fatal("local_change_observed must be absent without observations")
	}
	// A second reconcile is a no-op.
	again := only(t, listHere(t, root, true)).Record
	if again.ReconciledAt != rec.ReconciledAt {
		t.Fatal("a closed record was reconciled twice")
	}
}

func TestReconcileChildStates(t *testing.T) {
	for mode, want := range map[string]string{
		"hold-live-child":         ChildPossiblyRunning, // parent killed while the child continues
		"hold-dead-child":         ChildNotFound,        // child ended, launcher killed before closing
		"hold-unidentified-child": ChildUnknown,         // pid without a start time proves nothing
	} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			h := startHelper(t, mode, root)
			h.kill(t)
			rec := only(t, listHere(t, root, true)).Record
			if rec.ChildState != want {
				t.Fatalf("child_state = %q, want %q", rec.ChildState, want)
			}
			if rec.ObservedExit != nil {
				t.Fatal("reconcile invented an exit")
			}
		})
	}
}

func TestReconcileNeverProbesAnotherHostsProcessTable(t *testing.T) {
	root := t.TempDir()
	h := startHelper(t, "hold-live-child", root)
	h.kill(t)
	probed := false
	entries, err := List(root, ListOptions{Reconcile: true, Host: "some-other-host", Probe: func(ProcessRef) string {
		probed = true
		return ChildPossiblyRunning
	}})
	if err != nil {
		t.Fatal(err)
	}
	if rec := only(t, entries).Record; probed || rec.ChildState != ChildUnknown {
		t.Fatalf("probed=%v child_state=%q", probed, rec.ChildState)
	}
}

func TestInitialRecordAndLockRace(t *testing.T) {
	root := t.TempDir()

	// Directory made, lock not yet created: skipped while new, never repaired.
	bare := filepath.Join(Dir(root), "20260101T000000Z-bare")
	if err := os.MkdirAll(bare, 0o700); err != nil {
		t.Fatal(err)
	}
	if entries := listHere(t, root, true); len(entries) != 0 {
		t.Fatalf("a new record-less directory was reported: %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(bare, lockName)); !os.IsNotExist(err) {
		t.Fatal("a reader created a lock in someone else's attempt directory")
	}

	// Lock held, record not yet published: starting, untouched.
	startHelper(t, "lock-only", root)
	var starting *Entry
	for _, entry := range listHere(t, root, true) {
		if entry.ID == "20260101T000000Z-lockonly" {
			starting = &entry
		}
	}
	if starting == nil || starting.State != StateStarting || starting.Record != nil {
		t.Fatalf("locked record-less attempt = %+v", starting)
	}

	// Once old, an abandoned record-less directory surfaces instead of vanishing.
	entries, err := List(root, ListOptions{Reconcile: true, Now: func() time.Time { return time.Now().Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.ID == "20260101T000000Z-bare" {
			found = entry.State == StateIncomplete && entry.Error != ""
		}
	}
	if !found {
		t.Fatalf("an old record-less directory was not surfaced: %+v", entries)
	}
}

func TestInterruptedWriteAndCorruptRecordSurface(t *testing.T) {
	root := t.TempDir()
	h, err := Begin(root, Record{Harness: "codex", Umbrella: root, WorkDir: root, StartedAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(Dir(root), h.Record().ID)
	// A write interrupted before rename leaves a temp file behind.
	if err := os.WriteFile(filepath.Join(dir, tempPrefix+"leftover"), []byte("{\"half\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Close()
	if got := only(t, listHere(t, root, false)); got.Error != "" || got.Record == nil {
		t.Fatalf("a leftover temp file broke the read: %+v", got)
	}

	if err := os.WriteFile(filepath.Join(dir, recordName), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := only(t, listHere(t, root, true))
	if got.State != StateUnreadable || got.Error == "" {
		t.Fatalf("a corrupt record did not surface: %+v", got)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, recordName)); string(data) != "not json" {
		t.Fatal("reconcile overwrote a record it could not read")
	}
}

func TestConcurrentReconcilersAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	var ids []string
	for range 4 {
		h := startHelper(t, "hold", root)
		ids = append(ids, h.id)
		h.kill(t)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), "ATTEMPT_HELPER=reconcile", "ATTEMPT_ROOT="+root)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("reconciler failed: %v\n%s", err, out)
			}
		}()
	}
	wg.Wait()
	entries := listHere(t, root, false)
	if len(entries) != len(ids) {
		t.Fatalf("want %d entries, got %d", len(ids), len(entries))
	}
	for _, entry := range entries {
		if entry.Error != "" || entry.Record == nil || entry.Record.ClosureReason != ClosureLauncherLost || entry.Record.ReconciledAt == "" {
			t.Fatalf("after concurrent reconcile: %+v", entry)
		}
		leftovers, _ := filepath.Glob(filepath.Join(Dir(root), entry.ID, tempPrefix+"*"))
		if len(leftovers) != 0 {
			t.Fatalf("temp files left behind: %v", leftovers)
		}
	}
}

func TestFinalizerWriteIsWhatAReconcilerSees(t *testing.T) {
	root := t.TempDir()
	h, err := Begin(root, Record{Harness: "codex", Umbrella: root, WorkDir: root, StartedAt: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Update(func(r *Record) {
		r.ObservedExit = &Exit{Code: 3, At: "2026-01-01T00:01:00Z"}
		r.ClosureReason = ClosureExited
	}); err != nil {
		t.Fatal(err)
	}
	h.Close()
	got := only(t, listHere(t, root, true))
	if got.State != StateClosed || got.Record.ClosureReason != ClosureExited || got.Record.ObservedExit.Code != 3 || got.Record.ReconciledAt != "" {
		t.Fatalf("a finalized record was reconciled: %+v", got.Record)
	}
}

func TestProcessStartNotFoundOnlyWhenVerified(t *testing.T) {
	// A process that certainly existed and is certainly gone.
	child := exec.Command("true")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	pid := child.Process.Pid
	child.Wait()
	if _, found, err := ProcessStart(pid); found || err != nil {
		t.Fatalf("reaped pid %d: found=%v err=%v, want verified not found", pid, found, err)
	}
	if start, found, err := ProcessStart(os.Getpid()); !found || err != nil || start == "" {
		t.Fatalf("own pid: start=%q found=%v err=%v", start, found, err)
	}
}
