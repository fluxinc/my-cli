package recordindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// QMD is the local search engine behind an index generation.
type QMD interface {
	// Build creates the generation's collection and embeds its documents.
	Build(genDir string) error
	// Update re-indexes changed documents and embeds them.
	Update(genDir string) error
	// Query returns matching documents by file name, best first.
	Query(genDir, text string, limit int) ([]QueryResult, error)
}

// QueryResult is one qmd match. Its snippet is deliberately not kept: hits
// show only what the service returns now.
type QueryResult struct {
	File  string
	Score float64
}

// ErrQMDMissing means the qmd command is not installed.
var ErrQMDMissing = errors.New("qmd is not installed; semantic search is unavailable (the service's own search still works)")

const collection = "records"

// CommandQMD runs the qmd CLI isolated in the generation directory. qmd keeps
// its collections under $HOME/.config/qmd and its database where INDEX_PATH
// points, so both are pointed inside the generation; the user's own qmd
// index and configuration are never read or written. Models are shared
// read-only by linking the user's model cache, so nothing is downloaded twice.
type CommandQMD struct {
	// ModelCache is the user's qmd model directory (~/.cache/qmd/models).
	ModelCache string
}

func (c CommandQMD) Build(genDir string) error {
	if _, err := c.run(genDir, "collection", "add", filepath.Join(genDir, "docs"), "--name", collection, "--mask", "**/*.md"); err != nil {
		return err
	}
	_, err := c.run(genDir, "embed")
	return err
}

func (c CommandQMD) Update(genDir string) error {
	if _, err := c.run(genDir, "update"); err != nil {
		return err
	}
	_, err := c.run(genDir, "embed")
	return err
}

func (c CommandQMD) Query(genDir, text string, limit int) ([]QueryResult, error) {
	out, err := c.run(genDir, "query", text, "--json", "-n", fmt.Sprint(limit), "-c", collection)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || strings.HasPrefix(trimmed, "No results") {
		return nil, nil
	}
	var raw []struct {
		File  string  `json:"file"`
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, errors.New("qmd query returned output that is not JSON")
	}
	results := make([]QueryResult, 0, len(raw))
	for _, r := range raw {
		results = append(results, QueryResult{File: filepath.Base(r.File), Score: r.Score})
	}
	return results, nil
}

func (c CommandQMD) run(genDir string, args ...string) ([]byte, error) {
	bin, err := exec.LookPath("qmd")
	if err != nil {
		return nil, ErrQMDMissing
	}
	home := filepath.Join(genDir, "qmd-home")
	if err := os.MkdirAll(filepath.Join(home, ".cache", "qmd"), 0o700); err != nil {
		return nil, err
	}
	if c.ModelCache != "" {
		link := filepath.Join(home, ".cache", "qmd", "models")
		if _, err := os.Lstat(link); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(c.ModelCache); err == nil {
				_ = os.Symlink(c.ModelCache, link)
			}
		}
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = genDir
	cmd.Env = append(withoutEnv(os.Environ(), "HOME", "INDEX_PATH", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"),
		"HOME="+home, "INDEX_PATH="+filepath.Join(home, "index.sqlite"))
	restore := privateUmask()
	out, err := cmd.Output()
	restore()
	tightenModes(home)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("qmd %s failed (exit %d)", args[0], exitErr.ExitCode())
		}
		return nil, fmt.Errorf("qmd %s failed", args[0])
	}
	return out, nil
}

func withoutEnv(env []string, names ...string) []string {
	out := env[:0:0]
	for _, entry := range env {
		keep := true
		for _, name := range names {
			if strings.HasPrefix(entry, name+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, entry)
		}
	}
	return out
}

// tightenModes makes everything qmd wrote private, whatever its own umask
// handling did. The model link is left alone (it points outside).
func tightenModes(root string) {
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			os.Chmod(path, 0o700)
		} else {
			os.Chmod(path, 0o600)
		}
		return nil
	})
}
