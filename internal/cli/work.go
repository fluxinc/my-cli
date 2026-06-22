package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/worksession"
	"github.com/fluxinc/my-cli/internal/workspace"
)

type workCommonOpts struct {
	home         string
	manifestName string
	umbrellaRoot string
	jsonOut      bool
}

func bindWorkCommonFlags(fs *flag.FlagSet, opts *workCommonOpts) {
	fs.StringVar(&opts.home, "home", "", "override home directory (testing)")
	fs.StringVar(&opts.manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON output")
}

func workValueFlags() map[string]bool {
	return map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
		"slug":     true,
		"message":  true,
	}
}

type sessionStartCommandReport struct {
	worksession.Session
	LaunchCommand string `json:"launch_command,omitempty"`
	JoinCommand   string `json:"join_command"`
	FinishCommand string `json:"finish_command"`
}

func (a app) runSession(args []string) error {
	return a.runSessionGroup("session", args)
}

func (a app) runWork(args []string) error {
	return a.runSessionGroup("work", args)
}

func (a app) runSessionGroup(group string, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		a.printSessionGroupUsage(group)
		return nil
	}
	switch args[0] {
	case "start":
		return a.runWorkStart(args[1:], group)
	case "join":
		if group != "session" {
			return fmt.Errorf("unknown work subcommand %q (expected start|status|list|resume|finish)", args[0])
		}
		return a.runSessionJoin(args[1:])
	case "status", "list":
		return a.runWorkStatus(args[1:], group, args[0])
	case "resume":
		return a.runWorkResume(args[1:], group)
	case "finish":
		return a.runWorkFinish(args[1:], group)
	default:
		if group == "session" {
			return fmt.Errorf("unknown session subcommand %q (expected start|join|status|list|resume|finish)", args[0])
		}
		return fmt.Errorf("unknown work subcommand %q (expected start|status|list|resume|finish)", args[0])
	}
}

func isHelpArg(arg string) bool {
	return arg == "-h" || arg == "--help" || arg == "help"
}

func (a app) printSessionGroupUsage(group string) {
	if group == "work" {
		fmt.Fprintln(a.stdout, `Usage of my work (deprecated; use my session):
  my work start [--slug SLUG] [--json] [--print] [harness] [-- harness args...]
  my work status [--all] [--json]
  my work list [--all] [--json]
  my work resume [session-id] [harness] [--json]
  my work finish [session-id] --land|--publish|--discard [--message TEXT] [--verbose] [--json]`)
		return
	}
	fmt.Fprintln(a.stdout, `Usage of my session:
  my session start [--slug SLUG] [--json] [--print] [harness] [-- harness args...]
  my session join <session-id> <harness> [-- harness args...]
  my session resume [session-id] [harness] [--json]
  my session status [--all] [--json]
  my session list [--all] [--json]
  my session finish [session-id] --land|--publish|--discard [--message TEXT] [--verbose] [--json]`)
}

func (a app) runWorkStart(args []string, group string) error {
	var opts workCommonOpts
	var slug string
	var printOnly bool
	fs := newFlagSet("my "+group+" start", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.StringVar(&slug, "slug", "", "short session slug (lowercase, digits, hyphens)")
	fs.BoolVar(&printOnly, "print", false, "print the launch command without execing")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if opts.jsonOut && printOnly {
		return fmt.Errorf("--json cannot be combined with --print")
	}
	var harnessName string
	var harnessArgs []string
	var h harness.Harness
	if len(rest) > 0 {
		harnessName = rest[0]
		parsed, err := harness.Parse(harnessName)
		if err != nil {
			return err
		}
		h = parsed
		harnessArgs = append([]string(nil), rest[1:]...)
	}

	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := a.migrateSessionLayout(root); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	specs, err := sessionMountSpecs(opts.home, opts.manifestName, root)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(specs) == 0 {
		return a.maybeJSONError(opts.jsonOut, structuredCommandError{
			code:        "no_session_mounts",
			message:     "no synced content mounts eligible for a session worktree under " + root,
			remediation: "run my setup to clone the manifest's content mounts first",
		})
	}
	doc, err := launchGuidanceDoc(opts.home, opts.manifestName, root)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	guidanceCtx, err := sessionGuidanceContext(root, doc)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}

	session, err := worksession.Start(worksession.StartOptions{
		Root:     root,
		Slug:     slug,
		Mounts:   specs,
		Guidance: guidanceCtx,
	})
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	report := sessionStartReport(session, h, harnessArgs)
	if opts.jsonOut {
		return printJSON(a.stdout, report)
	}
	if printOnly {
		a.printSessionCreatedHint(a.stderr, session)
		if harnessName == "" {
			fmt.Fprintf(a.stdout, "cd %s\n", shellQuote(session.Path))
			return nil
		}
		fmt.Fprintln(a.stdout, shellCommandLine(session.Path, h.CommandName(), harnessArgs))
		return nil
	}
	if harnessName != "" {
		a.printSessionCreatedHint(a.stderr, session)
		fmt.Fprintf(a.stderr, "launching %s...\n", h.CommandName())
		return a.runLaunch(existingSessionLaunchArgs(opts, session.ID, harnessName, harnessArgs))
	}
	a.printSessionStarted(a.stdout, session)
	return nil
}

func sessionStartReport(session worksession.Session, h harness.Harness, harnessArgs []string) sessionStartCommandReport {
	report := sessionStartCommandReport{
		Session:       session,
		JoinCommand:   "my session join " + session.ID + " <harness>",
		FinishCommand: "my session finish " + session.ID + " --land|--publish|--discard",
	}
	if h != "" {
		report.LaunchCommand = "my ai --session " + shellQuote(session.ID) + " " + shellCommandParts(h.CommandName(), harnessArgs)
	} else {
		report.LaunchCommand = "cd " + shellQuote(session.Path)
	}
	return report
}

func shellCommandParts(command string, args []string) string {
	parts := []string{shellQuote(command)}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func (a app) printSessionStarted(w interface{ Write([]byte) (int, error) }, session worksession.Session) {
	fmt.Fprintf(w, "started session %s\n", session.ID)
	fmt.Fprintf(w, "  path: %s\n", session.Path)
	for _, m := range session.Mounts {
		fmt.Fprintf(w, "  %s -> %s (from %s)\n", m.ID, m.Branch, m.BaseBranch)
	}
	fmt.Fprintf(w, "  join (another harness): my session join %s <harness>\n", session.ID)
	fmt.Fprintf(w, "  finish:                 my session finish %s --land | --publish | --discard\n", session.ID)
}

func (a app) printSessionCreatedHint(w interface{ Write([]byte) (int, error) }, session worksession.Session) {
	fmt.Fprintf(w, "started session %s (path: %s)\n", session.ID, session.Path)
	fmt.Fprintf(w, "  join (another harness): my session join %s <harness>\n", session.ID)
	fmt.Fprintf(w, "  finish:                 my session finish %s --land|--publish|--discard\n", session.ID)
}

func existingSessionLaunchArgs(opts workCommonOpts, sessionID, harnessName string, harnessArgs []string) []string {
	args := []string{"--session", sessionID}
	args = appendWorkLaunchScopeArgs(args, opts)
	args = append(args, harnessName)
	args = append(args, harnessArgs...)
	return args
}

func appendWorkLaunchScopeArgs(args []string, opts workCommonOpts) []string {
	if opts.home != "" {
		args = append(args, "--home", opts.home)
	}
	if opts.manifestName != "" {
		args = append(args, "--manifest", opts.manifestName)
	}
	if opts.umbrellaRoot != "" {
		args = append(args, "--umbrella", opts.umbrellaRoot)
	}
	return args
}

func (a app) migrateSessionLayout(root string) error {
	report, err := worksession.Migrate(root)
	if err != nil {
		return err
	}
	for _, session := range report.Sessions {
		switch session.Status {
		case "fixed":
			fmt.Fprintf(a.stderr, "migrated session %s to %s\n", session.ID, session.To)
		case "skipped":
			fmt.Fprintf(a.stderr, "warning: session %s not migrated: %s\n", session.ID, session.Message)
		}
	}
	return nil
}

func (a app) runSessionJoin(args []string) error {
	var opts workCommonOpts
	fs := newFlagSet("my session join", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return fmt.Errorf("--json cannot be used with my session join")
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: my session join <session-id> <harness> [-- harness args...]")
	}
	sessionID := rest[0]
	harnessName := rest[1]
	if _, err := harness.Parse(harnessName); err != nil {
		return err
	}
	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return err
	}
	if err := a.migrateSessionLayout(root); err != nil {
		return err
	}
	return a.runLaunch(existingSessionLaunchArgs(opts, sessionID, harnessName, rest[2:]))
}

func (a app) runWorkStatus(args []string, group string, command string) error {
	var opts workCommonOpts
	var all bool
	fs := newFlagSet("my "+group+" "+command, a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.BoolVar(&all, "all", false, "include finished and discarded sessions")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my %s %s [--all] [--json]", group, command)
	}

	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := a.migrateSessionLayout(root); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	sessions, err := worksession.List(root)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	statuses := []worksession.SessionStatus{}
	for _, session := range sessions {
		if !all && session.Status != worksession.StatusActive {
			continue
		}
		if session.Status != worksession.StatusActive {
			statuses = append(statuses, archivedWorkSessionStatus(session))
			continue
		}
		status, err := worksession.Inspect(session, nil)
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		statuses = append(statuses, status)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, statuses)
	}
	if len(statuses) == 0 {
		fmt.Fprintln(a.stdout, "no active sessions")
		return nil
	}
	for _, status := range statuses {
		fmt.Fprintf(a.stdout, "%s  %s  created %s\n", status.ID, status.Status, status.CreatedAt)
		for _, m := range status.Mounts {
			line := fmt.Sprintf("  %s  dirty=%d unlanded=%d", m.ID, len(m.Dirty), m.Unlanded)
			if m.Error != "" {
				line += "  error=" + m.Error
			}
			fmt.Fprintln(a.stdout, line)
		}
	}
	return nil
}

func archivedWorkSessionStatus(session worksession.Session) worksession.SessionStatus {
	status := worksession.SessionStatus{Session: session}
	for _, mount := range session.Mounts {
		status.Mounts = append(status.Mounts, worksession.MountStatus{Mount: mount})
	}
	return status
}

func (a app) runWorkResume(args []string, group string) error {
	var opts workCommonOpts
	fs := newFlagSet("my "+group+" resume", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.Usage = func() {
		fmt.Fprintf(a.stderr, `Usage of my %s resume:
  my %s resume [session-id] [harness] [--json]

Print a shell cd command for an active work session. This command does not
change the parent shell by itself. To launch a harness in the session, use:

  my session resume [session-id] [harness]

Options:
`, group, group)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	var sessionArg []string
	var harnessName string
	var harnessArgs []string
	if len(rest) > 0 {
		if _, err := harness.Parse(rest[0]); err == nil {
			harnessName = rest[0]
			harnessArgs = append([]string(nil), rest[1:]...)
		} else {
			sessionArg = []string{rest[0]}
			if len(rest) > 1 {
				harnessName = rest[1]
				if _, err := harness.Parse(harnessName); err != nil {
					return err
				}
				harnessArgs = append([]string(nil), rest[2:]...)
			}
		}
	}
	if harnessName == "" && len(rest) > 1 {
		return fmt.Errorf("usage: my %s resume [session-id] [harness] [--json]", group)
	}
	if harnessName != "" {
		if _, err := harness.Parse(harnessName); err != nil {
			return err
		}
	}
	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := a.migrateSessionLayout(root); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	sessionID, err := selectWorkSessionID(root, sessionArg)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	session, err := worksession.Load(root, sessionID)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if session.Status != worksession.StatusActive {
		return a.maybeJSONError(opts.jsonOut, fmt.Errorf("session %s is %s", session.ID, session.Status))
	}
	if opts.jsonOut {
		return printJSON(a.stdout, session)
	}
	if harnessName != "" {
		return a.runLaunch(existingSessionLaunchArgs(opts, session.ID, harnessName, harnessArgs))
	}
	fmt.Fprintf(a.stdout, "cd %s\n", shellQuote(session.Path))
	return nil
}

type workFinishCommandReport struct {
	Mode   string                   `json:"mode"`
	Finish worksession.FinishResult `json:"finish"`
	Sync   *syncer.Report           `json:"sync,omitempty"`
}

func (a app) runWorkFinish(args []string, group string) error {
	var opts workCommonOpts
	var land bool
	var publish bool
	var discard bool
	var verbose bool
	var message string
	fs := newFlagSet("my "+group+" finish", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.BoolVar(&land, "land", false, "merge the session into the base checkouts")
	fs.BoolVar(&publish, "publish", false, "land the session and publish landed content")
	fs.BoolVar(&discard, "discard", false, "discard the session worktrees and branches")
	fs.BoolVar(&verbose, "verbose", false, "show per-mount and sync detail in human output")
	fs.StringVar(&message, "message", "", "commit message for dirty session content")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return fmt.Errorf("usage: my %s finish [session-id] --land|--publish|--discard", group)
	}
	modeCount := boolCount(land, publish, discard)
	if modeCount != 1 {
		return fmt.Errorf("choose exactly one of --land, --publish, or --discard")
	}
	if discard && strings.TrimSpace(message) != "" {
		return fmt.Errorf("--message cannot be used with --discard")
	}

	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := a.migrateSessionLayout(root); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	sessionID, err := selectWorkSessionID(root, rest)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}

	mode := "land"
	var finish worksession.FinishResult
	if discard {
		mode = "discard"
		finish, err = worksession.Discard(worksession.DiscardOptions{Root: root, ID: sessionID})
	} else {
		if publish {
			mode = "publish"
		}
		finish, err = worksession.Land(worksession.LandOptions{
			Root:    root,
			ID:      sessionID,
			Message: message,
			Outcome: worksession.OutcomeLanded,
		})
	}
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}

	report := workFinishCommandReport{Mode: mode, Finish: finish}
	if publish {
		syncReport, err := a.syncFinishedSessionMounts(opts.home, opts.manifestName, root, finish.Session, message)
		report.Sync = &syncReport
		if err == nil && syncReportFullyPublished(syncReport) {
			session, markErr := worksession.MarkOutcome(root, finish.Session.ID, worksession.OutcomePublished, time.Time{})
			if markErr != nil {
				return a.maybeJSONError(opts.jsonOut, markErr)
			}
			report.Finish.Session = session
			finish.Session = session
		}
		if opts.jsonOut {
			if printErr := printJSON(a.stdout, report); printErr != nil {
				return printErr
			}
		} else {
			a.printWorkFinishReport(report, verbose)
		}
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		return nil
	}

	if opts.jsonOut {
		return printJSON(a.stdout, report)
	}
	a.printWorkFinishReport(report, verbose)
	return nil
}

func boolCount(values ...bool) int {
	var count int
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func selectWorkSessionID(root string, args []string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	active, err := activeWorkSessions(root)
	if err != nil {
		return "", err
	}
	switch len(active) {
	case 1:
		return active[0].ID, nil
	case 0:
		return "", fmt.Errorf("no active sessions")
	default:
		return "", fmt.Errorf("multiple active sessions; pass a session id")
	}
}

func activeWorkSessions(root string) ([]worksession.Session, error) {
	sessions, err := worksession.List(root)
	if err != nil {
		return nil, err
	}
	var active []worksession.Session
	for _, session := range sessions {
		if session.Status == worksession.StatusActive {
			active = append(active, session)
		}
	}
	return active, nil
}

func (a app) syncFinishedSessionMounts(home, manifestName, root string, session worksession.Session, message string) (syncer.Report, error) {
	entries, err := a.collectSyncEntries(home, manifestName, root, "content")
	if err != nil {
		return syncer.Report{}, err
	}
	sessionRepos := map[string]bool{}
	for _, mount := range session.Mounts {
		abs, err := filepath.Abs(mount.RepoPath)
		if err != nil {
			return syncer.Report{}, err
		}
		sessionRepos[abs] = true
	}
	var selected []syncer.Entry
	for _, entry := range entries {
		abs, err := filepath.Abs(entry.LocalPath)
		if err != nil {
			return syncer.Report{}, err
		}
		if sessionRepos[abs] {
			selected = append(selected, entry)
		}
	}
	if len(selected) == 0 {
		return syncer.Report{}, fmt.Errorf("no content sync entries matched session %s", session.ID)
	}
	gnitRoot := findGnitWorkspaceRoot(root)
	backend := "builtin"
	if gnitRoot != "" {
		backend = "gnit"
	}
	sessionHolds, err := collectSessionHolds(root)
	if err != nil {
		return syncer.Report{}, err
	}
	publish, err := a.syncPushPublish(home, manifestName)
	if err != nil {
		return syncer.Report{}, err
	}
	report := syncer.Run(selected, syncer.Options{
		Backend:      backend,
		GnitRoot:     gnitRoot,
		Publish:      publish,
		Message:      message,
		Visibility:   a.githubRepoVisibility,
		SessionHolds: sessionHolds,
	})
	if err := a.saveLastSyncReport(home, manifestName, root, report); err != nil {
		return report, err
	}
	return report, nil
}

func syncReportFullyPublished(report syncer.Report) bool {
	if len(report.Results) == 0 {
		return false
	}
	for _, result := range report.Results {
		switch result.Status {
		case "pushed", "already landed":
			continue
		default:
			return false
		}
	}
	return true
}

func (a app) printWorkFinishReport(report workFinishCommandReport, verbose bool) {
	session := report.Finish.Session
	fmt.Fprintf(a.stdout, "session\t%s\t%s", session.ID, session.Status)
	if session.Outcome != "" {
		fmt.Fprintf(a.stdout, "\t%s", session.Outcome)
	}
	fmt.Fprintln(a.stdout)
	for _, mount := range report.Finish.Mounts {
		if !workFinishMountVisible(mount, verbose) {
			continue
		}
		line := fmt.Sprintf("mount\t%s\t%s\t%s", mount.ID, mount.Branch, mount.Status)
		if mount.Commit != "" {
			line += "\tcommit=" + mount.Commit
		}
		if len(mount.Dirty) != 0 {
			line += "\tdirty=" + strings.Join(mount.Dirty, ",")
		}
		if len(mount.Changed) != 0 {
			line += "\tchanged=" + strings.Join(mount.Changed, ",")
		}
		if mount.Message != "" {
			line += "\t" + strings.ReplaceAll(mount.Message, "\n", " ")
		}
		fmt.Fprintln(a.stdout, line)
	}
	if report.Sync != nil {
		a.printSyncReport(*report.Sync, verbose, syncNextCommands{
			Apply:  "my sync --push",
			Review: "my sync --push --print",
		})
	}
	if label, command := workFinishNextStep(report); command != "" {
		fmt.Fprintf(a.stdout, "next\t%s\t%s\n", label, command)
	}
}

func workFinishNextStep(report workFinishCommandReport) (string, string) {
	switch report.Mode {
	case "land":
		return "publish", "my sync --push"
	case "publish":
		if report.Finish.Session.Outcome == worksession.OutcomePublished {
			return "status", "my session status"
		}
		if report.Sync != nil && syncReportHasPublishDisabledHold(*report.Sync) {
			return "", ""
		}
		return "review", "my sync --push --print"
	case "discard":
		return "status", "my session status"
	default:
		return "", ""
	}
}

func workFinishMountVisible(mount worksession.MountFinishResult, verbose bool) bool {
	if verbose {
		return true
	}
	return mount.Status != "landed" && mount.Status != "discarded"
}

// collectSessionHolds reports the active sessions with dirty files or
// unlanded commits per mount repository, so sync can hold outbound publish of
// those repositories until each session is finished or discarded.
func collectSessionHolds(root string) ([]syncer.SessionHold, error) {
	sessions, err := worksession.List(root)
	if err != nil {
		return nil, err
	}
	var holds []syncer.SessionHold
	for _, session := range sessions {
		if session.Status != worksession.StatusActive {
			continue
		}
		status, err := worksession.Inspect(session, nil)
		if err != nil {
			return nil, err
		}
		for _, mount := range status.Mounts {
			if len(mount.Dirty) == 0 && mount.Unlanded == 0 && mount.Error == "" {
				continue
			}
			hold := syncer.SessionHold{
				SessionID:     session.ID,
				SessionPath:   session.Path,
				MountID:       mount.ID,
				RepoPath:      mount.RepoPath,
				DirtyCount:    len(mount.Dirty),
				UnlandedCount: mount.Unlanded,
			}
			holds = append(holds, hold)
		}
	}
	return holds, nil
}

// resolveWorkUmbrella locates the umbrella root for work commands: explicit
// flag, walk-up discovery, then the configured root of registered manifests.
func resolveWorkUmbrella(home, manifestName, explicit string) (string, error) {
	if explicit != "" {
		return resolveUmbrellaRoot(home, explicit)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return root, nil
	}
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, doc := range docs {
		root, err := umbrella.ResolveRoot(home, "", "", doc.doc)
		if err != nil {
			return "", err
		}
		if _, err := umbrella.LoadWorkspace(root); err == nil {
			if !stringInSlice(candidates, root) {
				candidates = append(candidates, root)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return "", noUmbrellaError("no my umbrella found; run my setup or pass --umbrella", "run my setup or pass --umbrella <path>")
	default:
		return "", structuredCommandError{
			code:        "ambiguous_umbrella",
			message:     fmt.Sprintf("multiple umbrellas configured (%v); pass --umbrella or --manifest", candidates),
			remediation: "pass --umbrella <path> to select one umbrella",
		}
	}
}

// sessionMountSpecs returns the content mounts under root that are eligible
// for session worktrees: every locally cloned mount except repo-kind code
// mounts, which keep their own product-style flow.
func sessionMountSpecs(home, manifestName, root string) ([]worksession.MountSpec, error) {
	mounts, err := workspace.ListMounts(home, manifestName, root)
	if err != nil {
		return nil, err
	}
	var specs []worksession.MountSpec
	seen := map[string]bool{}
	for _, mount := range mounts {
		if mount.UmbrellaRoot != root || mount.Kind == "repo" || seen[mount.LocalPath] {
			continue
		}
		if !isGitCheckout(mount.LocalPath) {
			continue
		}
		seen[mount.LocalPath] = true
		specs = append(specs, worksession.MountSpec{
			ID:           mount.ID,
			Kind:         mount.Kind,
			RepoPath:     mount.LocalPath,
			ContentPaths: syncContentPaths(mount),
		})
	}
	return specs, nil
}

// doctorSessions reports work-session health under root: live state for each
// active session and a single archived count for finished/discarded records.
func doctorSessions(root string) []doctorItem {
	sessions, err := worksession.List(root)
	if err != nil {
		return []doctorItem{{Name: "registry", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	finished, discarded := 0, 0
	for _, session := range sessions {
		switch session.Status {
		case worksession.StatusFinished:
			finished++
		case worksession.StatusDiscarded:
			discarded++
		default:
			items = append(items, doctorSessionItem(session))
		}
	}
	if finished+discarded > 0 {
		items = append(items, doctorItem{
			Name:    "archived",
			Status:  "ok",
			Message: fmt.Sprintf("finished=%d discarded=%d", finished, discarded),
		})
	}
	if legacy, err := worksession.LegacyLayout(root); err != nil {
		items = append(items, doctorItem{Name: "legacy-layout", Status: "error", Message: err.Error()})
	} else {
		for _, session := range legacy.Sessions {
			items = append(items, doctorItem{
				Name:     session.ID,
				Status:   "warning",
				Path:     session.From,
				Message:  "legacy session layout; run my session status or my doctor --fix to migrate",
				WouldFix: "migrate session layout to " + session.To,
			})
		}
		for _, orphan := range legacy.Orphans {
			items = append(items, doctorItem{
				Name:    "orphan:" + filepath.Base(orphan),
				Status:  "warning",
				Path:    orphan,
				Message: "orphan legacy work directory has no session registry record; inspect and remove manually if obsolete",
			})
		}
	}
	return items
}

func doctorSessionItem(session worksession.Session) doctorItem {
	item := doctorItem{Name: session.ID, Path: session.Path}
	for _, mount := range session.Mounts {
		if _, err := os.Stat(mount.WorktreePath); err != nil {
			item.Status = "error"
			item.Message = "worktree missing for mount " + mount.ID
			item.Details = append(item.Details, "discard the session record with: my session finish "+session.ID+" --discard")
			return item
		}
	}
	status, err := worksession.Inspect(session, nil)
	if err != nil {
		item.Status = "error"
		item.Message = err.Error()
		return item
	}
	dirty, unlanded := 0, 0
	for _, mount := range status.Mounts {
		if mount.Error != "" {
			item.Status = "error"
			item.Message = mount.ID + ": " + mount.Error
			return item
		}
		dirty += len(mount.Dirty)
		unlanded += mount.Unlanded
	}
	if dirty == 0 && unlanded == 0 {
		item.Status = "ok"
		item.Message = "active, clean"
		return item
	}
	item.Status = "warning"
	item.Message = fmt.Sprintf("active: %d dirty, %d unlanded; finish with: my session finish %s --land", dirty, unlanded, session.ID)
	return item
}

func (a app) doctorFixSessionLayout(root string) []doctorItem {
	if root == "" {
		return nil
	}
	report, err := worksession.Migrate(root)
	if err != nil {
		return []doctorItem{{Name: "session-layout", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	for _, session := range report.Sessions {
		item := doctorItem{
			Name:    "session-layout:" + session.ID,
			Path:    session.To,
			Message: session.Message,
		}
		switch session.Status {
		case "fixed":
			item.Status = "fixed"
			if item.Message == "" {
				item.Message = "migrated session layout"
			}
			item.Details = append(item.Details, "from="+session.From)
		case "skipped":
			item.Status = "skipped"
			if item.Message == "" {
				item.Message = "session layout migration skipped"
			}
		default:
			item.Status = session.Status
		}
		items = append(items, item)
	}
	for _, orphan := range report.Orphans {
		items = append(items, doctorItem{
			Name:    "session-layout:orphan:" + filepath.Base(orphan),
			Status:  "skipped",
			Path:    orphan,
			Message: "orphan legacy work directory has no session registry record",
		})
	}
	return items
}
