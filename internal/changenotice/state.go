package changenotice

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is what one person has been told about one manifest. It lives in that
// person's home, so the notice is per user.
type State struct {
	Schema   int    `json:"schema"`
	Manifest string `json:"manifest"`
	// Noticed is the manifest commit this person was last told about, or the
	// starting point recorded at their first launch.
	Noticed   string `json:"noticed"`
	NoticedAt string `json:"noticed_at"`
	// Baseline marks a starting point recorded without a notice.
	Baseline bool `json:"baseline,omitempty"`
	// Reserved is a launch that is presenting Noticed..To right now, so a
	// launch overlapping it does not present the same changes again.
	Reserved *Reservation `json:"reserved,omitempty"`
}

// Reservation identifies one launch's claim on presenting a range.
type Reservation struct {
	Token        string `json:"token"`
	From         string `json:"from"`
	To           string `json:"to"`
	PID          int    `json:"pid"`
	ProcessStart string `json:"process_start,omitempty"`
	At           string `json:"at"`
}

const stateSchema = 1

// ReservationTTL bounds how long a reservation can hold back other launches
// when its process cannot be checked.
const ReservationTTL = 10 * time.Minute

// StatePath is ~/.local/share/my-cli/state/notice/<manifest>.json.
func StatePath(home, manifestName string) (string, error) {
	if manifestName == "" || strings.ContainsAny(manifestName, `/\`) || manifestName == "." || manifestName == ".." {
		return "", fmt.Errorf("invalid manifest name %q", manifestName)
	}
	return filepath.Join(home, ".local", "share", "my-cli", "state", "notice", manifestName+".json"), nil
}

// Load returns the state, or found=false when this person has none yet.
func Load(home, manifestName string) (State, bool, error) {
	path, err := StatePath(home, manifestName)
	if err != nil {
		return State{}, false, err
	}
	return load(path)
}

func load(path string) (State, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	if s.Noticed == "" {
		return State{}, false, nil
	}
	return s, true, nil
}

// Update runs fn on the state under an exclusive lock and saves the result
// when fn reports a change. Every writer goes through it, so a read and the
// write that depends on it are one step.
func Update(home, manifestName string, fn func(s *State, found bool) (bool, error)) (State, error) {
	path, err := StatePath(home, manifestName)
	if err != nil {
		return State{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return State{}, err
	}
	unlock, err := lock(path + ".lock")
	if err != nil {
		return State{}, err
	}
	defer unlock()
	s, found, err := load(path)
	if err != nil {
		return State{}, err
	}
	changed, err := fn(&s, found)
	if err != nil || !changed {
		return s, err
	}
	s.Schema, s.Manifest = stateSchema, manifestName
	return s, save(path, s)
}

// Record sets commit as noticed unconditionally: a person's explicit choice
// of starting point.
func Record(home, manifestName, commit string, baseline bool, now time.Time) error {
	_, err := Update(home, manifestName, func(s *State, _ bool) (bool, error) {
		s.Noticed, s.NoticedAt, s.Baseline, s.Reserved = commit, now.UTC().Format(time.RFC3339), baseline, nil
		return true, nil
	})
	return err
}

// Advance records to as noticed only if the person's state still says from,
// so a notice computed from an older revision can never move the record
// backwards or skip a newer one. It reports whether it advanced. A
// reservation is cleared only when token names it.
func Advance(home, manifestName, from, to, token string, now time.Time) (bool, error) {
	advanced := false
	_, err := Update(home, manifestName, func(s *State, found bool) (bool, error) {
		changed := false
		if s.Reserved != nil && token != "" && s.Reserved.Token == token {
			s.Reserved = nil
			changed = true
		}
		if found && s.Noticed == from && from != to {
			s.Noticed, s.NoticedAt, s.Baseline = to, now.UTC().Format(time.RFC3339), false
			advanced, changed = true, true
		}
		return changed, nil
	})
	return advanced, err
}

// Release drops the reservation named by token, leaving any other.
func Release(home, manifestName, token string) error {
	_, err := Update(home, manifestName, func(s *State, _ bool) (bool, error) {
		if s.Reserved == nil || s.Reserved.Token != token {
			return false, nil
		}
		s.Reserved = nil
		return true, nil
	})
	return err
}

// NewToken names one reservation.
func NewToken() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Live reports whether a reservation still holds back other launches: its
// process is running (the same process, by start time) and it is younger
// than ReservationTTL. start looks a process up; found=false means gone.
func (r Reservation) Live(now time.Time, start func(pid int) (string, bool, error)) bool {
	at, err := time.Parse(time.RFC3339, r.At)
	if err != nil || now.Sub(at) > ReservationTTL || now.Before(at.Add(-time.Minute)) {
		return false
	}
	if start == nil || r.PID <= 0 {
		return true
	}
	current, found, err := start(r.PID)
	if err != nil {
		return true // unknown: fall back to the age limit
	}
	return found && (r.ProcessStart == "" || current == r.ProcessStart)
}

func save(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".notice-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
