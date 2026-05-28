package harness

import "testing"

func TestConfigDirAndSkillTargetPath(t *testing.T) {
	home := "/tmp/flux-home"
	tests := []struct {
		harness Harness
		config  string
		target  string
	}{
		{ClaudeCode, "/tmp/flux-home/.claude", "/tmp/flux-home/.claude/skills/demo"},
		{Codex, "/tmp/flux-home/.codex", "/tmp/flux-home/.codex/skills/demo"},
		{OpenCode, "/tmp/flux-home/.config/opencode", "/tmp/flux-home/.config/opencode/skills/demo"},
		{Gemini, "/tmp/flux-home/.gemini", ""},
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
		"gemini":      Gemini,
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
}

func TestCommandName(t *testing.T) {
	tests := map[Harness]string{
		ClaudeCode: "claude",
		Codex:      "codex",
		OpenCode:   "opencode",
		Gemini:     "gemini",
	}
	for h, want := range tests {
		if got := h.CommandName(); got != want {
			t.Fatalf("%s.CommandName() = %q, want %q", h, got, want)
		}
	}
}
