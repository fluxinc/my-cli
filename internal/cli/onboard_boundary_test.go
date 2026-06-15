package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/umbrella"
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
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard EOF: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if _, err := umbrella.LoadState(umbrellaRoot); !os.IsNotExist(err) {
		t.Fatalf("EOF onboard must not create state (no silent setup): %v", err)
	}
	if !strings.Contains(stdout.String(), "onboard\tunmarked") {
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
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
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
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
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
	if err := a.run([]string{"our", "onboard", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
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
	if err := a.run([]string{"our", "setup", "--interactive", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
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

func TestOnboardAgentZeroManifestExecsHarnessFromCurrentDir(t *testing.T) {
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
	if err := a.run([]string{"our", "onboard", "--agent", "--harness", "codex", "--home", home}); err != nil {
		t.Fatalf("onboard --agent zero manifest: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if gotPath != "/test/bin/codex" || gotDir != cwd {
		t.Fatalf("exec path=%q dir=%q, want codex in cwd %q", gotPath, gotDir, cwd)
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
	if _, err := os.Lstat(filepath.Join(home, ".codex", "skills", "our")); err != nil {
		t.Fatalf("self-skill was not installed for zero-manifest agent launch: %v", err)
	}
}

func TestOnboardAgentPromptsForHarnessWhenOmitted(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	var gotPath string
	a := app{
		stdout: &stdout,
		stderr: &stderr,
		stdin:  bufio.NewReader(strings.NewReader("2\n")),
		lookPath: func(name string) (string, error) {
			if name != "codex" {
				t.Fatalf("lookPath name = %q, want codex from prompt choice", name)
			}
			return "/test/bin/codex", nil
		},
		execHarness: func(path string, args []string, dir string) error {
			gotPath = path
			return nil
		},
	}
	if err := a.run([]string{"our", "onboard", "--agent", "--home", home}); err != nil {
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

func TestOnboardAgentManifestLaunchesThroughOurAIWithPrompt(t *testing.T) {
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
	if err := a.run([]string{"our", "onboard", "--agent", "--harness", "codex", "--manifest", "acme", "--home", home, "--no-refresh", "--no-update-check"}); err != nil {
		t.Fatalf("onboard --agent manifest: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if gotPath != "/test/bin/codex" || gotDir != umbrellaRoot {
		t.Fatalf("exec path=%q dir=%q, want codex in umbrella %q", gotPath, gotDir, umbrellaRoot)
	}
	if len(gotArgs) != 1 || !strings.Contains(gotArgs[0], "Branch: JOIN") ||
		!strings.Contains(gotArgs[0], "acme") || !strings.Contains(gotArgs[0], umbrellaRoot) {
		t.Fatalf("initial prompt args = %#v", gotArgs)
	}
	if _, err := os.Stat(filepath.Join(umbrellaRoot, "AGENTS.md")); err != nil {
		t.Fatalf("onboard --agent did not run setup before launch: %v", err)
	}
}

// configuredUmbrella runs a plain (non-interactive) setup to create a
// configured umbrella with a selected role but no tour marker.
func configuredUmbrella(t *testing.T, home string) string {
	t.Helper()
	umbrellaRoot := writeRoleSetupManifest(t, home)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"our", "setup", "--manifest", "acme", "--home", home, "--role", "operator", "--no-refresh", "--no-update-check"}); err != nil {
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
