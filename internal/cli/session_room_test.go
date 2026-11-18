package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/attempt"
	"github.com/fluxinc/my-cli/internal/worksession"
)

func TestParseTTRoomEnsureAcceptsOnlyCreatedOrExistsWithAbsolutePin(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "sessions", "s1")
	for _, tc := range []struct {
		name    string
		out     string
		pin     string
		warning string
		fail    string
	}{
		{name: "created", out: `{"status":"created","room_id":"r","room_path":"` + jsonPath(abs) + `","pin":"` + jsonPath(abs) + `"}`, pin: abs},
		{name: "pin differs from room_path", out: `{"status":"exists","room_path":"/elsewhere/main","pin":"` + jsonPath(abs) + `"}`, pin: abs},
		{name: "string warning accepted", out: `{"status":"exists","pin":"` + jsonPath(abs) + `","warning":"declared name differs"}`, pin: abs, warning: "declared name differs"},
		{name: "object warning accepted", out: `{"status":"exists","pin":"` + jsonPath(abs) + `","warning":{"code":"w"}}`, pin: abs, warning: `{"code":"w"}`},
		{name: "bad JSON", out: `not json`, fail: "unreadable"},
		{name: "unknown status", out: `{"status":"opened","pin":"` + jsonPath(abs) + `"}`, fail: `status "opened"`},
		{name: "missing pin", out: `{"status":"created","room_path":"` + jsonPath(abs) + `"}`, fail: "no pin"},
		{name: "relative pin", out: `{"status":"created","pin":"sessions/s1"}`, fail: "relative pin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			room, err := parseTTRoomEnsure([]byte(tc.out))
			if tc.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fail) {
					t.Fatalf("err = %v, want %q", err, tc.fail)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if room.Pin != tc.pin || room.Warning != tc.warning {
				t.Fatalf("room = %#v, want pin %q warning %q", room, tc.pin, tc.warning)
			}
		})
	}
}

func TestSessionLaunchEnvironmentPinsOnlyAVerifiedRoom(t *testing.T) {
	inherited := []string{"PATH=/bin", "TT_ROOM=/umbrella", "MYCLI_SESSION_ID=parent", "MYCLI_SESSION_PATH=/parent"}
	session := &worksession.Session{ID: "s1", Path: "/u/sessions/s1"}

	boundEnv := sessionLaunchEnvironment(inherited, session, sessionRoom{Pin: "/u/sessions/s1", Status: "exists"})
	if countEnv(boundEnv, "TT_ROOM") != 1 || countEnv(boundEnv, "MYCLI_SESSION_ID") != 1 {
		t.Fatalf("bound env duplicates keys: %#v", boundEnv)
	}
	bound := envMap(boundEnv)
	if bound["TT_ROOM"] != "/u/sessions/s1" || bound["MYCLI_SESSION_ID"] != "s1" || bound["MYCLI_SESSION_PATH"] != "/u/sessions/s1" || bound["PATH"] != "/bin" {
		t.Fatalf("bound env = %#v", bound)
	}

	failed := sessionLaunchEnvironment(inherited, session, sessionRoom{Failure: "tt room ensure failed"})
	if got := envMap(failed); got["TT_ROOM"] != "/umbrella" || got["MYCLI_SESSION_ID"] != "s1" {
		t.Fatalf("failed env = %#v", got)
	}
	if countEnv(failed, "TT_ROOM") != 1 || countEnv(failed, "MYCLI_SESSION_ID") != 1 {
		t.Fatalf("failed env duplicates keys: %#v", failed)
	}

	outside := envMap(sessionLaunchEnvironment(inherited, nil, sessionRoom{}))
	if outside["TT_ROOM"] != "/umbrella" {
		t.Fatalf("non-session launch changed TT_ROOM: %#v", outside)
	}
	if _, ok := outside["MYCLI_SESSION_ID"]; ok {
		t.Fatalf("non-session launch kept a parent session id: %#v", outside)
	}
	if _, ok := outside["MYCLI_SESSION_PATH"]; ok {
		t.Fatalf("non-session launch kept a parent session path: %#v", outside)
	}
}

// fakeTT scripts `tt room ensure` replies and records each call.
type fakeTT struct {
	replies []fakeTTReply
	calls   []fakeTTCall
}

type fakeTTReply struct {
	out   string
	err   error
	block bool
}

type fakeTTCall struct {
	dir string
	env []string
}

func (f *fakeTT) ensure(ctx context.Context, dir string, env []string) ([]byte, error) {
	f.calls = append(f.calls, fakeTTCall{dir: dir, env: append([]string(nil), env...)})
	if len(f.replies) == 0 {
		return nil, errors.New("fake tt: unexpected call")
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	if reply.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []byte(reply.out), reply.err
}

func ensureJSON(status, pin string) string {
	return `{"status":"` + status + `","room_id":"id-1","room_path":"` + jsonPath(pin) + `","pin":"` + jsonPath(pin) + `","name":"n","declaration":"d"}`
}

type sessionLaunch struct {
	raw    []string
	env    map[string]string
	dir    string
	stderr string
}

func startTestSession(t *testing.T, home string) sessionStartCommandReport {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "session", "start", "--slug", "room", "--home", home, "--json"}); err != nil {
		t.Fatalf("session start: %v\nstderr: %s", err, stderr.String())
	}
	var report sessionStartCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	return report
}

func runSessionLaunch(t *testing.T, tt *fakeTT, timeout time.Duration, args ...string) sessionLaunch {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var got sessionLaunch
	a := app{
		stdout:             &stdout,
		stderr:             &stderr,
		lookPath:           func(name string) (string, error) { return "/test/bin/" + name, nil },
		sessionRoomTimeout: timeout,
		execHarnessEnvironment: func(_ string, _ []string, dir string, env []string) error {
			got.dir = dir
			got.raw = append([]string(nil), env...)
			got.env = envMap(env)
			return nil
		},
	}
	if tt != nil {
		a.ttRoomEnsure = tt.ensure
	}
	if err := a.run(append([]string{"my"}, args...)); err != nil {
		t.Fatalf("my %s: %v\nstderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	got.stderr = stderr.String()
	return got
}

func TestSessionJoinPinsVerifiedRoomOnEveryLaunch(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	session := startTestSession(t, home)
	t.Setenv("TT_ROOM", umbrellaRoot)

	tt := &fakeTT{replies: []fakeTTReply{{out: ensureJSON("created", session.Path)}, {out: ensureJSON("exists", session.Path)}}}
	first := runSessionLaunch(t, tt, 0, "session", "join", session.ID, "codex", "--home", home)
	second := runSessionLaunch(t, tt, 0, "session", "join", session.ID, "claude-code", "--home", home)

	if len(tt.calls) != 2 {
		t.Fatalf("ensure calls = %d, want one per launch", len(tt.calls))
	}
	for i, call := range tt.calls {
		if call.dir != session.Path {
			t.Fatalf("call %d dir = %q, want %q", i, call.dir, session.Path)
		}
		if _, ok := envMap(call.env)["TT_ROOM"]; ok {
			t.Fatalf("call %d passed an inherited TT_ROOM to tt", i)
		}
	}
	for i, launch := range []sessionLaunch{first, second} {
		if countEnv(launch.raw, "TT_ROOM") != 1 {
			t.Fatalf("launch %d env repeats TT_ROOM", i)
		}
		if launch.dir != session.Path || launch.env["TT_ROOM"] != session.Path ||
			launch.env["MYCLI_SESSION_ID"] != session.ID || launch.env["MYCLI_SESSION_PATH"] != session.Path {
			t.Fatalf("launch %d: dir %q env TT_ROOM=%q id=%q path=%q", i, launch.dir, launch.env["TT_ROOM"], launch.env["MYCLI_SESSION_ID"], launch.env["MYCLI_SESSION_PATH"])
		}
		if !strings.Contains(launch.stderr, "room\t"+session.Path) ||
			!strings.Contains(launch.stderr, "tt chat "+shellQuote(session.Path)) ||
			!strings.Contains(launch.stderr, "replaced inherited TT_ROOM "+umbrellaRoot) {
			t.Fatalf("launch %d stderr = %q", i, launch.stderr)
		}
	}
	if !strings.Contains(first.stderr, "\tcreated") || !strings.Contains(second.stderr, "\texists") {
		t.Fatalf("status not reported: %q / %q", first.stderr, second.stderr)
	}
	guidance := readSessionAgents(t, session.Path)
	if !strings.Contains(guidance, "## Conversation Room") || !strings.Contains(guidance, "- Room: "+session.Path) {
		t.Fatalf("AGENTS.md lacks the verified room:\n%s", guidance)
	}
	if rooms := attemptRooms(t, umbrellaRoot); len(rooms) != 2 || rooms[0] != session.Path || rooms[1] != session.Path {
		t.Fatalf("launch record rooms = %#v", rooms)
	}
}

func TestSessionRoomFailurePreservesInheritedRoomAndClaimsNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reply  fakeTTReply
		reason string
	}{
		{name: "nonzero exit", reply: fakeTTReply{err: errors.New("tt room ensure failed (exit status 1): service unavailable")}, reason: "service unavailable"},
		{name: "old tt usage error", reply: fakeTTReply{err: errors.New("tt room ensure failed (exit status 2): Usage: tt room create [path]")}, reason: "Usage: tt room create"},
		{name: "bad JSON", reply: fakeTTReply{out: "Room ensured."}, reason: "unreadable"},
		{name: "unknown status", reply: fakeTTReply{out: `{"status":"pending","pin":"/abs"}`}, reason: `status "pending"`},
		{name: "relative pin", reply: fakeTTReply{out: `{"status":"created","pin":"sessions/x"}`}, reason: "relative pin"},
		{name: "missing pin", reply: fakeTTReply{out: `{"status":"created","room_path":"/abs"}`}, reason: "no pin"},
		{name: "timeout", reply: fakeTTReply{block: true}, reason: "did not answer within"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspaceRoot := setupCLIRecordWorkspace(t)
			umbrellaRoot := filepath.Dir(workspaceRoot)
			ensureCLIGuidance(t, home, umbrellaRoot)
			session := startTestSession(t, home)
			t.Setenv("TT_ROOM", umbrellaRoot)

			// A verified room first, so the failed launch must also retract it.
			tt := &fakeTT{replies: []fakeTTReply{{out: ensureJSON("created", session.Path)}, tc.reply}}
			runSessionLaunch(t, tt, 50*time.Millisecond, "session", "join", session.ID, "codex", "--home", home)
			failed := runSessionLaunch(t, tt, 50*time.Millisecond, "session", "join", session.ID, "codex", "--home", home)

			if failed.env["TT_ROOM"] != umbrellaRoot {
				t.Fatalf("TT_ROOM = %q, want inherited %q kept", failed.env["TT_ROOM"], umbrellaRoot)
			}
			if failed.env["MYCLI_SESSION_ID"] != session.ID {
				t.Fatalf("session id not exported: %#v", failed.env)
			}
			if !strings.Contains(failed.stderr, "session room not set: ") ||
				!strings.Contains(failed.stderr, tc.reason) ||
				!strings.Contains(failed.stderr, "TT_ROOM kept as "+umbrellaRoot) {
				t.Fatalf("stderr = %q", failed.stderr)
			}
			if strings.Contains(failed.stderr, "room\t") || strings.Contains(failed.stderr, "tt chat") {
				t.Fatalf("failed launch still claims a room: %q", failed.stderr)
			}
			if guidance := readSessionAgents(t, session.Path); strings.Contains(guidance, "Conversation Room") {
				t.Fatalf("stale room section kept after failed ensure:\n%s", guidance)
			}
			if rooms := attemptRooms(t, umbrellaRoot); len(rooms) != 2 || rooms[1] != "" {
				t.Fatalf("launch record rooms = %#v, want none for the failed launch", rooms)
			}
		})
	}
}

func TestSessionResumeRewritesRoomSectionFromAFreshEnsure(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	ensureCLIGuidance(t, home, filepath.Dir(workspaceRoot))
	session := startTestSession(t, home)
	unsetEnv(t, "TT_ROOM")
	tt := &fakeTT{replies: []fakeTTReply{{out: ensureJSON("created", session.Path)}, {out: "garbage"}}}
	resume := func() string {
		var stdout, stderr bytes.Buffer
		a := app{stdout: &stdout, stderr: &stderr, ttRoomEnsure: tt.ensure}
		if err := a.run([]string{"my", "session", "resume", session.ID, "--home", home}); err != nil {
			t.Fatalf("session resume: %v\nstderr: %s", err, stderr.String())
		}
		if stdout.String() != "cd "+shellQuote(session.Path)+"\n" {
			t.Fatalf("resume stdout = %q", stdout.String())
		}
		return stderr.String()
	}

	first := resume()
	if !strings.Contains(first, "room\t"+session.Path) || !strings.Contains(readSessionAgents(t, session.Path), "- Room: "+session.Path) {
		t.Fatalf("verified room not written: %q", first)
	}
	second := resume()
	if !strings.Contains(second, "session room not set: ") || strings.Contains(second, "TT_ROOM") {
		t.Fatalf("resume failure stderr = %q", second)
	}
	if strings.Contains(readSessionAgents(t, session.Path), "Conversation Room") {
		t.Fatal("resume kept a room section after a failed ensure")
	}
}

func TestSessionRoomFailureWithoutInheritedRoomSaysSo(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	ensureCLIGuidance(t, home, filepath.Dir(workspaceRoot))
	session := startTestSession(t, home)
	unsetEnv(t, "TT_ROOM")
	tt := &fakeTT{replies: []fakeTTReply{{out: "nope"}}}
	launch := runSessionLaunch(t, tt, 0, "session", "join", session.ID, "codex", "--home", home)
	if _, ok := launch.env["TT_ROOM"]; ok || !strings.Contains(launch.stderr, "TT_ROOM is not set") {
		t.Fatalf("env TT_ROOM=%q stderr=%q", launch.env["TT_ROOM"], launch.stderr)
	}
}

func TestSessionRoomWarningIsShownAndAccepted(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	ensureCLIGuidance(t, home, filepath.Dir(workspaceRoot))
	session := startTestSession(t, home)
	unsetEnv(t, "TT_ROOM")
	tt := &fakeTT{replies: []fakeTTReply{{out: `{"status":"exists","pin":"` + jsonPath(session.Path) + `","warning":"declaration predates ensure"}`}}}
	launch := runSessionLaunch(t, tt, 0, "session", "join", session.ID, "codex", "--home", home)
	if launch.env["TT_ROOM"] != session.Path || !strings.Contains(launch.stderr, "notice\ttalking-stick\tdeclaration predates ensure") {
		t.Fatalf("env TT_ROOM=%q stderr=%q", launch.env["TT_ROOM"], launch.stderr)
	}
	if strings.Contains(launch.stderr, "replaced inherited") {
		t.Fatalf("no inherited TT_ROOM to replace: %q", launch.stderr)
	}
}

func TestSessionRoomWithoutTTIsSilent(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	session := startTestSession(t, home)
	t.Setenv("TT_ROOM", "/somewhere/else")
	tt := &fakeTT{replies: []fakeTTReply{{err: errTTNotInstalled}}}
	launch := runSessionLaunch(t, tt, 0, "session", "join", session.ID, "codex", "--home", home)
	if strings.Contains(launch.stderr, "talking-stick") || strings.Contains(launch.stderr, "room\t") {
		t.Fatalf("stderr mentions rooms without tt: %q", launch.stderr)
	}
	if launch.env["TT_ROOM"] != "/somewhere/else" || launch.env["MYCLI_SESSION_ID"] != session.ID {
		t.Fatalf("env = %#v", launch.env)
	}
}

func TestNonSessionLaunchNeverEnsuresARoom(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	umbrellaRoot := filepath.Dir(workspaceRoot)
	ensureCLIGuidance(t, home, umbrellaRoot)
	startTestSession(t, home)
	t.Setenv("TT_ROOM", "/chosen")
	t.Setenv("MYCLI_SESSION_ID", "parent")
	t.Setenv("MYCLI_SESSION_PATH", "/parent")
	tt := &fakeTT{}
	launch := runSessionLaunch(t, tt, 0, "ai", "--no-session", "--home", home, "codex")
	if len(tt.calls) != 0 {
		t.Fatalf("ensure ran for a non-session launch: %#v", tt.calls)
	}
	if launch.dir != umbrellaRoot || launch.env["TT_ROOM"] != "/chosen" {
		t.Fatalf("dir %q env %#v", launch.dir, launch.env)
	}
	if _, ok := launch.env["MYCLI_SESSION_ID"]; ok {
		t.Fatalf("parent session id leaked into a non-session launch")
	}
	if _, ok := launch.env["MYCLI_SESSION_PATH"]; ok {
		t.Fatalf("parent session path leaked into a non-session launch")
	}
}

func TestSessionStartWithHarnessEnsuresOnce(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	ensureCLIGuidance(t, home, filepath.Dir(workspaceRoot))
	unsetEnv(t, "TT_ROOM")
	tt := &fakeTT{}
	var stdout, stderr bytes.Buffer
	var env map[string]string
	var dir string
	a := app{
		stdout:   &stdout,
		stderr:   &stderr,
		lookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
		execHarnessEnvironment: func(_ string, _ []string, d string, e []string) error {
			dir, env = d, envMap(e)
			return nil
		},
		ttRoomEnsure: func(ctx context.Context, d string, e []string) ([]byte, error) {
			tt.replies = []fakeTTReply{{out: ensureJSON("created", d)}}
			return tt.ensure(ctx, d, e)
		},
	}
	if err := a.run([]string{"my", "session", "start", "--slug", "once", "--home", home, "codex"}); err != nil {
		t.Fatalf("session start codex: %v\nstderr: %s", err, stderr.String())
	}
	if len(tt.calls) != 1 || tt.calls[0].dir != dir || env["TT_ROOM"] != dir {
		t.Fatalf("calls=%d dir=%q TT_ROOM=%q", len(tt.calls), dir, env["TT_ROOM"])
	}
	if strings.Count(stderr.String(), "room\t") != 1 {
		t.Fatalf("room reported more than once: %q", stderr.String())
	}
}

func TestSessionStartJSONAndPrintCarryVerifiedRoom(t *testing.T) {
	home, workspaceRoot := setupCLIRecordWorkspace(t)
	ensureCLIGuidance(t, home, filepath.Dir(workspaceRoot))
	unsetEnv(t, "TT_ROOM")
	ensureAt := func(ctx context.Context, d string, _ []string) ([]byte, error) {
		return []byte(ensureJSON("created", d)), nil
	}

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, ttRoomEnsure: ensureAt}
	if err := a.run([]string{"my", "session", "start", "--slug", "json", "--home", home, "--json"}); err != nil {
		t.Fatalf("session start --json: %v\nstderr: %s", err, stderr.String())
	}
	var report sessionStartCommandReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if report.TTRoom != report.Path {
		t.Fatalf("tt_room = %q, want %q", report.TTRoom, report.Path)
	}
	if guidance := readSessionAgents(t, report.Path); !strings.Contains(guidance, "- Room: "+report.Path) {
		t.Fatalf("AGENTS.md lacks the room:\n%s", guidance)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "session", "start", "--slug", "print", "--home", home, "--print", "codex", "--", "--model", "x"}); err != nil {
		t.Fatalf("session start --print: %v\nstderr: %s", err, stderr.String())
	}
	sessions, err := worksession.List(filepath.Dir(workspaceRoot))
	if err != nil {
		t.Fatal(err)
	}
	var printed worksession.Session
	for _, s := range sessions {
		if strings.Contains(s.ID, "-print-") {
			printed = s
		}
	}
	want := "cd " + shellQuote(printed.Path) + " && TT_ROOM=" + shellQuote(printed.Path) + " codex --model x\n"
	if stdout.String() != want {
		t.Fatalf("print = %q, want %q", stdout.String(), want)
	}

	stdout.Reset()
	stderr.Reset()
	if err := a.run([]string{"my", "ai", "--print", "--session", printed.ID, "--home", home, "claude-code"}); err != nil {
		t.Fatalf("my ai --print: %v\nstderr: %s", err, stderr.String())
	}
	want = "cd " + shellQuote(printed.Path) + " && TT_ROOM=" + shellQuote(printed.Path) + " claude\n"
	if stdout.String() != want {
		t.Fatalf("my ai --print = %q, want %q", stdout.String(), want)
	}
}

func TestRunTTRoomEnsureCallsOnlyEnsure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub")
	}
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(logPath) + "\nprintf '%s' \"${TT_ROOM-unset}\" >> " + shellQuote(logPath) + "\n" +
		"printf '{\"status\":\"created\",\"pin\":\"%s\"}' \"$3\"\n"
	if err := os.WriteFile(filepath.Join(bin, "tt"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("TT_ROOM", "/umbrella")
	dir := filepath.Join(t.TempDir(), "sessions", "s1")
	room := app{ttRoomEnsure: runTTRoomEnsure}.ensureSessionRoom(worksession.Session{ID: "s1", Path: dir})
	if room.Pin != dir || room.Failure != "" {
		t.Fatalf("room = %#v", room)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "room\nensure\n" + dir + "\n--json\nunset"; string(logged) != want {
		t.Fatalf("tt saw %q, want %q", logged, want)
	}

	t.Setenv("PATH", t.TempDir())
	if room := (app{ttRoomEnsure: runTTRoomEnsure}).ensureSessionRoom(worksession.Session{Path: dir}); room != (sessionRoom{}) {
		t.Fatalf("missing tt gave %#v, want silence", room)
	}

	slow := "#!/bin/sh\nexec /bin/sleep 5\n"
	if err := os.WriteFile(filepath.Join(bin, "tt"), []byte(slow), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	start := time.Now()
	room = app{ttRoomEnsure: runTTRoomEnsure, sessionRoomTimeout: 200 * time.Millisecond}.ensureSessionRoom(worksession.Session{Path: dir})
	if !strings.Contains(room.Failure, "did not answer within") || time.Since(start) > 3*time.Second {
		t.Fatalf("slow tt: room=%#v after %s", room, time.Since(start))
	}
}

func jsonPath(path string) string {
	encoded, _ := json.Marshal(path)
	return strings.Trim(string(encoded), `"`)
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, entry := range env {
		if key, value, ok := strings.Cut(entry, "="); ok {
			out[key] = value
		}
	}
	return out
}

func countEnv(env []string, key string) int {
	n := 0
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			n++
		}
	}
	return n
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func readSessionAgents(t *testing.T, sessionPath string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sessionPath, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func attemptRooms(t *testing.T, root string) []string {
	t.Helper()
	entries, err := attempt.List(root, attempt.ListOptions{})
	if errors.Is(err, attempt.ErrUnsupported) {
		t.Skip("launch records unsupported here")
	}
	if err != nil {
		t.Fatal(err)
	}
	var rooms []string
	for _, entry := range entries {
		if entry.Record == nil {
			t.Fatalf("unreadable launch record: %s", entry.Error)
		}
		rooms = append(rooms, entry.Record.TTRoom)
	}
	return rooms
}
