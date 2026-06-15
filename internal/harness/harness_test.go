package harness

import "testing"

func TestConfigDirAndSkillTargetPath(t *testing.T) {
	home := "/tmp/our-home"
	tests := []struct {
		harness Harness
		config  string
		target  string
	}{
		{ClaudeCode, "/tmp/our-home/.claude", "/tmp/our-home/.claude/skills/demo"},
		{Codex, "/tmp/our-home/.codex", "/tmp/our-home/.codex/skills/demo"},
		{OpenCode, "/tmp/our-home/.config/opencode", "/tmp/our-home/.config/opencode/skills/demo"},
		{Antigravity, "/tmp/our-home/.agents", "/tmp/our-home/.agents/skills/demo"},
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
		"claude-code": ClaudeCode,
		"claude":      ClaudeCode,
		"codex":       Codex,
		"opencode":    OpenCode,
		"antigravity": Antigravity,
		"agy":         Antigravity,
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
}

func TestCommandName(t *testing.T) {
	tests := map[Harness]string{
		ClaudeCode:  "claude",
		Codex:       "codex",
		OpenCode:    "opencode",
		Antigravity: "agy",
	}
	for h, want := range tests {
		if got := h.CommandName(); got != want {
			t.Fatalf("%s.CommandName() = %q, want %q", h, got, want)
		}
	}
}

func TestAllIncludesSupportedHarnesses(t *testing.T) {
	got := All()
	want := []Harness{ClaudeCode, Codex, OpenCode, Antigravity}
	if len(got) != len(want) {
		t.Fatalf("All() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("All() = %#v, want %#v", got, want)
		}
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
}
