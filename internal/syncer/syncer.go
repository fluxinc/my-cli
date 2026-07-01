// Package syncer reconciles local My AI-managed Git repositories with remotes.
package syncer

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Runner executes external commands. Tests can replace it.
type Runner func(name string, args ...string) ([]byte, error)

// DirRunner executes external commands in a specific working directory.
type DirRunner func(dir, name string, args ...string) ([]byte, error)

// VisibilityFunc returns a repository visibility string such as PRIVATE.
type VisibilityFunc func(gitURL string) (string, error)

// Entry is one local repository My AI knows how to sync.
type Entry struct {
	Manifest     string   `json:"manifest,omitempty"`
	ID           string   `json:"id"`
	Role         string   `json:"role"`
	Kind         string   `json:"kind,omitempty"`
	GitURL       string   `json:"git_url"`
	LocalPath    string   `json:"local_path"`
	ContentPaths []string `json:"content_paths,omitempty"`
}

// SessionHold names one active work session with pending state on a mount
// repository; outbound publish of that repository is held until the session
// is finished or discarded.
type SessionHold struct {
	SessionID     string
	SessionPath   string
	MountID       string
	RepoPath      string
	DirtyCount    int
	UnlandedCount int
}

// Options controls a sync run.
type Options struct {
	Publish      string
	Backend      string
	GnitRoot     string
	DryRun       bool
	Message      string
	Runner       Runner
	DirRunner    DirRunner
	Visibility   VisibilityFunc
	SessionHolds []SessionHold
}

// InspectOptions controls report-only repository inspection.
type InspectOptions struct {
	Fetch  bool
	Runner Runner
}

// FastForwardOptions controls a single ff-only pull.
type FastForwardOptions struct {
	DryRun bool
	Runner Runner
}

// Report is a machine-readable sync report.
type Report struct {
	Publish        string   `json:"publish"`
	Backend        string   `json:"backend,omitempty"`
	GnitRoot       string   `json:"gnit_root,omitempty"`
	BackendMessage string   `json:"backend_message,omitempty"`
	DryRun         bool     `json:"dry_run,omitempty"`
	Results        []Result `json:"results"`
}

// Result describes one repository's sync state or action.
type Result struct {
	Manifest  string `json:"manifest,omitempty"`
	ID        string `json:"id"`
	Role      string `json:"role"`
	Kind      string `json:"kind,omitempty"`
	GitURL    string `json:"git_url"`
	LocalPath string `json:"local_path"`
	Branch    string `json:"branch,omitempty"`
	Head      string `json:"head,omitempty"`
	Status    string `json:"status"`
	Direction string `json:"direction,omitempty"`
	Ahead     int    `json:"ahead,omitempty"`
	Behind    int    `json:"behind,omitempty"`
	// BehindUnknown means the remote ref could not be refreshed, so Behind is
	// intentionally omitted instead of reporting a stale tracking-ref count.
	BehindUnknown bool     `json:"behind_unknown,omitempty"`
	FetchError    string   `json:"fetch_error,omitempty"`
	Dirty         []string `json:"dirty,omitempty"`
	Changed       []string `json:"changed,omitempty"`
	Message       string   `json:"message,omitempty"`
	ReasonCode    string   `json:"reason_code,omitempty"`
	NextCommand   string   `json:"next_command,omitempty"`
	Error         string   `json:"error,omitempty"`
}

type inspection struct {
	entry        Entry
	result       Result
	upstream     string
	remoteKey    string
	dirty        []string
	dirtyDetails []dirtyFile
	changed      []string
	contentOnly  bool
	private      bool
	visibilityOK bool
}

type dirtyFile struct {
	status string
	path   string
}

type inspectMode struct {
	fetch      bool
	fetchFatal bool
}

// Run inspects and optionally reconciles repositories.
func Run(entries []Entry, opts Options) Report {
	if opts.Backend == "gnit" {
		return runGnit(entries, opts)
	}
	if opts.Backend == "" || opts.Backend == "auto" {
		opts.Backend = "builtin"
	}
	return runBuiltin(entries, opts)
}

// Inspect returns current repository state without publishing or pulling.
func Inspect(entries []Entry, opts InspectOptions) []Result {
	runner := opts.Runner
	if runner == nil {
		runner = execCommand
	}
	inspections := make([]inspection, 0, len(entries))
	for _, entry := range entries {
		inspections = append(inspections, inspectWithMode(entry, Options{}, runner, inspectMode{
			fetch:      opts.Fetch,
			fetchFatal: false,
		}))
	}
	return collectResults(inspections)
}

// FastForward fetches and then pulls one clean, behind repository with --ff-only.
func FastForward(entry Entry, opts FastForwardOptions) Result {
	runner := opts.Runner
	if runner == nil {
		runner = execCommand
	}
	in := inspectWithMode(entry, Options{DryRun: opts.DryRun}, runner, inspectMode{
		fetch:      !opts.DryRun,
		fetchFatal: true,
	})
	if in.result.Status != "pending" {
		return in.result
	}
	reconcileInbound(&in, Options{DryRun: opts.DryRun}, runner)
	if in.result.Status == "pending" {
		hold(&in, "not eligible for fast-forward")
	}
	if in.result.Status == "pulled" {
		if head, _, err := gitTrim(runner, entry.LocalPath, "rev-parse", "HEAD"); err == nil {
			in.result.Head = head
		}
	}
	return in.result
}

func runBuiltin(entries []Entry, opts Options) Report {
	if opts.Publish == "" {
		opts.Publish = "auto"
	}
	if opts.Message == "" {
		opts.Message = "Sync My AI content"
	}
	runner := opts.Runner
	if runner == nil {
		runner = execCommand
	}

	inspections := make([]inspection, 0, len(entries))
	for _, entry := range entries {
		inspections = append(inspections, inspect(entry, opts, runner))
	}
	markDuplicatePending(inspections)
	for i := range inspections {
		reconcile(&inspections[i], inspections, opts, runner)
	}
	return Report{Publish: opts.Publish, Backend: "builtin", DryRun: opts.DryRun, Results: collectResults(inspections)}
}

func runGnit(entries []Entry, opts Options) Report {
	if opts.Publish == "" {
		opts.Publish = "auto"
	}
	if opts.Message == "" {
		opts.Message = "Sync My AI content"
	}
	runner := opts.Runner
	if runner == nil {
		runner = execCommand
	}
	report := Report{Publish: opts.Publish, Backend: "gnit", GnitRoot: opts.GnitRoot, DryRun: opts.DryRun}
	if opts.GnitRoot == "" || !hasGnitWorkspace(opts.GnitRoot) {
		report.BackendMessage = "umbrella is not a Gnit control workspace; use the built-in sync backend or initialize Gnit before forcing --backend gnit"
		report.Results = heldResults(entries, "umbrella is not a Gnit control workspace")
		return report
	}
	if opts.Publish == "pr" {
		report.BackendMessage = "PR mode belongs to My AI's gh policy layer; Gnit handles branch publishing, not PR creation"
		report.Results = heldResults(entries, "PR mode is not implemented yet")
		return report
	}

	inspections := make([]inspection, 0, len(entries))
	for _, entry := range entries {
		inspections = append(inspections, inspect(entry, opts, runner))
	}

	for i := range inspections {
		reconcileInbound(&inspections[i], opts, runner)
	}

	unsafeDuplicateRemotes := unsafeDuplicateRemoteReasons(inspections, opts.GnitRoot)
	if len(unsafeDuplicateRemotes) != 0 {
		for i := range inspections {
			reason, ok := unsafeDuplicateRemotes[inspections[i].remoteKey]
			if ok && inspections[i].result.Status == "pending" {
				hold(&inspections[i], reason)
			}
		}
		report.BackendMessage = "My AI must reconcile unsafe duplicate checkouts before delegating those remotes to Gnit"
	}

	var stagePaths []string
	var publishable []*inspection
	for i := range inspections {
		in := &inspections[i]
		if in.result.Status != "pending" {
			continue
		}
		if in.result.Behind > 0 {
			holdRemoteBehind(in)
			continue
		}
		if opts.Publish == "never" {
			hold(in, "publish disabled")
			continue
		}
		if opts.Publish == "auto" {
			if in.entry.Role != "content" {
				hold(in, "auto publish only applies to content mounts")
				continue
			}
			if !in.contentOnly {
				hold(in, "not content-only inside declared content paths")
				continue
			}
			if !in.visibilityOK || !in.private {
				hold(in, "repository privacy is not confirmed private")
				continue
			}
		}
		if !in.contentOnly && len(in.dirty) != 0 {
			hold(in, "dirty changes are outside declared content paths")
			continue
		}
		if holdUnadoptedContent(in) {
			continue
		}
		if !pathWithin(in.entry.LocalPath, opts.GnitRoot) {
			hold(in, "checkout is outside the Gnit workspace; canonicalize or adopt it before Gnit publish")
			continue
		}
		entryStagePaths := stagePathsWithin(in.dirty, in.entry.ContentPaths)
		if len(entryStagePaths) == 0 {
			entryStagePaths = in.dirty
		}
		for i, path := range entryStagePaths {
			rel, err := filepath.Rel(opts.GnitRoot, filepath.Join(in.entry.LocalPath, filepath.FromSlash(path)))
			if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "../") || filepath.IsAbs(rel) {
				hold(in, "dirty path is outside the Gnit workspace")
				continue
			}
			entryStagePaths[i] = filepath.ToSlash(rel)
		}
		if in.result.Status == "pending" {
			stagePaths = append(stagePaths, entryStagePaths...)
			publishable = append(publishable, in)
		}
	}
	stagePaths = unique(stagePaths)
	if len(publishable) == 0 {
		report.Results = collectResults(inspections)
		return report
	}
	if opts.DryRun {
		message := gnitDryRunMessage(len(stagePaths) != 0)
		for _, in := range publishable {
			in.result.Status = "dry-run"
			in.result.Direction = "outbound"
			in.result.Message = message
		}
		report.Results = collectResults(inspections)
		return report
	}

	out, err := runGnitPublish(opts, stagePaths)
	if err != nil {
		msg := commandError(out, err)
		for _, in := range publishable {
			in.result.Status = "failed"
			in.result.Direction = "outbound"
			in.result.Error = msg
		}
		report.Results = collectResults(inspections)
		return report
	}
	msg := strings.TrimSpace(string(out))
	for _, in := range publishable {
		in.result.Status = "pushed"
		in.result.Direction = "outbound"
		in.result.Message = "published by gnit"
		if msg != "" {
			in.result.Message += ": " + msg
		}
		pullCleanSiblings(in, inspections, runner)
	}
	report.Results = collectResults(inspections)
	return report
}

func inspect(entry Entry, opts Options, runner Runner) inspection {
	return inspectWithMode(entry, opts, runner, inspectMode{fetch: !opts.DryRun, fetchFatal: true})
}

func inspectWithMode(entry Entry, opts Options, runner Runner, mode inspectMode) inspection {
	res := Result{
		Manifest:  entry.Manifest,
		ID:        entry.ID,
		Role:      entry.Role,
		Kind:      entry.Kind,
		GitURL:    entry.GitURL,
		LocalPath: entry.LocalPath,
	}
	in := inspection{entry: entry, result: res, remoteKey: normalizeRemote(entry.GitURL)}
	if !isGitRepo(entry.LocalPath, runner) {
		hold(&in, "not cloned; run my mounts sync or my setup first")
		return in
	}
	if _, err := git(runner, entry.LocalPath, "remote", "get-url", "origin"); err != nil {
		in.result.Status = "local-only"
		in.result.Message = "no origin remote configured; nothing to pull or push until the repository is published (run my publish)"
		return in
	}
	fetchFailed := false
	if mode.fetch {
		if out, err := git(runner, entry.LocalPath, "fetch", "origin"); err != nil {
			msg := commandError(out, err)
			if mode.fetchFatal {
				in.result.Status = "failed"
				in.result.Error = msg
				return in
			}
			fetchFailed = true
			in.result.BehindUnknown = true
			in.result.FetchError = msg
		}
	}
	branch, out, err := gitTrim(runner, entry.LocalPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		in.result.Status = "failed"
		in.result.Error = commandError(out, err)
		return in
	}
	in.result.Branch = branch
	head, out, err := gitTrim(runner, entry.LocalPath, "rev-parse", "HEAD")
	if err != nil {
		in.result.Status = "failed"
		in.result.Error = commandError(out, err)
		return in
	}
	in.result.Head = head
	if branch == "HEAD" {
		hold(&in, "detached HEAD")
		return in
	}
	upstream, _, err := gitTrim(runner, entry.LocalPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil || upstream == "" {
		upstream = "origin/" + branch
	}
	in.upstream = upstream
	behind, ahead, err := aheadBehind(entry.LocalPath, upstream, runner)
	if err != nil {
		if fetchFailed {
			in.result.Status = "unknown"
			in.result.Message = "remote freshness unknown"
			return in
		}
		in.result.Status = "failed"
		in.result.Error = err.Error()
		return in
	}
	if !fetchFailed {
		in.result.Behind = behind
	}
	in.result.Ahead = ahead
	dirty, err := dirtyFiles(entry.LocalPath, runner)
	if err != nil {
		in.result.Status = "failed"
		in.result.Error = err.Error()
		return in
	}
	in.dirtyDetails = dirty
	in.dirty = dirtyFilePaths(dirty)
	in.result.Dirty = in.dirty
	if ahead > 0 {
		changed, err := changedFiles(entry.LocalPath, upstream, runner)
		if err != nil {
			in.result.Status = "failed"
			in.result.Error = err.Error()
			return in
		}
		in.changed = changed
		in.result.Changed = changed
	}
	allChanged := unique(append(append([]string{}, in.dirty...), in.changed...))
	in.contentOnly = len(allChanged) != 0 && pathsWithin(allChanged, entry.ContentPaths)
	if opts.Visibility != nil {
		visibility, err := opts.Visibility(entry.GitURL)
		if err == nil {
			in.visibilityOK = true
			in.private = strings.EqualFold(visibility, "PRIVATE")
		}
	}
	if fetchFailed {
		if ahead != 0 || len(dirty) != 0 {
			in.result.Status = "pending"
			return in
		}
		in.result.Status = "unknown"
		in.result.Message = "remote freshness unknown"
		return in
	}
	if behind == 0 && ahead == 0 && len(dirty) == 0 {
		in.result.Status = "already landed"
		return in
	}
	in.result.Status = "pending"
	return in
}

func reconcile(in *inspection, all []inspection, opts Options, runner Runner) {
	if in.result.Status != "pending" {
		return
	}
	if reconcileInbound(in, opts, runner) {
		return
	}
	if in.result.Behind > 0 {
		holdRemoteBehind(in)
		return
	}
	if opts.Publish == "never" {
		hold(in, "publish disabled")
		return
	}
	if opts.Publish == "pr" {
		hold(in, "PR mode is not implemented yet; use --print or --publish direct")
		return
	}
	if hasDuplicatePendingSibling(in, all) {
		hold(in, "another checkout of the same remote has pending changes")
		return
	}
	if holdActiveSession(in, opts) {
		return
	}
	if opts.Publish == "auto" {
		if in.entry.Role != "content" {
			hold(in, "auto publish only applies to content mounts")
			return
		}
		if !in.contentOnly {
			hold(in, "not content-only inside declared content paths")
			return
		}
		if !in.visibilityOK || !in.private {
			hold(in, "repository privacy is not confirmed private")
			return
		}
	}
	if !in.contentOnly && len(in.dirty) != 0 {
		hold(in, "dirty changes are outside declared content paths")
		return
	}
	if holdUnadoptedContent(in) {
		return
	}
	if opts.DryRun {
		in.result.Status = "dry-run"
		in.result.Direction = "outbound"
		if len(in.dirty) != 0 {
			in.result.Message = "would commit and push"
		} else {
			in.result.Message = "would push"
		}
		return
	}
	if len(in.dirty) != 0 {
		stagePaths := stagePathsWithin(in.dirty, in.entry.ContentPaths)
		if len(stagePaths) == 0 {
			stagePaths = in.dirty
		}
		if out, err := gitAdd(runner, in.entry.LocalPath, stagePaths); err != nil {
			in.result.Status = "failed"
			in.result.Error = commandError(out, err)
			return
		}
		out, err := git(runner, in.entry.LocalPath, "commit", "-m", opts.Message)
		if err != nil {
			in.result.Status = "failed"
			in.result.Error = commandError(out, err)
			return
		}
		in.result.Message = strings.TrimSpace(string(out))
	}
	out, err := git(runner, in.entry.LocalPath, "push", "origin", "HEAD:"+in.result.Branch)
	if err != nil {
		in.result.Status = "failed"
		in.result.Error = commandError(out, err)
		return
	}
	in.result.Status = "pushed"
	in.result.Direction = "outbound"
	if msg := strings.TrimSpace(string(out)); msg != "" {
		if in.result.Message != "" {
			in.result.Message += "\n" + msg
		} else {
			in.result.Message = msg
		}
	}
	pullCleanSiblings(in, all, runner)
}

func reconcileInbound(in *inspection, opts Options, runner Runner) bool {
	if in.result.Status != "pending" {
		return false
	}
	if in.result.Behind == 0 || in.result.Ahead != 0 || len(in.dirty) != 0 {
		return false
	}
	if opts.DryRun {
		in.result.Status = "dry-run"
		in.result.Direction = "inbound"
		in.result.Message = "would pull --ff-only"
		return true
	}
	out, err := git(runner, in.entry.LocalPath, "pull", "--ff-only")
	if err != nil {
		in.result.Status = "failed"
		in.result.Error = commandError(out, err)
		return true
	}
	in.result.Status = "pulled"
	in.result.Direction = "inbound"
	in.result.Message = strings.TrimSpace(string(out))
	return true
}

func hold(in *inspection, reason string) {
	in.result.Status = "held back"
	in.result.Message = reason
	in.result.ReasonCode = holdReasonCode(reason)
	in.result.NextCommand = holdNextCommand(*in, in.result.ReasonCode)
}

func holdRemoteBehind(in *inspection) {
	if in.result.Ahead != 0 && in.result.Behind != 0 {
		hold(in, fmt.Sprintf("local and remote both have commits (ahead %d, behind %d); run my doctor and reconcile divergent history before publishing", in.result.Ahead, in.result.Behind))
		return
	}
	if len(in.dirty) != 0 {
		hold(in, fmt.Sprintf("remote has new commits and checkout has uncommitted files (%s); commit, stash, or discard local files, then run my sync", strings.Join(in.dirty, ", ")))
		return
	}
	hold(in, "remote has new commits; pull or rebase before publishing")
}

func holdReasonCode(reason string) string {
	switch {
	case strings.HasPrefix(reason, "active session "):
		return "active_session"
	case strings.HasPrefix(reason, "unadopted untracked content"):
		return "unadopted_content"
	case strings.Contains(reason, "same remote"):
		return "duplicate_remote"
	case strings.Contains(reason, "outside declared content paths"):
		return "outside_content_paths"
	case reason == "auto publish only applies to content mounts":
		return "auto_non_content"
	case reason == "not content-only inside declared content paths":
		return "not_content_only"
	case reason == "repository privacy is not confirmed private":
		return "privacy_unconfirmed"
	case strings.HasPrefix(reason, "local and remote both have commits"):
		return "diverged"
	case strings.HasPrefix(reason, "remote has new commits and checkout has uncommitted files"):
		return "dirty_behind"
	case reason == "remote has new commits; pull or rebase before publishing":
		return "remote_ahead"
	case reason == "publish disabled":
		return "publish_disabled"
	case strings.HasPrefix(reason, "PR mode"):
		return "pr_mode_unimplemented"
	case reason == "detached HEAD":
		return "detached_head"
	case reason == "not cloned; run my mounts sync or my setup first":
		return "not_cloned"
	case reason == "not eligible for fast-forward":
		return "not_fast_forward"
	case strings.Contains(reason, "Gnit control workspace"):
		return "gnit_not_control_workspace"
	case strings.Contains(reason, "outside the Gnit workspace"):
		return "outside_gnit_workspace"
	default:
		return "held_back"
	}
}

func holdNextCommand(in inspection, reasonCode string) string {
	switch reasonCode {
	case "unadopted_content":
		paths := unadoptedContentPaths(&in)
		if len(paths) == 1 {
			return "my record adopt " + shellArg(paths[0])
		}
		return "my record adopt <path>"
	case "auto_non_content":
		if in.entry.Role == "manifest" && in.entry.Manifest != "" {
			return "my publish --manifest " + shellArg(in.entry.Manifest)
		}
		return "my sync --publish direct --print"
	case "publish_disabled":
		return "my sync --push --print"
	case "remote_ahead":
		return "my sync"
	case "dirty_behind":
		if in.entry.LocalPath != "" {
			return "git -C " + shellArg(in.entry.LocalPath) + " status --short"
		}
		return "my doctor"
	case "diverged":
		return "my doctor"
	case "not_cloned":
		return "my setup"
	case "privacy_unconfirmed":
		// Steer toward inspecting/confirming the remote is private, not toward
		// `--publish direct`, which skips the private-repo visibility gate that
		// only runs under Publish==auto.
		return "my doctor"
	case "detached_head", "duplicate_remote", "outside_content_paths", "not_content_only", "not_fast_forward", "outside_gnit_workspace":
		return "my doctor"
	case "gnit_not_control_workspace":
		return "my sync --backend builtin --push --print"
	case "pr_mode_unimplemented":
		return "my sync --publish direct --print"
	default:
		return ""
	}
}

// holdActiveSession holds outbound publish of a repository while an active
// work session on it has dirty files or unlanded commits. Inbound pulls are
// unaffected: session branches do not move when the base branch fast-forwards.
func holdActiveSession(in *inspection, opts Options) bool {
	for _, sessionHold := range opts.SessionHolds {
		if !samePath(sessionHold.RepoPath, in.entry.LocalPath) {
			continue
		}
		hold(in, sessionHoldMessage(sessionHold, in.dirty))
		in.result.NextCommand = sessionHoldNextCommand(sessionHold, in.entry.LocalPath, in.dirty)
		return true
	}
	return false
}

// sessionHoldMessage explains why an active session holds this mount's publish
// and what to run next. baseDirty is the base checkout's own uncommitted files
// (the same files `my session finish --land` and `--publish` refuse on via
// requireBaseReady); when it is non-empty we sequence the guidance so the
// operator resolves the base first instead of being bounced into a finish that
// will refuse (#28).
func sessionHoldMessage(sessionHold SessionHold, baseDirty []string) string {
	var pending []string
	if sessionHold.DirtyCount > 0 {
		pending = append(pending, fmt.Sprintf("%d dirty file(s)", sessionHold.DirtyCount))
	}
	if sessionHold.UnlandedCount > 0 {
		pending = append(pending, fmt.Sprintf("%d unlanded commit(s)", sessionHold.UnlandedCount))
	}
	detail := strings.Join(pending, " and ")
	if detail == "" {
		detail = "pending work"
	}
	location := sessionHold.SessionID
	if sessionHold.SessionPath != "" {
		location += " (" + sessionHold.SessionPath + ")"
	}
	if len(baseDirty) != 0 {
		return fmt.Sprintf(
			"active session %s has %s on mount %s, and the base checkout has uncommitted files (%s) that will block --land/--publish; commit, stash, or discard those base files first, then run my session finish %s --land (or --publish). To throw away the session, run my session finish %s --discard; use my session status to inspect",
			location, detail, sessionHold.MountID, strings.Join(baseDirty, ", "), sessionHold.SessionID, sessionHold.SessionID,
		)
	}
	return fmt.Sprintf(
		"active session %s has %s on mount %s; run my session finish %s --land|--publish (or --discard), or my session status to inspect",
		location, detail, sessionHold.MountID, sessionHold.SessionID,
	)
}

func sessionHoldNextCommand(sessionHold SessionHold, repoPath string, baseDirty []string) string {
	if len(baseDirty) != 0 {
		if repoPath != "" {
			return "git -C " + shellArg(repoPath) + " status --short"
		}
		return "my session status"
	}
	return "my session finish " + shellArg(sessionHold.SessionID) + " --land"
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return absA == absB
}

func holdUnadoptedContent(in *inspection) bool {
	if in.entry.Role != "content" {
		return false
	}
	paths := unadoptedContentPaths(in)
	if len(paths) == 0 {
		return false
	}
	hold(in, unadoptedContentMessage(paths))
	return true
}

func unadoptedContentPaths(in *inspection) []string {
	var paths []string
	for _, file := range in.dirtyDetails {
		if file.status == "??" && pathsWithin([]string{file.path}, in.entry.ContentPaths) {
			paths = append(paths, file.path)
		}
	}
	return unique(paths)
}

func unadoptedContentMessage(paths []string) string {
	if len(paths) == 1 {
		return fmt.Sprintf("unadopted untracked content file %s; run my record adopt %s", paths[0], paths[0])
	}
	return fmt.Sprintf("unadopted untracked content files: %s; run my record adopt <path> for each file to publish", strings.Join(paths, ", "))
}

func markDuplicatePending(inspections []inspection) {
	pending := map[string][]string{}
	for _, in := range inspections {
		if in.remoteKey == "" || in.result.Status == "failed" {
			continue
		}
		if in.result.Ahead != 0 || len(in.dirty) != 0 {
			pending[in.remoteKey] = append(pending[in.remoteKey], in.entry.ID)
		}
	}
	for i := range inspections {
		ids := pending[inspections[i].remoteKey]
		if len(ids) > 1 && inspections[i].result.Status == "pending" {
			inspections[i].result.Message = "same remote pending: " + strings.Join(ids, ", ")
		}
	}
}

func hasDuplicatePendingSibling(in *inspection, all []inspection) bool {
	for i := range all {
		other := &all[i]
		if other.entry.LocalPath == in.entry.LocalPath || other.remoteKey == "" || other.remoteKey != in.remoteKey {
			continue
		}
		if other.result.Ahead != 0 || len(other.dirty) != 0 {
			return true
		}
	}
	return false
}

func unsafeDuplicateRemoteReasons(inspections []inspection, gnitRoot string) map[string]string {
	groups := map[string][]int{}
	for i, in := range inspections {
		if in.remoteKey == "" {
			continue
		}
		groups[in.remoteKey] = append(groups[in.remoteKey], i)
	}
	out := map[string]string{}
	for remote, indexes := range groups {
		if len(indexes) < 2 {
			continue
		}
		inGnit := 0
		for _, idx := range indexes {
			if pathWithin(inspections[idx].entry.LocalPath, gnitRoot) {
				inGnit++
			}
		}
		switch {
		case inGnit > 1:
			out[remote] = "same remote has multiple Gnit workspace checkouts; collapse to one canonical checkout before Gnit publish"
			continue
		case inGnit == 0:
			out[remote] = "same remote has multiple checkouts but no canonical Gnit workspace checkout"
			continue
		}
		for _, idx := range indexes {
			in := inspections[idx]
			if pathWithin(in.entry.LocalPath, gnitRoot) {
				continue
			}
			switch {
			case in.result.Status == "failed":
				out[remote] = "same remote sibling failed sync inspection"
			case in.result.Ahead != 0 || len(in.dirty) != 0:
				out[remote] = "same remote sibling has pending changes; reconcile or move them into the canonical checkout before Gnit publish"
			}
			if out[remote] != "" {
				break
			}
		}
	}
	return out
}

func pullCleanSiblings(in *inspection, all []inspection, runner Runner) {
	for i := range all {
		other := &all[i]
		if other.entry.LocalPath == in.entry.LocalPath || other.remoteKey == "" || other.remoteKey != in.remoteKey {
			continue
		}
		if other.result.Status != "already landed" || len(other.dirty) != 0 || other.result.Ahead != 0 {
			continue
		}
		out, err := git(runner, other.entry.LocalPath, "pull", "--ff-only")
		if err != nil {
			other.result.Status = "failed"
			other.result.Error = commandError(out, err)
			continue
		}
		other.result.Status = "pulled"
		other.result.Direction = "inbound"
		other.result.Message = strings.TrimSpace(string(out))
	}
}

func collectResults(inspections []inspection) []Result {
	results := make([]Result, 0, len(inspections))
	for _, in := range inspections {
		results = append(results, in.result)
	}
	return results
}

func heldResults(entries []Entry, reason string) []Result {
	results := make([]Result, 0, len(entries))
	for _, entry := range entries {
		results = append(results, Result{
			Manifest:    entry.Manifest,
			ID:          entry.ID,
			Role:        entry.Role,
			Kind:        entry.Kind,
			GitURL:      entry.GitURL,
			LocalPath:   entry.LocalPath,
			Status:      "held back",
			Message:     reason,
			ReasonCode:  holdReasonCode(reason),
			NextCommand: holdNextCommand(inspection{entry: entry}, holdReasonCode(reason)),
		})
	}
	return results
}

func shellArg(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			strings.ContainsRune("_+-./:=@", r) {
			continue
		}
		return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
	}
	return value
}

func aheadBehind(repo, upstream string, runner Runner) (int, int, error) {
	out, err := git(runner, repo, "rev-list", "--left-right", "--count", upstream+"...HEAD")
	if err != nil {
		return 0, 0, fmt.Errorf("compare upstream %s: %s", upstream, commandError(out, err))
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list output: %q", strings.TrimSpace(string(out)))
	}
	behind, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, err
	}
	ahead, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return behind, ahead, nil
}

func dirtyFiles(repo string, runner Runner) ([]dirtyFile, error) {
	out, err := git(runner, repo, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("status: %s", commandError(out, err))
	}
	return parseStatusFiles(string(out)), nil
}

func changedFiles(repo, upstream string, runner Runner) ([]string, error) {
	out, err := git(runner, repo, "diff", "--name-only", upstream+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("diff %s..HEAD: %s", upstream, commandError(out, err))
	}
	return nonemptyLines(string(out)), nil
}

func parseStatusFiles(text string) []dirtyFile {
	var files []dirtyFile
	seen := map[string]bool{}
	parts := strings.Split(text, "\x00")
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if len(part) < 4 {
			continue
		}
		status := part[:2]
		path := part[3:]
		if path == "" {
			continue
		}
		path = filepath.ToSlash(path)
		if !seen[path] {
			files = append(files, dirtyFile{status: status, path: path})
			seen[path] = true
		}
		if status[0] == 'R' || status[0] == 'C' || status[1] == 'R' || status[1] == 'C' {
			i++
			if i < len(parts) && (status[0] == 'R' || status[1] == 'R') {
				oldPath := filepath.ToSlash(parts[i])
				if oldPath != "" && !seen[oldPath] {
					files = append(files, dirtyFile{status: status, path: oldPath})
					seen[oldPath] = true
				}
			}
		}
	}
	return files
}

func dirtyFilePaths(files []dirtyFile) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.path)
	}
	return unique(paths)
}

func nonemptyLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, filepath.ToSlash(line))
		}
	}
	return unique(out)
}

func pathsWithin(paths, prefixes []string) bool {
	if len(paths) == 0 || len(prefixes) == 0 {
		return false
	}
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimPrefix(path, "./"))
		ok := false
		for _, prefix := range prefixes {
			prefix = strings.Trim(filepath.ToSlash(prefix), "/")
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func stagePathsWithin(paths, prefixes []string) []string {
	if len(paths) == 0 || len(prefixes) == 0 {
		return nil
	}
	var out []string
	for _, path := range paths {
		path = filepath.ToSlash(strings.TrimPrefix(path, "./"))
		for _, prefix := range prefixes {
			prefix = strings.Trim(filepath.ToSlash(prefix), "/")
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				out = append(out, prefix)
				break
			}
		}
	}
	return unique(out)
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func isGitRepo(path string, runner Runner) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	_, err := git(runner, path, "rev-parse", "--is-inside-work-tree")
	return err == nil
}

func gitAdd(runner Runner, repo string, files []string) ([]byte, error) {
	args := []string{"-C", repo, "add", "-A", "--"}
	args = append(args, files...)
	return runner("git", args...)
}

func git(runner Runner, repo string, args ...string) ([]byte, error) {
	full := append([]string{"-C", repo}, args...)
	return runner("git", full...)
}

func gitTrim(runner Runner, repo string, args ...string) (string, []byte, error) {
	out, err := git(runner, repo, args...)
	return strings.TrimSpace(string(out)), out, err
}

func commandError(out []byte, err error) string {
	msg := strings.TrimSpace(string(out))
	if msg != "" {
		return msg
	}
	return err.Error()
}

func normalizeRemote(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimSuffix(value, "/")
	return value
}

func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func execCommandInDir(dir, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func hasGnitWorkspace(root string) bool {
	if root == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(root, ".gnit", "roster.yaml"))
	return err == nil
}

func duplicateRemoteKeys(inspections []inspection) map[string]bool {
	pathsByRemote := map[string]map[string]bool{}
	for _, in := range inspections {
		if in.remoteKey == "" || in.result.Status == "failed" {
			continue
		}
		if pathsByRemote[in.remoteKey] == nil {
			pathsByRemote[in.remoteKey] = map[string]bool{}
		}
		pathsByRemote[in.remoteKey][in.entry.LocalPath] = true
	}
	out := map[string]bool{}
	for remote, paths := range pathsByRemote {
		if len(paths) > 1 {
			out[remote] = true
		}
	}
	return out
}

func gnitDryRunMessage(hasDirty bool) string {
	var steps []string
	if hasDirty {
		steps = append(steps, "would run gnit add for My AI-approved content paths")
		steps = append(steps, "would run gnit commit -m")
	}
	steps = append(steps, "would run gnit push")
	return strings.Join(steps, "; ")
}

func runGnitPublish(opts Options, stagePaths []string) ([]byte, error) {
	dirRunner := opts.DirRunner
	if dirRunner == nil {
		dirRunner = execCommandInDir
	}
	var combined []byte
	run := func(args ...string) error {
		out, err := dirRunner(opts.GnitRoot, "gnit", args...)
		combined = append(combined, out...)
		return err
	}
	if len(stagePaths) != 0 {
		args := append([]string{"add"}, stagePaths...)
		if err := run(args...); err != nil {
			return combined, err
		}
		if err := run("commit", "-m", opts.Message); err != nil {
			return combined, err
		}
	}
	if err := run("push"); err != nil {
		return combined, err
	}
	return combined, nil
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (!strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel))
}
