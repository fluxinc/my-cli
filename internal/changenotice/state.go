package changenotice

import (
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
	// baseline recorded at their first launch.
	Noticed   string `json:"noticed"`
	NoticedAt string `json:"noticed_at"`
	// Baseline marks a revision recorded without a notice.
	Baseline bool `json:"baseline,omitempty"`
}

const stateSchema = 1

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

// Save records commit as noticed. It writes a temporary file and renames it,
// so a reader never sees half a file.
func Save(home, manifestName, commit string, baseline bool, now time.Time) error {
	path, err := StatePath(home, manifestName)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(State{Schema: stateSchema, Manifest: manifestName, Noticed: commit, NoticedAt: now.UTC().Format(time.RFC3339), Baseline: baseline}, "", "  ")
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
