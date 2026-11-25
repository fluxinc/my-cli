package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/attempt"
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
	from := f.noticed(t).Noticed
	head := f.amend(t)

	got := f.launch(t, nil)
	if got.err != nil {
		t.Fatalf("launch: %v\n%s", got.err, got.stderr)
	}
	if len(got.args) != 1 {
		t.Fatalf("codex args = %q, want one positional prompt", got.args)
	}
	prompt := got.args[0]
	for _, want := range []string{"Before anything else, run `", " changes --manifest acme --home ", " --since " + from + " --to " + head + " --all`", "including every link it prints"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	// The organization's rules stay out of the harness command line, which
	// other users of a shared machine can read.
	if strings.Contains(prompt, "publishing") || strings.Contains(prompt, "magick") {
		t.Fatalf("prompt carries rule text:\n%s", prompt)
	}
	for _, want := range []string{
		`Amended: "Ask before publishing an image or its source files." (was "Ask before publishing an image.")`,
		"Added magick: Convert images for print",
		"Details: https://github.com/acme/acme-manifest/pull/12 https://github.com/acme/acme-manifest/compare/" + from + "..." + head,
		"codex will go over these changes with you first",
	} {
		if !strings.Contains(got.stderr, want) {
			t.Fatalf("terminal missing %q:\n%s", want, got.stderr)
		}
	}
	if state := f.noticed(t); state.Noticed != head || state.Baseline || state.Reserved != nil {
		t.Fatalf("not recorded as seen: %#v", state)
	}
	// The command in the prompt reads the range whatever the state says.
	if report := runChangesJSON(t, f, "--since", from, "--to", head, "--all"); len(report.Changes) != 2 || report.MarkedSeen {
		t.Fatalf("the agent's command = %#v", report)
	}

	again := f.launch(t, nil)
	if len(again.args) != 0 || strings.Contains(again.stderr, "Changes to the") {
		t.Fatalf("seen changes repeated: args=%q stderr=%s", again.args, again.stderr)
	}
}

func TestChangeNoticeOverlappingLaunchPresentsItOnce(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	from := f.noticed(t).Noticed
	head := f.amend(t)
	reserve := func(pid int, start string) {
		t.Helper()
		if _, err := changenotice.Update(f.home, "acme", func(s *changenotice.State, _ bool) (bool, error) {
			s.Reserved = &changenotice.Reservation{Token: "other", From: from, To: head, PID: pid, ProcessStart: start, At: time.Now().UTC().Format(time.RFC3339)}
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Another live launch (this test's parent process) is presenting it.
	parent := os.Getppid()
	start, found, err := attempt.ProcessStart(parent)
	if err != nil || !found {
		t.Skipf("cannot read the parent process: %v", err)
	}
	reserve(parent, start)
	quiet := f.launch(t, nil)
	if len(quiet.args) != 0 || !strings.Contains(quiet.stderr, "another launch is presenting the latest manifest changes") {
		t.Fatalf("overlapping launch: args=%q stderr=%s", quiet.args, quiet.stderr)
	}
	if state := f.noticed(t); state.Noticed != from || state.Reserved == nil || state.Reserved.Token != "other" {
		t.Fatalf("the quiet launch changed the state: %#v", state)
	}
	// That launch died before starting its harness: the next launch presents.
	reserve(parent, start+" (an earlier process)")
	shown := f.launch(t, nil)
	if len(shown.args) != 1 {
		t.Fatalf("a dead reservation still held the notice back: %s", shown.stderr)
	}
	if state := f.noticed(t); state.Noticed != head || state.Reserved != nil {
		t.Fatalf("state = %#v", state)
	}
}

func TestChangeNoticeConcurrentMyChangesWinsAndTheLaunchDoesNotUndoIt(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	head := f.amend(t)
	var during changesReport
	got := f.launch(t, func([]string) error {
		state := f.noticed(t)
		if state.Reserved == nil {
			t.Fatal("the launch holds no reservation while its harness runs")
		}
		during = runChangesJSON(t, f)
		return nil
	})
	if got.err != nil {
		t.Fatalf("launch: %v\n%s", got.err, got.stderr)
	}
	if !during.MarkedSeen {
		t.Fatalf("my changes during the launch = %#v", during)
	}
	if state := f.noticed(t); state.Noticed != head || state.Reserved != nil {
		t.Fatalf("state after both = %#v", state)
	}
}

func TestChangeNoticeUnreadableRangeIsShownButRetried(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	head := f.amend(t)
	gone := strings.Repeat("d", 40)
	if err := changenotice.Record(f.home, "acme", gone, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := f.launch(t, nil)
	if got.err != nil || len(got.args) != 1 || !strings.Contains(got.stderr, "cannot be read from the local manifest checkout") {
		t.Fatalf("launch err=%v args=%q stderr=%s", got.err, got.args, got.stderr)
	}
	// The agent gets a command that works: the read-only pending listing,
	// which carries the recovery note and the link to the current revision.
	if !strings.Contains(got.args[0], " --peek --all`") || strings.Contains(got.args[0], "--since") {
		t.Fatalf("recovery prompt = %s", got.args[0])
	}
	if report := runChangesJSON(t, f, "--peek", "--all"); !report.Incomplete || !strings.Contains(report.Note, "my changes --reset") || len(report.Links) != 1 || !strings.HasSuffix(report.Links[0], "/commit/"+head) {
		t.Fatalf("the agent's recovery command = %#v", report)
	}
	if state := f.noticed(t); state.Noticed != gone || state.Reserved != nil {
		t.Fatalf("an incomplete notice was recorded: %#v", state)
	}
	if report := runChangesJSON(t, f); !report.Incomplete || report.MarkedSeen || report.Note == "" {
		t.Fatalf("my changes on an unreadable range = %#v", report)
	}
	if report := runChangesJSON(t, f, "--reset"); !report.MarkedSeen || !report.Baseline {
		t.Fatalf("--reset = %#v", report)
	}
	if state := f.noticed(t); state.Noticed != head || !state.Baseline {
		t.Fatalf("after --reset: %#v", state)
	}
}

func TestMyChangesRecordsOnlyWhatItManagedToPrint(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	before := f.noticed(t).Noticed
	f.amend(t)
	for _, format := range [][]string{nil, {"--json"}} {
		a := app{stdout: failingWriter{}, stderr: &bytes.Buffer{}}
		args := append([]string{"my", "changes", "--home", f.home, "--manifest", "acme"}, format...)
		if err := a.run(args); err == nil {
			t.Fatalf("%v: a failed write was not reported", format)
		}
		if state := f.noticed(t); state.Noticed != before {
			t.Fatalf("%v: unprinted changes recorded as seen", format)
		}
	}
	fresh := setupChangesFixture(t)
	var stdout bytes.Buffer
	a := app{stdout: &stdout, stderr: &bytes.Buffer{}}
	if err := a.run([]string{"my", "changes", "--home", fresh.home, "--manifest", "acme", "--peek"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "--peek records nothing") {
		t.Fatalf("first --peek output: %s", stdout.String())
	}
	if _, found, _ := changenotice.Load(fresh.home, "acme"); found {
		t.Fatal("--peek recorded a starting point")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestChangesPromptQuotesItsArgumentsForTheShell(t *testing.T) {
	home := "/tmp/it's a home"
	notice := changenotice.Notice{From: "1111111111111111111111111111111111111111", To: "2222222222222222222222222222222222222222"}
	prompt := changesPrompt("acme's", home, notice, "Finish the poster layout.")
	command := prompt[strings.Index(prompt, "`")+1:]
	command = command[:strings.Index(command, "`")]
	fields := strings.SplitN(command, " changes ", 2)
	out, err := exec.Command("sh", "-c", "for a in changes "+fields[1]+`; do printf '%s\n' "$a"; done`).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := "changes\n--manifest\nacme's\n--home\n" + home + "\n--since\n" + notice.From + "\n--to\n" + notice.To + "\n--all\n"
	if string(out) != want {
		t.Fatalf("shell reads the command as\n%s\nwant\n%s", out, want)
	}
	if !strings.HasSuffix(prompt, "Then carry on.\n\nFinish the poster layout.") {
		t.Fatalf("an existing task is not carried on:\n%s", prompt)
	}
}

func TestChangeNoticeStaysPendingWhenTheLaunchPassesItsOwnArguments(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	before := f.noticed(t).Noticed
	head := f.amend(t)

	got := f.launch(t, func([]string) error {
		// Nobody presents these changes, so nothing may be held back while
		// this resumed harness runs.
		if state := f.noticed(t); state.Reserved != nil {
			t.Fatalf("a pass-through launch holds a reservation while it runs: %#v", state.Reserved)
		}
		return nil
	}, "--", "resume", "--last")
	if got.err != nil {
		t.Fatalf("launch: %v\n%s", got.err, got.stderr)
	}
	// The caller's arguments reach the harness as given; nothing is added.
	if joined := strings.Join(got.args, " "); !strings.HasSuffix(joined, "resume --last") || strings.Contains(joined, "Before anything else") {
		t.Fatalf("caller arguments changed: %q", got.args)
	}
	if !strings.Contains(got.stderr, "Details: https://github.com/acme/acme-manifest/pull/12") || !strings.Contains(got.stderr, "stay pending until you or the agent run my changes") {
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
	if !report.MarkedSeen || len(report.Changes) != 2 || len(report.Links) != 2 {
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

func TestChangeNoticeNeverTurnsAMalformedRecordIntoACommand(t *testing.T) {
	f := setupChangesFixture(t)
	f.launch(t, nil)
	f.amend(t)
	marker := filepath.Join(t.TempDir(), "ran")
	bad := "x; touch " + marker + " $(touch " + marker + ")"
	if err := changenotice.Record(f.home, "acme", bad, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := f.launch(t, nil)
	if got.err != nil || len(got.args) != 1 {
		t.Fatalf("launch err=%v args=%q", got.err, got.args)
	}
	if strings.Contains(got.args[0], "touch") || strings.Contains(got.stderr, "touch") || !strings.Contains(got.args[0], " --peek --all`") {
		t.Fatalf("malformed record reached the prompt or terminal:\nprompt: %s\nstderr: %s", got.args[0], got.stderr)
	}
	report := runChangesJSON(t, f, "--peek")
	if !report.Incomplete || strings.Contains(report.Note, "touch") || report.From != "" {
		t.Fatalf("my changes echoed the malformed record: %#v", report)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the malformed record ran a command")
	}
	if state := f.noticed(t); state.Noticed != bad {
		t.Fatalf("a notice that could not be read was recorded: %#v", state)
	}
}

func TestChangeNoticeFirstBaselineWaitsForAStartedHarness(t *testing.T) {
	f := setupChangesFixture(t)
	got := f.launch(t, func([]string) error { return errors.New("exec format error") })
	if got.err == nil {
		t.Fatal("start failure lost")
	}
	if _, found, _ := changenotice.Load(f.home, "acme"); found {
		t.Fatal("a launch that never started recorded the starting point")
	}
	head := strings.TrimSpace(gitCLIOutput(t, f.cache, "rev-parse", "HEAD"))
	f.launch(t, nil)
	if state := f.noticed(t); state.Noticed != head || !state.Baseline {
		t.Fatalf("baseline after a started launch = %#v", state)
	}
}
