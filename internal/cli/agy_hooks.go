package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fluxinc/my-cli/internal/harness"
)

const antigravityContractHookGroup = "my-cli-contract"

// ensureAntigravityContractHooks installs or updates the env-gated My AI live
// contract hook group in <home>/.gemini/config/hooks.json while preserving all
// foreign hook groups. Antigravity does not accept hooks via --settings or
// project-local hooks.json, so gating on MYCLI_CONTRACT_INSTANCE keeps non-my
// agy sessions untouched. installed is true only when the group is newly added.
func ensureAntigravityContractHooks(home, executable string) (installed bool, hooksPath string, err error) {
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return false, "", err
		}
	}
	configDir := harness.Antigravity.ConfigDir(home)
	hooksPath = filepath.Join(configDir, "hooks.json")

	var existing []byte
	data, readErr := os.ReadFile(hooksPath)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return false, hooksPath, fmt.Errorf("read antigravity hooks.json: %w", readErr)
	}
	if readErr == nil {
		existing = data
	}

	merged, changed, newlyAdded, err := mergeAntigravityContractHooks(existing, executable)
	if err != nil {
		return false, hooksPath, err
	}
	if !changed {
		return false, hooksPath, nil
	}
	if err := writeAntigravityHooksAtomic(configDir, hooksPath, merged); err != nil {
		return false, hooksPath, err
	}
	return newlyAdded, hooksPath, nil
}

// removeAntigravityContractHooks removes only the My AI contract hook group
// from <home>/.gemini/config/hooks.json, leaving foreign groups intact.
func removeAntigravityContractHooks(home string, dryRun bool) (removed bool, hooksPath string, err error) {
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return false, "", err
		}
	}
	configDir := harness.Antigravity.ConfigDir(home)
	hooksPath = filepath.Join(configDir, "hooks.json")

	existing, readErr := os.ReadFile(hooksPath)
	if errors.Is(readErr, fs.ErrNotExist) {
		return false, hooksPath, nil
	}
	if readErr != nil {
		return false, hooksPath, fmt.Errorf("read antigravity hooks.json: %w", readErr)
	}
	root := map[string]any{}
	if trimmed := bytes.TrimSpace(existing); len(trimmed) != 0 {
		if err := json.Unmarshal(trimmed, &root); err != nil {
			return false, hooksPath, fmt.Errorf("parse antigravity hooks.json: %w", err)
		}
		if root == nil {
			return false, hooksPath, fmt.Errorf("antigravity hooks.json must be a JSON object")
		}
	}
	if _, exists := root[antigravityContractHookGroup]; !exists {
		return false, hooksPath, nil
	}
	if dryRun {
		return true, hooksPath, nil
	}
	delete(root, antigravityContractHookGroup)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, hooksPath, err
	}
	out = append(out, '\n')
	if err := writeAntigravityHooksAtomic(configDir, hooksPath, out); err != nil {
		return false, hooksPath, err
	}
	return true, hooksPath, nil
}

func writeAntigravityHooksAtomic(configDir, hooksPath string, data []byte) error {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("create antigravity config dir: %w", err)
	}
	tmp, err := os.CreateTemp(configDir, ".hooks-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), hooksPath)
}

func mergeAntigravityContractHooks(existing []byte, executable string) (merged []byte, changed bool, newlyAdded bool, err error) {
	root := map[string]any{}
	if trimmed := bytes.TrimSpace(existing); len(trimmed) != 0 {
		if err := json.Unmarshal(trimmed, &root); err != nil {
			return nil, false, false, fmt.Errorf("parse antigravity hooks.json: %w", err)
		}
		if root == nil {
			return nil, false, false, fmt.Errorf("antigravity hooks.json must be a JSON object")
		}
	}
	_, hadGroup := root[antigravityContractHookGroup]

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

	before, _ := json.Marshal(root)
	root[antigravityContractHookGroup] = group
	after, err := json.Marshal(root)
	if err != nil {
		return nil, false, false, err
	}
	if len(existing) != 0 && bytes.Equal(before, after) {
		return existing, false, false, nil
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, false, err
	}
	out = append(out, '\n')
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
