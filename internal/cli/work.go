package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

func (a app) runWork(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: my work start|status|list|resume|finish [flags]")
	}
	switch args[0] {
	case "start":
		return a.runWorkStart(args[1:])
	case "status", "list":
		return a.runWorkStatus(args[1:], args[0])
	case "resume":
		return a.runWorkResume(args[1:])
	case "finish":
		return a.runWorkFinish(args[1:])
	default:
		return fmt.Errorf("unknown work subcommand %q (expected start|status|list|resume|finish)", args[0])
	}
}

func (a app) runWorkStart(args []string) error {
	var opts workCommonOpts
	var slug string
	fs := newFlagSet("my work start", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.StringVar(&slug, "slug", "", "short session slug (lowercase, digits, hyphens)")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my work start [--slug SLUG] [--json]")
	}

	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
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
	if opts.jsonOut {
		return printJSON(a.stdout, session)
	}
	fmt.Fprintf(a.stdout, "started session %s\n", session.ID)
	fmt.Fprintf(a.stdout, "  path: %s\n", session.Path)
	for _, m := range session.Mounts {
		fmt.Fprintf(a.stdout, "  %s -> %s (from %s)\n", m.ID, m.Branch, m.BaseBranch)
	}
	fmt.Fprintf(a.stdout, "finish with: my work finish %s --land | --publish | --discard\n", session.ID)
	return nil
}

func (a app) runWorkStatus(args []string, command string) error {
	var opts workCommonOpts
	var all bool
	fs := newFlagSet("my work "+command, a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.BoolVar(&all, "all", false, "include finished and discarded sessions")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my work %s [--all] [--json]", command)
	}

	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
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

func (a app) runWorkResume(args []string) error {
	var opts workCommonOpts
	fs := newFlagSet("my work resume", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, `Usage of my work resume:
  my work resume [session-id] [--json]

Print a shell cd command for an active work session. This command does not
change the parent shell by itself. To launch a harness in the session, use:

  my ai -r [session-id] [harness]

Options:`)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return fmt.Errorf("usage: my work resume [session-id] [--json]")
	}
	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	sessionID, err := selectWorkSessionID(root, rest)
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
	fmt.Fprintf(a.stdout, "cd %s\n", shellQuote(session.Path))
	return nil
}

type workFinishCommandReport struct {
	Mode   string                   `json:"mode"`
	Finish worksession.FinishResult `json:"finish"`
	Sync   *syncer.Report           `json:"sync,omitempty"`
}

func (a app) runWorkFinish(args []string) error {
	var opts workCommonOpts
	var land bool
	var publish bool
	var discard bool
	var message string
	fs := newFlagSet("my work finish", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.BoolVar(&land, "land", false, "merge the session into the base checkouts")
	fs.BoolVar(&publish, "publish", false, "land the session and publish landed content")
	fs.BoolVar(&discard, "discard", false, "discard the session worktrees and branches")
	fs.StringVar(&message, "message", "", "commit message for dirty session content")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return fmt.Errorf("usage: my work finish [session-id] --land|--publish|--discard")
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
			a.printWorkFinishReport(report)
		}
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		return nil
	}

	if opts.jsonOut {
		return printJSON(a.stdout, report)
	}
	a.printWorkFinishReport(report)
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
	report := syncer.Run(selected, syncer.Options{
		Backend:      backend,
		GnitRoot:     gnitRoot,
		Publish:      "auto",
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

func (a app) printWorkFinishReport(report workFinishCommandReport) {
	session := report.Finish.Session
	fmt.Fprintf(a.stdout, "session\t%s\t%s", session.ID, session.Status)
	if session.Outcome != "" {
		fmt.Fprintf(a.stdout, "\t%s", session.Outcome)
	}
	fmt.Fprintln(a.stdout)
	for _, mount := range report.Finish.Mounts {
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
		a.printSyncReport(*report.Sync)
	}
	if label, command := workFinishNextStep(report); command != "" {
		fmt.Fprintf(a.stdout, "next\t%s\t%s\n", label, command)
	}
}

func workFinishNextStep(report workFinishCommandReport) (string, string) {
	switch report.Mode {
	case "land":
		return "publish", "my sync"
	case "publish":
		if report.Finish.Session.Outcome == worksession.OutcomePublished {
			return "status", "my work status"
		}
		return "review", "my sync --print"
	case "discard":
		return "status", "my work status"
	default:
		return "", ""
	}
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
	return items
}

func doctorSessionItem(session worksession.Session) doctorItem {
	item := doctorItem{Name: session.ID, Path: session.Path}
	for _, mount := range session.Mounts {
		if _, err := os.Stat(mount.WorktreePath); err != nil {
			item.Status = "error"
			item.Message = "worktree missing for mount " + mount.ID
			item.Details = append(item.Details, "discard the session record with: my work finish "+session.ID+" --discard")
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
	item.Message = fmt.Sprintf("active: %d dirty, %d unlanded; finish with: my work finish %s --land", dirty, unlanded, session.ID)
	return item
}
