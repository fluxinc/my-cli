package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/fluxinc/my-cli/internal/contractstate"
	"github.com/fluxinc/my-cli/internal/harness"
)

func TestEnsureAndRemoveAntigravityContractHooksPreservesForeignGroupsAndLargeIntegers(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".gemini", "config", "hooks.json")
	writeCLITestFile(t, hooksPath, `{
  "herdr": {
    "PreInvocation": [{"type": "command", "command": "herdr hook", "timeout": 10}],
    "big_id": 9007199254740993
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
	if !strings.Contains(string(first), "9007199254740993") {
		t.Fatalf("64-bit integer corrupted across install:\n%s", string(first))
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(first, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["herdr"]; !ok {
		t.Fatal("foreign herdr hook group dropped")
	}
	if _, ok := parsed["shrooms-antigravity"]; !ok {
		t.Fatal("foreign shrooms-antigravity hook group dropped")
	}
	if !isManagedAntigravityHookGroup(parsed[antigravityContractHookGroup]) {
		t.Fatalf("installed group failed managed check: %s", string(parsed[antigravityContractHookGroup]))
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

	// Uninstall via `my skills self uninstall antigravity` removes only our group
	// and still preserves 64-bit integers in foreign groups.
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
	if !strings.Contains(string(afterUninstall), "9007199254740993") {
		t.Fatalf("64-bit integer corrupted across uninstall:\n%s", string(afterUninstall))
	}
	var afterParsed map[string]json.RawMessage
	if err := json.Unmarshal(afterUninstall, &afterParsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := afterParsed[antigravityContractHookGroup]; ok {
		t.Fatal("my-cli-contract group still present after uninstall")
	}
	if _, ok := afterParsed["herdr"]; !ok || afterParsed["shrooms-antigravity"] == nil {
		t.Fatal("foreign hook groups dropped during uninstall")
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

func TestAntigravityContractHooksPreservesSymlinkOnInstallAndRemove(t *testing.T) {
	home := t.TempDir()
	dotfilesDir := filepath.Join(home, "dotfiles")
	realHooks := filepath.Join(dotfilesDir, "agy-hooks.json")
	writeCLITestFile(t, realHooks, `{"herdr":{"big":9007199254740993}}`+"\n")

	configDir := filepath.Join(home, ".gemini", "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksLink := filepath.Join(configDir, "hooks.json")
	if err := os.Symlink(realHooks, hooksLink); err != nil {
		t.Fatal(err)
	}

	installed, _, err := ensureAntigravityContractHooks(home, "/usr/local/bin/my")
	if err != nil {
		t.Fatalf("install through symlink failed: %v", err)
	}
	if !installed {
		t.Fatal("expected installed=true")
	}
	info, err := os.Lstat(hooksLink)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("hooks.json symlink was replaced with non-symlink: info=%v err=%v", info, err)
	}
	targetData, err := os.ReadFile(realHooks)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(targetData), antigravityContractHookGroup) || !strings.Contains(string(targetData), "9007199254740993") {
		t.Fatalf("symlink target missing expected content:\n%s", string(targetData))
	}

	removed, _, err := removeAntigravityContractHooks(home, false)
	if err != nil || !removed {
		t.Fatalf("remove through symlink = (%v, %v), want (true, nil)", removed, err)
	}
	info, err = os.Lstat(hooksLink)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("hooks.json symlink was replaced during remove: info=%v err=%v", info, err)
	}
	afterRemove, _ := os.ReadFile(realHooks)
	if strings.Contains(string(afterRemove), antigravityContractHookGroup) || !strings.Contains(string(afterRemove), "9007199254740993") {
		t.Fatalf("symlink target after remove unexpected:\n%s", string(afterRemove))
	}
}

func TestAntigravityContractHooksRefusesUserEditedOrCollidingGroup(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "foreign collision",
			body: `{"my-cli-contract":{"PreInvocation":[{"type":"command","command":"my-custom-hook","timeout":5}]}}` + "\n",
		},
		{
			name: "user modified managed group",
			body: `{"my-cli-contract":{"SessionStart":[{"type":"command","command":"if [ -n \"${MYCLI_CONTRACT_INSTANCE:-}\" ]; then /usr/local/bin/my contract pending --hook antigravity --event SessionStart; else printf '{}\\n'; fi","timeout":10}],"PreInvocation":[{"type":"command","command":"if [ -n \"${MYCLI_CONTRACT_INSTANCE:-}\" ]; then /usr/local/bin/my contract pending --hook antigravity --event PreInvocation; else printf '{}\\n'; fi","timeout":10}],"Stop":[{"type":"command","command":"custom-stop","timeout":5}]}}` + "\n",
		},
		{
			name: "user added extra flag after executable",
			body: `{"my-cli-contract":{"SessionStart":[{"type":"command","command":"if [ -n \"${MYCLI_CONTRACT_INSTANCE:-}\" ]; then /bin/my --custom contract pending --hook antigravity --event SessionStart; else printf '{}\\n'; fi","timeout":10}],"PreInvocation":[{"type":"command","command":"if [ -n \"${MYCLI_CONTRACT_INSTANCE:-}\" ]; then /bin/my --custom contract pending --hook antigravity --event PreInvocation; else printf '{}\\n'; fi","timeout":10}]}}` + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			hooksPath := filepath.Join(home, ".gemini", "config", "hooks.json")
			writeCLITestFile(t, hooksPath, tc.body)

			if _, _, err := ensureAntigravityContractHooks(home, "/usr/local/bin/my"); err == nil || !strings.Contains(err.Error(), "leaving it intact") {
				t.Fatalf("install error = %v, want diagnostic leaving it intact", err)
			}
			got, _ := os.ReadFile(hooksPath)
			if string(got) != tc.body {
				t.Fatalf("install overwrote user-edited group:\ngot:  %s\nwant: %s", string(got), tc.body)
			}

			if _, _, err := removeAntigravityContractHooks(home, false); err == nil || !strings.Contains(err.Error(), "leaving it intact") {
				t.Fatalf("remove error = %v, want diagnostic leaving it intact", err)
			}
			got, _ = os.ReadFile(hooksPath)
			if string(got) != tc.body {
				t.Fatalf("remove modified user-edited group:\ngot:  %s\nwant: %s", string(got), tc.body)
			}
		})
	}
}

func TestAntigravityContractHooksSerializesConcurrentEdits(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".gemini", "config", "hooks.json")
	writeCLITestFile(t, hooksPath, `{"foreign":{"id":9007199254740993}}`+"\n")

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := ensureAntigravityContractHooks(home, "/usr/local/bin/my"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ensureAntigravityContractHooks error: %v", err)
	}
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("corrupted JSON after concurrent updates: %v\n%s", err, string(data))
	}
	if !strings.Contains(string(data), "9007199254740993") || !isManagedAntigravityHookGroup(parsed[antigravityContractHookGroup]) {
		t.Fatalf("unexpected final hooks.json:\n%s", string(data))
	}
}

func TestAntigravityContractHooksSpecialCharactersAndRelocation(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".gemini", "config", "hooks.json")

	// Special shell characters (& and space, plus apostrophe) must round-trip cleanly across install, re-install, relocation, and removal.
	firstExec := "/Studio A&B/bin/my"
	installed, _, err := ensureAntigravityContractHooks(home, firstExec)
	if err != nil || !installed {
		t.Fatalf("first install(%q) = (%v, %v), want (true, nil)", firstExec, installed, err)
	}
	installed, _, err = ensureAntigravityContractHooks(home, firstExec)
	if err != nil || installed {
		t.Fatalf("second install(%q) = (%v, %v), want (false, nil)", firstExec, installed, err)
	}

	// Genuine executable relocation updates the managed group without treating it as user-modified.
	relocatedExec := "/Studio O'Brien & Co/bin/my"
	installed, _, err = ensureAntigravityContractHooks(home, relocatedExec)
	if err != nil || installed {
		t.Fatalf("relocated install(%q) = (%v, %v), want (false, nil)", relocatedExec, installed, err)
	}
	data, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Group struct {
			PreInvocation []struct {
				Command string `json:"command"`
			} `json:"PreInvocation"`
		} `json:"my-cli-contract"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Group.PreInvocation) != 1 || !strings.Contains(parsed.Group.PreInvocation[0].Command, shellQuote(relocatedExec)) || strings.Contains(string(data), "Studio A&B") {
		t.Fatalf("relocation did not update executable path cleanly:\n%s", string(data))
	}

	removed, _, err := removeAntigravityContractHooks(home, false)
	if err != nil || !removed {
		t.Fatalf("remove after relocation = (%v, %v), want (true, nil)", removed, err)
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

func TestLaunchGuidanceArgsRepoAndGrokPreserveUserConfigAndAvoidArgvSecrets(t *testing.T) {
	body := `{"manifest_version":1,"organization":{"id":"acme","name":"Acme Example"},"contract":["PRIVATE-SECRET-RULE-99"]}`
	home, root, _, _, _ := setupCLITrackedManifestBody(t, body)
	repoDir := filepath.Join(root, "repos", "sample-service")

	// Antigravity --repo gets --add-dir <umbrella>.
	agyArgs := launchGuidanceArgs(harness.Antigravity, launchCommandOpts{repoID: "sample-service"}, root, repoDir, nil)
	if !reflect.DeepEqual(agyArgs, []string{"--add-dir", root}) {
		t.Fatalf("agy --repo args = %#v, want --add-dir %q", agyArgs, root)
	}
	if dup := launchGuidanceArgs(harness.Antigravity, launchCommandOpts{repoID: "sample-service"}, root, repoDir, []string{"--add-dir", root}); len(dup) != 0 {
		t.Fatalf("agy duplicate --add-dir = %#v", dup)
	}

	// Claude --repo already walks up parent dirs to umbrella CLAUDE.md, so no extra args.
	if claudeArgs := launchGuidanceArgs(harness.ClaudeCode, launchCommandOpts{repoID: "sample-service"}, root, repoDir, nil); len(claudeArgs) != 0 {
		t.Fatalf("claude --repo args = %#v, want empty", claudeArgs)
	}

	// Codex --repo uses project_root_markers=[".my-cli"] so preconfigured developer_instructions
	// in config.toml/profiles are never overridden and umbrella AGENTS.md + skills load natively.
	codexArgs := launchGuidanceArgs(harness.Codex, launchCommandOpts{repoID: "sample-service"}, root, repoDir, nil)
	wantCodex := []string{"-c", `project_root_markers=[".my-cli"]`}
	if !reflect.DeepEqual(codexArgs, wantCodex) {
		t.Fatalf("codex --repo args = %#v, want %#v", codexArgs, wantCodex)
	}
	if dup := launchGuidanceArgs(harness.Codex, launchCommandOpts{repoID: "sample-service"}, root, repoDir, []string{"-c", `project_root_markers=[".git"]`}); len(dup) != 0 {
		t.Fatalf("codex project_root_markers override ignored: %#v", dup)
	}

	// Grok gets neutral --rules pointer (no private rule text in argv) and --add-dir on --repo.
	grokUmb := launchGuidanceArgs(harness.Grok, launchCommandOpts{}, root, root, nil)
	if len(grokUmb) != 2 || grokUmb[0] != "--rules" || !strings.Contains(grokUmb[1], "AGENTS.md") {
		t.Fatalf("grok umbrella args = %#v", grokUmb)
	}
	if strings.Contains(strings.Join(grokUmb, " "), "PRIVATE-SECRET-RULE-99") {
		t.Fatalf("grok argv leaked private contract rule: %#v", grokUmb)
	}
	grokRepo := launchGuidanceArgs(harness.Grok, launchCommandOpts{repoID: "sample-service"}, root, repoDir, nil)
	if len(grokRepo) != 2 || grokRepo[0] != "--rules" || !strings.Contains(grokRepo[1], filepath.Join(root, "AGENTS.md")) || !strings.Contains(grokRepo[1], filepath.Join(repoDir, "AGENTS.md")) {
		t.Fatalf("grok --repo args = %#v", grokRepo)
	}
	if dup := launchGuidanceArgs(harness.Grok, launchCommandOpts{}, root, root, []string{"--rules", "mine"}); len(dup) != 0 {
		t.Fatalf("grok --rules override ignored: %#v", dup)
	}
	_ = home
}

func TestLaunchPrintAndMissingBinaryFallbackIncludeGuidanceArgs(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	if err := os.MkdirAll(filepath.Join(umbrellaRoot, ".my-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(umbrellaRoot, "repos", "sample-service")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Run from inside the inherited umbrella workspace with user flags.
	t.Chdir(umbrellaRoot)

	// --print for antigravity --repo with user flags
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "ai", "--home", home, "--manifest", "acme", "--repo", "sample-service", "--print", "agy", "--", "--model", "gemini-4"}); err != nil {
		t.Fatal(err)
	}
	gotPrint := strings.TrimSpace(stdout.String())
	if !strings.Contains(gotPrint, "--add-dir "+umbrellaRoot) || !strings.HasSuffix(gotPrint, "--model gemini-4") {
		t.Fatalf("agy --repo --print = %q, want --add-dir %s and user flags", gotPrint, umbrellaRoot)
	}

	// Missing-binary fallback for codex --repo with user flags
	stdout.Reset()
	stderr.Reset()
	aMissing := app{
		stdout:   &stdout,
		stderr:   &stderr,
		lookPath: func(string) (string, error) { return "", exec.ErrNotFound },
	}
	err := aMissing.run([]string{"my", "ai", "--home", home, "--manifest", "acme", "--no-refresh", "--no-update-check", "--repo", "sample-service", "codex", "--", "--profile", "work"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("missing binary err = %v, want errAlreadyPrinted", err)
	}
	if !strings.Contains(stderr.String(), `project_root_markers=[".my-cli"]`) || !strings.Contains(stderr.String(), "--profile work") {
		t.Fatalf("missing binary fallback stderr = %q, want project_root_markers and user flags", stderr.String())
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

func TestChangeNoticeOrdersLaunchGuidanceArgsBeforePrompt(t *testing.T) {
	cases := []struct {
		name         string
		harnessArg   string
		repoLaunch   bool
		wantPrefix   []string
		wantPromptAt int
		wantFlag     string
	}{
		{
			name:         "antigravity repo launch",
			harnessArg:   "agy",
			repoLaunch:   true,
			wantPrefix:   []string{"--add-dir", "<ROOT>"},
			wantFlag:     "--prompt-interactive",
			wantPromptAt: 3,
		},
		{
			name:         "codex repo launch",
			harnessArg:   "codex",
			repoLaunch:   true,
			wantPrefix:   []string{"-c", `project_root_markers=[".my-cli"]`},
			wantPromptAt: 2,
		},
		{
			name:         "grok umbrella launch",
			harnessArg:   "grok",
			repoLaunch:   false,
			wantPrefix:   []string{"--rules", "<POINTER>"},
			wantPromptAt: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupChangesFixture(t)
			if tc.repoLaunch {
				repoDir := filepath.Join(f.root, "repos", "sample-service")
				if err := os.MkdirAll(repoDir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			runLaunch := func() ([]string, string, error) {
				var stdout, stderr bytes.Buffer
				var gotArgs []string
				a := app{
					stdout:   &stdout,
					stderr:   &stderr,
					lookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
					execHarnessEnvironment: func(_ string, args []string, _ string, _ []string) error {
						gotArgs = append([]string(nil), args...)
						return nil
					},
				}
				args := []string{"my", "ai", "--home", f.home, "--umbrella", f.root, "--no-refresh", "--no-update-check"}
				if tc.repoLaunch {
					args = append(args, "--repo", "sample-service")
				} else {
					args = append(args, "--no-session")
				}
				args = append(args, tc.harnessArg)
				err := a.run(args)
				return gotArgs, stderr.String(), err
			}

			// Baseline launch records starting revision.
			if _, stderr, err := runLaunch(); err != nil {
				t.Fatalf("baseline launch: %v\n%s", err, stderr)
			}
			head := f.amend(t)

			// Launch after manifest change must place launchGuidanceArgs before notice prompt args.
			gotArgs, stderr, err := runLaunch()
			if err != nil {
				t.Fatalf("notice launch: %v\n%s", err, stderr)
			}
			if len(gotArgs) != tc.wantPromptAt+1 {
				t.Fatalf("args = %#v (len %d), want len %d\nstderr:\n%s", gotArgs, len(gotArgs), tc.wantPromptAt+1, stderr)
			}
			for i, want := range tc.wantPrefix {
				switch want {
				case "<ROOT>":
					if gotArgs[i] != f.root {
						t.Fatalf("args[%d] = %q, want %q (all=%#v)", i, gotArgs[i], f.root, gotArgs)
					}
				case "<POINTER>":
					if !strings.Contains(gotArgs[i], filepath.Join(f.root, "AGENTS.md")) {
						t.Fatalf("args[%d] = %q, want AGENTS.md pointer (all=%#v)", i, gotArgs[i], gotArgs)
					}
				default:
					if gotArgs[i] != want {
						t.Fatalf("args[%d] = %q, want %q (all=%#v)", i, gotArgs[i], want, gotArgs)
					}
				}
			}
			if tc.wantFlag != "" && gotArgs[tc.wantPromptAt-1] != tc.wantFlag {
				t.Fatalf("prompt flag args[%d] = %q, want %q (all=%#v)", tc.wantPromptAt-1, gotArgs[tc.wantPromptAt-1], tc.wantFlag, gotArgs)
			}
			prompt := gotArgs[tc.wantPromptAt]
			if !strings.Contains(prompt, "changes --manifest acme") || !strings.Contains(prompt, "--to "+head) {
				t.Fatalf("notice prompt = %q, want changes command for %s", prompt, head)
			}
			if state := f.noticed(t); state.Noticed != head || state.Baseline {
				t.Fatalf("notice state = %#v, want Noticed=%s", state, head)
			}
		})
	}
}

func TestRunHarnessSetsPWDAndOpenCodeRepoGuidanceEnv(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)
	if err := os.MkdirAll(filepath.Join(umbrellaRoot, ".my-cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(umbrellaRoot, "repos", "sample-service")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantAgents := filepath.Join(umbrellaRoot, "AGENTS.md")
	callerDir := t.TempDir()
	t.Chdir(callerDir)
	t.Setenv("PWD", callerDir)

	// 1. Without inherited OPENCODE_CONFIG_CONTENT: --print and missing-binary
	//    fallback include the safe generated OPENCODE_CONFIG_CONTENT.
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	var stdout, stderr bytes.Buffer
	aPrint := app{stdout: &stdout, stderr: &stderr}
	if err := aPrint.run([]string{"my", "ai", "--home", home, "--manifest", "acme", "--repo", "sample-service", "--print", "opencode"}); err != nil {
		t.Fatalf("opencode --repo --print (no inherited config): %v", err)
	}
	if !strings.Contains(stdout.String(), "OPENCODE_CONFIG_CONTENT=") || !strings.Contains(stdout.String(), wantAgents) {
		t.Fatalf("opencode --repo --print = %q, want OPENCODE_CONFIG_CONTENT with %s", stdout.String(), wantAgents)
	}

	// 2. With inherited OPENCODE_CONFIG_CONTENT containing a synthetic secret:
	//    live launch preserves and merges it in child env only; --print stdout
	//    and missing-binary stderr emit a rerunnable my ai command and NEVER
	//    disclose the synthetic secret on either stream.
	const syntheticSecret = "SYNTHETIC_API_KEY_DO_NOT_LEAK_9911"
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"provider":{"example":{"options":{"apiKey":"`+syntheticSecret+`"}}},"instructions":["/existing/rules.md"]}`)

	stdout.Reset()
	stderr.Reset()
	var gotDir string
	var gotEnv []string
	a := app{
		stdout:   &stdout,
		stderr:   &stderr,
		lookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
		execHarnessEnvironment: func(_ string, _ []string, dir string, env []string) error {
			gotDir = dir
			gotEnv = append([]string(nil), env...)
			return nil
		},
	}
	if err := a.run([]string{"my", "ai", "--home", home, "--manifest", "acme", "--no-refresh", "--no-update-check", "--repo", "sample-service", "opencode"}); err != nil {
		t.Fatalf("opencode --repo launch: %v\n%s", err, stderr.String())
	}
	if !samePath(gotDir, repoDir) {
		t.Fatalf("launch dir = %q, want %q", gotDir, repoDir)
	}
	if gotPWD := envValue(gotEnv, "PWD"); !samePath(gotPWD, repoDir) {
		t.Fatalf("launch PWD = %q, want %q", gotPWD, repoDir)
	}
	rawCfg := envValue(gotEnv, openCodeConfigContentEnv)
	if !strings.Contains(rawCfg, syntheticSecret) {
		t.Fatalf("child env OPENCODE_CONFIG_CONTENT lost inherited provider apiKey")
	}
	var cfg struct {
		Instructions []string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(rawCfg), &cfg); err != nil {
		t.Fatalf("unmarshal OPENCODE_CONFIG_CONTENT %q: %v", rawCfg, err)
	}
	if len(cfg.Instructions) != 2 || cfg.Instructions[0] != "/existing/rules.md" || !samePath(cfg.Instructions[1], wantAgents) {
		t.Fatalf("instructions = %#v, want [/existing/rules.md %s]", cfg.Instructions, wantAgents)
	}

	// --print with inherited config: rerunnable my ai command, zero secret disclosure on stdout or stderr.
	stdout.Reset()
	stderr.Reset()
	if err := aPrint.run([]string{"my", "ai", "--home", home, "--manifest", "acme", "--repo", "sample-service", "--print", "opencode", "--", "--model", "opencode/big-pickle"}); err != nil {
		t.Fatalf("opencode --repo --print (inherited config): %v", err)
	}
	if strings.Contains(stdout.String(), syntheticSecret) || strings.Contains(stderr.String(), syntheticSecret) {
		t.Fatalf("secret leaked in --print output!\nstdout=%q\nstderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String(), "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("--print serialized inherited OPENCODE_CONFIG_CONTENT: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "my ai") || !strings.Contains(stdout.String(), "--repo sample-service") || !strings.Contains(stdout.String(), "opencode -- --model opencode/big-pickle") {
		t.Fatalf("--print = %q, want rerunnable my ai command", stdout.String())
	}

	// Missing-binary fallback with inherited config: rerunnable my ai command, zero secret disclosure on stdout or stderr.
	stdout.Reset()
	stderr.Reset()
	aMissing := app{
		stdout:   &stdout,
		stderr:   &stderr,
		lookPath: func(string) (string, error) { return "", exec.ErrNotFound },
	}
	err := aMissing.run([]string{"my", "ai", "--home", home, "--manifest", "acme", "--no-refresh", "--no-update-check", "--repo", "sample-service", "opencode", "--", "--model", "opencode/big-pickle"})
	if !errors.Is(err, errAlreadyPrinted) {
		t.Fatalf("missing binary err = %v, want errAlreadyPrinted", err)
	}
	if strings.Contains(stdout.String(), syntheticSecret) || strings.Contains(stderr.String(), syntheticSecret) {
		t.Fatalf("secret leaked in missing-binary fallback!\nstdout=%q\nstderr=%q", stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("missing-binary fallback serialized inherited OPENCODE_CONFIG_CONTENT: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "my ai") || !strings.Contains(stderr.String(), "--repo sample-service") || !strings.Contains(stderr.String(), "opencode -- --model opencode/big-pickle") {
		t.Fatalf("missing-binary stderr = %q, want rerunnable my ai command", stderr.String())
	}

	// 3. Unmergeable inherited OPENCODE_CONFIG_CONTENT: preserve unchanged in child env,
	//    emit actionable non-secret diagnostic on stderr, never disclose raw contents.
	const badSecret = "SYNTHETIC_MALFORMED_SECRET_4422"
	badRaw := `{"apiKey":"` + badSecret + `", not-json`
	t.Setenv("OPENCODE_CONFIG_CONTENT", badRaw)
	stdout.Reset()
	stderr.Reset()
	gotEnv = nil
	if err := a.run([]string{"my", "ai", "--home", home, "--manifest", "acme", "--no-refresh", "--no-update-check", "--repo", "sample-service", "opencode"}); err != nil {
		t.Fatalf("opencode --repo launch with invalid config: %v", err)
	}
	if got := envValue(gotEnv, openCodeConfigContentEnv); got != badRaw {
		t.Fatalf("invalid OPENCODE_CONFIG_CONTENT was modified: got %q, want %q", got, badRaw)
	}
	if strings.Contains(stderr.String(), badSecret) || strings.Contains(stdout.String(), badSecret) {
		t.Fatalf("malformed config contents leaked on output!\nstdout=%q\nstderr=%q", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "OPENCODE_CONFIG_CONTENT could not be merged") || !strings.Contains(stderr.String(), "without adding umbrella AGENTS.md") {
		t.Fatalf("expected actionable diagnostic for unmergeable OPENCODE_CONFIG_CONTENT, got stderr=%q", stderr.String())
	}

	// 4. Relative instruction paths are deduplicated against launch targetDir, not caller cwd.
	t.Chdir(umbrellaRoot)
	mergedRelRepo, ok := mergeOpenCodeInstructionsJSON(`{"instructions":["AGENTS.md"]}`, repoDir, wantAgents)
	if !ok {
		t.Fatal("mergeOpenCodeInstructionsJSON failed for repo-relative AGENTS.md")
	}
	var relCfg struct {
		Instructions []string `json:"instructions"`
	}
	if err := json.Unmarshal([]byte(mergedRelRepo), &relCfg); err != nil {
		t.Fatal(err)
	}
	if len(relCfg.Instructions) != 2 || relCfg.Instructions[0] != "AGENTS.md" || !samePath(relCfg.Instructions[1], wantAgents) {
		t.Fatalf("relative AGENTS.md in repoDir was wrongly deduplicated against caller cwd: %#v", relCfg.Instructions)
	}

	t.Chdir(callerDir)
	mergedRelParent, ok := mergeOpenCodeInstructionsJSON(`{"instructions":["../../AGENTS.md"]}`, repoDir, wantAgents)
	if !ok {
		t.Fatal("mergeOpenCodeInstructionsJSON failed for ../../AGENTS.md")
	}
	relCfg.Instructions = nil
	if err := json.Unmarshal([]byte(mergedRelParent), &relCfg); err != nil {
		t.Fatal(err)
	}
	if len(relCfg.Instructions) != 1 || relCfg.Instructions[0] != "../../AGENTS.md" {
		t.Fatalf("expected ../../AGENTS.md relative to repoDir to deduplicate with umbrella AGENTS.md, got %#v", relCfg.Instructions)
	}
}

func TestEnsureLaunchSelfSkillAllowsLaunchWhenLegacyCleanupFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod read-only parent dir test is unix-specific")
	}
	home := t.TempDir()
	// Seed a managed legacy ~/.agents/skills/my-cli symlink inside a read-only parent
	// so cleanupLegacySkillDirs cannot remove it.
	sourceRoot := filepath.Join(home, ".local", "share", "my-cli", "skills")
	oldSource := filepath.Join(sourceRoot, "my-cli")
	writeCLITestFile(t, filepath.Join(oldSource, "SKILL.md"), "---\nname: my-cli\n---\nold\n")
	writeCLITestFile(t, filepath.Join(oldSource, ".my-cli-managed.json"), `{"installer":"my-cli","canonical_id":"my:self"}`+"\n")
	legacyParent := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(legacyParent, 0o755); err != nil {
		t.Fatal(err)
	}
	oldLink := filepath.Join(legacyParent, "my-cli")
	if err := os.Symlink(oldSource, oldLink); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(legacyParent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(legacyParent, 0o755) })

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.ensureLaunchSelfSkill(harness.Antigravity, home); err != nil {
		t.Fatalf("ensureLaunchSelfSkill should allow launch when primary install succeeds and only legacy cleanup fails: %v\nstderr:\n%s", err, stderr.String())
	}
	newLink := filepath.Join(home, ".gemini", "config", "skills", "my-cli")
	if _, err := os.Stat(filepath.Join(newLink, "SKILL.md")); err != nil {
		t.Fatalf("primary antigravity self-skill missing: %v", err)
	}
	if !strings.Contains(stderr.String(), "notice\tself-skill\tcould not clean up legacy") {
		t.Fatalf("expected non-fatal legacy cleanup notice in stderr, got:\n%s", stderr.String())
	}
}
