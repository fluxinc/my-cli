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
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/worksession"
	"github.com/fluxinc/my-cli/internal/workspace"
	"github.com/fluxinc/my-cli/internal/worktreecheck"
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
	// TTRoom is the Talking Stick room `tt room ensure` verified for the
	// session during this command; omitted when none was verified.
	TTRoom string `json:"tt_room,omitempty"`
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
	case "leftovers":
		if group != "session" {
			return fmt.Errorf("unknown work subcommand %q (expected start|status|list|resume|finish)", args[0])
		}
		return a.runSessionLeftovers(args[1:])
	case "close-worktree":
		if group != "session" {
			return fmt.Errorf("unknown work subcommand %q (expected start|status|list|resume|finish)", args[0])
		}
		return a.runSessionCloseWorktree(args[1:])
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
			return fmt.Errorf("unknown session subcommand %q (expected start|join|status|list|resume|finish|leftovers|close-worktree)", args[0])
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
  my session finish [session-id] --land|--publish|--discard [--message TEXT] [--verbose] [--json]
  my session leftovers [--all] [--json]
  my session close-worktree <path> [--yes] [--json]`)
}

type leftoverCommandRow struct {
	worktreecheck.Entry
	NextCommand string `json:"next_command,omitempty"`
}

type leftoverCommandReport struct {
	Entries []leftoverCommandRow  `json:"entries"`
	Issues  []worktreecheck.Issue `json:"issues,omitempty"`
}

func (a app) runSessionLeftovers(args []string) error {
	var opts workCommonOpts
	var all bool
	fs := newFlagSet("my session leftovers", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.BoolVar(&all, "all", false, "also scan the registered manifest checkout")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my session leftovers [--all] [--json]")
	}
	report, err := a.inspectWorktreeLeftovers(opts, all)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	commandReport := leftoverReportWithCommands(report)
	if opts.jsonOut {
		return printJSON(a.stdout, commandReport)
	}
	a.printLeftoverReport(commandReport)
	return nil
}

func (a app) runSessionCloseWorktree(args []string) error {
	var opts workCommonOpts
	var yes bool
	fs := newFlagSet("my session close-worktree", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.BoolVar(&yes, "yes", false, "confirm removing the clean worktree while preserving its branch")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my session close-worktree <path> [--yes] [--json]")
	}
	report, err := a.inspectWorktreeLeftovers(opts, false)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	var target *worktreecheck.Entry
	for i := range report.Entries {
		entry := &report.Entries[i]
		if samePath(entry.Path, rest[0]) {
			target = entry
			break
		}
	}
	if target == nil {
		return a.maybeJSONError(opts.jsonOut, structuredCommandError{
			code:        "unknown_worktree",
			message:     "worktree is not in the current managed leftover inventory: " + rest[0],
			remediation: "run my session leftovers and choose an exact listed path",
		})
	}
	if err := validateCloseWorktree(*target); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if !yes {
		if !a.interactive || opts.jsonOut {
			return a.maybeJSONError(opts.jsonOut, fmt.Errorf("closing a worktree requires --yes after reviewing `my session leftovers`; branch %s will be preserved", target.Branch))
		}
		warning := "Remove clean worktree " + target.Path + " and preserve branch " + target.Branch + "?"
		if target.Unlanded {
			warning = "Remove clean unlanded worktree " + target.Path + "? Commits remain on preserved branch " + target.Branch + "."
		}
		confirmed, _, err := a.promptConfirm(warning, false)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(a.stdout, "left worktree unchanged")
			return nil
		}
	}
	// Re-inspect immediately before mutation so dirty/lock/session state cannot
	// silently drift between the displayed inventory and the close operation.
	recheck, err := a.inspectWorktreeLeftovers(opts, false)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	var current *worktreecheck.Entry
	for i := range recheck.Entries {
		if samePath(recheck.Entries[i].Path, target.Path) {
			current = &recheck.Entries[i]
			break
		}
	}
	if current == nil {
		return a.maybeJSONError(opts.jsonOut, fmt.Errorf("worktree inventory changed before close; run my session leftovers again"))
	}
	if err := validateCloseWorktree(*current); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := worktreecheck.RemoveClean(*current); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	result := map[string]any{
		"status":           "closed",
		"path":             current.Path,
		"repo_path":        current.RepoPath,
		"branch":           current.Branch,
		"branch_preserved": true,
	}
	if opts.jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "closed worktree %s; preserved branch %s\n", current.Path, current.Branch)
	return nil
}

func validateCloseWorktree(entry worktreecheck.Entry) error {
	switch entry.Class {
	case worktreecheck.ClassLeftover, worktreecheck.ClassRegisteredFinishedResidue:
		// These are the only classes owned by the explicit close surface.
	case worktreecheck.ClassRegisteredActive:
		return fmt.Errorf("worktree belongs to active session %s; run my session finish %s --land|--publish|--discard", entry.SessionID, entry.SessionID)
	case worktreecheck.ClassPrunable:
		return fmt.Errorf("worktree path is missing; inspect retained Git metadata before running git -C %s worktree prune", shellQuote(entry.RepoPath))
	case worktreecheck.ClassBase:
		return fmt.Errorf("refusing to close managed base checkout %s", entry.Path)
	default:
		return fmt.Errorf("worktree class %s cannot be closed", entry.Class)
	}
	if entry.Locked {
		return fmt.Errorf("worktree is locked; inspect why before running git -C %s worktree unlock %s", shellQuote(entry.RepoPath), shellQuote(entry.Path))
	}
	if !entry.Exists {
		return fmt.Errorf("worktree path is missing; no files or Git metadata were changed")
	}
	if entry.Detached || entry.Branch == "" {
		return fmt.Errorf("detached worktree cannot be closed safely; preserve HEAD first with git -C %s branch RECOVERY_BRANCH %s", shellQuote(entry.Path), shellQuote(entry.Head))
	}
	if len(entry.Dirty) != 0 {
		return fmt.Errorf("worktree has %d dirty or untracked path(s); inspect with git -C %s status --short", len(entry.Dirty), shellQuote(entry.Path))
	}
	if entry.InspectionError != "" {
		return fmt.Errorf("worktree could not be proven safe to close: %s", entry.InspectionError)
	}
	return nil
}

func (a app) inspectWorktreeLeftovers(opts workCommonOpts, includeManifest bool) (worktreecheck.Report, error) {
	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return worktreecheck.Report{}, err
	}
	manifestName := opts.manifestName
	if name, ok, err := defaultManifestNameIfAny(opts.home, manifestName, root); err != nil {
		return worktreecheck.Report{}, err
	} else if ok {
		manifestName = name
	}
	checkouts, err := worktreeCheckouts(opts.home, manifestName, root, includeManifest)
	if err != nil {
		return worktreecheck.Report{}, err
	}
	registrations, err := worktreeRegistrations(root)
	if err != nil {
		return worktreecheck.Report{}, err
	}
	return worktreecheck.Inspect(checkouts, registrations), nil
}

func worktreeCheckouts(home, manifestName, root string, includeManifest bool) ([]worktreecheck.Checkout, error) {
	var checkouts []worktreecheck.Checkout
	mounts, err := workspace.ListMounts(home, manifestName, root)
	if err != nil {
		return nil, err
	}
	for _, mount := range mounts {
		if isGitCheckout(mount.LocalPath) {
			checkouts = append(checkouts, worktreecheck.Checkout{ID: mount.ID, Kind: mount.Kind, Path: mount.LocalPath})
		}
	}
	repos, err := manifest.LoadRepoCatalog(home, manifestName)
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		path := umbrella.RepoPath(root, repo.ID)
		if isGitCheckout(path) {
			checkouts = append(checkouts, worktreecheck.Checkout{ID: repo.ID, Kind: "repo", Path: path})
		}
	}
	if includeManifest {
		doc, err := loadSingleRegisteredDoc(home, manifestName)
		if err != nil {
			return nil, err
		}
		if isGitCheckout(doc.ref.LocalPath) {
			checkouts = append(checkouts, worktreecheck.Checkout{ID: doc.ref.Name, Kind: "manifest", Path: doc.ref.LocalPath})
		}
	}
	return checkouts, nil
}

func worktreeRegistrations(root string) ([]worktreecheck.Registration, error) {
	sessions, err := worksession.List(root)
	if err != nil {
		return nil, err
	}
	var registrations []worktreecheck.Registration
	for _, session := range sessions {
		for _, mount := range session.Mounts {
			registrations = append(registrations, worktreecheck.Registration{
				SessionID: session.ID,
				Status:    session.Status,
				Path:      mount.WorktreePath,
			})
		}
	}
	return registrations, nil
}

func leftoverReportWithCommands(report worktreecheck.Report) leftoverCommandReport {
	result := leftoverCommandReport{Issues: report.Issues}
	for _, entry := range report.Entries {
		if entry.Class == worktreecheck.ClassBase {
			continue
		}
		result.Entries = append(result.Entries, leftoverCommandRow{Entry: entry, NextCommand: leftoverNextCommand(entry)})
	}
	return result
}

func leftoverNextCommand(entry worktreecheck.Entry) string {
	switch {
	case entry.RepoKind == "manifest":
		return "git -C " + shellQuote(entry.Path) + " status --short"
	case entry.Class == worktreecheck.ClassRegisteredActive:
		return "my session finish " + shellQuote(entry.SessionID) + " --land|--publish|--discard"
	case entry.Class == worktreecheck.ClassPrunable:
		return "git -C " + shellQuote(entry.RepoPath) + " worktree list --porcelain"
	case entry.Locked:
		return "git -C " + shellQuote(entry.RepoPath) + " worktree unlock " + shellQuote(entry.Path)
	case entry.Detached || entry.Branch == "":
		return "git -C " + shellQuote(entry.Path) + " branch RECOVERY_BRANCH " + shellQuote(entry.Head)
	case len(entry.Dirty) != 0 || entry.InspectionError != "":
		return "git -C " + shellQuote(entry.Path) + " status --short"
	default:
		return "my session close-worktree " + shellQuote(entry.Path) + " --yes"
	}
}

func (a app) printLeftoverReport(report leftoverCommandReport) {
	for _, issue := range report.Issues {
		fmt.Fprintf(a.stdout, "leftover\t%s\terror\t%s\t%s\n", issue.RepoID, issue.RepoPath, issue.Error)
	}
	if len(report.Entries) == 0 && len(report.Issues) == 0 {
		fmt.Fprintln(a.stdout, "no leftover worktrees")
		return
	}
	for _, row := range report.Entries {
		details := []string{"repo=" + row.RepoPath}
		if row.Branch != "" {
			details = append(details, "branch="+row.Branch)
		}
		if row.Detached {
			details = append(details, "detached=true")
		}
		if row.Locked {
			details = append(details, "locked=true")
		}
		if len(row.Dirty) != 0 {
			details = append(details, fmt.Sprintf("dirty=%d", len(row.Dirty)))
		}
		if row.Unlanded {
			details = append(details, "unlanded=true")
		}
		fmt.Fprintf(a.stdout, "leftover\t%s\t%s\t%s\t%s\n", row.RepoID, row.Class, row.Path, strings.Join(details, " "))
		if row.NextCommand != "" {
			fmt.Fprintf(a.stdout, "leftover\t%s\tnext\t%s\n", row.RepoID, row.NextCommand)
		}
	}
}

func doctorLeftoverItems(report leftoverCommandReport) []doctorItem {
	var items []doctorItem
	for _, issue := range report.Issues {
		items = append(items, doctorItem{Name: issue.RepoID, Status: "error", Path: issue.RepoPath, Message: issue.Error})
	}
	for _, row := range report.Entries {
		status := "warning"
		if row.Class == worktreecheck.ClassRegisteredActive {
			status = "active"
		}
		message := row.Class
		if row.NextCommand != "" {
			message += "; run " + row.NextCommand
		}
		details := []string{"repo=" + row.RepoPath}
		if row.Branch != "" {
			details = append(details, "branch="+row.Branch)
		}
		if row.Detached {
			details = append(details, "detached=true")
		}
		if row.Locked {
			details = append(details, "locked=true")
		}
		if len(row.Dirty) != 0 {
			details = append(details, fmt.Sprintf("dirty=%d", len(row.Dirty)))
		}
		if row.Unlanded {
			details = append(details, "unlanded=true")
		}
		items = append(items, doctorItem{
			Name: row.RepoID + ":" + filepath.Base(row.Path), Status: status,
			Path: row.Path, Message: message, Details: details,
		})
	}
	return items
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
	// A harness launch ensures the session's room itself. Every other form
	// ensures here, once, so its hints, guidance and report agree.
	var room sessionRoom
	if harnessName == "" || printOnly || opts.jsonOut {
		room = a.ensureSessionRoom(session)
		if room.Pin != "" {
			guidanceCtx.RoomPin = room.Pin
			if err := worksession.EnsureGuidance(session, guidanceCtx); err != nil {
				return a.maybeJSONError(opts.jsonOut, err)
			}
		}
	}
	report := sessionStartReport(session, h, harnessArgs)
	report.TTRoom = room.Pin
	if opts.jsonOut {
		printSessionRoom(a.stderr, room, false)
		return printJSON(a.stdout, report)
	}
	if printOnly {
		a.printSessionCreatedHint(a.stderr, session)
		printSessionRoom(a.stderr, room, harnessName != "")
		if harnessName == "" {
			fmt.Fprintf(a.stdout, "cd %s\n", shellQuote(session.Path))
			return nil
		}
		fmt.Fprintln(a.stdout, harnessCommandLine(session.Path, room, h.CommandName(), harnessArgs))
		return nil
	}
	if harnessName != "" {
		a.printSessionCreatedHint(a.stderr, session)
		fmt.Fprintf(a.stderr, "launching %s...\n", h.CommandName())
		return a.runLaunch(existingSessionLaunchArgs(opts, session.ID, harnessName, harnessArgs))
	}
	a.printSessionStarted(a.stdout, session)
	printSessionRoom(a.stderr, room, false)
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

A work session is a persistent workspace, not a harness chat: resuming it
starts a fresh conversation. Resume an earlier conversation with the harness's
own resume command (for example claude --resume <chat-id>).

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
	if harnessName == "" {
		doc, err := launchGuidanceDoc(opts.home, opts.manifestName, root)
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		guidanceCtx, err := sessionGuidanceContext(root, doc)
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		// Rewriting the guidance re-verifies the room it names, so a stale
		// room section never survives a failed ensure.
		room := a.ensureSessionRoom(session)
		guidanceCtx.RoomPin = room.Pin
		if err := worksession.EnsureGuidance(session, guidanceCtx); err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		printSessionRoom(a.stderr, room, false)
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
	sessionHolds, err := collectSessionHolds(root)
	if err != nil {
		return syncer.Report{}, err
	}
	publish, err := a.syncPushPublish(home, manifestName)
	if err != nil {
		return syncer.Report{}, err
	}
	report := syncer.Run(selected, syncer.Options{
		Backend:      "auto",
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
				PendingPaths:  append([]string(nil), mount.PendingPaths...),
				PathsKnown:    mount.PathsKnown,
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
