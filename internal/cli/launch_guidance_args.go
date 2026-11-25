package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/fluxinc/my-cli/internal/harness"
)

// launchGuidanceArgs returns harness-specific CLI flags that ensure the
// organization contract and umbrella guidance reach the model in launch modes
// where the harness would otherwise miss them, without placing private contract
// text in process argv:
//   - --repo launches: nested git checkouts stop Antigravity, Cursor, and Codex
//     from discovering the parent umbrella's AGENTS.md (Claude Code already
//     walks up to umbrella CLAUDE.md). Antigravity and Cursor accept
//     --add-dir <umbrella>; Codex accepts -c developer_instructions=<pointer>.
//   - Grok launches: Grok skips project AGENTS.md and project skills when a
//     directory is untrusted (every new umbrella and session dir), so passing a
//     neutral --rules pointer tells Grok to read the generated AGENTS.md file.
func launchGuidanceArgs(h harness.Harness, opts launchCommandOpts, doc registeredDoc, root, targetDir string, existing []string) []string {
	var extra []string
	if opts.repoID != "" && root != "" {
		switch h {
		case harness.Antigravity, harness.Cursor:
			if !hasFlagWithValue(existing, "--add-dir", root) {
				extra = append(extra, "--add-dir", root)
			}
		case harness.Codex:
			if !hasCodexConfigKey(existing, "developer_instructions") {
				if text := buildLaunchGuidancePointer(root, targetDir); text != "" {
					encoded, err := json.Marshal(text)
					if err == nil {
						extra = append(extra, "-c", "developer_instructions="+string(encoded))
					}
				}
			}
		case harness.Grok:
			if !hasFlagWithValue(existing, "--add-dir", root) {
				extra = append(extra, "--add-dir", root)
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
