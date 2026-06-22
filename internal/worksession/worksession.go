// Package worksession manages isolated work sessions: per-session git
// worktrees of umbrella content mounts plus a JSON registry under .my-cli.
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

	"github.com/fluxinc/my-cli/internal/umbrella"
)

const (
	SchemaVersion = 1
	WorkDirName   = "sessions"
	RegistryDir   = "sessions"
	BranchPrefix  = "my/session/"

	LegacyWorkDirName  = "work"
	LegacyBranchPrefix = "my/work/"

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

	// Guidance carries optional CLI-composed organization context. Start still
	// writes usable session guidance without it, but callers that know the
	// umbrella and manifest should pass it so launched harnesses can orient
	// without probing.
	Guidance GuidanceContext
}

// GuidanceContext is optional organization context rendered into a session's
// generated AGENTS.md/CLAUDE.md contract.
type GuidanceContext struct {
	UmbrellaRoot     string
	ManifestName     string
	OrganizationID   string
	OrganizationName string
	SelectedRole     string
	BaseGuidance     []byte
}

// MountStatus is one mount's live git state inside a session.
type MountStatus struct {
	Mount
	Dirty    []string `json:"dirty"`
	Unlanded int      `json:"unlanded"`
	Error    string   `json:"error,omitempty"`
}

// MigrationReport describes legacy work/ -> sessions/ migration work.
type MigrationReport struct {
	Sessions []MigrationSessionResult `json:"sessions,omitempty"`
	Orphans  []string                 `json:"orphans,omitempty"`
}

// MigrationSessionResult describes one session considered during migration.
type MigrationSessionResult struct {
	ID      string                 `json:"id"`
	Status  string                 `json:"status"`
	From    string                 `json:"from,omitempty"`
	To      string                 `json:"to,omitempty"`
	Message string                 `json:"message,omitempty"`
	Mounts  []MigrationMountResult `json:"mounts,omitempty"`
	Details []string               `json:"details,omitempty"`
	Session *Session               `json:"session,omitempty"`
}

// MigrationMountResult describes one mount considered during migration.
type MigrationMountResult struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Message string `json:"message,omitempty"`
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

// NewID builds a session id of the form YYYY-MM-DD-<4hex>, or
// YYYY-MM-DD-<slug>-<4hex> when a custom slug is supplied.
func NewID(now time.Time, slug string, random io.Reader) (string, error) {
	slug = strings.TrimSpace(slug)
	if slug != "" && !slugPattern.MatchString(slug) {
		return "", fmt.Errorf("invalid session slug %q: use lowercase letters, digits, and hyphens", slug)
	}
	if random == nil {
		random = rand.Reader
	}
	suffix := make([]byte, 2)
	if _, err := io.ReadFull(random, suffix); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	date := now.UTC().Format("2006-01-02")
	if slug == "" {
		return fmt.Sprintf("%s-%s", date, hex.EncodeToString(suffix)), nil
	}
	return fmt.Sprintf("%s-%s-%s", date, slug, hex.EncodeToString(suffix)), nil
}

// Start creates one session: a sessions/<id> directory with a git worktree per
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
	slug := strings.TrimSpace(opts.Slug)
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
	if err := writeSessionGuidance(session, opts.Guidance); err != nil {
		cleanup()
		return Session{}, err
	}
	if err := Save(opts.Root, session); err != nil {
		cleanup()
		return Session{}, err
	}
	return session, nil
}

// EnsureGuidance rewrites the generated session guidance for an active
// session. It is used when an older session is resumed after the guidance
// contract has changed.
func EnsureGuidance(session Session, guidance GuidanceContext) error {
	if session.ID == "" {
		return errors.New("worksession: session id is required")
	}
	if session.Path == "" {
		return errors.New("worksession: session path is required")
	}
	return writeSessionGuidance(session, guidance)
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
	b.WriteString("`my session finish --land | --publish | --discard`.\n")
	b.WriteString("\nUseful commands:\n")
	b.WriteString("- Status: `my session status`\n")
	fmt.Fprintf(&b, "- Join another harness here: `my session join %s <harness>`\n", session.ID)
	fmt.Fprintf(&b, "- Resume a harness here: `my session resume %s <harness>`\n", session.ID)
	fmt.Fprintf(&b, "- Finish: `my session finish %s --land | --publish | --discard`\n", session.ID)
	return os.WriteFile(filepath.Join(session.Path, "SESSION.md"), []byte(b.String()), 0o644)
}

// writeSessionGuidance writes a session-aware AGENTS.md (with a CLAUDE.md
// alias) so harnesses launched inside the session keep their work here.
func writeSessionGuidance(session Session, guidance GuidanceContext) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Work Session %s\n\n", session.ID)
	b.WriteString("## Session Context\n\n")
	if guidance.OrganizationID != "" || guidance.OrganizationName != "" {
		if guidance.OrganizationID != "" && guidance.OrganizationName != "" {
			fmt.Fprintf(&b, "- Organization: %s (%s)\n", guidance.OrganizationName, guidance.OrganizationID)
		} else if guidance.OrganizationName != "" {
			fmt.Fprintf(&b, "- Organization: %s\n", guidance.OrganizationName)
		} else {
			fmt.Fprintf(&b, "- Organization: %s\n", guidance.OrganizationID)
		}
	}
	if guidance.ManifestName != "" {
		fmt.Fprintf(&b, "- Manifest: %s\n", guidance.ManifestName)
	}
	if guidance.SelectedRole != "" {
		fmt.Fprintf(&b, "- Selected role: %s\n", guidance.SelectedRole)
	}
	if guidance.UmbrellaRoot != "" {
		fmt.Fprintf(&b, "- Umbrella root: %s\n", guidance.UmbrellaRoot)
	}
	fmt.Fprintf(&b, "- Session id: %s\n", session.ID)
	if session.Path != "" {
		fmt.Fprintf(&b, "- Session path: %s\n", session.Path)
	}
	if session.Status != "" {
		fmt.Fprintf(&b, "- Status: %s\n", session.Status)
	}
	if session.CreatedAt != "" {
		fmt.Fprintf(&b, "- Created: %s\n", session.CreatedAt)
	}
	b.WriteString("- Mounts:\n")
	for _, m := range session.Mounts {
		fmt.Fprintf(&b, "  - %s/ (%s): worktree %s; branch %s from %s @ %s\n", m.ID, m.Kind, m.WorktreePath, m.Branch, m.BaseBranch, shortHead(m.BaseHead))
	}
	fmt.Fprintf(&b, "\nExact commands for this session:\n\n")
	b.WriteString("```sh\n")
	b.WriteString("my session status\n")
	fmt.Fprintf(&b, "my session join %s <harness>\n", session.ID)
	fmt.Fprintf(&b, "my ai -r %s <harness>\n", session.ID)
	fmt.Fprintf(&b, "my session finish %s --land | --publish | --discard\n", session.ID)
	b.WriteString("```\n\n")

	b.WriteString("## Session Rules\n\n")
	b.WriteString("This directory is an isolated My AI work session. Keep all work inside this session:\n\n")
	b.WriteString("- SESSION.md records the session creation facts and mount list.\n")
	b.WriteString("- Edit content only in the session's mount worktrees:\n")
	for _, m := range session.Mounts {
		fmt.Fprintf(&b, "  - %s/ - git worktree on branch %s, isolated from the base umbrella\n", m.ID, m.Branch)
	}
	b.WriteString("- Use scratch/ for unversioned session-local files; never commit them.\n")
	b.WriteString("- Commit changes inside the worktrees as you go; `my session status` shows\n  dirty and unlanded state.\n")
	b.WriteString("- Work leaves the session only through\n  `my session finish --land | --publish | --discard`.\n")
	if base := strings.TrimSpace(string(guidance.BaseGuidance)); base != "" {
		b.WriteString("\n## Base Umbrella Guidance\n\n")
		b.WriteString("The generated umbrella guidance below still applies inside this session. Interpret base-layout paths relative to the umbrella root above.\n\n")
		b.WriteString(base)
		b.WriteString("\n")
	}
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

// Save writes one session registry record under <root>/.my-cli/sessions/.
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

// LegacyLayout reports active sessions and orphan directories that still use
// the pre-session-consolidation work/ layout. It is read-only.
func LegacyLayout(root string) (MigrationReport, error) {
	sessions, err := List(root)
	if err != nil {
		return MigrationReport{}, err
	}
	report := MigrationReport{}
	for _, session := range sessions {
		if session.Status == StatusActive && sessionHasLegacyLayout(root, session) {
			report.Sessions = append(report.Sessions, MigrationSessionResult{
				ID:      session.ID,
				Status:  "pending",
				From:    legacySessionPath(root, session),
				To:      sessionPath(root, session.ID),
				Message: "legacy session layout",
			})
		}
	}
	orphans, err := legacyOrphanDirs(root, sessions)
	if err != nil {
		return report, err
	}
	report.Orphans = orphans
	return report, nil
}

// Migrate lazily migrates active sessions from work/<id> + my/work/<id> to
// sessions/<id> + my/session/<id>. It is idempotent and skips ambiguous
// sessions instead of guessing.
func Migrate(root string) (MigrationReport, error) {
	sessions, err := List(root)
	if err != nil {
		return MigrationReport{}, err
	}
	report := MigrationReport{}
	for _, session := range sessions {
		if session.Status != StatusActive || !sessionHasLegacyLayout(root, session) {
			continue
		}
		report.Sessions = append(report.Sessions, migrateSession(root, session, execRunner))
	}
	orphans, err := legacyOrphanDirs(root, sessions)
	if err != nil {
		return report, err
	}
	report.Orphans = orphans
	_ = removeDirIfEmpty(filepath.Join(root, LegacyWorkDirName))
	return report, nil
}

type mountMigrationPlan struct {
	index        int
	id           string
	oldPath      string
	newPath      string
	oldBranch    string
	newBranch    string
	movePath     bool
	renameBranch bool
}

func migrateSession(root string, session Session, runner Runner) MigrationSessionResult {
	oldPath := legacySessionPath(root, session)
	newPath := sessionPath(root, session.ID)
	result := MigrationSessionResult{
		ID:     session.ID,
		Status: "fixed",
		From:   oldPath,
		To:     newPath,
	}
	plans := make([]mountMigrationPlan, 0, len(session.Mounts))
	for i, mount := range session.Mounts {
		plan, mountResult, err := planMountMigration(root, session, i, mount, runner)
		result.Mounts = append(result.Mounts, mountResult)
		if err != nil {
			result.Status = "skipped"
			result.Message = err.Error()
			return result
		}
		plans = append(plans, plan)
	}
	if err := preflightSessionSidecars(oldPath, newPath); err != nil {
		result.Status = "skipped"
		result.Message = err.Error()
		return result
	}
	if err := os.MkdirAll(newPath, 0o755); err != nil {
		result.Status = "skipped"
		result.Message = "create target session dir: " + err.Error()
		return result
	}
	for _, plan := range plans {
		mountResult := &result.Mounts[plan.index]
		if plan.renameBranch {
			if out, err := runner("git", "-C", session.Mounts[plan.index].RepoPath, "branch", "-m", plan.oldBranch, plan.newBranch); err != nil {
				mountResult.Status = "skipped"
				mountResult.Message = "rename branch: " + commandMessage(out, err)
				result.Status = "skipped"
				result.Message = "session partially migrated; rerun after resolving mount " + plan.id
				return result
			}
		}
		if plan.movePath {
			if out, err := runner("git", "-C", session.Mounts[plan.index].RepoPath, "worktree", "move", plan.oldPath, plan.newPath); err != nil {
				mountResult.Status = "skipped"
				mountResult.Message = "move worktree: " + commandMessage(out, err)
				result.Status = "skipped"
				result.Message = "session partially migrated; rerun after resolving mount " + plan.id
				return result
			}
		}
		session.Mounts[plan.index].Branch = plan.newBranch
		session.Mounts[plan.index].WorktreePath = plan.newPath
		mountResult.Status = "fixed"
	}
	for _, name := range []string{"SESSION.md", "AGENTS.md", "CLAUDE.md", "scratch"} {
		if err := moveSessionEntry(oldPath, newPath, name); err != nil {
			result.Status = "skipped"
			result.Message = "move " + name + ": " + err.Error()
			return result
		}
	}
	session.Path = newPath
	if err := Save(root, session); err != nil {
		result.Status = "skipped"
		result.Message = "save migrated session record: " + err.Error()
		return result
	}
	_ = removeDirIfEmpty(oldPath)
	result.Message = "migrated legacy session layout"
	migrated := session
	result.Session = &migrated
	return result
}

func planMountMigration(root string, session Session, index int, mount Mount, runner Runner) (mountMigrationPlan, MigrationMountResult, error) {
	oldBranch := mount.Branch
	if !strings.HasPrefix(oldBranch, LegacyBranchPrefix) {
		oldBranch = LegacyBranchPrefix + session.ID
	}
	newBranch := BranchPrefix + session.ID
	oldPath := legacyMountPath(root, session, mount)
	newPath := filepath.Join(sessionPath(root, session.ID), mount.ID)
	result := MigrationMountResult{
		ID:     mount.ID,
		Status: "pending",
		From:   oldPath,
		To:     newPath,
		Branch: newBranch,
	}
	oldBranchExists := branchExists(runner, mount.RepoPath, oldBranch)
	newBranchExists := branchExists(runner, mount.RepoPath, newBranch)
	if oldBranchExists && newBranchExists {
		return mountMigrationPlan{}, result, fmt.Errorf("mount %s has both legacy branch %s and session branch %s", mount.ID, oldBranch, newBranch)
	}
	if !oldBranchExists && !newBranchExists {
		return mountMigrationPlan{}, result, fmt.Errorf("mount %s has neither legacy branch %s nor session branch %s", mount.ID, oldBranch, newBranch)
	}
	oldPathExists := pathExists(oldPath)
	newPathExists := pathExists(newPath)
	if oldPathExists && newPathExists {
		return mountMigrationPlan{}, result, fmt.Errorf("mount %s has both legacy worktree %s and session worktree %s", mount.ID, oldPath, newPath)
	}
	if !oldPathExists && !newPathExists {
		return mountMigrationPlan{}, result, fmt.Errorf("mount %s has neither legacy worktree %s nor session worktree %s", mount.ID, oldPath, newPath)
	}
	return mountMigrationPlan{
		index:        index,
		id:           mount.ID,
		oldPath:      oldPath,
		newPath:      newPath,
		oldBranch:    oldBranch,
		newBranch:    newBranch,
		movePath:     oldPathExists,
		renameBranch: oldBranchExists,
	}, result, nil
}

func sessionHasLegacyLayout(root string, session Session) bool {
	if pathWithinRoot(session.Path, filepath.Join(root, LegacyWorkDirName)) {
		return true
	}
	for _, mount := range session.Mounts {
		if strings.HasPrefix(mount.Branch, LegacyBranchPrefix) ||
			pathWithinRoot(mount.WorktreePath, filepath.Join(root, LegacyWorkDirName)) {
			return true
		}
	}
	return false
}

func legacySessionPath(root string, session Session) string {
	if pathWithinRoot(session.Path, filepath.Join(root, LegacyWorkDirName)) {
		return session.Path
	}
	return filepath.Join(root, LegacyWorkDirName, session.ID)
}

func sessionPath(root, id string) string {
	return filepath.Join(root, WorkDirName, id)
}

func legacyMountPath(root string, session Session, mount Mount) string {
	if pathWithinRoot(mount.WorktreePath, filepath.Join(root, LegacyWorkDirName)) {
		return mount.WorktreePath
	}
	return filepath.Join(legacySessionPath(root, session), mount.ID)
}

func legacyOrphanDirs(root string, sessions []Session) ([]string, error) {
	legacyRoot := filepath.Join(root, LegacyWorkDirName)
	entries, err := os.ReadDir(legacyRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, session := range sessions {
		known[session.ID] = true
		if pathWithinRoot(session.Path, legacyRoot) {
			known[filepath.Base(session.Path)] = true
		}
	}
	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() || known[entry.Name()] {
			continue
		}
		orphans = append(orphans, filepath.Join(legacyRoot, entry.Name()))
	}
	sort.Strings(orphans)
	return orphans, nil
}

func preflightSessionSidecars(oldPath, newPath string) error {
	for _, name := range []string{"SESSION.md", "AGENTS.md", "CLAUDE.md", "scratch"} {
		oldEntry := filepath.Join(oldPath, name)
		newEntry := filepath.Join(newPath, name)
		if pathExists(oldEntry) && pathExists(newEntry) {
			return fmt.Errorf("both legacy and session %s exist", name)
		}
	}
	return nil
}

func moveSessionEntry(oldPath, newPath, name string) error {
	oldEntry := filepath.Join(oldPath, name)
	if !pathExists(oldEntry) {
		return nil
	}
	newEntry := filepath.Join(newPath, name)
	if pathExists(newEntry) {
		return fmt.Errorf("target already exists: %s", newEntry)
	}
	return os.Rename(oldEntry, newEntry)
}

func branchExists(runner Runner, repo, branch string) bool {
	_, err := gitOutput(runner, repo, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func removeDirIfEmpty(path string) error {
	if path == "" {
		return nil
	}
	return os.Remove(path)
}

func pathWithinRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
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
