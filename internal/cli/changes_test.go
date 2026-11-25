package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/changenotice"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

const changesManifestBody = `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"umbrella":{"recommended_path":"~/acme"},"contract":["Ask before publishing an image."]}`

const changesManifestAmended = `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"umbrella":{"recommended_path":"~/acme"},"contract":["Ask before publishing an image or its source files."],"tools":[{"id":"magick","mode":"optional","purpose":"Convert images for print"}]}`

type changesFixture struct {
	home, root, cache, writer string
}

func setupChangesFixture(t *testing.T) changesFixture {
	t.Helper()
	home, root, cache, _, writer := setupCLITrackedManifestBody(t, changesManifestBody)
	if _, _, err := umbrella.Ensure(root, "acme", "acme"); err != nil {
		t.Fatal(err)
	}
	ensureCLIGuidance(t, home, root)
	return changesFixture{home: home, root: root, cache: cache, writer: writer}
}

// amend publishes a contract amendment and a new tool through a merged pull
// request, then brings the person's manifest checkout up to date with a
// GitHub origin, as a real sync would leave it.
func (f changesFixture) amend(t *testing.T) string {
	t.Helper()
	writeCLITestFile(t, filepath.Join(f.writer, "manifest.json"), changesManifestAmended)
	commitAndPushCLIGit(t, f.writer, "Merge pull request #12 from acme/source-files")
	runCLIGit(t, f.cache, "pull", "-q", "--ff-only")
	runCLIGit(t, f.cache, "remote", "set-url", "origin", "https://github.com/acme/acme-manifest.git")
	return strings.TrimSpace(gitCLIOutput(t, f.cache, "rev-parse", "HEAD"))
}

type changesLaunch struct {
	args   []string
	stderr string
	err    error
}

func (f changesFixture) launch(t *testing.T, run func([]string) error, extra ...string) changesLaunch {
	t.Helper()
	var stdout, stderr bytes.Buffer
	var got changesLaunch
	a := app{
		stdout:   &stdout,
		stderr:   &stderr,
		lookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
		execHarnessEnvironment: func(_ string, args []string, _ string, _ []string) error {
			got.args = append([]string(nil), args...)
			if run != nil {
				return run(args)
			}
			return nil
		},
	}
	args := []string{"my", "ai", "--home", f.home, "--umbrella", f.root, "--no-refresh", "--no-update-check", "--no-session", "codex"}
	got.err = a.run(append(args, extra...))
	got.stderr = stderr.String()
	return got
}

func (f changesFixture) noticed(t *testing.T) changenotice.State {
	t.Helper()
	state, found, err := changenotice.Load(f.home, "acme")
	if err != nil || !found {
		t.Fatalf("notice state: found=%v err=%v", found, err)
	}
	return state
}

func TestChangeNoticeFirstLaunchRecordsABaselineAndSaysNothing(t *testing.T) {
	f := setupChangesFixture(t)
	head := strings.TrimSpace(gitCLIOutput(t, f.cache, "rev-parse", "HEAD"))
	got := f.launch(t, nil)
	if got.err != nil {
		t.Fatalf("launch: %v\n%s", got.err, got.stderr)
	}
	if len(got.args) != 0 || strings.Contains(got.stderr, "Changes to the") {
		t.Fatalf("first launch told the person about history: args=%q stderr=%s", got.args, got.stderr)
	}
	if state := f.noticed(t); state.Noticed != head || !state.Baseline {
		t.Fatalf("baseline = %#v, want %s", state, head)
	}
}

func TestChangeNoticeGoesToTheAgentOnceWithThePullRequestLink(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	head := f.amend(t)

	got := f.launch(t, nil)
	if got.err != nil {
		t.Fatalf("launch: %v\n%s", got.err, got.stderr)
	}
	if len(got.args) != 1 {
		t.Fatalf("codex args = %q, want one positional prompt", got.args)
	}
	prompt := got.args[0]
	for _, want := range []string{
		"Before anything else, tell me",
		`Amended: "Ask before publishing an image or its source files." (was "Ask before publishing an image.")`,
		"Added magick: Convert images for print",
		"Details: https://github.com/acme/acme-manifest/pull/12",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
		if want != "Before anything else, tell me" && !strings.Contains(got.stderr, want) {
			t.Fatalf("terminal missing %q:\n%s", want, got.stderr)
		}
	}
	if !strings.Contains(got.stderr, "codex will go over these changes with you first") {
		t.Fatalf("terminal does not say the agent has them:\n%s", got.stderr)
	}
	if state := f.noticed(t); state.Noticed != head || state.Baseline {
		t.Fatalf("not recorded as seen: %#v", state)
	}

	again := f.launch(t, nil)
	if len(again.args) != 0 || strings.Contains(again.stderr, "Changes to the") {
		t.Fatalf("seen changes repeated: args=%q stderr=%s", again.args, again.stderr)
	}
}

func TestChangeNoticeStaysPendingWhenTheLaunchPassesItsOwnArguments(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	before := f.noticed(t).Noticed
	head := f.amend(t)

	got := f.launch(t, nil, "--", "resume", "--last")
	if got.err != nil {
		t.Fatalf("launch: %v\n%s", got.err, got.stderr)
	}
	// The caller's arguments reach the harness as given; nothing is added.
	if joined := strings.Join(got.args, " "); !strings.HasSuffix(joined, "resume --last") || strings.Contains(joined, "Before anything else") {
		t.Fatalf("caller arguments changed: %q", got.args)
	}
	if !strings.Contains(got.stderr, "Details: https://github.com/acme/acme-manifest/pull/12") || !strings.Contains(got.stderr, "stays pending until you or the agent run my changes") {
		t.Fatalf("terminal did not show the pending notice:\n%s", got.stderr)
	}
	if state := f.noticed(t); state.Noticed != before {
		t.Fatalf("changes marked seen though the agent never got them: %#v", state)
	}

	report := runChangesJSON(t, f, "--peek")
	if report.MarkedSeen || report.From != before || report.To != head || len(report.Changes) != 2 {
		t.Fatalf("peek = %#v", report)
	}
	if state := f.noticed(t); state.Noticed != before {
		t.Fatal("--peek recorded the changes as seen")
	}
	report = runChangesJSON(t, f)
	if !report.MarkedSeen || len(report.Changes) != 2 || len(report.Links) != 1 {
		t.Fatalf("my changes = %#v", report)
	}
	if state := f.noticed(t); state.Noticed != head {
		t.Fatalf("my changes did not record them: %#v", state)
	}
	if report := runChangesJSON(t, f); report.MarkedSeen || len(report.Changes) != 0 {
		t.Fatalf("second my changes repeated them: %#v", report)
	}
	if report := runChangesJSON(t, f, "--since", before); len(report.Changes) != 2 || report.MarkedSeen {
		t.Fatalf("--since = %#v", report)
	}
}

func TestChangeNoticeIsNotSeenWhenTheHarnessNeverStarts(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	before := f.noticed(t).Noticed
	f.amend(t)

	got := f.launch(t, func([]string) error { return errors.New("exec format error") })
	if got.err == nil || len(got.args) != 1 {
		t.Fatalf("launch err=%v args=%q", got.err, got.args)
	}
	if state := f.noticed(t); state.Noticed != before {
		t.Fatalf("failed start recorded the changes as seen: %#v", state)
	}
	exited := f.launch(t, func([]string) error { return exitCodeError{code: 2} })
	if exited.err == nil {
		t.Fatal("exit status lost")
	}
	if state := f.noticed(t); state.Noticed == before {
		t.Fatal("a harness that ran and exited did not record the changes as seen")
	}
}

func TestChangeNoticeIsNotConsumedByPrint(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	before := f.noticed(t).Noticed
	f.amend(t)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "ai", "--home", f.home, "--umbrella", f.root, "--no-refresh", "--no-update-check", "--no-session", "--print", "codex"}); err != nil {
		t.Fatalf("print: %v\n%s", err, stderr.String())
	}
	if state := f.noticed(t); state.Noticed != before {
		t.Fatalf("--print recorded the changes as seen: %#v", state)
	}
}

func runChangesJSON(t *testing.T, f changesFixture, extra ...string) changesReport {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	args := append([]string{"my", "changes", "--home", f.home, "--manifest", "acme", "--json"}, extra...)
	if err := a.run(args); err != nil {
		t.Fatalf("my changes: %v\n%s", err, stderr.String())
	}
	var report changesReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	return report
}
