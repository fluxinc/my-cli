// Package bundle resolves and materializes the public skill bundle.
package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	ourai "github.com/fluxinc/our-ai"
	"github.com/fluxinc/our-ai/internal/version"
)

const (
	modulePath  = "github.com/fluxinc/our-ai"
	installer   = "our"
	skillsDir   = "skills"
	sourceFlag  = "--source flag"
	sourceEnv   = "$OUR_HOME"
	sourceRepo  = "repo walk-up"
	sourceEmbed = "embedded"
)

// MarkerName is the filename used to identify our-managed directories.
const MarkerName = ".our-managed.json"

// ResolveOptions controls skill source selection.
type ResolveOptions struct {
	ExplicitSource string
	Cwd            string
	Home           string
	Env            map[string]string
}

// Source is the resolved filesystem skill source.
type Source struct {
	Kind         string
	SkillsDir    string
	Materialized bool
}

// Marker is written to our-managed materialized and copy-mode directories.
type Marker struct {
	Installer   string `json:"installer"`
	Version     string `json:"version"`
	Mode        string `json:"mode"`
	Source      string `json:"source"`
	CanonicalID string `json:"canonical_id,omitempty"`
}

// ResolveSkillsSource returns a filesystem skills directory using the R1
// source-selection order: explicit flag, OUR_HOME, repo walk-up, embedded.
func ResolveSkillsSource(opts ResolveOptions) (Source, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = "."
	}

	if opts.ExplicitSource != "" {
		dir, err := requireDir(opts.ExplicitSource)
		if err != nil {
			return Source{}, fmt.Errorf("%s: %w", sourceFlag, err)
		}
		return Source{Kind: sourceFlag, SkillsDir: dir}, nil
	}

	if root := lookupEnv(opts.Env, "OUR_HOME"); root != "" {
		dir, err := requireDir(filepath.Join(root, skillsDir))
		if err != nil {
			return Source{}, fmt.Errorf("%s: %w", sourceEnv, err)
		}
		return Source{Kind: sourceEnv, SkillsDir: dir}, nil
	}

	if root, ok := findRepoRoot(cwd); ok {
		return Source{Kind: sourceRepo, SkillsDir: filepath.Join(root, skillsDir)}, nil
	}

	dir, err := materializeEmbedded(opts.Home)
	if err != nil {
		return Source{}, err
	}
	return Source{Kind: sourceEmbed, SkillsDir: dir, Materialized: true}, nil
}

// Description is a human-readable source-observability line.
func (s Source) Description() string {
	if s.Materialized {
		return fmt.Sprintf("# source: %s -> materialized at %s", s.Kind, s.SkillsDir)
	}
	return fmt.Sprintf("# source: %s -> %s", s.Kind, s.SkillsDir)
}

// SkillsRoot returns the stable on-disk parent used for materialized and
// tool-provided skills. It does not create the directory.
func SkillsRoot(homeOverride string) (string, error) {
	home, err := resolveHome(homeOverride)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "our", skillsDir), nil
}

// MaterializeEmbedded writes the embedded public skill bundle to the stable
// Our AI-managed skill source directory.
func MaterializeEmbedded(homeOverride string) (Source, error) {
	dir, err := materializeEmbedded(homeOverride)
	if err != nil {
		return Source{}, err
	}
	return Source{Kind: sourceEmbed, SkillsDir: dir, Materialized: true}, nil
}

func materializeEmbedded(homeOverride string) (string, error) {
	dst, err := SkillsRoot(homeOverride)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}

	if err := fs.WalkDir(ourai.Embedded, skillsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(skillsDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := ourai.Embedded.ReadFile(path)
		if err != nil {
			return err
		}
		return writeFileIfChanged(target, data, 0o644)
	}); err != nil {
		return "", fmt.Errorf("materialize embedded skills: %w", err)
	}

	marker := Marker{
		Installer: installer,
		Version:   Version(),
		Mode:      "symlink",
		Source:    dst,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	if err := writeFileIfChanged(filepath.Join(dst, MarkerName), data, 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

// Version returns the build-stamped module version when available.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version.Version
	}
	return info.Main.Version
}

func writeFileIfChanged(path string, data []byte, perm fs.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return os.UserHomeDir()
}

func requireDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

func findRepoRoot(start string) (string, bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}
	for {
		if moduleMatches(filepath.Join(dir, "go.mod")) {
			if info, err := os.Stat(filepath.Join(dir, skillsDir)); err == nil && info.IsDir() {
				return dir, true
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func moduleMatches(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" && fields[1] == modulePath {
			return true
		}
	}
	return false
}

func lookupEnv(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}
