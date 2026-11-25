package harness

import (
	"reflect"
	"strings"
	"testing"
)

func TestConfigDirAndSkillTargetPath(t *testing.T) {
	home := "/tmp/my-home"
	tests := []struct {
		harness Harness
		config  string
		target  string
	}{
		{ClaudeCode, "/tmp/my-home/.claude", "/tmp/my-home/.claude/skills/demo"},
		{Codex, "/tmp/my-home/.codex", "/tmp/my-home/.codex/skills/demo"},
		{OpenCode, "/tmp/my-home/.config/opencode", "/tmp/my-home/.config/opencode/skills/demo"},
		{Antigravity, "/tmp/my-home/.gemini/config", "/tmp/my-home/.gemini/config/skills/demo"},
		{Grok, "/tmp/my-home/.grok", "/tmp/my-home/.grok/skills/demo"},
		{Cursor, "/tmp/my-home/.cursor", "/tmp/my-home/.cursor/skills/demo"},
	}

	for _, tt := range tests {
		t.Run(string(tt.harness), func(t *testing.T) {
			if got := tt.harness.ConfigDir(home); got != tt.config {
				t.Fatalf("ConfigDir() = %q, want %q", got, tt.config)
			}
			if got := tt.harness.SkillTargetPath(home, "demo"); got != tt.target {
				t.Fatalf("SkillTargetPath() = %q, want %q", got, tt.target)
			}
		})
	}
}

func TestParseAliases(t *testing.T) {
	tests := map[string]Harness{
		"claude-code":  ClaudeCode,
		"claude":       ClaudeCode,
		"codex":        Codex,
		"opencode":     OpenCode,
		"antigravity":  Antigravity,
		"agy":          Antigravity,
		"grok":         Grok,
		"cursor":       Cursor,
		"cursor-agent": Cursor,
	}
	for input, want := range tests {
		got, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("Parse(%q) = %q, want %q", input, got, want)
		}
	}
	if _, err := Parse("unknown"); err == nil {
		t.Fatal("Parse unknown returned nil error")
	}
	if _, err := Parse("gemini"); err == nil {
		t.Fatal("Parse gemini returned nil error")
	}
	err := parseError(t, "agent")
	if !strings.Contains(err, "unknown harness") || !strings.Contains(err, "cursor") {
		t.Fatalf("Parse(agent) error = %q, want unknown harness listing cursor", err)
	}
}

func parseError(t *testing.T, name string) string {
	t.Helper()
	_, err := Parse(name)
	if err == nil {
		t.Fatalf("Parse(%q) returned nil error", name)
	}
	return err.Error()
}

func TestCommandName(t *testing.T) {
	tests := map[Harness]string{
		ClaudeCode:  "claude",
		Codex:       "codex",
		OpenCode:    "opencode",
		Antigravity: "agy",
		Grok:        "grok",
		Cursor:      "cursor-agent",
	}
	for h, want := range tests {
		if got := h.CommandName(); got != want {
			t.Fatalf("%s.CommandName() = %q, want %q", h, got, want)
		}
	}
}

func TestAllIncludesSupportedHarnesses(t *testing.T) {
	got := All()
	want := []Harness{ClaudeCode, Codex, OpenCode, Antigravity, Grok, Cursor}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("All() = %#v, want %#v", got, want)
	}
	if !strings.Contains(Names(), "grok") {
		t.Fatalf("Names() = %q, want grok listed", Names())
	}
}

func TestLaunchSkillDiscoveryCapabilities(t *testing.T) {
	root := "/tmp/launch-root"
	tests := map[Harness]struct {
		reads    bool
		supports bool
		mirror   string
	}{
		ClaudeCode:  {supports: true, mirror: "/tmp/launch-root/.claude/skills"},
		Codex:       {reads: true, supports: true},
		OpenCode:    {},
		Antigravity: {reads: true, supports: true},
		Grok:        {supports: true, mirror: "/tmp/launch-root/.grok/skills"},
		Cursor:      {reads: true, supports: true},
	}
	for h, want := range tests {
		if got := h.ReadsAgentsSkills(); got != want.reads {
			t.Fatalf("%s ReadsAgentsSkills() = %v, want %v", h, got, want.reads)
		}
		if got := h.SupportsLaunchRootSkills(); got != want.supports {
			t.Fatalf("%s SupportsLaunchRootSkills() = %v, want %v", h, got, want.supports)
		}
		if got := h.MirrorSkillDir(root); got != want.mirror {
			t.Fatalf("%s MirrorSkillDir() = %q, want %q", h, got, want.mirror)
		}
	}
	if !strings.Contains(LaunchSkillNames(), "grok") {
		t.Fatalf("LaunchSkillNames() = %q, want grok", LaunchSkillNames())
	}
}

func TestCursorCommandCandidatesAvoidAmbiguousAgentFirst(t *testing.T) {
	want := []string{"cursor-agent", "agent"}
	if got := Cursor.CommandCandidates(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Cursor.CommandCandidates() = %#v, want %#v", got, want)
	}
	if got := Grok.CommandCandidates(); !reflect.DeepEqual(got, []string{"grok"}) {
		t.Fatalf("Grok.CommandCandidates() = %#v", got)
	}
}

func TestLoginMarkers(t *testing.T) {
	home := "/tmp/my-home"
	got := Grok.LoginMarkers(home)
	want := []string{"/tmp/my-home/.grok/auth.json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Grok.LoginMarkers() = %#v, want %#v", got, want)
	}
}

func TestInitialPromptArgs(t *testing.T) {
	if got := Grok.InitialPromptArgs("hello"); !reflect.DeepEqual(got, []string{"hello"}) {
		t.Fatalf("Grok.InitialPromptArgs() = %#v, want positional prompt", got)
	}
	if got := Cursor.InitialPromptArgs("hello"); !reflect.DeepEqual(got, []string{"hello"}) {
		t.Fatalf("Cursor.InitialPromptArgs() = %#v, want positional prompt", got)
	}
	if got := ClaudeCode.InitialPromptArgs("hello"); !reflect.DeepEqual(got, []string{"hello"}) {
		t.Fatalf("ClaudeCode.InitialPromptArgs() = %#v", got)
	}
	if got := OpenCode.InitialPromptArgs("hello"); !reflect.DeepEqual(got, []string{"--prompt", "hello"}) {
		t.Fatalf("OpenCode.InitialPromptArgs() = %#v", got)
	}
	if got := Antigravity.InitialPromptArgs("hello"); !reflect.DeepEqual(got, []string{"--prompt-interactive", "hello"}) {
		t.Fatalf("Antigravity.InitialPromptArgs() = %#v, want --prompt-interactive flag", got)
	}
	if got := Grok.InitialPromptArgs(""); got != nil {
		t.Fatalf("empty prompt args = %#v, want nil", got)
	}
}

func TestAntigravityPresenceAndLegacySkillPaths(t *testing.T) {
	home := "/tmp/my-home"
	if got := Antigravity.PresenceDir(home); got != "/tmp/my-home/.gemini" {
		t.Fatalf("Antigravity.PresenceDir() = %q, want /tmp/my-home/.gemini", got)
	}
	if got := Codex.PresenceDir(home); got != "/tmp/my-home/.codex" {
		t.Fatalf("Codex.PresenceDir() = %q, want /tmp/my-home/.codex", got)
	}
	wantLegacy := []string{
		"/tmp/my-home/.agents/skills/my-cli",
		"/tmp/my-home/.gemini/skills/my-cli",
		"/tmp/my-home/.gemini/antigravity-cli/skills/my-cli",
	}
	if got := Antigravity.LegacySkillTargetPaths(home, "my-cli"); !reflect.DeepEqual(got, wantLegacy) {
		t.Fatalf("Antigravity.LegacySkillTargetPaths() = %#v, want %#v", got, wantLegacy)
	}
	if got := Codex.LegacySkillTargetPaths(home, "my-cli"); got != nil {
		t.Fatalf("Codex.LegacySkillTargetPaths() = %#v, want nil", got)
	}
}
