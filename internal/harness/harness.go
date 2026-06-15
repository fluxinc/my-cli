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
	ClaudeCode  Harness = "claude-code"
	Codex       Harness = "codex"
	OpenCode    Harness = "opencode"
	Antigravity Harness = "antigravity"
)

// All returns every supported harness in stable order.
func All() []Harness {
	return []Harness{ClaudeCode, Codex, OpenCode, Antigravity}
}

// CommandName returns the executable normally used to start the harness.
func (h Harness) CommandName() string {
	switch h {
	case ClaudeCode:
		return "claude"
	case Codex:
		return "codex"
	case OpenCode:
		return "opencode"
	case Antigravity:
		return "agy"
	}
	return string(h)
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
	case "antigravity", "agy":
		return Antigravity, nil
	}
	return "", fmt.Errorf("unknown harness %q (valid: claude-code, codex, opencode, antigravity)", s)
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
	case Antigravity:
		return filepath.Join(home, ".agents")
	}
	return ""
}

// SkillTargetPath returns where a skill directory should land for a harness.
func (h Harness) SkillTargetPath(home, skillName string) string {
	return filepath.Join(h.ConfigDir(home), "skills", skillName)
}

// ReadsAgentsSkills reports whether the harness reads launch-root
// .agents/skills directly.
func (h Harness) ReadsAgentsSkills() bool {
	return h == Codex || h == Antigravity
}

// SupportsLaunchRootSkills reports whether the harness can consume
// launch-scoped organization skills from a per-launch directory today.
func (h Harness) SupportsLaunchRootSkills() bool {
	return h == ClaudeCode || h == Codex || h == Antigravity
}

// MirrorSkillDir returns the launch-root mirror directory for harnesses that do
// not read .agents/skills directly. Empty means no mirror is needed.
func (h Harness) MirrorSkillDir(launchRoot string) string {
	if !h.SupportsLaunchRootSkills() || h.ReadsAgentsSkills() {
		return ""
	}
	switch h {
	case ClaudeCode:
		return filepath.Join(launchRoot, ".claude", "skills")
	default:
		return ""
	}
}
