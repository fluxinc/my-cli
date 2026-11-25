package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/attempt"
	"github.com/fluxinc/my-cli/internal/changenotice"
	"github.com/fluxinc/my-cli/internal/manifest"
)

// changesReport is the JSON form of `my changes`.
type changesReport struct {
	changenotice.Notice
	// Baseline means no earlier revision was recorded for this person.
	Baseline bool `json:"baseline,omitempty"`
	// MarkedSeen means this command recorded To as told to this person.
	MarkedSeen bool `json:"marked_seen"`
}

func (a app) runChanges(args []string) error {
	var home, name, root, since, to string
	var jsonOut, peek, all, reset bool
	fs := newFlagSet("my changes", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&name, "manifest", "", "select a registered manifest")
	fs.StringVar(&root, "umbrella", "", "select an umbrella root")
	fs.StringVar(&since, "since", "", "list changes since this manifest commit; records nothing")
	fs.StringVar(&to, "to", "", "with --since, list changes up to this commit instead of the current one")
	fs.BoolVar(&peek, "peek", false, "list unseen changes without recording them as seen")
	fs.BoolVar(&all, "all", false, "list every change in full, without the launch summary's limit")
	fs.BoolVar(&reset, "reset", false, "record the current revision as your starting point without listing anything")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, `Usage: my changes [--since REV [--to REV]] [--peek] [--all] [--reset] [--json] [--manifest NAME] [--home DIR] [--umbrella DIR]

Lists what changed in the organization manifest since you were last told:
contract rules added, amended or retracted; guidance files added, removed or
updated; skills, tools, services, mounts, policies and roles added, removed or
updated; other manifest settings that changed; and links to the pull requests
and the whole range. Listing them records them as seen, so your next session
start does not repeat them. A listing that could not be read in full is never
recorded.

  --peek    list without recording
  --since   list any range (to the current revision, or --to); records nothing
  --all     list everything in full
  --reset   make the current revision your starting point, listing nothing`)
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "manifest": true, "umbrella": true, "since": true, "to": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 || to != "" && since == "" || reset && (since != "" || peek) {
		return fmt.Errorf("usage: my changes [--since REV [--to REV]] [--peek] [--all] [--reset] [--json]")
	}
	if name == "" {
		name = os.Getenv(contractManifestEnv)
	}
	name, err = defaultManifestName(home, name, root)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	ref, ok, err := manifest.Find(home, name)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !ok {
		return a.maybeJSONError(jsonOut, fmt.Errorf("manifest %q not registered", name))
	}
	stateHome, err := resolveHome(home)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	head, err := manifestHead(ref.LocalPath)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	report := changesReport{Notice: changenotice.Notice{Manifest: ref.Name, To: head, Changes: []changenotice.Change{}}}
	if reset {
		if err := changenotice.Record(stateHome, ref.Name, head, true, time.Now()); err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
		report.Baseline, report.MarkedSeen = true, true
		return a.printChanges(report, jsonOut, all)
	}
	if since != "" {
		from, err := resolveManifestCommit(ref.LocalPath, since)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
		if to != "" {
			if report.To, err = resolveManifestCommit(ref.LocalPath, to); err != nil {
				return a.maybeJSONError(jsonOut, err)
			}
		}
		if from != report.To {
			report.Notice = manifestChangeNotice(ref, from, report.To)
		}
		return a.printChanges(report, jsonOut, all)
	}
	state, found, err := changenotice.Load(stateHome, ref.Name)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !found {
		report.Baseline = true
		if !peek {
			// Print before recording: a listing that never reached the
			// person must not count as told.
			report.MarkedSeen = true
		}
		if err := a.printChanges(report, jsonOut, all); err != nil {
			return err
		}
		if !peek {
			_, err := changenotice.Update(stateHome, ref.Name, func(s *changenotice.State, found bool) (bool, error) {
				if found {
					return false, nil
				}
				s.Noticed, s.NoticedAt, s.Baseline = head, time.Now().UTC().Format(time.RFC3339), true
				return true, nil
			})
			return err
		}
		return nil
	}
	from := state.Noticed
	if from != head {
		report.Notice = manifestChangeNotice(ref, from, head)
	}
	record := !peek && from != head && !report.Incomplete
	report.MarkedSeen = record
	if err := a.printChanges(report, jsonOut, all); err != nil {
		return err
	}
	if record {
		if _, err := changenotice.Advance(stateHome, ref.Name, from, head, "", time.Now()); err != nil {
			return err
		}
	}
	return nil
}

func (a app) printChanges(report changesReport, jsonOut, all bool) error {
	if report.Changes == nil {
		report.Changes = []changenotice.Change{}
	}
	if jsonOut {
		return printJSON(a.stdout, report)
	}
	var err error
	switch {
	case report.Baseline && report.MarkedSeen:
		_, err = fmt.Fprintf(a.stdout, "%s is now your starting point for %s; later changes will be listed here and at your next session start.\n", changenotice.Short(report.To), report.Manifest)
	case report.Baseline:
		_, err = fmt.Fprintf(a.stdout, "No earlier revision of %s is recorded for you, and --peek records nothing. Run my changes to make %s your starting point.\n", report.Manifest, changenotice.Short(report.To))
	case report.Empty():
		_, err = fmt.Fprintf(a.stdout, "No changes to %s since your last session (at %s).\n", report.Manifest, changenotice.Short(report.To))
	case all:
		_, err = io.WriteString(a.stdout, changenotice.RenderAll(report.Notice))
	default:
		_, err = io.WriteString(a.stdout, changenotice.Render(report.Notice))
	}
	return err
}

// noticeClaim is one launch's reservation of a range to present.
type noticeClaim struct {
	home, manifest, from, to, token string
	stderr                          io.Writer
}

// seen records the range as told, if nobody moved the record meanwhile, and
// drops this launch's reservation.
func (c noticeClaim) seen() {
	if _, err := changenotice.Advance(c.home, c.manifest, c.from, c.to, c.token, time.Now()); err != nil {
		fmt.Fprintf(c.stderr, "notice\tchanges\tcould not record these changes as seen: %v\n", err)
	}
}

// release drops this launch's reservation and nothing else. After seen it
// finds nothing to do.
func (c noticeClaim) release() {
	_ = changenotice.Release(c.home, c.manifest, c.token)
}

// launchChangeNotice returns the changes this person has not been told
// about and a claim on presenting them, or nil. For a person with no record
// yet it returns instead a baseline function to run once the harness has
// started, which records the current revision as the starting point. A
// launch that overlaps another one presenting the same range stays quiet.
// Failures are printed and never stop a launch.
func (a app) launchChangeNotice(home string, ref manifest.Ref) (*changenotice.Notice, *noticeClaim, func()) {
	stateHome, err := resolveHome(home)
	if err != nil {
		return nil, nil, nil
	}
	head, err := manifestHead(ref.LocalPath)
	if err != nil {
		return nil, nil, nil
	}
	token, err := changenotice.NewToken()
	if err != nil {
		return nil, nil, nil
	}
	now := time.Now()
	pid := os.Getpid()
	processStart, _, _ := attempt.ProcessStart(pid)
	var claim *noticeClaim
	busy, first := false, false
	_, err = changenotice.Update(stateHome, ref.Name, func(s *changenotice.State, found bool) (bool, error) {
		if !found {
			first = true
			return false, nil
		}
		if s.Noticed == head {
			return false, nil
		}
		if r := s.Reserved; r != nil && r.To == head && r.From == s.Noticed && r.PID != pid && r.Live(now, attempt.ProcessStart) {
			busy = true
			return false, nil
		}
		s.Reserved = &changenotice.Reservation{Token: token, From: s.Noticed, To: head, PID: pid, ProcessStart: processStart, At: now.UTC().Format(time.RFC3339)}
		claim = &noticeClaim{home: stateHome, manifest: ref.Name, from: s.Noticed, to: head, token: token, stderr: a.stderr}
		return true, nil
	})
	if err != nil {
		fmt.Fprintf(a.stderr, "notice\tchanges\tcould not check which manifest changes you were shown: %v\n", err)
		return nil, nil, nil
	}
	if first {
		return nil, nil, func() {
			_, err := changenotice.Update(stateHome, ref.Name, func(s *changenotice.State, found bool) (bool, error) {
				if found {
					return false, nil
				}
				s.Noticed, s.NoticedAt, s.Baseline = head, time.Now().UTC().Format(time.RFC3339), true
				return true, nil
			})
			if err != nil {
				fmt.Fprintf(a.stderr, "notice\tchanges\tcould not record the manifest revision: %v\n", err)
			}
		}
	}
	if busy {
		fmt.Fprintln(a.stderr, "notice\tchanges\tanother launch is presenting the latest manifest changes; run my changes to see them here")
		return nil, nil, nil
	}
	if claim == nil {
		return nil, nil, nil
	}
	notice := manifestChangeNotice(ref, claim.from, claim.to)
	if notice.Empty() && !notice.Incomplete {
		// Only files nobody is told about changed (for example a README).
		claim.seen()
		return nil, nil, nil
	}
	return &notice, claim, nil
}

// changesPrompt asks the agent to read the range itself, so the
// organization's rule text never appears in the harness's command line. The
// revisions come from a state file, so they are used only when both are full
// Git object ids; otherwise, and for a range that could not be read, the
// agent gets the read-only listing of pending changes, which explains what
// went wrong and how to recover.
func changesPrompt(manifestName, home string, notice changenotice.Notice, initialPrompt string) string {
	command := []string{myCommandForAgent(), "changes", "--manifest", shellQuote(manifestName)}
	if home != "" {
		command = append(command, "--home", shellQuote(home))
	}
	if !notice.Incomplete && validObjectID(notice.From) && validObjectID(notice.To) {
		command = append(command, "--since", shellQuote(notice.From), "--to", shellQuote(notice.To), "--all")
	} else {
		command = append(command, "--peek", "--all")
	}
	prompt := "Before anything else, run `" + strings.Join(command, " ") + "` and tell me in plain words what changed in my organization's manifest since my last session, including every link it prints, so I can read the details. Then carry on."
	if initialPrompt != "" {
		prompt += "\n\n" + initialPrompt
	}
	return prompt
}

var objectIDPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// validObjectID reports whether s is a full SHA-1 or SHA-256 Git object id.
func validObjectID(s string) bool {
	return objectIDPattern.MatchString(s)
}

// myCommandForAgent is how an agent runs this same my: plain `my` when that
// is what PATH finds, else the quoted path of this executable.
func myCommandForAgent() string {
	self, err := os.Executable()
	if err != nil {
		return "my"
	}
	if found, err := exec.LookPath("my"); err == nil && sameExecutable(found, self) {
		return "my"
	}
	return shellQuote(self)
}

func sameExecutable(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return false
	}
	ia, errA := os.Stat(ra)
	ib, errB := os.Stat(rb)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// manifestChangeNotice compares two manifest commits. It always returns a
// notice. Anything it could not read is explained in Note and marks the
// notice incomplete, so the range is retried rather than recorded.
func manifestChangeNotice(ref manifest.Ref, from, to string) changenotice.Notice {
	notice := changenotice.Notice{Manifest: ref.Name, From: from, To: to}
	repo := ref.LocalPath
	web, webOK := manifestWebRepository(repo)
	incomplete := func(note string) changenotice.Notice {
		notice.Note, notice.Incomplete = note, true
		notice.Changes = nil
		notice.Links = changenotice.Links(web, webOK, "", to, nil, false)
		return notice
	}
	if !validObjectID(from) {
		notice.From = ""
		return incomplete("The revision recorded as your last notice is not a valid commit id, so the changes cannot be listed. Run my contract list to read the rules in force, or my changes --reset to start from the current revision.")
	}
	if _, err := gitStdout(repo, "cat-file", "-e", from+"^{commit}"); err != nil {
		return incomplete(fmt.Sprintf("The revision you last saw (%s) cannot be read from the local manifest checkout, so the changes cannot be listed yet. Run my sync to retry, my contract list to read the rules in force, or my changes --reset to start from the current revision.", changenotice.Short(from)))
	}
	newRev, err := manifestRevisionAt(repo, to)
	if err != nil {
		return incomplete("The current manifest revision could not be read, so the changes cannot be listed yet: " + err.Error())
	}
	oldRev, err := manifestRevisionAt(repo, from)
	if err != nil {
		return incomplete(fmt.Sprintf("The revision you last saw (%s) could not be read, so the changes cannot be listed yet: %v", changenotice.Short(from), err))
	}
	out, err := gitStdout(repo, "diff", "--name-only", "-z", from, to, "--")
	if err != nil {
		return incomplete("The files changed in the manifest could not be listed, so the changes cannot be listed yet: " + err.Error())
	}
	var changed []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			changed = append(changed, p)
		}
	}
	notice.Changes = changenotice.Diff(oldRev, newRev, changed)
	linear := false
	var prs []int
	if _, err := gitStdout(repo, "merge-base", "--is-ancestor", from, to); err == nil {
		linear = true
		if out, err := gitStdout(repo, "log", "--format=%s", from+".."+to); err == nil {
			prs = changenotice.PullRequests(strings.Split(out, "\n"))
		}
	}
	if len(notice.Changes) > 0 {
		notice.Links = changenotice.Links(web, webOK, from, to, prs, linear)
	}
	return notice
}

// manifestRevisionAt reads manifest.json and its contract at one commit.
func manifestRevisionAt(repo, commit string) (changenotice.Revision, error) {
	raw, err := gitStdout(repo, "show", commit+":manifest.json")
	if err != nil {
		return changenotice.Revision{}, fmt.Errorf("manifest.json is unavailable at %s", changenotice.Short(commit))
	}
	var doc manifest.Document
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return changenotice.Revision{}, err
	}
	var data []byte
	var readErr error
	if file := doc.ContractPath(); file != "" {
		if err := manifest.CheckContractSource(doc); err != nil {
			return changenotice.Revision{}, err
		}
		if manifest.ValidContractFilePath(file) {
			var text string
			text, readErr = gitStdout(repo, "show", commit+":"+file)
			data = []byte(text)
		} else {
			readErr = errors.New("unsafe contract source path")
		}
	}
	contract, err := manifest.ResolveContractBytes(doc, data, readErr)
	if err != nil {
		return changenotice.Revision{}, err
	}
	return changenotice.Revision{Doc: doc, Rules: contract.Rules(), Raw: []byte(raw)}, nil
}

func resolveManifestCommit(repo, rev string) (string, error) {
	commit, err := gitStdout(repo, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve manifest revision %q", rev)
	}
	return commit, nil
}

func manifestHead(repo string) (string, error) {
	head, err := gitStdout(repo, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("manifest checkout %s has no commit", repo)
	}
	return head, nil
}

func manifestWebRepository(repo string) (changenotice.WebRepository, bool) {
	remote, err := gitStdout(repo, "remote", "get-url", "origin")
	if err != nil {
		return changenotice.WebRepository{}, false
	}
	return changenotice.ParseRemote(remote)
}

// gitStdout runs git in repo and returns stdout only, so warnings on stderr
// never mix into file contents. Output other than file contents is trimmed.
func gitStdout(repo string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	text := string(out)
	if len(args) > 0 && args[0] != "show" {
		text = strings.TrimRight(text, "\n")
	}
	return text, nil
}
