package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/harness"
)

const antigravityContractHookGroup = "my-cli-contract"

// ensureAntigravityContractHooks installs or updates the env-gated My AI live
// contract hook group in <home>/.gemini/config/hooks.json while preserving all
// foreign hook groups byte-for-byte, preserving dotfile symlinks, refusing to
// overwrite user-edited my-cli-contract groups, and serializing concurrent my
// writers with an exclusive file lock.
func ensureAntigravityContractHooks(home, executable string) (installed bool, hooksPath string, err error) {
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return false, "", err
		}
	}
	configDir := harness.Antigravity.ConfigDir(home)
	hooksPath = filepath.Join(configDir, "hooks.json")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return false, hooksPath, fmt.Errorf("create antigravity config dir: %w", err)
	}
	unlock, err := lockAntigravityHooks(filepath.Join(configDir, ".hooks.json.lock"))
	if err != nil {
		return false, hooksPath, err
	}
	defer unlock()

	writePath, err := resolveAntigravityHooksWriteTarget(hooksPath)
	if err != nil {
		return false, hooksPath, err
	}

	var existing []byte
	data, readErr := os.ReadFile(writePath)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return false, hooksPath, fmt.Errorf("read antigravity hooks.json: %w", readErr)
	}
	if readErr == nil {
		existing = data
	}

	merged, changed, newlyAdded, err := mergeAntigravityContractHooks(existing, hooksPath, executable)
	if err != nil {
		return false, hooksPath, err
	}
	if !changed {
		return false, hooksPath, nil
	}
	if err := writeAntigravityHooksAtomic(writePath, merged); err != nil {
		return false, hooksPath, err
	}
	return newlyAdded, hooksPath, nil
}

// removeAntigravityContractHooks removes only a My AI-managed my-cli-contract
// hook group from <home>/.gemini/config/hooks.json, preserving foreign groups
// byte-for-byte, dotfile symlinks, and user-edited my-cli-contract groups.
func removeAntigravityContractHooks(home string, dryRun bool) (removed bool, hooksPath string, err error) {
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return false, "", err
		}
	}
	configDir := harness.Antigravity.ConfigDir(home)
	hooksPath = filepath.Join(configDir, "hooks.json")

	if _, err := os.Lstat(hooksPath); errors.Is(err, fs.ErrNotExist) {
		return false, hooksPath, nil
	}
	unlock, err := lockAntigravityHooks(filepath.Join(configDir, ".hooks.json.lock"))
	if err != nil {
		return false, hooksPath, err
	}
	defer unlock()

	writePath, err := resolveAntigravityHooksWriteTarget(hooksPath)
	if err != nil {
		return false, hooksPath, err
	}

	existing, readErr := os.ReadFile(writePath)
	if errors.Is(readErr, fs.ErrNotExist) {
		return false, hooksPath, nil
	}
	if readErr != nil {
		return false, hooksPath, fmt.Errorf("read antigravity hooks.json: %w", readErr)
	}
	root, err := parseAntigravityHooksRoot(existing)
	if err != nil {
		return false, hooksPath, err
	}
	rawGroup, exists := root[antigravityContractHookGroup]
	if !exists {
		return false, hooksPath, nil
	}
	if !isManagedAntigravityHookGroup(rawGroup) {
		return false, hooksPath, fmt.Errorf("%s hook group in %s was modified or is not My AI-managed; leaving it intact", antigravityContractHookGroup, hooksPath)
	}
	if dryRun {
		return true, hooksPath, nil
	}
	delete(root, antigravityContractHookGroup)
	out, err := marshalAntigravityHooksRoot(root)
	if err != nil {
		return false, hooksPath, err
	}
	if err := writeAntigravityHooksAtomic(writePath, out); err != nil {
		return false, hooksPath, err
	}
	return true, hooksPath, nil
}

// resolveAntigravityHooksWriteTarget keeps dotfile symlinks intact by writing
// atomically to the resolved target file instead of replacing the symlink.
func resolveAntigravityHooksWriteTarget(hooksPath string) (string, error) {
	info, err := os.Lstat(hooksPath)
	if errors.Is(err, fs.ErrNotExist) {
		return hooksPath, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect antigravity hooks.json: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return hooksPath, nil
	}
	resolved, err := filepath.EvalSymlinks(hooksPath)
	if err != nil {
		return "", fmt.Errorf("antigravity hooks.json at %s is a broken or unreadable symlink; fix the symlink target before installing hooks: %w", hooksPath, err)
	}
	return resolved, nil
}

func writeAntigravityHooksAtomic(targetPath string, data []byte) error {
	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create antigravity hooks dir: %w", err)
	}
	perm := fs.FileMode(0o644)
	if info, err := os.Stat(targetPath); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".hooks-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), targetPath)
}

func parseAntigravityHooksRoot(existing []byte) (map[string]json.RawMessage, error) {
	root := map[string]json.RawMessage{}
	trimmed := bytes.TrimSpace(existing)
	if len(trimmed) == 0 {
		return root, nil
	}
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return nil, fmt.Errorf("parse antigravity hooks.json: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("antigravity hooks.json must be a JSON object")
	}
	return root, nil
}

func marshalAntigravityHooksRoot(root map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(root))
	for k := range root {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString("\n  ")
		keyJSON, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteString(": ")
		var indented bytes.Buffer
		if err := json.Indent(&indented, bytes.TrimSpace(root[k]), "  ", "  "); err != nil {
			return nil, err
		}
		buf.Write(indented.Bytes())
	}
	if len(keys) > 0 {
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}

func buildAntigravityContractHookGroup(executable string) (json.RawMessage, error) {
	hookCmd := func(event string) string {
		return fmt.Sprintf(
			`if [ -n "${%s:-}" ]; then %s contract pending --hook antigravity --event %s; else printf '{}\n'; fi`,
			contractInstanceEnv, shellQuote(executable), event,
		)
	}
	group := map[string]any{
		"SessionStart": []map[string]any{
			{"type": "command", "command": hookCmd("SessionStart"), "timeout": 10},
		},
		"PreInvocation": []map[string]any{
			{"type": "command", "command": hookCmd("PreInvocation"), "timeout": 10},
		},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(group); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimSpace(buf.Bytes())), nil
}

// isManagedAntigravityHookGroup verifies that an existing my-cli-contract
// entry matches My AI's exact managed hook schema and commands for one
// canonical shell-quoted absolute executable path (allowing the executable
// path to relocate across upgrades). User-created or user-edited groups fail
// this check and are preserved untouched.
func isManagedAntigravityHookGroup(raw json.RawMessage) bool {
	var events map[string]json.RawMessage
	if err := json.Unmarshal(raw, &events); err != nil || len(events) != 2 {
		return false
	}
	var commonExec string
	for _, event := range []string{"SessionStart", "PreInvocation"} {
		entriesRaw, ok := events[event]
		if !ok {
			return false
		}
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(entriesRaw, &entries); err != nil || len(entries) != 1 {
			return false
		}
		entry := entries[0]
		if len(entry) != 3 {
			return false
		}
		var typ, cmd string
		var timeout int
		if err := json.Unmarshal(entry["type"], &typ); err != nil || typ != "command" {
			return false
		}
		if err := json.Unmarshal(entry["timeout"], &timeout); err != nil || timeout != 10 {
			return false
		}
		if err := json.Unmarshal(entry["command"], &cmd); err != nil {
			return false
		}
		prefix := fmt.Sprintf(`if [ -n "${%s:-}" ]; then `, contractInstanceEnv)
		suffix := fmt.Sprintf(` contract pending --hook antigravity --event %s; else printf '{}\n'; fi`, event)
		if !strings.HasPrefix(cmd, prefix) || !strings.HasSuffix(cmd, suffix) {
			return false
		}
		execPart := strings.TrimSuffix(strings.TrimPrefix(cmd, prefix), suffix)
		unquoted, ok := parseCanonicalShellQuotedPath(execPart)
		if !ok || !filepath.IsAbs(unquoted) {
			return false
		}
		if commonExec == "" {
			commonExec = unquoted
		} else if commonExec != unquoted {
			return false
		}
	}
	return commonExec != ""
}

// parseCanonicalShellQuotedPath parses a single argument produced by shellQuote
// and rejects unquoted whitespace, extra arguments, or non-canonical quoting.
func parseCanonicalShellQuotedPath(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	if !strings.HasPrefix(token, "'") {
		if shellQuote(token) != token {
			return "", false
		}
		return token, true
	}
	if len(token) < 2 || !strings.HasSuffix(token, "'") {
		return "", false
	}
	inner := token[1 : len(token)-1]
	var b strings.Builder
	for i := 0; i < len(inner); {
		if inner[i] == '\'' {
			if strings.HasPrefix(inner[i:], `'\''`) {
				b.WriteByte('\'')
				i += 4
				continue
			}
			return "", false
		}
		b.WriteByte(inner[i])
		i++
	}
	unquoted := b.String()
	if unquoted == "" || shellQuote(unquoted) != token {
		return "", false
	}
	return unquoted, true
}

func mergeAntigravityContractHooks(existing []byte, hooksPath, executable string) (merged []byte, changed bool, newlyAdded bool, err error) {
	root, err := parseAntigravityHooksRoot(existing)
	if err != nil {
		return nil, false, false, err
	}
	prevRaw, hadGroup := root[antigravityContractHookGroup]
	if hadGroup && !isManagedAntigravityHookGroup(prevRaw) {
		return nil, false, false, fmt.Errorf("%s hook group in %s was modified or is not My AI-managed; leaving it intact", antigravityContractHookGroup, hooksPath)
	}
	newGroup, err := buildAntigravityContractHookGroup(executable)
	if err != nil {
		return nil, false, false, err
	}
	if hadGroup {
		var prevCompact, newCompact bytes.Buffer
		if json.Compact(&prevCompact, prevRaw) == nil && json.Compact(&newCompact, newGroup) == nil && bytes.Equal(prevCompact.Bytes(), newCompact.Bytes()) {
			return existing, false, false, nil
		}
	}
	root[antigravityContractHookGroup] = newGroup
	out, err := marshalAntigravityHooksRoot(root)
	if err != nil {
		return nil, false, false, err
	}
	return out, true, !hadGroup, nil
}

func (a app) ensureAntigravityHooksWithNotice(home string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	installed, hooksPath, err := ensureAntigravityContractHooks(home, executable)
	if err != nil {
		return err
	}
	if installed && a.stderr != nil {
		fmt.Fprintf(a.stderr, "antigravity\thooks\tinstalled\t%s (env-gated on %s; remove with my skills self uninstall antigravity)\n", hooksPath, contractInstanceEnv)
	}
	return nil
}

func harnessSelected(hs []harness.Harness, want harness.Harness) bool {
	for _, h := range hs {
		if h == want {
			return true
		}
	}
	return false
}

func antigravityPresentInHome(home string) bool {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return false
		}
	}
	if _, err := os.Stat(harness.Antigravity.ConfigDir(home)); err == nil {
		return true
	}
	if _, err := os.Stat(harness.Antigravity.PresenceDir(home)); err == nil {
		return true
	}
	return false
}
