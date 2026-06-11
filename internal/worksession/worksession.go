// Package worksession manages isolated work sessions: per-session git
// worktrees of umbrella content mounts plus a JSON registry under .our.
package worksession

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/our-ai/internal/umbrella"
)

const (
	SchemaVersion = 1
	WorkDirName   = "work"
	RegistryDir   = "sessions"
	BranchPrefix  = "our/work/"

	StatusActive    = "active"
	StatusFinished  = "finished"
	StatusDiscarded = "discarded"

	OutcomeLanded    = "landed"
	OutcomePublished = "published"
	OutcomeDiscarded = "discarded"
)

// Runner executes one external command and returns its combined output.
type Runner func(name string, args ...string) ([]byte, error)

// Session is the registry record for one work session.
type Session struct {
	SchemaVersion int     `json:"schema_version"`
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	CreatedAt     string  `json:"created_at"`
	Status        string  `json:"status"`
	Outcome       string  `json:"outcome,omitempty"`
	FinishedAt    string  `json:"finished_at,omitempty"`
	Path          string  `json:"path"`
	Mounts        []Mount `json:"mounts"`
}

// Mount records one mount worktree inside a session.
type Mount struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	RepoPath     string   `json:"repo_path"`
	WorktreePath string   `json:"worktree_path"`
	BaseBranch   string   `json:"base_branch"`
	BaseHead     string   `json:"base_head"`
	Branch       string   `json:"branch"`
	ContentPaths []string `json:"content_paths,omitempty"`
}

// MountSpec selects one umbrella mount for inclusion in a new session.
type MountSpec struct {
	ID           string
	Kind         string
	RepoPath     string
	ContentPaths []string
}

// StartOptions configures Start.
type StartOptions struct {
	Root   string
	Slug   string
	Now    time.Time
	Rand   io.Reader
	Runner Runner
	Mounts []MountSpec
}

// MountStatus is one mount's live git state inside a session.
type MountStatus struct {
	Mount
	Dirty    []string `json:"dirty"`
	Unlanded int      `json:"unlanded"`
	Error    string   `json:"error,omitempty"`
}

// SessionStatus is one session plus the live state of its mounts.
type SessionStatus struct {
	Session
	Mounts []MountStatus `json:"mounts"`
}

// LandOptions configures Land.
type LandOptions struct {
	Root    string
	ID      string
	Message string
	Outcome string
	Now     time.Time
	Runner  Runner
}

// DiscardOptions configures Discard.
type DiscardOptions struct {
	Root   string
	ID     string
	Now    time.Time
	Runner Runner
}

// FinishResult describes the changes made while finishing a session.
type FinishResult struct {
	Session Session             `json:"session"`
	Mounts  []MountFinishResult `json:"mounts,omitempty"`
}

// MountFinishResult describes finish work for one mount.
type MountFinishResult struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind,omitempty"`
	Branch  string   `json:"branch"`
	Status  string   `json:"status"`
	Dirty   []string `json:"dirty,omitempty"`
	Changed []string `json:"changed,omitempty"`
	Commit  string   `json:"commit,omitempty"`
	Message string   `json:"message,omitempty"`
}

type landPlan struct {
	mount        Mount
	contentPaths []string
	dirty        []dirtyFile
	changed      []string
}

type dirtyFile struct {
	status string
	path   string
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// NewID builds a session id of the form YYYY-MM-DD-<slug>-<4hex>.
func NewID(now time.Time, slug string, random io.Reader) (string, error) {
	if slug == "" {
		slug = "work"
	}
	if !slugPattern.MatchString(slug) {
		return "", fmt.Errorf("invalid session slug %q: use lowercase letters, digits, and hyphens", slug)
	}
	if random == nil {
		random = rand.Reader
	}
	suffix := make([]byte, 2)
	if _, err := io.ReadFull(random, suffix); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return fmt.Sprintf("%s-%s-%s", now.UTC().Format("2006-01-02"), slug, hex.EncodeToString(suffix)), nil
}

// Start creates one session: a work/<id> directory with a git worktree per
// mount on a fresh session branch, a scratch dir, SESSION.md, and a registry
// record. A failed start cleans up everything it created.
func Start(opts StartOptions) (Session, error) {
	if opts.Root == "" {
		return Session{}, errors.New("worksession: root is required")
	}
	if len(opts.Mounts) == 0 {
		return Session{}, errors.New("worksession: no mounts eligible for a session worktree")
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner
	}
	slug := opts.Slug
	if slug == "" {
		slug = "work"
	}
	id, err := NewID(now, slug, opts.Rand)
	if err != nil {
		return Session{}, err
	}
	sessionPath := filepath.Join(opts.Root, WorkDirName, id)
	if _, err := os.Stat(sessionPath); err == nil {
		return Session{}, fmt.Errorf("session path already exists: %s", sessionPath)
	}
	branch := BranchPrefix + id

	session := Session{
		SchemaVersion: SchemaVersion,
		ID:            id,
		Slug:          slug,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Status:        StatusActive,
		Path:          sessionPath,
	}

	cleanup := func() {
		for _, m := range session.Mounts {
			_, _ = runner("git", "-C", m.RepoPath, "worktree", "remove", "--force", m.WorktreePath)
			_, _ = runner("git", "-C", m.RepoPath, "branch", "-D", m.Branch)
		}
		_ = os.RemoveAll(sessionPath)
	}

	if err := os.MkdirAll(filepath.Join(sessionPath, "scratch"), 0o755); err != nil {
		return Session{}, err
	}
	for _, spec := range opts.Mounts {
		mount, err := addWorktree(runner, spec, sessionPath, branch)
		if err != nil {
			cleanup()
			return Session{}, fmt.Errorf("mount %s: %w", spec.ID, err)
		}
		session.Mounts = append(session.Mounts, mount)
	}
	if err := writeSessionDoc(session); err != nil {
		cleanup()
		return Session{}, err
	}
	if err := writeSessionGuidance(session); err != nil {
		cleanup()
		return Session{}, err
	}
	if err := Save(opts.Root, session); err != nil {
		cleanup()
		return Session{}, err
	}
	return session, nil
}

func addWorktree(runner Runner, spec MountSpec, sessionPath, branch string) (Mount, error) {
	baseBranch, err := gitTrim(runner, spec.RepoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return Mount{}, err
	}
	baseHead, err := gitTrim(runner, spec.RepoPath, "rev-parse", "HEAD")
	if err != nil {
		return Mount{}, err
	}
	worktree := filepath.Join(sessionPath, spec.ID)
	if out, err := runner("git", "-C", spec.RepoPath, "worktree", "add", "-b", branch, worktree); err != nil {
		return Mount{}, fmt.Errorf("git worktree add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return Mount{
		ID:           spec.ID,
		Kind:         spec.Kind,
		RepoPath:     spec.RepoPath,
		WorktreePath: worktree,
		BaseBranch:   baseBranch,
		BaseHead:     baseHead,
		Branch:       branch,
		ContentPaths: append([]string(nil), spec.ContentPaths...),
	}, nil
}

func writeSessionDoc(session Session) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session %s\n\n", session.ID)
	fmt.Fprintf(&b, "- Created: %s\n", session.CreatedAt)
	if session.Slug != "" {
		fmt.Fprintf(&b, "- Slug: %s\n", session.Slug)
	}
	b.WriteString("- Mounts:\n")
	for _, m := range session.Mounts {
		fmt.Fprintf(&b, "  - %s/ (branch %s from %s @ %s)\n", m.ID, m.Branch, m.BaseBranch, shortHead(m.BaseHead))
	}
	b.WriteString("\nWork in the mount worktrees above; use scratch/ for unversioned\n")
	b.WriteString("session-local files. Work leaves a session only through\n")
	b.WriteString("`our work finish --land | --publish | --discard`.\n")
	return os.WriteFile(filepath.Join(session.Path, "SESSION.md"), []byte(b.String()), 0o644)
}

// writeSessionGuidance writes a session-aware AGENTS.md (with a CLAUDE.md
// alias) so harnesses launched inside the session keep their work here.
func writeSessionGuidance(session Session) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Work Session %s\n\n", session.ID)
	b.WriteString("This directory is an isolated Our AI work session; SESSION.md says what\nit is for. Keep all work inside this session:\n\n")
	b.WriteString("- Edit content only in the session's mount worktrees:\n")
	for _, m := range session.Mounts {
		fmt.Fprintf(&b, "  - %s/ — git worktree on branch %s, isolated from the base umbrella\n", m.ID, m.Branch)
	}
	b.WriteString("- Use scratch/ for unversioned session-local files; never commit them.\n")
	b.WriteString("- Commit changes inside the worktrees as you go; `our work status` shows\n  dirty and unlanded state.\n")
	b.WriteString("- Work leaves the session only through\n  `our work finish --land | --publish | --discard`.\n")
	content := []byte(b.String())
	agentsPath := filepath.Join(session.Path, "AGENTS.md")
	if err := os.WriteFile(agentsPath, content, 0o644); err != nil {
		return err
	}
	claudePath := filepath.Join(session.Path, "CLAUDE.md")
	if err := os.Symlink("AGENTS.md", claudePath); err != nil {
		return os.WriteFile(claudePath, content, 0o644)
	}
	return nil
}

func shortHead(head string) string {
	if len(head) > 12 {
		return head[:12]
	}
	return head
}

// Save writes one session registry record under <root>/.our/sessions/.
func Save(root string, session Session) error {
	if session.ID == "" {
		return errors.New("worksession: session id is required")
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := registryPath(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, session.ID+".json"), data, 0o644)
}

// Load reads one session registry record by id.
func Load(root, id string) (Session, error) {
	data, err := os.ReadFile(filepath.Join(registryPath(root), id+".json"))
	if err != nil {
		return Session{}, err
	}
	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return Session{}, fmt.Errorf("read session %s: %w", id, err)
	}
	return session, nil
}

// List returns all registry records sorted by id.
func List(root string) ([]Session, error) {
	entries, err := os.ReadDir(registryPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sessions []Session
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		session, err := Load(root, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })
	return sessions, nil
}

// Inspect reports the live git state of one session's mounts.
func Inspect(session Session, runner Runner) (SessionStatus, error) {
	if runner == nil {
		runner = execRunner
	}
	status := SessionStatus{Session: session}
	for _, m := range session.Mounts {
		ms := MountStatus{Mount: m}
		if dirty, err := gitTrim(runner, m.WorktreePath, "status", "--porcelain=v1", "--untracked-files=all"); err != nil {
			ms.Error = err.Error()
		} else if dirty != "" {
			ms.Dirty = strings.Split(dirty, "\n")
		}
		if ms.Error == "" {
			ms.Unlanded, ms.Error = unlandedCount(runner, m)
		}
		status.Mounts = append(status.Mounts, ms)
	}
	return status, nil
}

// Land commits any intentional dirty content in the session worktrees, merges
// each session branch into its clean base checkout, removes the worktrees and
// branches, and marks the registry record finished.
func Land(opts LandOptions) (FinishResult, error) {
	if opts.Root == "" || opts.ID == "" {
		return FinishResult{}, errors.New("worksession: root and id are required")
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner
	}
	session, err := Load(opts.Root, opts.ID)
	if err != nil {
		return FinishResult{}, err
	}
	if session.Status != StatusActive {
		return FinishResult{}, fmt.Errorf("session %s is %s, not active", session.ID, session.Status)
	}
	message := strings.TrimSpace(opts.Message)
	if message == "" {
		message = "Finish work session " + session.ID
	}
	outcome := opts.Outcome
	if outcome == "" {
		outcome = OutcomeLanded
	}

	plans := make([]landPlan, 0, len(session.Mounts))
	for _, mount := range session.Mounts {
		plan, err := planLandMount(runner, mount)
		if err != nil {
			return FinishResult{Session: session}, err
		}
		plans = append(plans, plan)
	}

	result := FinishResult{Session: session}
	for _, plan := range plans {
		mountResult, err := applyLandMount(runner, plan, message)
		result.Mounts = append(result.Mounts, mountResult)
		if err != nil {
			return result, err
		}
	}

	finishedAt := finishTime(opts.Now)
	session.Status = StatusFinished
	session.Outcome = outcome
	session.FinishedAt = finishedAt
	if err := Save(opts.Root, session); err != nil {
		return result, err
	}
	result.Session = session
	return result, nil
}

// Discard force-removes a session's worktrees and branches, removes the
// visible session directory, and marks the registry record discarded.
func Discard(opts DiscardOptions) (FinishResult, error) {
	if opts.Root == "" || opts.ID == "" {
		return FinishResult{}, errors.New("worksession: root and id are required")
	}
	runner := opts.Runner
	if runner == nil {
		runner = execRunner
	}
	session, err := Load(opts.Root, opts.ID)
	if err != nil {
		return FinishResult{}, err
	}
	if session.Status != StatusActive {
		return FinishResult{}, fmt.Errorf("session %s is %s, not active", session.ID, session.Status)
	}
	result := FinishResult{Session: session}
	for _, mount := range session.Mounts {
		mountResult := MountFinishResult{
			ID:     mount.ID,
			Kind:   mount.Kind,
			Branch: mount.Branch,
			Status: "discarded",
		}
		if out, err := runner("git", "-C", mount.RepoPath, "worktree", "remove", "--force", mount.WorktreePath); err != nil {
			mountResult.Status = "failed"
			mountResult.Message = commandMessage(out, err)
			result.Mounts = append(result.Mounts, mountResult)
			return result, fmt.Errorf("discard %s worktree: %s", mount.ID, mountResult.Message)
		}
		if out, err := runner("git", "-C", mount.RepoPath, "branch", "-D", mount.Branch); err != nil {
			mountResult.Status = "failed"
			mountResult.Message = commandMessage(out, err)
			result.Mounts = append(result.Mounts, mountResult)
			return result, fmt.Errorf("discard %s branch: %s", mount.ID, mountResult.Message)
		}
		result.Mounts = append(result.Mounts, mountResult)
	}
	if err := os.RemoveAll(session.Path); err != nil {
		return result, err
	}
	session.Status = StatusDiscarded
	session.Outcome = OutcomeDiscarded
	session.FinishedAt = finishTime(opts.Now)
	if err := Save(opts.Root, session); err != nil {
		return result, err
	}
	result.Session = session
	return result, nil
}

// MarkOutcome updates the outcome on an already-finished session, used after
// a land-then-publish flow succeeds.
func MarkOutcome(root, id, outcome string, now time.Time) (Session, error) {
	session, err := Load(root, id)
	if err != nil {
		return Session{}, err
	}
	if session.Status != StatusFinished {
		return Session{}, fmt.Errorf("session %s is %s, not finished", session.ID, session.Status)
	}
	session.Outcome = outcome
	if session.FinishedAt == "" {
		session.FinishedAt = finishTime(now)
	}
	if err := Save(root, session); err != nil {
		return Session{}, err
	}
	return session, nil
}

func planLandMount(runner Runner, mount Mount) (landPlan, error) {
	plan := landPlan{mount: mount, contentPaths: contentPathsForMount(mount)}
	if len(plan.contentPaths) == 0 {
		return plan, fmt.Errorf("mount %s has no declared content paths", mount.ID)
	}
	if err := requireBaseReady(runner, mount); err != nil {
		return plan, err
	}
	dirty, err := dirtyFiles(runner, mount.WorktreePath)
	if err != nil {
		return plan, fmt.Errorf("inspect %s dirty files: %w", mount.ID, err)
	}
	plan.dirty = dirty
	if err := validateDirtyFiles(mount, plan.contentPaths, dirty); err != nil {
		return plan, err
	}
	changed, err := changedFiles(runner, mount)
	if err != nil {
		return plan, fmt.Errorf("inspect %s branch changes: %w", mount.ID, err)
	}
	plan.changed = changed
	if !pathsWithin(changed, plan.contentPaths) {
		return plan, fmt.Errorf("mount %s has committed changes outside declared content paths: %s", mount.ID, strings.Join(pathsOutside(changed, plan.contentPaths), ", "))
	}
	return plan, nil
}

func requireBaseReady(runner Runner, mount Mount) error {
	branch, err := gitTrim(runner, mount.RepoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("inspect %s base branch: %w", mount.ID, err)
	}
	if mount.BaseBranch != "" && mount.BaseBranch != "HEAD" && branch != mount.BaseBranch {
		return fmt.Errorf("mount %s base checkout is on %s, expected %s", mount.ID, branch, mount.BaseBranch)
	}
	dirty, err := dirtyFiles(runner, mount.RepoPath)
	if err != nil {
		return fmt.Errorf("inspect %s base checkout: %w", mount.ID, err)
	}
	if len(dirty) != 0 {
		return fmt.Errorf("mount %s base checkout is dirty: %s", mount.ID, strings.Join(dirtyFilePaths(dirty), ", "))
	}
	return nil
}

func validateDirtyFiles(mount Mount, contentPaths []string, dirty []dirtyFile) error {
	for _, file := range dirty {
		if !pathWithin(file.path, contentPaths) {
			return fmt.Errorf("mount %s has dirty path outside declared content paths: %s", mount.ID, file.path)
		}
		if file.status == "??" {
			return fmt.Errorf("mount %s has unadopted untracked content file %s; run git -C %s add -N -- %s or remove it", mount.ID, file.path, mount.WorktreePath, file.path)
		}
	}
	return nil
}

func applyLandMount(runner Runner, plan landPlan, message string) (MountFinishResult, error) {
	mount := plan.mount
	result := MountFinishResult{
		ID:      mount.ID,
		Kind:    mount.Kind,
		Branch:  mount.Branch,
		Status:  "landed",
		Dirty:   dirtyFilePaths(plan.dirty),
		Changed: append([]string(nil), plan.changed...),
	}
	if len(plan.dirty) != 0 {
		args := []string{"-C", mount.WorktreePath, "add", "-A", "--"}
		args = append(args, dirtyFilePaths(plan.dirty)...)
		if out, err := runner("git", args...); err != nil {
			result.Status = "failed"
			result.Message = commandMessage(out, err)
			return result, fmt.Errorf("stage %s content: %s", mount.ID, result.Message)
		}
		if out, err := runner("git", "-C", mount.WorktreePath, "commit", "-m", message); err != nil {
			result.Status = "failed"
			result.Message = commandMessage(out, err)
			return result, fmt.Errorf("commit %s session content: %s", mount.ID, result.Message)
		}
		head, err := gitTrim(runner, mount.WorktreePath, "rev-parse", "HEAD")
		if err != nil {
			return result, err
		}
		result.Commit = head
	}
	if out, err := runner("git", "-C", mount.RepoPath, "merge", "--no-edit", mount.Branch); err != nil {
		result.Status = "failed"
		result.Message = commandMessage(out, err)
		return result, fmt.Errorf("merge %s session branch: %s", mount.ID, result.Message)
	}
	if out, err := runner("git", "-C", mount.RepoPath, "worktree", "remove", mount.WorktreePath); err != nil {
		result.Status = "failed"
		result.Message = commandMessage(out, err)
		return result, fmt.Errorf("remove %s worktree: %s", mount.ID, result.Message)
	}
	if out, err := runner("git", "-C", mount.RepoPath, "branch", "-d", mount.Branch); err != nil {
		result.Status = "failed"
		result.Message = commandMessage(out, err)
		return result, fmt.Errorf("delete %s session branch: %s", mount.ID, result.Message)
	}
	return result, nil
}

func dirtyFiles(runner Runner, repo string) ([]dirtyFile, error) {
	out, err := gitOutput(runner, repo, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("%s", commandMessage(out, err))
	}
	return parseStatusFiles(string(out)), nil
}

func changedFiles(runner Runner, mount Mount) ([]string, error) {
	base := mount.BaseHead
	if base == "" {
		base = mount.BaseBranch
	}
	out, err := gitOutput(runner, mount.RepoPath, "diff", "--name-only", base+".."+mount.Branch)
	if err != nil {
		return nil, fmt.Errorf("%s", commandMessage(out, err))
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
		path := filepath.ToSlash(part[3:])
		if path == "" {
			continue
		}
		if !seen[path] {
			files = append(files, dirtyFile{status: status, path: path})
			seen[path] = true
		}
		if status[0] == 'R' || status[0] == 'C' || status[1] == 'R' || status[1] == 'C' {
			i++
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
	if len(paths) == 0 {
		return true
	}
	for _, path := range paths {
		if !pathWithin(path, prefixes) {
			return false
		}
	}
	return true
}

func pathWithin(path string, prefixes []string) bool {
	path = filepath.ToSlash(strings.TrimPrefix(path, "./"))
	for _, prefix := range prefixes {
		prefix = strings.Trim(filepath.ToSlash(prefix), "/")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func pathsOutside(paths, prefixes []string) []string {
	var out []string
	for _, path := range paths {
		if !pathWithin(path, prefixes) {
			out = append(out, path)
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

func contentPathsForMount(mount Mount) []string {
	if len(mount.ContentPaths) != 0 {
		return append([]string(nil), mount.ContentPaths...)
	}
	switch mount.Kind {
	case "handbook":
		return []string{"meetings", "support", "decisions", "projects", "policy", "people"}
	case "meetings":
		return []string{"meetings"}
	case "support":
		return []string{"support"}
	case "fleet":
		return []string{"fleet"}
	case "policy":
		return []string{"policy"}
	case "docs":
		return []string{"docs"}
	default:
		return nil
	}
}

func finishTime(now time.Time) string {
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC().Format(time.RFC3339)
}

func unlandedCount(runner Runner, m Mount) (int, string) {
	base := m.BaseBranch
	if base == "" || base == "HEAD" {
		base = m.BaseHead
	}
	out, err := gitTrim(runner, m.RepoPath, "rev-list", "--count", m.Branch, "--not", base)
	if err != nil {
		// The base branch may have been deleted; fall back to the recorded head.
		out, err = gitTrim(runner, m.RepoPath, "rev-list", "--count", m.Branch, "--not", m.BaseHead)
		if err != nil {
			return 0, err.Error()
		}
	}
	n, convErr := strconv.Atoi(out)
	if convErr != nil {
		return 0, fmt.Sprintf("parse rev-list count %q", out)
	}
	return n, ""
}

func registryPath(root string) string {
	return filepath.Join(root, umbrella.DirName, RegistryDir)
}

func gitTrim(runner Runner, repo string, args ...string) (string, error) {
	out, err := gitOutput(runner, repo, args...)
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func gitOutput(runner Runner, repo string, args ...string) ([]byte, error) {
	full := append([]string{"-C", repo}, args...)
	return runner("git", full...)
}

func commandMessage(out []byte, err error) string {
	msg := strings.TrimSpace(string(out))
	if msg != "" {
		return msg
	}
	return err.Error()
}

func execRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
