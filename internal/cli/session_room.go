package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/worksession"
)

// Environment a session launch hands its harness. TT_ROOM belongs to Talking
// Stick; my sets it only from a room that `tt room ensure` just verified.
const (
	ttRoomEnv      = "TT_ROOM"
	sessionIDEnv   = "MYCLI_SESSION_ID"
	sessionPathEnv = "MYCLI_SESSION_PATH"
)

const defaultSessionRoomTimeout = 5 * time.Second

// errTTNotInstalled means Talking Stick is not in use here: no room is
// claimed and nothing is reported.
var errTTNotInstalled = errors.New("tt is not installed")

// ttRoomEnsureFunc runs `tt room ensure <dir> --json` and returns its stdout.
type ttRoomEnsureFunc func(ctx context.Context, dir string, env []string) ([]byte, error)

// sessionRoom is the outcome of one `tt room ensure` for a session. Pin is set
// only for a verified room; Failure says why none was verified. Both empty
// means Talking Stick is not installed.
type sessionRoom struct {
	Pin     string
	Status  string
	Warning string
	Failure string
}

// runTTRoomEnsure is the production ttRoomEnsureFunc. It never joins a room
// or takes a lease: ensure only declares the directory's room and names it.
func runTTRoomEnsure(ctx context.Context, dir string, env []string) ([]byte, error) {
	path, err := exec.LookPath("tt")
	if err != nil {
		return nil, errTTNotInstalled
	}
	cmd := exec.CommandContext(ctx, path, "room", "ensure", dir, "--json")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second // don't wait on a killed tt's pipes
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		detail := firstLine(stderr.String())
		if detail == "" {
			detail = firstLine(stdout.String())
		}
		if detail == "" {
			return nil, fmt.Errorf("tt room ensure failed (%v)", err)
		}
		return nil, fmt.Errorf("tt room ensure failed (%v): %s", err, detail)
	}
	return stdout.Bytes(), nil
}

// parseTTRoomEnsure accepts only status created or exists with an absolute
// pin. room_path is the room's identity and is never used as the selector: in
// a linked worktree it names the main checkout.
func parseTTRoomEnsure(out []byte) (sessionRoom, error) {
	var reply struct {
		Status  string          `json:"status"`
		Pin     string          `json:"pin"`
		Warning json.RawMessage `json:"warning"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return sessionRoom{}, fmt.Errorf("tt room ensure returned unreadable output %q", firstLine(string(out)))
	}
	if reply.Status != "created" && reply.Status != "exists" {
		return sessionRoom{}, fmt.Errorf("tt room ensure returned status %q", reply.Status)
	}
	if reply.Pin == "" {
		return sessionRoom{}, errors.New("tt room ensure returned no pin")
	}
	if !filepath.IsAbs(reply.Pin) {
		return sessionRoom{}, fmt.Errorf("tt room ensure returned a relative pin %q", reply.Pin)
	}
	room := sessionRoom{Pin: reply.Pin, Status: reply.Status}
	if len(reply.Warning) != 0 && string(reply.Warning) != "null" {
		var text string
		if json.Unmarshal(reply.Warning, &text) == nil {
			room.Warning = text
		} else {
			var compact bytes.Buffer
			if json.Compact(&compact, reply.Warning) == nil {
				room.Warning = compact.String()
			}
		}
	}
	return room, nil
}

// ensureSessionRoom asks Talking Stick for the session's room once. It never
// fails the caller: an unverified room is reported, not fatal.
func (a app) ensureSessionRoom(session worksession.Session) sessionRoom {
	if a.ttRoomEnsure == nil {
		return sessionRoom{}
	}
	timeout := a.sessionRoomTimeout
	if timeout <= 0 {
		timeout = defaultSessionRoomTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// The session directory selects the room; an inherited TT_ROOM must not.
	out, err := a.ttRoomEnsure(ctx, session.Path, withoutEnv(os.Environ(), ttRoomEnv))
	if errors.Is(err, errTTNotInstalled) {
		return sessionRoom{}
	}
	if errors.Is(err, context.DeadlineExceeded) || (err != nil && ctx.Err() != nil) {
		return sessionRoom{Failure: fmt.Sprintf("tt room ensure did not answer within %s", timeout)}
	}
	if err != nil {
		return sessionRoom{Failure: err.Error()}
	}
	room, err := parseTTRoomEnsure(out)
	if err != nil {
		return sessionRoom{Failure: err.Error()}
	}
	return room
}

// printSessionRoom reports one ensure outcome. launch says whether this
// command starts a harness, whose TT_ROOM the outcome decides.
func printSessionRoom(w io.Writer, room sessionRoom, launch bool) {
	if room.Warning != "" {
		fmt.Fprintf(w, "notice\ttalking-stick\t%s\n", room.Warning)
	}
	inherited, hasInherited := os.LookupEnv(ttRoomEnv)
	switch {
	case room.Pin != "":
		fmt.Fprintf(w, "room\t%s\t%s\tpeople join with: tt chat --in %s\n", room.Pin, room.Status, shellQuote(room.Pin))
		if launch && hasInherited && inherited != "" && filepath.Clean(inherited) != filepath.Clean(room.Pin) {
			fmt.Fprintf(w, "notice\ttalking-stick\treplaced inherited TT_ROOM %s with this session's room\n", inherited)
		}
	case room.Failure != "":
		kept := ""
		if launch {
			kept = "; TT_ROOM is not set"
			if hasInherited && inherited != "" {
				kept = "; TT_ROOM kept as " + inherited
			}
		}
		fmt.Fprintf(w, "notice\ttalking-stick\tsession room not set: %s%s\n", room.Failure, kept)
	}
}

// sessionLaunchEnvironment is the base harness environment for one launch.
// Inherited session variables are always dropped so a child never claims its
// parent's session; a session launch sets its own, and TT_ROOM only when the
// room was verified. Otherwise an inherited TT_ROOM is kept exactly.
func sessionLaunchEnvironment(inherited []string, session *worksession.Session, room sessionRoom) []string {
	pin := session != nil && room.Pin != ""
	env := make([]string, 0, len(inherited)+3)
	for _, entry := range inherited {
		if envKeyIs(entry, sessionIDEnv) || envKeyIs(entry, sessionPathEnv) || (pin && envKeyIs(entry, ttRoomEnv)) {
			continue
		}
		env = append(env, entry)
	}
	if session == nil {
		return env
	}
	env = append(env, sessionIDEnv+"="+session.ID, sessionPathEnv+"="+session.Path)
	if pin {
		env = append(env, ttRoomEnv+"="+room.Pin)
	}
	return env
}

// harnessCommandLine is shellCommandLine with the verified room assigned on
// the harness command itself, not on the preceding cd.
func harnessCommandLine(dir string, room sessionRoom, command string, args []string) string {
	return harnessCommandLineWithEnv(dir, room, nil, command, args)
}

func harnessCommandLineWithEnv(dir string, room sessionRoom, extraEnv []string, command string, args []string) string {
	var envParts []string
	if room.Pin != "" {
		envParts = append(envParts, ttRoomEnv+"="+shellQuote(room.Pin))
	}
	for _, pair := range extraEnv {
		key, val, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		envParts = append(envParts, key+"="+shellQuote(val))
	}
	if len(envParts) == 0 {
		return shellCommandLine(dir, command, args)
	}
	line := strings.Join(envParts, " ") + " " + shellCommandLine("", command, args)
	if dir == "" {
		return line
	}
	return "cd " + shellQuote(dir) + " && " + line
}

func withoutEnv(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if !envKeyIs(entry, key) {
			out = append(out, entry)
		}
	}
	return out
}

func envKeyIs(entry, key string) bool {
	name, _, ok := strings.Cut(entry, "=")
	if !ok {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(name, key)
	}
	return name == key
}

func firstLine(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}
