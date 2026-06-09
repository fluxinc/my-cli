// Package syncer reconciles local Flux-managed Git repositories with remotes.
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

// Entry is one local repository Flux knows how to sync.
type Entry struct {
	Manifest     string   `json:"manifest,omitempty"`
	ID           string   `json:"id"`
	Role         string   `json:"role"`
	Kind         string   `json:"kind,omitempty"`
	GitURL       string   `json:"git_url"`
	LocalPath    string   `json:"local_path"`
	ContentPaths []string `json:"content_paths,omitempty"`
}

// Options controls a sync run.
type Options struct {
	Publish    string
	Backend    string
	NitRoot    string
	DryRun     bool
	Message    string
	Runner     Runner
	DirRunner  DirRunner
	Visibility VisibilityFunc
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
	NitRoot        string   `json:"nit_root,omitempty"`
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
	Error         string   `json:"error,omitempty"`
}

type inspection struct {
	entry        Entry
	result       Result
	upstream     string
	remoteKey    string
	dirty        []string
	changed      []string
	contentOnly  bool
	private      bool
	visibilityOK bool
}

type inspectMode struct {
	fetch      bool
	fetchFatal bool
}

// Run inspects and optionally reconciles repositories.
func Run(entries []Entry, opts Options) Report {
	if opts.Backend == "nit" {
		return runNit(entries, opts)
	}
	if opts.Backend == "" || opts.Backend == "auto" {
		opts.Backend = "flux"
	}
	return runFlux(entries, opts)
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

func runFlux(entries []Entry, opts Options) Report {
	if opts.Publish == "" {
		opts.Publish = "auto"
	}
	if opts.Message == "" {
		opts.Message = "Sync Flux content"
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
	return Report{Publish: opts.Publish, Backend: "flux", DryRun: opts.DryRun, Results: collectResults(inspections)}
}

func runNit(entries []Entry, opts Options) Report {
	if opts.Publish == "" {
		opts.Publish = "auto"
	}
	if opts.Message == "" {
		opts.Message = "Sync Flux content"
	}
	runner := opts.Runner
	if runner == nil {
		runner = execCommand
	}
	report := Report{Publish: opts.Publish, Backend: "nit", NitRoot: opts.NitRoot, DryRun: opts.DryRun}
	if opts.NitRoot == "" || !hasNitWorkspace(opts.NitRoot) {
		report.BackendMessage = "Nit workspace not initialized; run nit init --control for the Flux umbrella before using the Nit backend"
		report.Results = heldResults(entries, "Nit workspace not initialized")
		return report
	}
	if opts.Publish == "pr" {
		report.BackendMessage = "PR mode belongs to Flux's gh policy layer; Nit handles branch publishing, not PR creation"
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

	unsafeDuplicateRemotes := unsafeDuplicateRemoteReasons(inspections, opts.NitRoot)
	if len(unsafeDuplicateRemotes) != 0 {
		for i := range inspections {
			reason, ok := unsafeDuplicateRemotes[inspections[i].remoteKey]
			if ok && inspections[i].result.Status == "pending" {
				hold(&inspections[i], reason)
			}
		}
		report.BackendMessage = "Flux must reconcile unsafe duplicate checkouts before delegating those remotes to Nit"
	}

	var stagePaths []string
	var publishable []*inspection
	for i := range inspections {
		in := &inspections[i]
		if in.result.Status != "pending" {
			continue
		}
		if in.result.Behind > 0 {
			hold(in, "remote has new commits; pull or rebase before publishing")
			continue
		}
		if opts.Publish == "never" {
			hold(in, "publish disabled")
			continue
		}
		if opts.Publish == "auto" {
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
		if !pathWithin(in.entry.LocalPath, opts.NitRoot) {
			hold(in, "checkout is outside the Nit workspace; canonicalize or adopt it before Nit publish")
			continue
		}
		var entryStagePaths []string
		for _, path := range in.dirty {
			rel, err := filepath.Rel(opts.NitRoot, filepath.Join(in.entry.LocalPath, filepath.FromSlash(path)))
			if err != nil || strings.HasPrefix(filepath.ToSlash(rel), "../") || filepath.IsAbs(rel) {
				hold(in, "dirty path is outside the Nit workspace")
				continue
			}
			entryStagePaths = append(entryStagePaths, filepath.ToSlash(rel))
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
		message := nitDryRunMessage(len(stagePaths) != 0)
		for _, in := range publishable {
			in.result.Status = "dry-run"
			in.result.Direction = "outbound"
			in.result.Message = message
		}
		report.Results = collectResults(inspections)
		return report
	}

	out, err := runNitPublish(opts, stagePaths)
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
		in.result.Message = "published by nit"
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
		in.result.Status = "held back"
		in.result.Message = "not cloned; run flux mount sync or flux onboard first"
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
		in.result.Status = "held back"
		in.result.Message = "detached HEAD"
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
	in.dirty = dirty
	in.result.Dirty = dirty
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
	allChanged := unique(append(append([]string{}, dirty...), in.changed...))
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
		hold(in, "remote has new commits; pull or rebase before publishing")
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
	if opts.Publish == "auto" {
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
		if out, err := gitAdd(runner, in.entry.LocalPath, in.dirty); err != nil {
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

func unsafeDuplicateRemoteReasons(inspections []inspection, nitRoot string) map[string]string {
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
		inNit := 0
		for _, idx := range indexes {
			if pathWithin(inspections[idx].entry.LocalPath, nitRoot) {
				inNit++
			}
		}
		switch {
		case inNit > 1:
			out[remote] = "same remote has multiple Nit workspace checkouts; collapse to one canonical checkout before Nit publish"
			continue
		case inNit == 0:
			out[remote] = "same remote has multiple checkouts but no canonical Nit workspace checkout"
			continue
		}
		for _, idx := range indexes {
			in := inspections[idx]
			if pathWithin(in.entry.LocalPath, nitRoot) {
				continue
			}
			switch {
			case in.result.Status == "failed":
				out[remote] = "same remote sibling failed sync inspection"
			case in.result.Ahead != 0 || len(in.dirty) != 0:
				out[remote] = "same remote sibling has pending changes; reconcile or move them into the canonical checkout before Nit publish"
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
			Manifest:  entry.Manifest,
			ID:        entry.ID,
			Role:      entry.Role,
			Kind:      entry.Kind,
			GitURL:    entry.GitURL,
			LocalPath: entry.LocalPath,
			Status:    "held back",
			Message:   reason,
		})
	}
	return results
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

func dirtyFiles(repo string, runner Runner) ([]string, error) {
	out, err := git(runner, repo, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("status: %s", commandError(out, err))
	}
	return parseStatusPaths(string(out)), nil
}

func changedFiles(repo, upstream string, runner Runner) ([]string, error) {
	out, err := git(runner, repo, "diff", "--name-only", upstream+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("diff %s..HEAD: %s", upstream, commandError(out, err))
	}
	return nonemptyLines(string(out)), nil
}

func parseStatusPaths(text string) []string {
	var paths []string
	for _, line := range strings.Split(text, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if i := strings.LastIndex(path, " -> "); i >= 0 {
			path = strings.TrimSpace(path[i+4:])
		}
		path = strings.Trim(path, `"`)
		if path != "" {
			paths = append(paths, filepath.ToSlash(path))
		}
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

func hasNitWorkspace(root string) bool {
	if root == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(root, ".nit", "roster.yaml"))
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

func nitDryRunMessage(hasDirty bool) string {
	var steps []string
	if hasDirty {
		steps = append(steps, "would run nit add for Flux-approved content paths")
		steps = append(steps, "would run nit commit -m")
	}
	steps = append(steps, "would run nit push")
	return strings.Join(steps, "; ")
}

func runNitPublish(opts Options, stagePaths []string) ([]byte, error) {
	dirRunner := opts.DirRunner
	if dirRunner == nil {
		dirRunner = execCommandInDir
	}
	var combined []byte
	run := func(args ...string) error {
		out, err := dirRunner(opts.NitRoot, "nit", args...)
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
