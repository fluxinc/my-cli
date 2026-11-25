package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/changenotice"
	"github.com/fluxinc/my-cli/internal/manifest"
)

// changesReport is the JSON form of `my changes`.
type changesReport struct {
	changenotice.Notice
	// Baseline means no earlier revision was recorded for this person, so the
	// current one became the starting point and nothing is listed.
	Baseline bool `json:"baseline,omitempty"`
	// MarkedSeen means this command recorded To as told to this person.
	MarkedSeen bool `json:"marked_seen"`
}

func (a app) runChanges(args []string) error {
	var home, name, root, since string
	var jsonOut, peek bool
	fs := newFlagSet("my changes", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&name, "manifest", "", "select a registered manifest")
	fs.StringVar(&root, "umbrella", "", "select an umbrella root")
	fs.StringVar(&since, "since", "", "list changes since this manifest commit; records nothing")
	fs.BoolVar(&peek, "peek", false, "list unseen changes without recording them as seen")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, `Usage: my changes [--since REV] [--peek] [--json] [--manifest NAME] [--home DIR] [--umbrella DIR]

Lists what changed in the organization manifest since you were last told:
contract rules added, amended or retracted, guidance files updated, and
skills, tools, services, mounts, policies and roles added, removed or updated,
with links to the pull requests. Listing them records them as seen, so the
next session start does not repeat them. --peek lists without recording;
--since lists any range and records nothing.`)
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "manifest": true, "umbrella": true, "since": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my changes [--since REV] [--peek] [--json]")
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
	from := since
	if since != "" {
		from, err = gitStdout(ref.LocalPath, "rev-parse", "--verify", "--end-of-options", since+"^{commit}")
		if err != nil {
			return a.maybeJSONError(jsonOut, fmt.Errorf("cannot resolve manifest revision %q", since))
		}
	} else {
		state, found, err := changenotice.Load(stateHome, ref.Name)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
		if !found {
			if !peek {
				if err := changenotice.Save(stateHome, ref.Name, head, true, time.Now()); err != nil {
					return a.maybeJSONError(jsonOut, err)
				}
			}
			report.Baseline, report.MarkedSeen = true, !peek
			return a.printChanges(report, jsonOut)
		}
		from = state.Noticed
	}
	if from != head {
		notice := manifestChangeNotice(ref, from, head)
		notice.Changes = append([]changenotice.Change{}, notice.Changes...)
		report.Notice = notice
	}
	if since == "" && !peek && from != head {
		if err := changenotice.Save(stateHome, ref.Name, head, false, time.Now()); err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
		report.MarkedSeen = true
	}
	return a.printChanges(report, jsonOut)
}

func (a app) printChanges(report changesReport, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, report)
	}
	switch {
	case report.Baseline:
		fmt.Fprintf(a.stdout, "No earlier revision of %s is recorded for you. %s is now the starting point; later changes will be listed here and at your next session start.\n", report.Manifest, changenotice.Short(report.To))
	case report.Empty():
		fmt.Fprintf(a.stdout, "No changes to %s since your last session (at %s).\n", report.Manifest, changenotice.Short(report.To))
	default:
		fmt.Fprint(a.stdout, changenotice.Render(report.Notice))
	}
	return nil
}

// launchChangeNotice returns the changes this person has not been told about,
// or nil. A first launch records the current revision as the starting point
// and reports nothing. Failures are printed and never stop a launch.
func (a app) launchChangeNotice(home string, ref manifest.Ref) (*changenotice.Notice, func()) {
	stateHome, err := resolveHome(home)
	if err != nil {
		return nil, nil
	}
	head, err := manifestHead(ref.LocalPath)
	if err != nil {
		return nil, nil
	}
	state, found, err := changenotice.Load(stateHome, ref.Name)
	if err != nil {
		fmt.Fprintf(a.stderr, "notice\tchanges\tcould not read which manifest changes you were shown: %v\n", err)
		return nil, nil
	}
	if !found {
		if err := changenotice.Save(stateHome, ref.Name, head, true, time.Now()); err != nil {
			fmt.Fprintf(a.stderr, "notice\tchanges\tcould not record the manifest revision: %v\n", err)
		}
		return nil, nil
	}
	if state.Noticed == head {
		return nil, nil
	}
	notice := manifestChangeNotice(ref, state.Noticed, head)
	seen := func() {
		if err := changenotice.Save(stateHome, ref.Name, head, false, time.Now()); err != nil {
			fmt.Fprintf(a.stderr, "notice\tchanges\tcould not record these changes as seen: %v\n", err)
		}
	}
	if notice.Empty() {
		// Only files nobody is told about changed (for example a README).
		seen()
		return nil, nil
	}
	return &notice, seen
}

// manifestChangeNotice compares two manifest commits. It always returns a
// notice; what could not be read is explained in its Note.
func manifestChangeNotice(ref manifest.Ref, from, to string) changenotice.Notice {
	notice := changenotice.Notice{Manifest: ref.Name, From: from, To: to}
	repo := ref.LocalPath
	web, webOK := manifestWebRepository(repo)
	if _, err := gitStdout(repo, "cat-file", "-e", from+"^{commit}"); err != nil {
		notice.Note = fmt.Sprintf("The revision you last saw (%s) is no longer in the local manifest history, so the changes cannot be listed. Run `my contract list` to read the rules in force.", changenotice.Short(from))
		notice.Links = changenotice.Links(web, webOK, "", to, nil)
		return notice
	}
	newRev, err := manifestRevisionAt(repo, to)
	if err != nil {
		notice.Note = "The current manifest revision could not be read: " + err.Error()
		return notice
	}
	oldRev, err := manifestRevisionAt(repo, from)
	if err != nil {
		notice.Note = fmt.Sprintf("The revision you last saw (%s) could not be read, so only links are listed: %v", changenotice.Short(from), err)
	}
	var changed []string
	if out, err := gitStdout(repo, "diff", "--name-only", "-z", from, to, "--"); err == nil {
		for _, p := range strings.Split(out, "\x00") {
			if p != "" {
				changed = append(changed, p)
			}
		}
	}
	if notice.Note == "" {
		notice.Changes = changenotice.Diff(oldRev, newRev, changed)
	}
	var prs []int
	if _, err := gitStdout(repo, "merge-base", "--is-ancestor", from, to); err == nil {
		if out, err := gitStdout(repo, "log", "--format=%s", from+".."+to); err == nil {
			prs = changenotice.PullRequests(strings.Split(out, "\n"))
		}
	}
	if len(notice.Changes) > 0 || notice.Note != "" {
		notice.Links = changenotice.Links(web, webOK, from, to, prs)
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
	return changenotice.Revision{Doc: doc, Rules: contract.Rules()}, nil
}

func manifestHead(repo string) (string, error) {
	head, err := gitStdout(repo, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("manifest checkout %s has no commit", repo)
	}
	return strings.TrimSpace(head), nil
}

func manifestWebRepository(repo string) (changenotice.WebRepository, bool) {
	remote, err := gitStdout(repo, "remote", "get-url", "origin")
	if err != nil {
		return changenotice.WebRepository{}, false
	}
	return changenotice.ParseRemote(remote)
}

// gitStdout runs git in repo and returns stdout only, so warnings on stderr
// never mix into file contents.
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
