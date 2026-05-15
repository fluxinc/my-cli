// Package harness resolves filesystem locations for the supported AI
// agent harnesses. All path resolution is pure: it takes an explicit
// home directory so tests can use t.TempDir() without touching $HOME.
package harness

import (
	"fmt"
	"path/filepath"
)

type Harness string

const (
	ClaudeCode Harness = "claude-code"
	Codex      Harness = "codex"
	OpenCode   Harness = "opencode"
	Gemini     Harness = "gemini"
)

// All returns every supported harness in stable order.
func All() []Harness {
	return []Harness{ClaudeCode, Codex, OpenCode, Gemini}
}

// Parse accepts canonical names and a few common aliases.
func Parse(s string) (Harness, error) {
	switch s {
	case "claude-code", "claude":
		return ClaudeCode, nil
	case "codex":
		return Codex, nil
	case "opencode":
		return OpenCode, nil
	case "gemini":
		return Gemini, nil
	}
	return "", fmt.Errorf("unknown harness %q (valid: claude-code, codex, opencode, gemini)", s)
}

// ConfigDir returns the harness's user config directory under home.
func (h Harness) ConfigDir(home string) string {
	switch h {
	case ClaudeCode:
		return filepath.Join(home, ".claude")
	case Codex:
		return filepath.Join(home, ".codex")
	case OpenCode:
		return filepath.Join(home, ".config", "opencode")
	case Gemini:
		return filepath.Join(home, ".gemini")
	}
	return ""
}

// SkillTargetPath returns where a skill directory should land for a
// filesystem-managed harness. Returns empty for Gemini, which manages
// skills through its own CLI rather than a directory layout.
func (h Harness) SkillTargetPath(home, skillName string) string {
	if h == Gemini {
		return ""
	}
	return filepath.Join(h.ConfigDir(home), "skills", skillName)
}

// IsFilesystem reports whether skills are installed as directories
// under the harness config dir (true for everything except Gemini).
func (h Harness) IsFilesystem() bool {
	return h != Gemini
}
