// Package contractstate records local contract observations and successful
// guidance installation. It never fetches repositories or claims model receipt.
package contractstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/fluxinc/my-cli/internal/safefs"
)

type Revision struct {
	ManifestCommit string `json:"manifest_commit,omitempty"`
	Blob           string `json:"blob"`
}

type Snapshot struct {
	Revision   Revision `json:"revision"`
	Configured bool     `json:"configured"`
	Source     string   `json:"source,omitempty"`
	Path       string   `json:"path,omitempty"`
	Provenance string   `json:"provenance"`
	Text       string   `json:"text"`
}

type Root struct {
	Umbrella          string    `json:"umbrella,omitempty"`
	Path              string    `json:"path"`
	Kind              string    `json:"kind"`
	SessionID         string    `json:"session_id,omitempty"`
	InstalledRevision *Revision `json:"installed_revision,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
}

type State struct {
	SchemaVersion             int             `json:"schema_version"`
	Active                    *Snapshot       `json:"active,omitempty"`
	Previous                  *Snapshot       `json:"previous,omitempty"`
	Roots                     map[string]Root `json:"roots,omitempty"`
	LastSuccessfulRemoteCheck string          `json:"last_successful_remote_check,omitempty"`
}

// Observe advances the local active revision. Absence after a configured
// contract is retained so the last rule's retraction can be delivered later.
func (s *State) Observe(next Snapshot) bool {
	if s.Active == nil && !next.Configured {
		return false
	}
	if s.Active != nil && *s.Active == next {
		return false
	}
	if s.Active != nil && s.Active.Text != next.Text {
		s.Previous = s.Active
	}
	copy := next
	s.Active = &copy
	return true
}

func (s *State) Register(path, kind, sessionID string) {
	if s.Roots == nil {
		s.Roots = map[string]Root{}
	}
	root := s.Roots[path]
	root.Path = path
	root.Kind = kind
	root.SessionID = sessionID
	s.Roots[path] = root
}

func (s *State) Installed(path string, revision Revision, message string) {
	root := s.Roots[path]
	root.Path = path
	root.LastError = message
	if message == "" {
		copy := revision
		root.InstalledRevision = &copy
	}
	if s.Roots == nil {
		s.Roots = map[string]Root{}
	}
	s.Roots[path] = root
}

func (s State) Pending(path string) bool {
	if s.Active == nil {
		return false
	}
	root, ok := s.Roots[path]
	return !ok || root.InstalledRevision == nil || *root.InstalledRevision != s.Active.Revision || root.LastError != ""
}

var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func Path(home, name string) (string, error) {
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("invalid manifest name %q", name)
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".local", "share", "my-cli", "state", "contract", name+".json"), nil
}

func Load(home, name string) (State, error) {
	path, err := Path(home, name)
	if err != nil {
		return State{}, err
	}
	return load(path)
}
func load(path string) (State, error) {
	s := State{SchemaVersion: 1, Roots: map[string]Root{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("read contract state: %w", err)
	}
	if s.SchemaVersion != 1 {
		return State{}, fmt.Errorf("unsupported contract state version %d", s.SchemaVersion)
	}
	if s.Roots == nil {
		s.Roots = map[string]Root{}
	}
	return s, nil
}

// Update serializes state changes across CLI processes. Callbacks must not do
// network I/O. Failed callbacks leave the previous complete state untouched.
func Update(home, name string, change func(*State) error) (State, error) {
	path, err := Path(home, name)
	if err != nil {
		return State{}, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return State{}, err
	}
	unlock, err := lock(path + ".lock")
	if err != nil {
		return State{}, err
	}
	defer unlock()
	s, err := load(path)
	if err != nil {
		return State{}, err
	}
	if err = change(&s); err != nil {
		return State{}, err
	}
	if s.Active == nil {
		return s, nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return State{}, err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(filepath.Dir(path), ".contract-*")
	if err != nil {
		return State{}, err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return State{}, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return State{}, err
	}
	if err = f.Close(); err != nil {
		return State{}, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return State{}, err
	}
	if err = safefs.SyncDirectory(filepath.Dir(path)); err != nil {
		return State{}, err
	}
	return s, nil
}
