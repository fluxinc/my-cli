// Package harness resolves filesystem locations for the supported AI
// agent harnesses. All path resolution is pure: it takes an explicit
// home directory so tests can use t.TempDir() without touching $HOME.
package harness

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Harness string

const (
	ClaudeCode  Harness = "claude-code"
	Codex       Harness = "codex"
	OpenCode    Harness = "opencode"
	Antigravity Harness = "antigravity"
	Grok        Harness = "grok"
	Cursor      Harness = "cursor"
)

// spec is the single source of harness facts. Methods stay I/O-free so
// tests can exercise resolution without touching the real home directory.
type spec struct {
	name            Harness
	aliases         []string
	command         string
	commands        []string
	configDir       []string
	presenceDir     []string
	legacySkillDirs [][]string
	loginMarkers    [][]string
	// promptFlag is empty for a positional initial prompt. Non-empty values
	// are passed as a flag name immediately before the prompt string.
	promptFlag   string
	readsAgents  bool
	launchSkills bool
	mirrorDir    []string
}

var specs = []spec{
	{
		name:         ClaudeCode,
		aliases:      []string{"claude"},
		command:      "claude",
		configDir:    []string{".claude"},
		loginMarkers: [][]string{{".claude", ".credentials.json"}},
		launchSkills: true,
		mirrorDir:    []string{".claude", "skills"},
	},
	{
		name:         Codex,
		command:      "codex",
		configDir:    []string{".codex"},
		loginMarkers: [][]string{{".codex", "auth.json"}},
		readsAgents:  true,
		launchSkills: true,
	},
	{
		name:      OpenCode,
		command:   "opencode",
		configDir: []string{".config", "opencode"},
		loginMarkers: [][]string{
			{".local", "share", "opencode", "auth.json"},
			{".config", "opencode", "auth.json"},
		},
		promptFlag: "--prompt",
	},
	{
		name:        Antigravity,
		aliases:     []string{"agy"},
		command:     "agy",
		configDir:   []string{".gemini", "config"},
		presenceDir: []string{".gemini"},
		legacySkillDirs: [][]string{
			{".agents", "skills"},
			{".gemini", "skills"},
			{".gemini", "antigravity-cli", "skills"},
		},
		loginMarkers: [][]string{
			{".gemini", "antigravity", "user_settings.pb"},
			{".gemini", "antigravity", "installation_id"},
			{".gemini", "antigravity-cli", "user_settings.pb"},
			{".antigravity", "auth.json"},
		},
		promptFlag:   "--prompt-interactive",
		readsAgents:  true,
		launchSkills: true,
	},
	{
		name:         Grok,
		command:      "grok",
		configDir:    []string{".grok"},
		loginMarkers: [][]string{{".grok", "auth.json"}},
		launchSkills: true,
		mirrorDir:    []string{".grok", "skills"},
	},
	{
		name:         Cursor,
		aliases:      []string{"cursor-agent"},
		command:      "cursor-agent",
		commands:     []string{"cursor-agent", "agent"},
		configDir:    []string{".cursor"},
		readsAgents:  true,
		launchSkills: true,
	},
}

func lookup(h Harness) (spec, bool) {
	for _, s := range specs {
		if s.name == h {
			return s, true
		}
	}
	return spec{}, false
}

// All returns every supported harness in stable order.
func All() []Harness {
	out := make([]Harness, len(specs))
	for i, s := range specs {
		out[i] = s.name
	}
	return out
}

// Names returns the canonical harness names as a comma-separated list.
func Names() string {
	all := All()
	parts := make([]string, len(all))
	for i, h := range all {
		parts[i] = string(h)
	}
	return strings.Join(parts, ", ")
}

// LaunchSkillNames returns canonical names of harnesses that consume
// launch-scoped organization skills.
func LaunchSkillNames() string {
	var parts []string
	for _, s := range specs {
		if s.launchSkills {
			parts = append(parts, string(s.name))
		}
	}
	return strings.Join(parts, ", ")
}

// CommandName returns the executable normally used to start the harness.
func (h Harness) CommandName() string {
	if s, ok := lookup(h); ok {
		return s.command
	}
	return string(h)
}

// CommandCandidates returns executable names accepted for a real launch, in
// preference order. Cursor's collision-safe legacy name wins when available;
// its current official `agent` name is the fallback and must be product-probed
// by the caller because Grok also ships an `agent` compatibility executable.
func (h Harness) CommandCandidates() []string {
	s, ok := lookup(h)
	if !ok {
		return []string{string(h)}
	}
	if len(s.commands) == 0 {
		return []string{s.command}
	}
	return append([]string(nil), s.commands...)
}

// InitialPromptArgs returns arguments that deliver an initial prompt to an
// interactive harness session.
func (h Harness) InitialPromptArgs(prompt string) []string {
	if prompt == "" {
		return nil
	}
	s, ok := lookup(h)
	if !ok {
		return nil
	}
	if s.promptFlag != "" {
		return []string{s.promptFlag, prompt}
	}
	return []string{prompt}
}

// Parse accepts canonical names and a few common aliases.
func Parse(s string) (Harness, error) {
	for _, spec := range specs {
		if s == string(spec.name) {
			return spec.name, nil
		}
		for _, alias := range spec.aliases {
			if s == alias {
				return spec.name, nil
			}
		}
	}
	return "", fmt.Errorf("unknown harness %q (valid: %s)", s, Names())
}

// LoginMarkers returns best-effort filesystem paths whose existence suggests
// the harness already has credentials configured. Detection is heuristic and
// path-only (the package stays pure); callers stat these and should treat a
// match as a preference hint, never a guarantee that the harness can run.
func (h Harness) LoginMarkers(home string) []string {
	s, ok := lookup(h)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(s.loginMarkers))
	for _, parts := range s.loginMarkers {
		out = append(out, joinHome(home, parts))
	}
	return out
}

// ConfigDir returns the harness's user config directory under home.
func (h Harness) ConfigDir(home string) string {
	s, ok := lookup(h)
	if !ok || len(s.configDir) == 0 {
		return ""
	}
	return joinHome(home, s.configDir)
}

// PresenceDir returns the directory whose existence indicates this harness is
// installed for a user, when it differs from ConfigDir (for example Antigravity
// creates ~/.gemini before ~/.gemini/config exists). Defaults to ConfigDir.
func (h Harness) PresenceDir(home string) string {
	s, ok := lookup(h)
	if !ok {
		return ""
	}
	if len(s.presenceDir) != 0 {
		return joinHome(home, s.presenceDir)
	}
	return h.ConfigDir(home)
}

// SkillTargetPath returns where a skill directory should land for a harness.
func (h Harness) SkillTargetPath(home, skillName string) string {
	return filepath.Join(h.ConfigDir(home), "skills", skillName)
}

// LegacySkillTargetPaths returns older global skill locations for this harness
// that should be cleaned up when My AI-managed self-skills are migrated.
func (h Harness) LegacySkillTargetPaths(home, skillName string) []string {
	s, ok := lookup(h)
	if !ok || len(s.legacySkillDirs) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.legacySkillDirs))
	for _, parts := range s.legacySkillDirs {
		out = append(out, filepath.Join(joinHome(home, parts), skillName))
	}
	return out
}

// ReadsAgentsSkills reports whether the harness reads launch-root
// .agents/skills directly.
func (h Harness) ReadsAgentsSkills() bool {
	s, ok := lookup(h)
	return ok && s.readsAgents
}

// SupportsLaunchRootSkills reports whether the harness can consume
// launch-scoped organization skills from a per-launch directory today.
func (h Harness) SupportsLaunchRootSkills() bool {
	s, ok := lookup(h)
	return ok && s.launchSkills
}

// MirrorSkillDir returns the launch-root mirror directory for harnesses that do
// not read .agents/skills directly. Empty means no mirror is needed.
func (h Harness) MirrorSkillDir(launchRoot string) string {
	s, ok := lookup(h)
	if !ok || !s.launchSkills || s.readsAgents || len(s.mirrorDir) == 0 {
		return ""
	}
	return filepath.Join(append([]string{launchRoot}, s.mirrorDir...)...)
}

func joinHome(home string, parts []string) string {
	return filepath.Join(append([]string{home}, parts...)...)
}
