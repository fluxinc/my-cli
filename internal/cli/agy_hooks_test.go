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

func TestEnsureAndRemoveAntigravityContractHooksPreservesForeignGroups(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".gemini", "config", "hooks.json")
	writeCLITestFile(t, hooksPath, `{
  "herdr": {
    "PreInvocation": [{"type": "command", "command": "herdr hook", "timeout": 10}]
  },
  "shrooms-antigravity": {
    "SessionStart": [{"type": "command", "command": "tt antigravity-hook SessionStart", "timeout": 3}]
  }
}
`)
	installed, gotPath, err := ensureAntigravityContractHooks(home, "/opt/my cli/my")
	if err != nil {
		t.Fatal(err)
	}
	if !installed || gotPath != hooksPath {
		t.Fatalf("first install = (%v, %q), want (true, %q)", installed, gotPath, hooksPath)
	}
	first, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(first, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["herdr"]; !ok {
		t.Fatal("foreign herdr hook group dropped")
	}
	if _, ok := parsed["shrooms-antigravity"]; !ok {
		t.Fatal("foreign shrooms-antigravity hook group dropped")
	}
	group, ok := parsed[antigravityContractHookGroup].(map[string]any)
	if !ok {
		t.Fatalf("missing %s hook group: %#v", antigravityContractHookGroup, parsed)
	}
	if _, ok := group["SessionStart"]; !ok {
		t.Fatal("missing SessionStart hook")
	}
	if _, ok := group["PreInvocation"]; !ok {
		t.Fatal("missing PreInvocation hook")
	}
	if !strings.Contains(string(first), contractInstanceEnv) || !strings.Contains(string(first), "--hook antigravity --event PreInvocation") {
		t.Fatalf("unexpected hooks.json content: %s", string(first))
	}

	// Second call must be a no-op with installed=false.
	installed, _, err = ensureAntigravityContractHooks(home, "/opt/my cli/my")
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		t.Fatal("second install reported newly installed")
	}
	second, _ := os.ReadFile(hooksPath)
	if !bytes.Equal(first, second) {
		t.Fatal("ensureAntigravityContractHooks rewrote unchanged hooks.json")
	}

	// Uninstall via `my skills self uninstall antigravity` removes only our group.
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "skills", "self", "uninstall", "antigravity", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "antigravity\thooks\tremoved") {
		t.Fatalf("uninstall stderr = %q, want hook removal notice", stderr.String())
	}
	afterUninstall, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var afterParsed map[string]any
	if err := json.Unmarshal(afterUninstall, &afterParsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := afterParsed[antigravityContractHookGroup]; ok {
		t.Fatal("my-cli-contract group still present after uninstall")
	}
	if _, ok := afterParsed["herdr"]; !ok {
		t.Fatal("foreign herdr hook group dropped during uninstall")
	}
	if _, ok := afterParsed["shrooms-antigravity"]; !ok {
		t.Fatal("foreign shrooms-antigravity hook group dropped during uninstall")
	}

	// Malformed hooks.json must fail without overwriting.
	writeCLITestFile(t, hooksPath, `{invalid json`)
	if _, _, err := ensureAntigravityContractHooks(home, "/opt/my cli/my"); err == nil {
		t.Fatal("expected error for malformed hooks.json")
	}
	after, _ := os.ReadFile(hooksPath)
	if string(after) != `{invalid json` {
		t.Fatal("malformed hooks.json was overwritten")
	}
}

func TestAntigravityContractPendingHookCommandAndNoopWithoutEnv(t *testing.T) {
	t.Setenv(contractHomeEnv, "")
	t.Setenv(contractManifestEnv, "")
	t.Setenv(contractInstanceEnv, "")
	out := &bytes.Buffer{}
	a := app{stdout: out, stderr: &bytes.Buffer{}, stdin: strings.NewReader(`{"conversationId":"conv-1"}`)}
	if err := a.runContractPending([]string{"--hook", "antigravity", "--event", "PreInvocation"}); err != nil {
		t.Fatalf("no-op hook outside my ai failed: %v", err)
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("no-op output = %q, want {}", out.String())
	}

	body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":["Always verify launch loading."]}`
	home, root, _, _, _ := setupCLITrackedManifestBody(t, body)
	if _, _, err := observeContract(home, "acme", "", false); err != nil {
		t.Fatal(err)
	}
	var gotInstance string
	stderr := &bytes.Buffer{}
	launchApp := app{
		stdout:       &bytes.Buffer{},
		stderr:       stderr,
		harnessProbe: func(string, ...string) ([]byte, error) { return []byte("1.5.17\n"), nil },
		execHarnessEnvironment: func(_ string, _ []string, _ string, env []string) error {
			for _, e := range env {
				if strings.HasPrefix(e, contractInstanceEnv+"=") {
					gotInstance = strings.TrimPrefix(e, contractInstanceEnv+"=")
				}
			}
			t.Setenv(contractHomeEnv, home)
			t.Setenv(contractManifestEnv, "acme")
			t.Setenv(contractInstanceEnv, gotInstance)
			hookOut := &bytes.Buffer{}
			hookApp := app{stdout: hookOut, stderr: &bytes.Buffer{}, stdin: strings.NewReader(`{"conversationId":"56036ed1-d52c-4e48-9143-1577ac619e1e"}`)}
			if err := hookApp.runContractPending([]string{"--hook", "antigravity", "--event", "PreInvocation"}); err != nil {
				return err
			}
			if !strings.Contains(hookOut.String(), `"injectSteps"`) || !strings.Contains(hookOut.String(), "Always verify launch loading.") {
				t.Fatalf("unexpected PreInvocation output: %s", hookOut.String())
			}
			return nil
		},
	}
	if err := launchApp.runContractHarness(harness.Antigravity, "agy", []string{"--print", "hi"}, root, home, "acme", root); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "antigravity\thooks\tinstalled") {
		t.Fatalf("first launch stderr = %q, want hook install notice", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "hooks.json")); err != nil {
		t.Fatalf("hooks.json not installed: %v", err)
	}
	state, err := contractstate.Load(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if state.Instances[gotInstance].Emitted == nil || state.Instances[gotInstance].Delivery != "emitted" {
		t.Fatalf("antigravity launch did not record emitted receipt: %#v", state.Instances[gotInstance])
	}
}

func TestAntigravityContractResumeTarget(t *testing.T) {
	const uuidA = "56036ed1-d52c-4e48-9143-1577ac619e1e"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-r", uuidA}, uuidA},
		{[]string{"--resume", uuidA}, uuidA},
		{[]string{"--conversation", uuidA}, uuidA},
		{[]string{"--conversation=" + uuidA}, uuidA},
		{[]string{"--resume=" + uuidA}, uuidA},
		{[]string{"--continue"}, ""},
		{[]string{"-r", "not-a-uuid"}, ""},
	} {
		if got := contractResumeTarget(harness.Antigravity, tc.args); got != tc.want {
			t.Errorf("contractResumeTarget(antigravity, %q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestParseLaunchArgsStripsDoubleDashAfterHarness(t *testing.T) {
	_, name, harnessArgs, help, err := parseLaunchArgs([]string{"--no-session", "agy", "--", "--print", "hello"})
	if err != nil || help {
		t.Fatalf("err=%v help=%v", err, help)
	}
	if name != "agy" || !reflect.DeepEqual(harnessArgs, []string{"--print", "hello"}) {
		t.Fatalf("name=%q harnessArgs=%#v", name, harnessArgs)
	}
	_, name, harnessArgs, help, err = parseLaunchArgs([]string{"-r", "codex", "--", "exec", "hello"})
	if err != nil || help {
		t.Fatalf("err=%v help=%v", err, help)
	}
	if name != "codex" || !reflect.DeepEqual(harnessArgs, []string{"exec", "hello"}) {
		t.Fatalf("name=%q harnessArgs=%#v", name, harnessArgs)
	}
}

func TestLaunchGuidanceArgsRepoAndGrokKeepPrivateContractOutOfArgv(t *testing.T) {
	body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme Example"},"contract":["PRIVATE-SECRET-RULE-99"]}`
	home, root, _, _, _ := setupCLITrackedManifestBody(t, body)
	doc, err := loadSingleRegisteredDoc(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(root, "repos", "sample-service")

	// Antigravity --repo gets --add-dir <umbrella>.
	agyArgs := launchGuidanceArgs(harness.Antigravity, launchCommandOpts{repoID: "sample-service"}, doc, root, repoDir, nil)
	if !reflect.DeepEqual(agyArgs, []string{"--add-dir", root}) {
		t.Fatalf("agy --repo args = %#v, want --add-dir %q", agyArgs, root)
	}
	if dup := launchGuidanceArgs(harness.Antigravity, launchCommandOpts{repoID: "sample-service"}, doc, root, repoDir, []string{"--add-dir", root}); len(dup) != 0 {
		t.Fatalf("agy duplicate --add-dir = %#v", dup)
	}

	// Claude --repo already walks up parent dirs to umbrella CLAUDE.md, so no extra args.
	if claudeArgs := launchGuidanceArgs(harness.ClaudeCode, launchCommandOpts{repoID: "sample-service"}, doc, root, repoDir, nil); len(claudeArgs) != 0 {
		t.Fatalf("claude --repo args = %#v, want empty", claudeArgs)
	}

	// Codex --repo gets neutral developer_instructions pointer (no private rule text in argv).
	codexArgs := launchGuidanceArgs(harness.Codex, launchCommandOpts{repoID: "sample-service"}, doc, root, repoDir, nil)
	if len(codexArgs) != 2 || codexArgs[0] != "-c" || !strings.HasPrefix(codexArgs[1], "developer_instructions=") || !strings.Contains(codexArgs[1], "AGENTS.md") {
		t.Fatalf("codex --repo args = %#v", codexArgs)
	}
	if strings.Contains(strings.Join(codexArgs, " "), "PRIVATE-SECRET-RULE-99") {
		t.Fatalf("codex argv leaked private contract rule: %#v", codexArgs)
	}
	if dup := launchGuidanceArgs(harness.Codex, launchCommandOpts{repoID: "sample-service"}, doc, root, repoDir, []string{"-c", "developer_instructions=\"custom\""}); len(dup) != 0 {
		t.Fatalf("codex override ignored: %#v", dup)
	}

	// Grok gets neutral --rules pointer (no private rule text in argv) and --add-dir on --repo.
	grokUmb := launchGuidanceArgs(harness.Grok, launchCommandOpts{}, doc, root, root, nil)
	if len(grokUmb) != 2 || grokUmb[0] != "--rules" || !strings.Contains(grokUmb[1], "AGENTS.md") {
		t.Fatalf("grok umbrella args = %#v", grokUmb)
	}
	if strings.Contains(strings.Join(grokUmb, " "), "PRIVATE-SECRET-RULE-99") {
		t.Fatalf("grok argv leaked private contract rule: %#v", grokUmb)
	}
	grokRepo := launchGuidanceArgs(harness.Grok, launchCommandOpts{repoID: "sample-service"}, doc, root, repoDir, nil)
	if len(grokRepo) != 4 || grokRepo[0] != "--add-dir" || grokRepo[1] != root || grokRepo[2] != "--rules" || !strings.Contains(grokRepo[3], "AGENTS.md") {
		t.Fatalf("grok --repo args = %#v", grokRepo)
	}
	if dup := launchGuidanceArgs(harness.Grok, launchCommandOpts{}, doc, root, root, []string{"--rules", "mine"}); len(dup) != 0 {
		t.Fatalf("grok --rules override ignored: %#v", dup)
	}
}

func TestEnsureLaunchSelfSkillMigratesLegacyAntigravityInstall(t *testing.T) {
	home := t.TempDir()
	// Seed an older My AI-managed ~/.agents/skills/my-cli symlink (pre-migration layout).
	sourceRoot := filepath.Join(home, ".local", "share", "my-cli", "skills")
	oldSource := filepath.Join(sourceRoot, "my-cli")
	writeCLITestFile(t, filepath.Join(oldSource, "SKILL.md"), "---\nname: my-cli\n---\nold\n")
	writeCLITestFile(t, filepath.Join(oldSource, ".my-cli-managed.json"), `{"installer":"my-cli","canonical_id":"my:self"}`+"\n")
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldLink := filepath.Join(home, ".agents", "skills", "my-cli")
	if err := os.Symlink(oldSource, oldLink); err != nil {
		t.Fatal(err)
	}

	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if err := a.ensureLaunchSelfSkill(harness.Antigravity, home); err != nil {
		t.Fatalf("ensureLaunchSelfSkill(antigravity) failed during legacy migration: %v", err)
	}
	newLink := filepath.Join(home, ".gemini", "config", "skills", "my-cli")
	if _, err := os.Stat(filepath.Join(newLink, "SKILL.md")); err != nil {
		t.Fatalf("new antigravity self-skill missing: %v", err)
	}
	if _, err := os.Lstat(oldLink); !os.IsNotExist(err) {
		t.Fatalf("legacy ~/.agents/skills/my-cli was not removed: %v", err)
	}
}
