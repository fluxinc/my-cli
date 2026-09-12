package cli

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

// Boundary regression tests authored during Claude's boundary-test turn
// (findings F1/F1b/F3/F2). They encode the agreed contract:
//   - onboard never silently mutates on EOF/no-input (F1, F1b);
//   - declining a review never marks the tour complete (F3);
//   - human-facing prompts re-prompt on invalid input rather than aborting (F2).
// onboard marks the tour complete ONLY when setup is actually run.

// F1: unconfigured onboard with empty stdin (EOF) must not run setup or create
// state — it should print guidance and leave the tour unmarked, like an
// explicit decline.
func TestOnboardUnconfiguredEOFDoesNotMutate(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader(""))}
	if err := a.run([]string{"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard EOF: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := umbrella.LoadState(umbrellaRoot); !os.IsNotExist(err) {
		t.Fatalf("EOF onboard must not create state (no silent setup): %v", err)
	}
	if !strings.Contains(stdout.String(), "Next step:") {
		t.Fatalf("EOF onboard should print unmarked guidance:\n%s", stdout.String())
	}
}

// F1b: configured-but-not-toured onboard with empty stdin (EOF) must not mark
// the tour complete.
func TestOnboardConfiguredEOFDoesNotMarkTour(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := configuredUmbrella(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader(""))}
	if err := a.run([]string{"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard configured EOF: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	assertTourUnmarkedRolePreserved(t, umbrellaRoot)
}

// F3: configured-but-not-toured onboard with an explicit decline ("n") must not
// mark the tour complete — declining means "later", not "done".
func TestOnboardConfiguredDeclineDoesNotMarkTour(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := configuredUmbrella(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader("n\n"))}
	if err := a.run([]string{"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard configured decline: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	assertTourUnmarkedRolePreserved(t, umbrellaRoot)
}

// F2 (confirm): a human typo at the confirm prompt must re-prompt rather than
// abort the whole tour with an error.
func TestOnboardConfirmRepromptsOnInvalidInput(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader("maybe\nn\n"))}
	if err := a.run([]string{"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard invalid-then-decline should not error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := umbrella.LoadState(umbrellaRoot); !os.IsNotExist(err) {
		t.Fatalf("declined onboard must not create state: %v", err)
	}
}

// F2 (role): a typo'd role at the interactive role prompt must re-prompt rather
// than abort setup.
func TestSetupInteractiveRoleRepromptsOnInvalidInput(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr, stdin: bufio.NewReader(strings.NewReader("bogus-role\noperator\n"))}
	if err := a.run([]string{"my", "setup", "--interactive", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("setup --interactive invalid-then-valid role should not error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatal(err)
	}
	if state.SelectedRole != "operator" {
		t.Fatalf("selected role after reprompt = %q, want operator", state.SelectedRole)
	}
}

func TestOnboardAgentZeroManifestExecsHarnessFromNeutralDir(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	var gotPath, gotDir string
	var gotArgs []string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "onboard", "--agent", "--harness", "codex", "--home", home}); err != nil {
		t.Fatalf("onboard --agent zero manifest: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if gotPath != "/test/bin/codex" || gotDir == cwd {
		t.Fatalf("exec path=%q dir=%q, want codex outside cwd %q", gotPath, gotDir, cwd)
	}
	if len(gotArgs) != 1 || !strings.Contains(gotArgs[0], "Branch: AUTHOR") ||
		!strings.Contains(gotArgs[0], "Agent-Operated Onboarding") {
		t.Fatalf("initial prompt args = %#v", gotArgs)
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Manifests) != 0 {
		t.Fatalf("onboard --agent must not register manifests itself: %#v", reg.Manifests)
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "my-cli")); err != nil {
		t.Fatalf("self-skill was not installed for zero-manifest agent launch: %v", err)
	}
}

func TestOnboardAgentRegisteredUnsyncedManifestExecsBootstrapHarness(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)
	const manifestURL = "https://github.com/acme/acme-manifest.git"
	if _, err := manifest.Add(home, "acme", manifestURL); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	var gotPath, gotDir string
	var gotArgs []string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{
		"my", "onboarding", "--agent", "--harness", "codex",
		"--manifest", "acme", "--home", home,
	}); err != nil {
		t.Fatalf("onboarding unsynced manifest: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if gotPath != "/test/bin/codex" || gotDir == cwd {
		t.Fatalf("exec path=%q dir=%q, want codex outside cwd %q", gotPath, gotDir, cwd)
	}
	if len(gotArgs) != 1 ||
		!strings.Contains(gotArgs[0], "Branch: JOIN_BOOTSTRAP") ||
		!strings.Contains(gotArgs[0], "acme") ||
		!strings.Contains(gotArgs[0], manifestURL) ||
		!strings.Contains(gotArgs[0], "my manifests sync acme") {
		t.Fatalf("initial prompt args = %#v", gotArgs)
	}
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "my-cli")); err != nil {
		t.Fatalf("self-skill was not installed for unsynced-manifest agent launch: %v", err)
	}
}

func TestOnboardAgentRegisteredInvalidManifestExecsRepairHarness(t *testing.T) {
	home := t.TempDir()
	ref, err := manifest.Add(home, "acme", "https://github.com/acme/acme-manifest.git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ref.LocalPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ref.LocalPath, "manifest.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	a := app{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		lookPath: func(string) (string, error) { return "/test/bin/codex", nil },
		execHarness: func(_ string, args []string, _ string) error {
			gotArgs = append([]string(nil), args...)
			return nil
		},
	}
	if err := a.run([]string{"my", "onboarding", "--agent", "--harness", "codex", "--manifest", "acme", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) != 1 || !strings.Contains(gotArgs[0], "Branch: JOIN_REPAIR") || !strings.Contains(gotArgs[0], "invalid JSON") || !strings.Contains(gotArgs[0], ref.LocalPath) {
		t.Fatalf("repair prompt args = %#v", gotArgs)
	}
}

func TestOnboardCompletionStatusRequiresExplicitCompletion(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := configuredUmbrella(t, home)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	statusArgs := []string{"my", "onboarding", "--status", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}
	if err := a.run(statusArgs); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("status before completion error = %v", err)
	}
	if err := a.run([]string{"my", "onboarding", "--complete", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	stdout.Reset()
	if err := a.run(statusArgs); err != nil {
		t.Fatalf("status after completion: %v", err)
	}
	if !strings.Contains(stdout.String(), "complete\tacme\t") {
		t.Fatalf("status stdout = %q", stdout.String())
	}
}

func TestOnboardCompletionRequiresRequiredToolsButNotOptionalTools(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := configuredUmbrella(t, home)
	ref, found, err := manifest.Find(home, "acme")
	if err != nil || !found {
		t.Fatalf("find manifest: found=%v err=%v", found, err)
	}
	doc, _, err := manifest.LoadDocument(ref.LocalPath)
	if err != nil {
		t.Fatal(err)
	}
	doc.Tools = []manifest.Tool{
		{ID: "acme-required", Mode: "required", Purpose: "required fixture"},
		{ID: "acme-optional", Mode: "optional", Purpose: "optional fixture"},
	}
	if err := manifest.SaveDocument(ref.LocalPath, doc); err != nil {
		t.Fatal(err)
	}
	args := []string{"my", "onboarding", "--complete", "--manifest", "acme", "--home", home, "--umbrella", umbrellaRoot}
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, lookPath: func(string) (string, error) { return "", exec.ErrNotFound }}
	if err := a.run(args); err == nil || !strings.Contains(err.Error(), `required tool "acme-required" is missing`) {
		t.Fatalf("missing required tool error = %v", err)
	}
	a.lookPath = func(name string) (string, error) {
		if name == "acme-required" {
			return "/test/bin/acme-required", nil
		}
		return "", exec.ErrNotFound
	}
	if err := a.run(args); err != nil {
		t.Fatalf("optional missing tool blocked completion: %v", err)
	}
}

func TestOnboardingInteractiveDefaultExecsHarness(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)

	var stdout, stderr bytes.Buffer
	var gotPath, gotDir string
	var gotArgs []string
	a := app{
		stdout:      &stdout,
		stderr:      &stderr,
		interactive: true,
		lookPath: func(name string) (string, error) {
			if name == "codex" {
				return "/test/bin/codex", nil
			}
			return "", exec.ErrNotFound
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "onboarding", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("interactive onboarding: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if gotPath != "/test/bin/codex" || gotDir == umbrellaRoot {
		t.Fatalf("exec path=%q dir=%q, want codex outside umbrella %q", gotPath, gotDir, umbrellaRoot)
	}
	if len(gotArgs) != 1 ||
		!strings.Contains(gotArgs[0], "learn-by-example walkthrough") ||
		!strings.Contains(gotArgs[0], "Run routine local checks and setup commands yourself") ||
		strings.Contains(gotArgs[0], "The human runs the commands") ||
		!strings.Contains(gotArgs[0], "basic human workflows") ||
		strings.Contains(gotArgs[0], "reply \"OK\"") ||
		strings.Contains(gotArgs[0], "Do not run any command until") {
		t.Fatalf("initial prompt args = %#v", gotArgs)
	}
	if !strings.Contains(stdout.String(), "Onboarding with codex (installed).") {
		t.Fatalf("stdout missing harness cue:\n%s", stdout.String())
	}
}

func TestBundledOnboardingSkillUsesLearnByExampleBasics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(cliRepositoryRoot(t), "skills", "my-cli", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "## Agent-Operated Onboarding")
	if start < 0 {
		t.Fatal("Agent-Operated Onboarding section missing")
	}
	end := strings.Index(text[start+1:], "\n## ")
	section := text[start:]
	if end >= 0 {
		section = text[start : start+1+end]
	}
	for _, want := range []string{
		"learn-by-example walkthrough",
		"Run routine local",
		"manual walkthrough",
		"authentication",
		"my ai --new-session <harness>",
		"my ai -r <session-id> <harness>",
		"my session finish <session-id> --land",
		`cd "$(my root)"`,
		"my sync --push --print",
		"file an issue",
		"personal scratch note",
		"paste it into the harness conversation",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("onboarding skill section missing %q:\n%s", want, section)
		}
	}
	for _, unwanted := range []string{
		"reply `OK`",
		"reply \"OK\"",
		"Do not run any command until",
		"my meetings add",
		"my support add",
		"my fleet",
		"my admin services add",
		"my admin roles add",
	} {
		if strings.Contains(section, unwanted) {
			t.Fatalf("onboarding skill section contains %q:\n%s", unwanted, section)
		}
	}
}

func TestOnboardingNoAgentUsesDeterministicWalkthrough(t *testing.T) {
	home := t.TempDir()
	umbrellaRoot := writeRoleSetupManifest(t, home)

	var stdout, stderr bytes.Buffer
	a := app{
		stdout:      &stdout,
		stderr:      &stderr,
		stdin:       bufio.NewReader(strings.NewReader("n\n")),
		interactive: true,
		lookPath: func(name string) (string, error) {
			t.Fatalf("--no-agent should not inspect harness %q", name)
			return "", exec.ErrNotFound
		},
		execHarness: func(path string, args []string, dir string) error {
			t.Fatalf("--no-agent should not exec harness %q", path)
			return nil
		},
	}
	if err := a.run([]string{"my", "onboarding", "--no-agent", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboarding --no-agent: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := umbrella.LoadState(umbrellaRoot); !os.IsNotExist(err) {
		t.Fatalf("--no-agent decline should not create state: %v", err)
	}
	if !strings.Contains(stdout.String(), "How it fits together:") ||
		!strings.Contains(stdout.String(), "Next step:") {
		t.Fatalf("stdout missing deterministic walkthrough:\n%s", stdout.String())
	}
}

func TestOnboardAgentPromptsForHarnessWhenOmitted(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	var gotPath string
	// All harnesses installed and none logged in => auto-detect is ambiguous, so
	// onboard falls back to the interactive harness prompt; "2" selects codex.
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		stdin:  bufio.NewReader(strings.NewReader("2\n")),
		lookPath: func(name string) (string, error) {
			return "/test/bin/" + name, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			return nil
		},
	}
	if err := a.run([]string{"my", "onboard", "--agent", "--home", home}); err != nil {
		t.Fatalf("onboard --agent prompt harness: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if gotPath != "/test/bin/codex" {
		t.Fatalf("exec path = %q, want prompted codex", gotPath)
	}
	if !strings.Contains(stdout.String(), "Select a harness:") ||
		!strings.Contains(stdout.String(), "Harness (--harness to skip this prompt):") {
		t.Fatalf("stdout missing harness prompt:\n%s", stdout.String())
	}
}

func TestOnboardAgentRequiresInstalledHarness(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(string) (string, error) {
			return "", exec.ErrNotFound
		},
	}
	err := a.run([]string{"my", "onboarding", "--agent", "--home", home})
	if err == nil || !strings.Contains(err.Error(), "install Codex, Claude Code") ||
		!strings.Contains(err.Error(), "rerun my onboarding") {
		t.Fatalf("error = %v", err)
	}
}

func TestAutoDetectHarnessPrefersSingleLoggedIn(t *testing.T) {
	home := t.TempDir()
	writeCLITestFile(t, filepath.Join(home, ".codex", "auth.json"), "{}\n")
	a := app{lookPath: func(name string) (string, error) {
		return "/test/bin/" + name, nil
	}}
	got, reason, ok := a.autoDetectHarness(home)
	if !ok || got != harness.Codex || reason != "installed, logged in" {
		t.Fatalf("autoDetectHarness = %q, %q, %v; want codex logged in", got, reason, ok)
	}
}

func TestAutoDetectHarnessUsesSingleInstalled(t *testing.T) {
	home := t.TempDir()
	a := app{lookPath: func(name string) (string, error) {
		if name == "codex" {
			return "/test/bin/codex", nil
		}
		return "", exec.ErrNotFound
	}}
	got, reason, ok := a.autoDetectHarness(home)
	if !ok || got != harness.Codex || reason != "installed" {
		t.Fatalf("autoDetectHarness = %q, %q, %v; want single installed codex", got, reason, ok)
	}
}

func TestAutoDetectHarnessSupportsGrokAndCursor(t *testing.T) {
	for _, tc := range []struct {
		harness harness.Harness
		command string
	}{
		{harness: harness.Grok, command: "grok"},
		{harness: harness.Cursor, command: "cursor-agent"},
	} {
		t.Run(string(tc.harness), func(t *testing.T) {
			home := t.TempDir()
			a := app{lookPath: func(name string) (string, error) {
				if name == tc.command {
					return "/test/bin/" + name, nil
				}
				return "", exec.ErrNotFound
			}}
			got, reason, ok := a.autoDetectHarness(home)
			if !ok || got != tc.harness || reason != "installed" {
				t.Fatalf("autoDetectHarness = %q, %q, %v; want %s installed", got, reason, ok, tc.harness)
			}
		})
	}
}

func TestAutoDetectHarnessReturnsFalseWhenAmbiguous(t *testing.T) {
	home := t.TempDir()
	a := app{lookPath: func(name string) (string, error) {
		return "/test/bin/" + name, nil
	}}
	got, reason, ok := a.autoDetectHarness(home)
	if ok || got != "" || reason != "" {
		t.Fatalf("autoDetectHarness = %q, %q, %v; want ambiguous false", got, reason, ok)
	}
}

func TestOnboardAgentManifestLaunchesBeforeSetupWithPrompt(t *testing.T) {
	home, umbrellaRoot := setupCLILaunchFixture(t)

	var stdout, stderr bytes.Buffer
	var gotPath, gotDir string
	var gotArgs []string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex", name)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			gotArgs = append([]string(nil), args...)
			gotDir = dir
			return nil
		},
	}
	if err := a.run([]string{"my", "onboard", "--agent", "--harness", "codex", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard --agent manifest: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if gotPath != "/test/bin/codex" || gotDir == umbrellaRoot {
		t.Fatalf("exec path=%q dir=%q, want codex outside umbrella %q", gotPath, gotDir, umbrellaRoot)
	}
	if len(gotArgs) != 1 || !strings.Contains(gotArgs[0], "Branch: JOIN") ||
		!strings.Contains(gotArgs[0], "acme") || !strings.Contains(gotArgs[0], umbrellaRoot) {
		t.Fatalf("initial prompt args = %#v", gotArgs)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("onboarding must not run setup before the agent: %v", err)
	}
}

// configuredUmbrella runs a plain (non-interactive) setup to create a
// configured umbrella with a selected role but no tour marker.
func configuredUmbrella(t *testing.T, home string) string {
	t.Helper()
	umbrellaRoot := writeRoleSetupManifest(t, home)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "setup", "--manifest", "acme", "--home", home, "--role", "operator", "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("pre-configure setup: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatalf("configured umbrella missing state: %v", err)
	}
	if state.Tour != nil {
		t.Fatalf("plain setup should not mark a tour: %+v", state.Tour)
	}
	return umbrellaRoot
}

func assertTourUnmarkedRolePreserved(t *testing.T, umbrellaRoot string) {
	t.Helper()
	state, err := umbrella.LoadState(umbrellaRoot)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if state.Tour != nil {
		t.Fatalf("tour must not be marked on decline/EOF: %+v", state.Tour)
	}
	if state.SelectedRole != "operator" {
		t.Fatalf("selected role must be preserved = %q, want operator", state.SelectedRole)
	}
}

func TestOnboardingGovernedManifestStartsBeforePrerequisites(t *testing.T) {
	f := newPolicyTestFixture(t)
	if _, _, err := umbrella.Ensure(f.umbrellaRoot, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	customRoot := filepath.Join(f.home, "custom-workspace")
	if err := os.Rename(f.umbrellaRoot, customRoot); err != nil {
		t.Fatal(err)
	}
	f.umbrellaRoot = customRoot
	// A retry from inside a configured umbrella must not load its instructions
	// or try provider/policy checks before the assistant can help with setup.
	t.Chdir(f.umbrellaRoot)
	t.Setenv("PATH", t.TempDir())
	var launched bool
	a := app{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		lookPath: func(name string) (string, error) {
			if name == "codex" {
				return "/fixture/codex", nil
			}
			return "", exec.ErrNotFound
		},
		accessRunner: func(name string, args ...string) ([]byte, error) {
			t.Fatalf("onboarding called provider before assistant: %s %v", name, args)
			return nil, nil
		},
		execHarness: func(path string, args []string, dir string) error {
			launched = true
			t.Cleanup(func() { os.RemoveAll(dir) })
			if dir == f.umbrellaRoot || !strings.HasPrefix(filepath.Base(dir), "my-onboarding-") {
				t.Fatalf("not a neutral launch directory: %s", dir)
			}
			for _, name := range []string{"AGENTS.md", ".mcp.json", ".agents"} {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatalf("bootstrap materialized organization file %s: %v", name, err)
				}
			}
			for _, want := range []string{"--home " + shellQuote(f.home), "--umbrella " + shellQuote(f.umbrellaRoot), "--manifest acme", "normal my ai launch"} {
				if !strings.Contains(args[0], want) {
					t.Fatalf("prompt missing %q", want)
				}
			}
			return nil
		},
	}
	if err := a.run([]string{"my", "onboarding", "--agent", "--harness", "codex", "--home", f.home}); err != nil {
		t.Fatal(err)
	}
	if !launched {
		t.Fatal("assistant was not launched")
	}
}

func TestOnboardingPreservesRelativeUmbrellaBeforeNeutralLaunch(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)
	if _, err := manifest.Add(home, "acme", "https://github.com/acme/manifest.git"); err != nil {
		t.Fatal(err)
	}
	a := app{
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{},
		lookPath: func(string) (string, error) { return "/fixture/codex", nil },
		execHarness: func(_ string, args []string, dir string) error {
			t.Cleanup(func() { os.RemoveAll(dir) })
			for _, flag := range []string{"--no-refresh", "--no-update-check"} {
				if !strings.Contains(args[0], flag) {
					t.Fatalf("prompt dropped %s", flag)
				}
			}
			want := "--umbrella " + shellQuote(filepath.Join(cwd, "custom workspace"))
			if !strings.Contains(args[0], want) {
				t.Fatalf("prompt missing %q: %v", want, args)
			}
			return nil
		},
	}
	if err := a.run([]string{"my", "onboarding", "--agent", "--harness", "codex", "--home", home, "--manifest", "acme", "--umbrella", "./custom workspace", "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatal(err)
	}
}
