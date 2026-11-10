package cli

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/attempt"
)

// attemptNoticeWindow bounds the startup notice to recent launches. Older
// records stay in `my attempts list`; an old observation is not news.
const attemptNoticeWindow = 7 * 24 * time.Hour

type attemptLaunch struct {
	harness string
	root    string
	dir     string
	goal    string
}

// withAttempt records local evidence of one harness launch around run. It
// never changes what run returns: a failure to record is a stderr notice.
func (a app) withAttempt(meta attemptLaunch, run func(app) error) error {
	now := time.Now()
	host, _ := os.Hostname()
	repos := attemptRepoTargets(meta.root, meta.dir)
	rec := attempt.Record{
		OSUser:    osUserName(),
		Host:      host,
		Harness:   meta.harness,
		Umbrella:  meta.root,
		WorkDir:   meta.dir,
		SessionID: attemptSessionID(meta.root, meta.dir),
		Goal:      meta.goal,
		StartedAt: now.UTC().Format(time.RFC3339),
		Launcher:  attempt.ProcessRef{PID: os.Getpid()},
		Before:    attempt.Observe(attempt.KindBefore, repos, attempt.RunGit, now),
	}
	if start, found, err := attempt.ProcessStart(os.Getpid()); err == nil && found {
		rec.Launcher.StartedAt = start
	}
	handle, err := attempt.Begin(meta.root, rec)
	if err != nil {
		if !errors.Is(err, attempt.ErrUnsupported) {
			fmt.Fprintf(a.stderr, "notice\tattempts\tthis launch is not being recorded: %v\n", err)
		}
		return run(a)
	}
	defer handle.Close()

	started := false
	a.onHarnessStart = func(pid int) {
		started = true
		ref := attempt.ProcessRef{PID: pid}
		// A failed or missing start time leaves identity unknown; a pid
		// alone is never treated as proof of which process this was.
		if start, found, err := attempt.ProcessStart(pid); err == nil && found {
			ref.StartedAt = start
		}
		// A write failure here is retried by the closing update below.
		_ = handle.Update(func(r *attempt.Record) { r.Child = &ref })
	}
	runErr := run(a)

	end := time.Now()
	after := attempt.Observe(attempt.KindAfter, repos, attempt.RunGit, end)
	writeErr := handle.Update(func(r *attempt.Record) {
		at := end.UTC().Format(time.RFC3339)
		var exit exitCodeError
		switch {
		case runErr == nil:
			r.ClosureReason = attempt.ClosureExited
			r.ObservedExit = &attempt.Exit{Code: 0, At: at}
		case errors.As(runErr, &exit):
			r.ClosureReason = attempt.ClosureExited
			r.ObservedExit = &attempt.Exit{Code: exit.code, Signal: exit.signal, At: at}
		case started:
			// The child ran but its end was not a plain exit status.
			r.ClosureReason = attempt.ClosureExited
			r.Errors = append(r.Errors, "child wait: "+runErr.Error())
		default:
			r.ClosureReason = attempt.ClosureStartFailed
			r.StartError = runErr.Error()
		}
		r.After = after
		r.LocalChangeObserved = attempt.LocalChange(r.Before, r.After)
	})
	if writeErr != nil {
		fmt.Fprintf(a.stderr, "notice\tattempts\tthe record of this launch could not be saved: %v\n", writeErr)
	}
	return runErr
}

// attemptRepoTargets finds the Git checkouts a launch can plausibly touch:
// the umbrella root, its mounts, opted-in repos, and the worktrees of the
// session the launch runs in. It is a bounded two-level scan, not a walk.
func attemptRepoTargets(root, dir string) []attempt.RepoTarget {
	seen := map[string]bool{}
	var targets []attempt.RepoTarget
	add := func(path string) {
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		if _, err := os.Lstat(filepath.Join(path, ".git")); err != nil {
			return
		}
		seen[path] = true
		label, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(label, "..") {
			label = path
		}
		targets = append(targets, attempt.RepoTarget{Label: filepath.ToSlash(label), Path: path})
	}
	children := func(parent string, skip map[string]bool) {
		items, err := os.ReadDir(parent)
		if err != nil {
			return
		}
		for _, item := range items {
			if item.IsDir() && !skip[item.Name()] {
				add(filepath.Join(parent, item.Name()))
			}
		}
	}
	add(root)
	children(root, map[string]bool{".my-cli": true, "sessions": true, "work": true, "repos": true, "personal": true})
	children(filepath.Join(root, "repos"), nil)
	if id := attemptSessionID(root, dir); id != "" {
		children(filepath.Join(root, "sessions", id), nil)
	}
	add(dir)
	sort.Slice(targets, func(i, j int) bool { return targets[i].Path < targets[j].Path })
	return targets
}

// attemptSessionID returns the session a launch directory belongs to.
func attemptSessionID(root, dir string) string {
	rel, err := filepath.Rel(filepath.Join(root, "sessions"), dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return strings.Split(filepath.ToSlash(rel), "/")[0]
}

func osUserName() string {
	if current, err := user.Current(); err == nil {
		return current.Username
	}
	return ""
}

func attemptListOptions(reconcile bool) attempt.ListOptions {
	host, _ := os.Hostname()
	return attempt.ListOptions{
		Reconcile: reconcile,
		Host:      host,
		Probe:     attempt.ProbeChild,
		Observe: func(repos []attempt.RepoTarget) *attempt.Observation {
			return attempt.Observe(attempt.KindAtReconcile, repos, attempt.RunGit, time.Now())
		},
	}
}

// attemptStartupNotice reconciles launches whose launcher is gone and prints
// one historical line when recent ones observed local changes. It is never
// called for --print.
func (a app) attemptStartupNotice(root string) {
	entries, err := attempt.List(root, attemptListOptions(true))
	if err != nil {
		if !errors.Is(err, attempt.ErrUnsupported) {
			fmt.Fprintf(a.stderr, "notice\tattempts\tearlier launch records could not be read: %v\n", err)
		}
		return
	}
	now := time.Now()
	count := 0
	for _, entry := range entries {
		if attemptEndedLauncher(entry) && attemptLocalChange(entry) && now.Sub(attemptEndedAt(entry)) <= attemptNoticeWindow {
			count++
		}
	}
	if count > 0 {
		fmt.Fprintf(a.stderr, "notice\tattempts\t%d earlier my ai %s observed local changes; run my attempts list --local-changes. No work-service tracking was established.\n", count, plural(count, "launch", "launches"))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// attemptEndedLauncher reports whether the launcher of this attempt is gone.
func attemptEndedLauncher(entry attempt.Entry) bool {
	return entry.State == attempt.StateClosed || entry.State == attempt.StateUnclosed
}

func attemptLocalChange(entry attempt.Entry) bool {
	return entry.Record != nil && entry.Record.LocalChangeObserved != nil && *entry.Record.LocalChangeObserved
}

func attemptEndedAt(entry attempt.Entry) time.Time {
	if entry.Record == nil {
		return time.Time{}
	}
	stamp := entry.Record.ReconciledAt
	if entry.Record.ObservedExit != nil {
		stamp = entry.Record.ObservedExit.At
	}
	if stamp == "" && entry.Record.After != nil {
		stamp = entry.Record.After.CollectedAt
	}
	at, _ := time.Parse(time.RFC3339, stamp)
	return at
}

// attemptProblems collects every error an attempt carries: the entry's own,
// the record's, and any checkout that could not be observed.
func attemptProblems(entry attempt.Entry) []string {
	var problems []string
	if entry.Error != "" {
		problems = append(problems, entry.Error)
	}
	if rec := entry.Record; rec != nil {
		problems = append(problems, rec.Errors...)
		for _, obs := range []*attempt.Observation{rec.Before, rec.After} {
			if obs == nil {
				continue
			}
			for _, repo := range obs.Repos {
				if repo.Error != "" {
					name := repo.Label
					if name == "" {
						name = repo.Path
					}
					problems = append(problems, fmt.Sprintf("could not observe %s (%s): %s", name, obs.Kind, repo.Error))
				}
			}
		}
	}
	return problems
}

type attemptListReport struct {
	SchemaVersion int             `json:"schema_version"`
	Supported     bool            `json:"supported"`
	Note          string          `json:"note,omitempty"`
	Attempts      []attempt.Entry `json:"attempts"`
}

const attemptUnsupportedNote = "attempt capture is not supported on native Windows; my ai launches are not recorded here"

func (a app) runAttempts(args []string) error {
	const usage = "usage: my attempts list [--running|--unclosed|--local-changes] [--json]"
	if len(args) == 0 || isHelpArg(args[0]) {
		fmt.Fprintln(a.stderr, usage+`

Lists local records of my ai launches. A record is evidence that a harness
was launched and what the nearby Git checkouts looked like before and after.
It does not say that work was finished, published, or tracked by any service,
and checkouts shared by several agents are observed, not attributed.

  --running        launches whose launcher process is alive
  --unclosed       launches whose launcher stopped without recording an exit
  --local-changes  launches that observed uncommitted or unpublished local changes`)
		return nil
	}
	if args[0] != "list" {
		return fmt.Errorf("unknown attempts subcommand %q (expected list)", args[0])
	}
	var opts workCommonOpts
	var running, unclosed, localChanges bool
	fs := newFlagSet("my attempts list", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.BoolVar(&running, "running", false, "only launches whose launcher is alive")
	fs.BoolVar(&unclosed, "unclosed", false, "only launches whose launcher stopped without recording an exit")
	fs.BoolVar(&localChanges, "local-changes", false, "only launches that observed local changes")
	rest, err := parseInterspersed(fs, args[1:], workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errors.New(usage)
	}
	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	report := attemptListReport{SchemaVersion: attempt.SchemaVersion, Supported: true, Attempts: []attempt.Entry{}}
	entries, err := attempt.List(root, attemptListOptions(true))
	switch {
	case errors.Is(err, attempt.ErrUnsupported):
		report.Supported, report.Note = false, attemptUnsupportedNote
	case err != nil:
		return a.maybeJSONError(opts.jsonOut, err)
	}
	for _, entry := range entries {
		// Anything carrying an error always shows: an error that disappears
		// behind a filter is an error nobody fixes.
		broken := len(attemptProblems(entry)) > 0
		keep := !running && !unclosed && !localChanges
		keep = keep || running && (entry.State == attempt.StateRunning || entry.State == attempt.StateStarting)
		keep = keep || unclosed && entry.State == attempt.StateUnclosed
		keep = keep || localChanges && attemptLocalChange(entry)
		if keep || broken {
			report.Attempts = append(report.Attempts, entry)
		}
	}
	if opts.jsonOut {
		return printJSON(a.stdout, report)
	}
	if !report.Supported {
		fmt.Fprintln(a.stdout, report.Note)
		return nil
	}
	if len(report.Attempts) == 0 {
		fmt.Fprintln(a.stdout, "no matching launch records")
		return nil
	}
	for _, entry := range report.Attempts {
		fmt.Fprintln(a.stdout, attemptLine(entry))
	}
	return nil
}

func attemptLine(entry attempt.Entry) string {
	fields := []string{entry.ID, entry.State}
	if rec := entry.Record; rec != nil {
		exit := "exit=unobserved"
		if rec.ObservedExit != nil {
			exit = fmt.Sprintf("exit=%d", rec.ObservedExit.Code)
			if rec.ObservedExit.Signal != "" {
				exit += " (" + rec.ObservedExit.Signal + ")"
			}
		}
		change := "local-changes=unknown"
		if rec.LocalChangeObserved != nil {
			change = fmt.Sprintf("local-changes=%t", *rec.LocalChangeObserved)
		}
		fields = append(fields, rec.Harness, exit, change, "started="+rec.StartedAt, rec.WorkDir)
		if rec.ChildState != "" {
			fields = append(fields, "child="+rec.ChildState)
		}
		if rec.Goal != "" {
			fields = append(fields, "goal="+rec.Goal)
		}
	}
	for _, problem := range attemptProblems(entry) {
		fields = append(fields, "error="+problem)
	}
	return strings.Join(fields, "\t")
}

// doctorAttempts summarizes launch records without changing them: doctor
// inspects, it does not reconcile.
func doctorAttempts(root string) []doctorItem {
	entries, err := attempt.List(root, attemptListOptions(false))
	if errors.Is(err, attempt.ErrUnsupported) {
		return []doctorItem{{Name: "capture", Status: "info", Path: attempt.Dir(root), Message: attemptUnsupportedNote}}
	}
	if err != nil {
		return []doctorItem{{Name: "records", Status: "error", Path: attempt.Dir(root), Message: err.Error()}}
	}
	var items []doctorItem
	running, unclosed, changed := 0, 0, 0
	// Records carrying errors are counted into one bounded warning; a broken
	// checkout would otherwise add a row for every launch that saw it.
	const maxProblemDetails = 5
	withProblems := 0
	var problemDetails []string
	for _, entry := range entries {
		switch entry.State {
		case attempt.StateRunning, attempt.StateStarting:
			running++
		case attempt.StateUnclosed:
			unclosed++
		}
		if attemptEndedLauncher(entry) && attemptLocalChange(entry) {
			changed++
		}
		if problems := attemptProblems(entry); len(problems) > 0 {
			withProblems++
			if len(problemDetails) < maxProblemDetails {
				problemDetails = append(problemDetails, entry.ID+": "+problems[0])
			}
		}
	}
	if withProblems > 0 {
		items = append(items, doctorItem{
			Name:    "errors",
			Status:  "warn",
			Path:    attempt.Dir(root),
			Message: fmt.Sprintf("%d launch %s carry errors (unreadable record, failed observation or wait error); run my attempts list", withProblems, plural(withProblems, "record", "records")),
			Details: problemDetails,
		})
	}
	if len(entries) == 0 {
		return items
	}
	status := "ok"
	if unclosed > 0 || changed > 0 {
		status = "info"
	}
	summary := doctorItem{
		Name:    "launches",
		Status:  status,
		Path:    attempt.Dir(root),
		Message: fmt.Sprintf("%d recorded, %d running, %d stopped without a recorded exit, %d observed local changes (historical evidence; no work-service tracking)", len(entries), running, unclosed, changed),
	}
	if unclosed > 0 || changed > 0 {
		summary.Details = []string{"my attempts list --unclosed", "my attempts list --local-changes"}
	}
	return append([]doctorItem{summary}, items...)
}
