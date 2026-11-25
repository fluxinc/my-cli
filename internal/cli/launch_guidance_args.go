package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

const openCodeConfigContentEnv = "OPENCODE_CONFIG_CONTENT"

// launchGuidanceArgs returns harness-specific CLI flags that ensure the
// organization contract, umbrella guidance, and skills reach the model in
// launch modes where the harness would otherwise miss them, without placing
// private contract text in process argv or overriding user developer
// instructions:
//   - --repo launches: nested git checkouts stop Antigravity, Cursor, and Codex
//     from discovering the parent umbrella's AGENTS.md and .agents/skills
//     (Claude Code already walks up to umbrella CLAUDE.md). Antigravity and
//     Cursor accept --add-dir <umbrella>; Codex accepts
//     -c project_root_markers=[".my-cli"], which lets Codex walk up past the
//     nested .git to load umbrella AGENTS.md, repo AGENTS.md, and umbrella
//     .agents/skills while preserving preconfigured developer_instructions.
//   - Grok launches: Grok skips project AGENTS.md and project skills when a
//     directory is untrusted (every new umbrella and session dir), and rejects
//     --add-dir at runtime ("additionalDirectories is not supported: a session
//     works only in its cwd"), so passing a neutral --rules pointer tells Grok
//     to read the generated AGENTS.md file(s).
func launchGuidanceArgs(h harness.Harness, opts launchCommandOpts, root, targetDir string, existing []string) []string {
	var extra []string
	if opts.repoID != "" && root != "" {
		switch h {
		case harness.Antigravity, harness.Cursor:
			if !hasFlagWithValue(existing, "--add-dir", root) {
				extra = append(extra, "--add-dir", root)
			}
		case harness.Codex:
			if !hasCodexConfigKey(existing, "project_root_markers") {
				extra = append(extra, "-c", fmt.Sprintf(`project_root_markers=["%s"]`, umbrella.DirName))
			}
		}
	}
	if h == harness.Grok && !hasGrokRulesOverride(existing) {
		if text := buildLaunchGuidancePointer(root, targetDir); text != "" {
			extra = append(extra, "--rules", text)
		}
	}
	return extra
}

// launchGuidanceEnvPairs returns harness-specific KEY=VALUE environment
// overrides needed for umbrella guidance to reach the model without placing
// private contract text in process arguments or clobbering user config:
//   - OpenCode --repo launches: OpenCode stops parent AGENTS.md discovery at a
//     nested git repo boundary, but merges OPENCODE_CONFIG_CONTENT's
//     instructions array with the user's opencode.json instructions.
func launchGuidanceEnvPairs(h harness.Harness, opts launchCommandOpts, root string, baseEnv []string) []string {
	if h != harness.OpenCode || opts.repoID == "" || root == "" {
		return nil
	}
	umbrellaAgents := filepath.Join(root, "AGENTS.md")
	existing := envValue(baseEnv, openCodeConfigContentEnv)
	merged, ok := mergeOpenCodeInstructionsJSON(existing, umbrellaAgents)
	if !ok {
		return nil
	}
	return []string{openCodeConfigContentEnv + "=" + merged}
}

func applyEnvPairs(env []string, pairs []string) []string {
	out := env
	for _, pair := range pairs {
		key, _, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out = append(withoutEnv(out, key), pair)
	}
	return out
}

func envValue(env []string, key string) string {
	val := ""
	for _, entry := range env {
		if envKeyIs(entry, key) {
			_, val, _ = strings.Cut(entry, "=")
		}
	}
	return val
}

func mergeOpenCodeInstructionsJSON(existing, instructionPath string) (string, bool) {
	doc := map[string]json.RawMessage{}
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &doc); err != nil || doc == nil {
			return "", false
		}
	}
	var instructions []string
	if raw, ok := doc["instructions"]; ok && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &instructions); err != nil {
			return "", false
		}
	}
	found := false
	for _, item := range instructions {
		if samePath(item, instructionPath) {
			found = true
			break
		}
	}
	if !found {
		instructions = append(instructions, instructionPath)
	}
	encodedInstr, err := marshalJSONNoEscapeHTML(instructions)
	if err != nil {
		return "", false
	}
	doc["instructions"] = encodedInstr
	encodedDoc, err := marshalJSONNoEscapeHTML(doc)
	if err != nil {
		return "", false
	}
	return string(encodedDoc), true
}

func marshalJSONNoEscapeHTML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// buildLaunchGuidancePointer emits a short, neutral instruction pointing the
// harness at the generated AGENTS.md file(s) so private organization rules
// never appear in process argv.
func buildLaunchGuidancePointer(root, targetDir string) string {
	if root == "" {
		return ""
	}
	umbrellaAgents := filepath.Join(root, "AGENTS.md")
	if targetDir != "" && !samePath(root, targetDir) {
		targetAgents := filepath.Join(targetDir, "AGENTS.md")
		return fmt.Sprintf("Before acting, read %s for the binding organization workspace guidance and contract (and %s for this directory's rules).", umbrellaAgents, targetAgents)
	}
	return fmt.Sprintf("Before acting, read %s for the binding organization workspace guidance and contract.", umbrellaAgents)
}

func hasFlagWithValue(args []string, flagName, want string) bool {
	prefix := flagName + "="
	for i, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			if samePath(strings.TrimPrefix(arg, prefix), want) {
				return true
			}
		}
		if arg == flagName && i+1 < len(args) {
			if samePath(args[i+1], want) {
				return true
			}
		}
	}
	return false
}

func hasCodexConfigKey(args []string, key string) bool {
	for i, arg := range args {
		value := arg
		if (arg == "-c" || arg == "--config") && i+1 < len(args) {
			value = args[i+1]
		} else if strings.HasPrefix(arg, "--config=") {
			value = strings.TrimPrefix(arg, "--config=")
		} else if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
			value = strings.TrimPrefix(arg, "-c")
		} else {
			continue
		}
		if strings.HasPrefix(value, key+"=") || strings.HasPrefix(value, key+".") {
			return true
		}
	}
	return false
}

func hasGrokRulesOverride(args []string) bool {
	for _, arg := range args {
		switch {
		case arg == "--rules" || strings.HasPrefix(arg, "--rules="):
			return true
		case arg == "--system-prompt" || strings.HasPrefix(arg, "--system-prompt="):
			return true
		case arg == "--system-prompt-override" || strings.HasPrefix(arg, "--system-prompt-override="):
			return true
		case arg == "--system-prompt-file" || strings.HasPrefix(arg, "--system-prompt-file="):
			return true
		}
	}
	return false
}
