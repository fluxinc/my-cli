package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/contractstate"
	"github.com/fluxinc/my-cli/internal/harness"
)

func TestContractAdapterDefinitionsAreStableAndPreserveSettings(t *testing.T) {
	home := t.TempDir()
	existing := filepath.Join(home, ".claude", "settings.json")
	writeCLITestFile(t, existing, `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"existing"}]}]}}`)
	original, _ := os.ReadFile(existing)
	a, err := contractAdapterArgs(home, harness.ClaudeCode, "/opt/my cli/my")
	if err != nil {
		t.Fatal(err)
	}
	b, err := contractAdapterArgs(home, harness.ClaudeCode, "/opt/my cli/my")
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatal("hook definition changes between launches")
	}
	data, err := os.ReadFile(a[1])
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "PostToolUse") || strings.Contains(string(data), "--instance") {
		t.Fatal("missing tool hook or per-instance command invalidates trust")
	}
	after, _ := os.ReadFile(existing)
	if !bytes.Equal(original, after) {
		t.Fatal("user hooks modified")
	}
	args, err := contractAdapterArgs(home, harness.Codex, "/opt/my cli/my")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "bypass-hook-trust") || !strings.Contains(joined, "additionalContextLimit=0") || !strings.Contains(joined, "hooks.PreToolUse") {
		t.Fatal(joined)
	}
	if !conflictingContractHookArgs(harness.ClaudeCode, []string{"--settings", "mine.json"}) || !conflictingContractHookArgs(harness.Codex, []string{"-c", "hooks.SessionStart=[]"}) {
		t.Fatal("explicit user settings not detected")
	}
}

func TestContractLaunchEnvironmentDoesNotLeakParentInstance(t *testing.T) {
	env := contractLaunchEnvironment([]string{"PATH=/bin", contractInstanceEnv + "=parent", contractHomeEnv + "=/old", contractManifestEnv + "=old"}, "/new", "new", "child")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "parent") || strings.Contains(joined, "/old") || !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, contractInstanceEnv+"=child") {
		t.Fatal(joined)
	}
}

func TestContractPendingFallbackDoesNotAdvanceReceipt(t *testing.T) {
	home := t.TempDir()
	_, err := contractstate.Update(home, "acme", func(s *contractstate.State) error {
		s.Observe(contractstate.Snapshot{Configured: true, Revision: contractstate.Revision{ManifestCommit: "one", Blob: "one"}, Text: "- Old.\n"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := contractstate.Register(home, "acme", "grok", "/base", "", "unsupported", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(contractHomeEnv, home)
	t.Setenv(contractManifestEnv, "acme")
	t.Setenv(contractInstanceEnv, instance.ID)
	t.Setenv("CODEX_THREAD_ID", "")
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	a := app{stdout: out, stderr: stderr}
	a.printContractFallback([]string{"my", "version"})
	if stderr.Len() != 0 {
		t.Fatal("unchanged fallback emitted noise")
	}
	_, err = contractstate.Update(home, "acme", func(s *contractstate.State) error {
		s.Observe(contractstate.Snapshot{Revision: contractstate.Revision{ManifestCommit: "two", Blob: "empty"}})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a.printContractFallback([]string{"my", "version"})
	if !strings.Contains(stderr.String(), "RETRACTED") || !strings.Contains(stderr.String(), "EMPTY") {
		t.Fatal(stderr.String())
	}
	if err := a.runContractPending([]string{"--json"}); err != nil {
		t.Fatal(err)
	}
	s, _ := contractstate.Load(home, "acme")
	if s.Instances[instance.ID].Emitted != nil {
		t.Fatal("manual output advanced emission receipt")
	}
}

func TestContractHookDoesNotRunStartupMaintenance(t *testing.T) {
	if shouldAutoSyncSelfSkill([]string{"my", "contract", "pending", "--hook", "codex"}) {
		t.Fatal("hook invokes startup mutations")
	}
}

func TestContractLaunchRegistersIsolatedEnvironmentAndHookReceipt(t *testing.T) {
	body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":["Rule."]}`
	home, root, _, _, _ := setupCLITrackedManifestBody(t, body)
	if _, _, err := observeContract(home, "acme", "", false); err != nil {
		t.Fatal(err)
	}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, harnessProbe: func(string, ...string) ([]byte, error) { return []byte("codex-cli 0.163.0"), nil }}
	var ids []string
	a.execHarnessEnvironment = func(_ string, args []string, dir string, env []string) error {
		if dir != root || !strings.Contains(strings.Join(args, " "), "hooks.SessionStart") {
			t.Fatal("missing launch hook configuration")
		}
		id := ""
		for _, entry := range env {
			if strings.HasPrefix(entry, contractInstanceEnv+"=") {
				id = strings.TrimPrefix(entry, contractInstanceEnv+"=")
			}
		}
		if id == "" {
			t.Fatal("missing instance environment")
		}
		ids = append(ids, id)
		return contractstate.Deliver(home, "acme", id, "codex", contractstate.HookInput{Event: "SessionStart", SessionID: "native-" + id}, &bytes.Buffer{})
	}
	for i := 0; i < 2; i++ {
		if err := a.runContractHarness(harness.Codex, "codex", []string{"prompt"}, root, home, "acme", root); err != nil {
			t.Fatal(err)
		}
	}
	if ids[0] == ids[1] {
		t.Fatal("separate launches share receipt")
	}
	state, err := contractstate.Load(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if state.Instances[id].Emitted == nil || state.Instances[id].ClosedAt == "" {
			t.Fatal("launch receipt or close state missing")
		}
	}
}
