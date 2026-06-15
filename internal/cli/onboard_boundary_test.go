package cli

import (
	"bufio"
	"bytes"
	"os"
	"strings"
	"testing"

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
