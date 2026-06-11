// Package cli implements the our command-line surface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/our-ai/internal/bundle"
	"github.com/fluxinc/our-ai/internal/fleet"
	"github.com/fluxinc/our-ai/internal/guidance"
	"github.com/fluxinc/our-ai/internal/harness"
	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/meetings"
	"github.com/fluxinc/our-ai/internal/record"
	"github.com/fluxinc/our-ai/internal/selfskill"
	"github.com/fluxinc/our-ai/internal/selfupdate"
	"github.com/fluxinc/our-ai/internal/skills"
	"github.com/fluxinc/our-ai/internal/support"
	"github.com/fluxinc/our-ai/internal/syncer"
	"github.com/fluxinc/our-ai/internal/umbrella"
	"github.com/fluxinc/our-ai/internal/worksession"
	"github.com/fluxinc/our-ai/internal/workspace"
)

// Run executes the CLI and returns a process exit code.
func Run(args []string) int {
	a := app{
		stdout: os.Stdout,
		stderr: os.Stderr,
	}
	if err := a.run(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		if errors.Is(err, errAlreadyPrinted) {
			return 1
		}
		var exitErr exitCodeError
		if errors.As(err, &exitErr) {
			return exitErr.code
		}
		fmt.Fprintf(a.stderr, "our: %v\n", err)
		return 1
	}
	return 0
}

type app struct {
	stdout               io.Writer
	stderr               io.Writer
	lookPath             func(string) (string, error)
	execHarness          func(path string, args []string, dir string) error
	updateSource         selfupdate.Source
	updateNow            func() time.Time
	updateCurrentVersion string
	updateTargetPath     string
	// publishRunner overrides external gh invocations during our publish.
	publishRunner manifest.Runner
}

func (a app) runStartupMaintenance(args []string) {
	if !shouldAutoSyncSelfSkill(args) {
		return
	}
	_, _ = selfskill.SyncExisting(selfskill.Options{Link: true, SkipMissing: true})
}

func shouldAutoSyncSelfSkill(args []string) bool {
	if os.Getenv("OUR_DISABLE_SELF_SKILL_SYNC") != "" {
		return false
	}
	if strings.HasSuffix(filepath.Base(os.Args[0]), ".test") {
		return false
	}
	if isKnownHarnessEnv() {
		return false
	}
	if len(args) < 2 {
		return false
	}
	switch args[1] {
	case "-h", "--help", "help", "-v", "--version", "version":
		return false
	case "skills":
		return len(args) < 3 || args[2] != "self"
	default:
		return true
	}
}

func isKnownHarnessEnv() bool {
	for _, key := range []string{"CLAUDECODE", "CODEX_THREAD_ID", "GEMINI_CLI", "OPENCODE"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return os.Getenv("CMUX_AGENT_LAUNCH_KIND") != ""
}

var errAlreadyPrinted = errors.New("error already printed")

type exitCodeError struct {
	code int
}

func (e exitCodeError) Error() string {
	return fmt.Sprintf("process exited with code %d", e.code)
}

func (a app) run(args []string) error {
	if len(args) < 2 {
		a.printUsage()
		return nil
	}

	a.runStartupMaintenance(args)

	switch args[1] {
	case "-h", "--help", "help":
		a.printUsage()
		return nil
	case "-v", "--version", "version":
		return a.runVersion(args[2:])
	case "update":
		return a.runUpdate(args[2:])
	case "init":
		return a.runInit(args[2:])
	case "publish":
		return a.runPublish(args[2:])
	case "skills":
		return a.runSkills(args[2:])
	case "setup":
		return a.runOnboard(args[2:])
	case "onboard":
		a.warnDeprecated("our onboard", "our setup")
		return a.runOnboard(args[2:])
	case "root":
		return a.runRoot(args[2:])
	case "ai":
		return a.runLaunch(args[2:])
	case "launch":
		a.warnDeprecated("our launch", "our ai")
		return a.runLaunch(args[2:])
	case "sync":
		return a.runSync(args[2:])
	case "manifests":
		return a.runManifest(args[2:])
	case "workspaces":
		return a.runWorkspace(args[2:])
	case "mounts":
		return a.runMount(args[2:])
	case "tools":
		return a.runTools(args[2:])
	case "doctor":
		return a.runDoctor(args[2:])
	case "meetings":
		return a.runMeetings(args[2:])
	case "support":
		return a.runSupport(args[2:])
	case "fleet":
		return a.runFleet(args[2:])
	case "record":
		return a.runRecord(args[2:])
	case "work":
		return a.runWork(args[2:])
	case "customers":
		return a.runCustomers(args[2:])
	case "products":
		return a.runProducts(args[2:])
	case "repos":
		return a.runRepos(args[2:])
	case "admin":
		return a.runAdmin(args[2:])
	default:
		return fmt.Errorf("unknown command %q", args[1])
	}
}

func (a app) warnDeprecated(old, replacement string) {
	fmt.Fprintf(a.stderr, "warning: `%s` is deprecated; use `%s`\n", old, replacement)
}

func (a app) printUsage() {
	fmt.Fprintln(a.stdout, `our installs and manages manifest-backed AI workspace tooling.

Usage:
  our setup [harness...] | --all [--print] [--copy] [--link] [--force] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  our root [--repo ID] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  our ai [--session ID|--no-session] [--repo ID] [--setup] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check] [harness] [-- harness args...]
  our update [--check] [--version X.Y.Z] [--json] [--yes]
  our init <org-id> [--name NAME] [--path DIR] [--umbrella DIR] [--home DIR] [--setup] [--json]
  our publish [--manifest NAME] [--home DIR] [--print] [--json]
  our sync [--backend auto|gnit|builtin] [--publish auto|never|direct|pr] [--scope all|local|content|manifest|repos] [--no-derived] [--print] [--json] [--manifest NAME] [--home DIR] [--umbrella DIR]
  our skills self install|uninstall|status ...
  our skills install [harness...] | --all [--skill ID_OR_SLUG] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
  our skills uninstall <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
  our skills sync [harness...] | --all [--skill ID_OR_SLUG] [--no-prune] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
  our skills purge <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
  our skills list [--json] [--source DIR] [--manifest NAME] [--home DIR]
  our skills show <id|slug> [--json] [--source DIR] [--manifest NAME] [--home DIR]
  our skills status [--skill ID_OR_SLUG] [--json] [--source DIR] [--manifest NAME] [--home DIR]
  our admin skills add <skill-dir> --id namespace:name --manifest-dir DIR [--install-slug SLUG] [--keep-original|--remove-original] [--force] [--json]
  our admin skills remove <id|slug> --manifest-dir DIR [--delete-source] [--prune-related] [--prune-orphans] [--force] [--json]
  our admin setup ...                      (alias of our setup)
  our admin manifests add|sync|validate ...   (alias of our manifests ...)
  our admin mounts add|remove|sync ...        (alias of our mounts ...)
  our admin meetings add ...                 (alias of our meetings add)
  our admin support add ...                  (alias of our support add)
  our admin customers add|edit ...           (edit manifest customer catalog)
  our admin tools add|edit|remove ...        (edit manifest tool hints)
  our manifests add <name> <git-url>
  our manifests list
  our manifests sync <name...> | --all [--umbrella DIR] [--no-derived] [--print]
  our manifests validate <name|path>
  our mounts list [--manifest NAME]
  our mounts add <kind:id|id> [--manifest NAME]
  our mounts sync <mount...> | --all [--manifest NAME] [--print]
  our mounts remove <mount...> [--print] [--force]
  our workspaces list [--manifest NAME]
  our workspaces sync <workspace...> | --all [--manifest NAME] [--print]
  our tools list
  our tools info <name>
  our meetings list
  our meetings search <text>
  our meetings get <id|path>
  our meetings add <slug>
  our support list
  our support search <text>
  our support get <id|path>
  our support add <slug>
  our fleet list
  our fleet search <text>
  our fleet get <id|identifier|path>
  our fleet add <id>
  our fleet set <id> KEY=VALUE...
  our record adopt <path>
  our work start [--slug SLUG] [--json]
  our work status [--all] [--json]
  our work finish [session-id] --land|--publish|--discard [--message TEXT] [--json]
  our customers list
  our products list
  our repos list [--json]
  our repos add <id>
  our repos remove <id> [--force]
  our doctor [--no-fetch] [--fix] [--json]
  our version`)
}

type initOptions struct {
	orgID        string
	orgName      string
	repoPath     string
	home         string
	umbrellaPath string
	setup        bool
	jsonOut      bool
}

type initResult struct {
	OrganizationID   string                `json:"organization_id"`
	OrganizationName string                `json:"organization_name"`
	RepoPath         string                `json:"repo_path"`
	ContentPath      string                `json:"content_path"`
	Manifest         manifest.Ref          `json:"manifest"`
	Sync             []manifest.SyncResult `json:"sync"`
	NextCommands     []initNextCommand     `json:"next_commands"`
}

type initNextCommand struct {
	Action  string `json:"action"`
	Command string `json:"command"`
}

func (a app) runInit(args []string) error {
	var opts initOptions
	fs := newFlagSet("our init", a.stderr)
	fs.StringVar(&opts.orgName, "name", "", "organization display name")
	fs.StringVar(&opts.repoPath, "path", "", "content repository path")
	fs.StringVar(&opts.umbrellaPath, "umbrella", "", "recommended umbrella path")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.BoolVar(&opts.setup, "setup", false, "run our setup after creating and syncing the manifest")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		a.printInitUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"name":     true,
		"path":     true,
		"umbrella": true,
		"home":     true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our init <org-id>")
	}
	if opts.setup && opts.jsonOut {
		return fmt.Errorf("--setup and --json are not supported together")
	}
	opts.orgID = rest[0]
	result, err := a.createInitScaffold(opts)
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return printJSON(a.stdout, result)
	}
	a.printInitResult(result)
	if opts.setup {
		setupArgs := []string{"--manifest", opts.orgID}
		if opts.home != "" {
			setupArgs = append(setupArgs, "--home", opts.home)
		}
		if opts.umbrellaPath != "" {
			setupArgs = append(setupArgs, "--umbrella", opts.umbrellaPath)
		}
		return a.runOnboard(setupArgs)
	}
	return nil
}

func (a app) printInitUsage() {
	fmt.Fprintln(a.stderr, `Usage of our init:
  our init <org-id> [--name NAME] [--path DIR] [--umbrella DIR] [--home DIR] [--setup] [--json]

Creates two local repositories and registers the organization: a private
manifest repository (the control plane: manifest, catalog, skills) and a
content repository mounted in the umbrella (the workspace handbook). Both are
local-only until our publish creates their remotes.

Examples:
  our init acme --name "Acme"
  our init acme --path ~/acme/handbook

Options:`)
}

func (a app) createInitScaffold(opts initOptions) (initResult, error) {
	if !portableKebab(opts.orgID) {
		return initResult{}, fmt.Errorf("organization id %q must be lowercase kebab-case", opts.orgID)
	}
	if _, exists, err := manifest.Find(opts.home, opts.orgID); err != nil {
		return initResult{}, err
	} else if exists {
		return initResult{}, fmt.Errorf("manifest %q is already registered", opts.orgID)
	}
	homeDir, err := resolveHome(opts.home)
	if err != nil {
		return initResult{}, err
	}
	umbrellaPath := strings.TrimSpace(opts.umbrellaPath)
	if umbrellaPath == "" {
		umbrellaPath = "~/" + opts.orgID
	}
	// Control plane: the private manifest repository at the registry path.
	manifestPath, err := manifest.DefaultCachePath(opts.home, opts.orgID)
	if err != nil {
		return initResult{}, err
	}
	// Data plane: the content repository mounted visibly in the umbrella.
	contentPath := opts.repoPath
	if contentPath == "" {
		contentPath = umbrella.MountPath(expandUserPath(homeDir, umbrellaPath), initMountID)
	} else {
		contentPath = expandUserPath(homeDir, contentPath)
	}
	contentPath, err = filepath.Abs(contentPath)
	if err != nil {
		return initResult{}, err
	}
	if err := ensureInitTarget(manifestPath); err != nil {
		return initResult{}, err
	}
	if err := ensureInitTarget(contentPath); err != nil {
		return initResult{}, err
	}
	orgName := strings.TrimSpace(opts.orgName)
	if orgName == "" {
		orgName = displayNameFromID(opts.orgID)
	}
	doc := initManifestDocument(opts.orgID, orgName, umbrellaPath, contentPath)
	if err := writeInitManifestScaffold(manifestPath, doc); err != nil {
		return initResult{}, err
	}
	if err := writeInitContentScaffold(contentPath, opts.orgID, orgName); err != nil {
		return initResult{}, err
	}
	if validation := manifest.ValidateFile(manifestPath); len(validation.Errors) != 0 {
		return initResult{}, fmt.Errorf("generated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
	}
	if err := gitInitCommit(manifestPath); err != nil {
		return initResult{}, err
	}
	if err := gitInitCommit(contentPath); err != nil {
		return initResult{}, err
	}
	ref, err := manifest.Add(opts.home, opts.orgID, manifestPath)
	if err != nil {
		return initResult{}, err
	}
	syncResults, err := manifest.Sync(opts.home, []string{opts.orgID}, false, false, nil)
	if err != nil {
		return initResult{}, err
	}
	if manifestResultsFailed(syncResults) {
		return initResult{}, fmt.Errorf("manifest sync failed")
	}
	result := initResult{
		OrganizationID:   opts.orgID,
		OrganizationName: orgName,
		RepoPath:         manifestPath,
		ContentPath:      contentPath,
		Manifest:         ref,
		Sync:             syncResults,
	}
	result.NextCommands = initNextCommands(opts.home, opts.orgID)
	return result, nil
}

// initMountID names the content mount our init declares: the org workspace,
// "the actual content" — distinct from the private manifest (control plane).
// The content repo lives at the umbrella mount path for this id and is
// published as <org>-workspace.
const initMountID = "workspace"

func initManifestDocument(orgID, orgName, umbrellaPath, contentGitURL string) manifest.Document {
	return manifest.Document{
		ManifestVersion: 1,
		Organization: manifest.Organization{
			ID:   orgID,
			Name: orgName,
		},
		Umbrella: manifest.Umbrella{
			RecommendedPath: umbrellaPath,
		},
		AgentGuidance: manifest.AgentGuidance{
			Paths: []string{"agent-guidance/" + orgID + ".md"},
		},
		Skills: []manifest.Skill{
			{
				ID:          orgID + ":handbook",
				InstallSlug: orgID + "-handbook",
				Path:        "skills/" + orgID + "-handbook",
				Capabilities: []string{
					"meetings",
					"support",
					"fleet",
					"decisions",
					"workspace",
				},
				Requires: []string{"workspace:" + initMountID},
			},
		},
		Mounts: []manifest.Mount{
			{
				ID:   initMountID,
				Kind: "handbook",
				// Local until our publish rewrites it to the hosted remote.
				GitURL: contentGitURL,
				Mode:   "default",
			},
		},
	}
}

func ensureInitTarget(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, 0o755)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("init path %s exists and is not a directory", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("init path %s is not empty", path)
	}
	return nil
}

func writeInitManifestScaffold(root string, doc manifest.Document) error {
	if err := manifest.SaveDocument(root, doc); err != nil {
		return err
	}
	orgID := doc.Organization.ID
	orgName := doc.Organization.Name
	files := map[string]string{
		"README.md": initManifestREADME(orgID, orgName),
		filepath.Join("agent-guidance", orgID+".md"):           initAgentGuidance(orgName),
		filepath.Join("skills", orgID+"-handbook", "SKILL.md"): initSkillDoc(orgID, orgName),
		filepath.Join("catalog", "customers.json"):             "[]\n",
		filepath.Join("catalog", "products.json"):              "[]\n",
	}
	for path, content := range files {
		if err := writeInitFile(filepath.Join(root, path), content); err != nil {
			return err
		}
	}
	return nil
}

func writeInitContentScaffold(root, orgID, orgName string) error {
	files := map[string]string{
		"README.md":                             initContentREADME(orgName),
		filepath.Join("meetings", "README.md"):  initSectionREADME("Meetings", "Record meeting notes with our meetings add, then publish with our sync."),
		filepath.Join("support", "README.md"):   initSectionREADME("Support", "Record anonymized problem-to-solution notes with our support add."),
		filepath.Join("fleet", "README.md"):     initSectionREADME("Fleet", "Track deployed instances or devices with our fleet add and our fleet set."),
		filepath.Join("decisions", "README.md"): initSectionREADME("Decisions", "Keep durable decisions and their context here."),
		filepath.Join("projects", "README.md"):  initSectionREADME("Projects", "Keep project notes that agents should be able to read."),
		filepath.Join("policy", "README.md"):    initSectionREADME("Policy", "Keep operating policies and rules of engagement here."),
		filepath.Join("people", "README.md"):    initSectionREADME("People", "Keep public-safe team role notes here."),
	}
	for path, content := range files {
		if err := writeInitFile(filepath.Join(root, path), content); err != nil {
			return err
		}
	}
	return nil
}

func writeInitFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func initManifestREADME(orgID, orgName string) string {
	return fmt.Sprintf(`# %s Our AI Manifest

This private repository is the %s organization manifest: the control plane
that defines mounts, skills, the catalog, and agent guidance. Day-to-day
workspace content lives in the mounted content repositories it declares, not
here. Restrict write access to workspace administrators.

## Joining %s

Register this repository, sync it, and onboard:

`+"```sh"+`
our manifests add %s <git-url-of-this-repository>
our manifests sync %s
our setup
our ai codex
`+"```"+`

## Publish

Run `+"`our publish`"+` from any directory to create the private remotes for
this manifest and its content repositories, rewrite local mount URLs, and
push everything. Do not push this repository while mounts still reference
local paths.
`, orgName, orgName, orgName, orgID, orgID)
}

func initContentREADME(orgName string) string {
	return fmt.Sprintf(`# %s Handbook

Workspace content for %s: meetings, support records, fleet records,
decisions, projects, policy, and people notes. Record entries with the our
CLI (our meetings add, our support add, our fleet add) and publish with
our sync.
`, orgName, orgName)
}

func initAgentGuidance(orgName string) string {
	return fmt.Sprintf(`# %s Agent Guidance

- Use the generated workspace root as the operating context.
- Prefer the handbook mount for organization-specific facts before asking the operator.
- Keep private notes and operating content in this workspace repository, not in public code.
`, orgName)
}

func initSkillDoc(orgID, orgName string) string {
	return fmt.Sprintf(`---
name: %s-handbook
description: Use the %s handbook for meetings, support records, fleet records, decisions, policy, people, projects, and workspace-specific operating context.
---

# %s Handbook

Use this skill when work depends on %s-specific context from the Our AI
workspace. Start with the generated root guidance, then inspect the relevant
handbook directories or use the `+"`our meetings`"+`, `+"`our support`"+`, and
`+"`our fleet`"+` commands.
`, orgID, orgName, orgName, orgName)
}

func initSectionREADME(title, body string) string {
	return fmt.Sprintf("# %s\n\n%s\n", title, body)
}

func gitInitCommit(root string) error {
	if err := runGit(root, "init", "--quiet"); err != nil {
		return err
	}
	if err := runGit(root, "add", "."); err != nil {
		return err
	}
	if err := runGit(root, "commit", "--quiet", "-m", "Initial Our AI workspace"); err == nil {
		return nil
	}
	// No usable git identity configured; commit with a neutral fallback.
	return runGit(root, "-c", "user.name=Our AI", "-c", "user.email=our-ai@example.invalid", "commit", "--quiet", "-m", "Initial Our AI workspace")
}

func runGit(root string, args ...string) error {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", cmdArgs...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(out))
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
}

func isGitCheckout(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

func isBareGitRepo(path string) bool {
	if isGitCheckout(path) {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "HEAD")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(path, "objects"))
	return err == nil
}

func gitCmdOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func gitDirtyFiles(dir string) ([]string, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git status: %s", strings.TrimSpace(string(out)))
	}
	var files []string
	// Porcelain lines are "XY path"; do not trim the line first — the
	// two-character status may start with a significant space.
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) > 3 {
			files = append(files, strings.TrimSpace(line[3:]))
		}
	}
	return files, nil
}

type publishStep struct {
	Target  string `json:"target"`
	Repo    string `json:"repo,omitempty"`
	Action  string `json:"action"`
	URL     string `json:"url,omitempty"`
	Message string `json:"message,omitempty"`
}

type publishResult struct {
	Manifest        string        `json:"manifest"`
	Steps           []publishStep `json:"steps"`
	TeammateCommand string        `json:"teammate_command,omitempty"`
}

func (a app) runPublish(args []string) error {
	var home string
	var manifestName string
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("our publish", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "publish one registered manifest")
	fs.BoolVar(&printOnly, "print", false, "print the planned actions without creating or pushing anything")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, `Usage of our publish:
  our publish [--manifest NAME] [--home DIR] [--print] [--json]

Publishes the organization: creates private remotes for content repositories
and the manifest repository when they have none, rewrites local mount URLs to
the published remotes, commits that manifest change, and pushes everything.
Idempotent; existing remotes are adopted and pushed, never recreated.

Options:`)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("publish does not accept positional arguments")
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return err
	}
	result, err := a.publishOrg(home, doc, printOnly)
	if jsonOut {
		if printErr := printJSON(a.stdout, result); printErr != nil {
			return printErr
		}
		return err
	}
	for _, step := range result.Steps {
		line := fmt.Sprintf("publish\t%s\t%s", step.Target, step.Action)
		if step.URL != "" {
			line += "\t" + step.URL
		}
		if step.Message != "" {
			line += "\t" + step.Message
		}
		fmt.Fprintln(a.stdout, line)
	}
	if result.TeammateCommand != "" {
		fmt.Fprintf(a.stdout, "next\tjoin\t%s\n", result.TeammateCommand)
	}
	return err
}

func (a app) publishOrg(home string, doc registeredDoc, printOnly bool) (publishResult, error) {
	runner := a.publishRunner
	if runner == nil {
		runner = func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		}
	}
	orgID := doc.doc.Organization.ID
	result := publishResult{Manifest: doc.ref.Name}
	newDoc := doc.doc
	rewritten := false
	for i := range newDoc.Mounts {
		mount := newDoc.Mounts[i]
		if !localMountGitURL(mount.GitURL) {
			continue
		}
		sourcePath := mount.GitURL
		if isBareGitRepo(sourcePath) {
			// Already a served remote (e.g. a local bare repository).
			continue
		}
		if !isGitCheckout(sourcePath) {
			result.Steps = append(result.Steps, publishStep{Target: "content:" + mount.ID, Action: "failed", Message: "mount source " + sourcePath + " is not a git repository"})
			return result, fmt.Errorf("mount %q source %s is not a git repository", mount.ID, sourcePath)
		}
		repoName := orgID + "-" + mount.ID
		url, step, err := a.ensurePublishedRepo(sourcePath, repoName, "content:"+mount.ID, printOnly, runner)
		result.Steps = append(result.Steps, step)
		if err != nil {
			return result, err
		}
		if !printOnly && url != "" {
			newDoc.Mounts[i].GitURL = url
			rewritten = true
		}
	}
	if rewritten {
		if err := manifest.SaveDocument(doc.ref.LocalPath, newDoc); err != nil {
			return result, err
		}
		if err := runGit(doc.ref.LocalPath, "add", "--", "manifest.json"); err != nil {
			return result, err
		}
		// Commit only the URL rewrite so unrelated admin edits stay local.
		if err := runGit(doc.ref.LocalPath, "commit", "--quiet", "-m", "Point mounts at published repositories", "--", "manifest.json"); err != nil {
			if identityErr := runGit(doc.ref.LocalPath, "-c", "user.name=Our AI", "-c", "user.email=our-ai@example.invalid", "commit", "--quiet", "-m", "Point mounts at published repositories", "--", "manifest.json"); identityErr != nil {
				return result, identityErr
			}
		}
		result.Steps = append(result.Steps, publishStep{Target: "manifest", Action: "rewrote-mounts"})
	}
	manifestURL, step, err := a.ensurePublishedRepo(doc.ref.LocalPath, orgID+"-manifest", "manifest", printOnly, runner)
	result.Steps = append(result.Steps, step)
	if err != nil {
		return result, err
	}
	if !printOnly && manifestURL != "" {
		if filepath.IsAbs(doc.ref.GitURL) && !manifest.SameRemote(doc.ref.GitURL, manifestURL) {
			if _, err := manifest.Add(home, doc.ref.Name, manifestURL); err != nil {
				return result, err
			}
			if _, err := manifest.SetLocalPath(home, doc.ref.Name, doc.ref.LocalPath); err != nil {
				return result, err
			}
		}
		result.TeammateCommand = "our manifests add " + doc.ref.Name + " " + manifestURL
	}
	return result, nil
}

// ensurePublishedRepo adopts an existing origin remote (verifying GitHub
// repositories are private) or creates a private remote for the checkout,
// then pushes; it never recreates an existing remote.
func (a app) ensurePublishedRepo(path, repoName, target string, printOnly bool, runner manifest.Runner) (string, publishStep, error) {
	origin, err := gitCmdOutput(path, "remote", "get-url", "origin")
	if err == nil && origin != "" {
		if printOnly {
			return origin, publishStep{Target: target, Repo: repoName, Action: "would push", URL: origin}, nil
		}
		if _, ok := githubRepoSlug(origin); ok {
			visibility, visErr := a.githubRepoVisibility(origin)
			if visErr != nil {
				return "", publishStep{Target: target, Action: "failed", URL: origin, Message: "cannot verify repository visibility"}, fmt.Errorf("cannot verify visibility of %s: %v; check gh auth status and repository access", origin, visErr)
			}
			if !strings.EqualFold(visibility, "PRIVATE") {
				return "", publishStep{Target: target, Action: "failed", URL: origin, Message: "repository is not private"}, fmt.Errorf("%s is %s; make it private or point origin at a private repository before publishing", origin, strings.ToLower(visibility))
			}
		}
		if err := runGit(path, "push", "origin", "HEAD"); err != nil {
			return "", publishStep{Target: target, Action: "failed", URL: origin, Message: err.Error()}, err
		}
		return origin, publishStep{Target: target, Repo: repoName, Action: "pushed", URL: origin}, nil
	}
	if printOnly {
		return "", publishStep{Target: target, Repo: repoName, Action: "would create", Message: "gh repo create " + repoName + " --private --source " + path + " --push"}, nil
	}
	out, err := runner("gh", "repo", "create", repoName, "--private", "--source", path, "--remote", "origin", "--push")
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return "", publishStep{Target: target, Repo: repoName, Action: "failed", Message: message}, fmt.Errorf("gh repo create %s: %s", repoName, message)
	}
	origin, err = gitCmdOutput(path, "remote", "get-url", "origin")
	if err != nil || origin == "" {
		return "", publishStep{Target: target, Repo: repoName, Action: "failed", Message: "origin remote missing after gh repo create"}, fmt.Errorf("origin remote missing after gh repo create %s", repoName)
	}
	return origin, publishStep{Target: target, Repo: repoName, Action: "created", URL: origin}, nil
}

func localMountGitURL(gitURL string) bool {
	gitURL = strings.TrimSpace(gitURL)
	if gitURL == "" {
		return false
	}
	if filepath.IsAbs(gitURL) {
		return true
	}
	return strings.HasPrefix(gitURL, "./") || strings.HasPrefix(gitURL, "../") || strings.HasPrefix(gitURL, "~/")
}

func initNextCommands(home, orgID string) []initNextCommand {
	manifestFlag := ""
	if initManifestFlagNeeded(home) {
		manifestFlag = " --manifest " + shellQuote(orgID)
	}
	return []initNextCommand{
		{Action: "setup", Command: "our setup" + manifestFlag},
		{Action: "launch", Command: "our ai" + manifestFlag + " claude"},
		{Action: "launch", Command: "our ai" + manifestFlag + " codex"},
		{Action: "publish", Command: "our publish" + manifestFlag},
	}
}

func initManifestFlagNeeded(home string) bool {
	reg, err := manifest.LoadRegistry(home)
	return err == nil && len(reg.Manifests) > 1
}

func (a app) printInitResult(result initResult) {
	fmt.Fprintf(a.stdout, "manifest-repo\tcreated\t%s\n", result.RepoPath)
	fmt.Fprintf(a.stdout, "content-repo\tcreated\t%s\n", result.ContentPath)
	fmt.Fprintf(a.stdout, "manifest\tregistered\t%s\t%s\n", result.Manifest.Name, result.Manifest.LocalPath)
	for _, item := range result.Sync {
		fmt.Fprintf(a.stdout, "manifest-sync\t%s\t%s\n", item.Status, item.LocalPath)
	}
	for _, next := range result.NextCommands {
		fmt.Fprintf(a.stdout, "next\t%s\t%s\n", next.Action, next.Command)
	}
}

func displayNameFromID(id string) string {
	parts := strings.Split(id, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func (a app) runRoot(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var repoID string
	var legacyProductID string
	var noRefresh bool
	var noUpdateCheck bool
	fs := newFlagSet("our root", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "use a registered manifest when no umbrella is found")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.StringVar(&repoID, "repo", "", "print this repo's path under the umbrella")
	fs.StringVar(&legacyProductID, "product", "", "removed: use --repo")
	fs.BoolVar(&noRefresh, "no-refresh", false, "skip best-effort auto-refresh")
	fs.BoolVar(&noUpdateCheck, "no-update-check", false, "skip best-effort update notice")
	fs.Usage = func() {
		a.printRootUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
		"repo":     true,
		"product":  true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("root does not accept positional arguments")
	}
	root, err := resolveOurRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	a.maybeAutoRefresh(home, manifestName, root, root, noRefresh)
	a.maybeUpdateNotice(home, noUpdateCheck)
	target := root
	if legacyProductID != "" {
		return a.maybePrintStructuredCommandError(structuredCommandError{
			code:        "product_flag_removed",
			message:     "products are business catalog entries, not checkouts; --product was removed from our root",
			remediation: "use our root --repo " + legacyProductID + " (see our repos list)",
		})
	}
	if repoID != "" {
		target = umbrella.RepoPath(root, repoID)
	}
	fmt.Fprintln(a.stdout, target)
	return nil
}

func (a app) printRootUsage() {
	fmt.Fprintln(a.stderr, `Usage of our root:
  our root [--repo ID] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]

Examples:
  cd "$(our root)" && claude
  cd "$(our root --repo sample-service)" && codex

Options:`)
}

type launchCommandOpts struct {
	home          string
	manifestName  string
	umbrellaRoot  string
	repoID        string
	legacyProduct string
	sessionID     string
	noSession     bool
	onboard       bool
	printOnly     bool
	noRefresh     bool
	noUpdateCheck bool
}

func (a app) runLaunch(args []string) error {
	opts, harnessName, harnessArgs, help, err := parseLaunchArgs(args)
	if help {
		a.printLaunchUsage()
		return flag.ErrHelp
	}
	if err != nil {
		return err
	}
	h, err := harness.Parse(harnessName)
	if err != nil {
		return err
	}
	commandName := h.CommandName()
	root, err := resolveOurRoot(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return err
	}
	if err := validateLaunchSessionOptions(opts); err != nil {
		var structured structuredCommandError
		if errors.As(err, &structured) {
			a.printStructuredCommandError(structured)
			return errAlreadyPrinted
		}
		return err
	}
	if opts.printOnly {
		targetDir, err := a.launchTargetDir(opts, root)
		if err != nil {
			return a.maybePrintStructuredCommandError(err)
		}
		line := shellCommandLine(targetDir, commandName, harnessArgs)
		fmt.Fprintln(a.stdout, line)
		return nil
	}
	a.maybeAutoRefresh(opts.home, opts.manifestName, root, root, opts.noRefresh)
	a.maybeUpdateNotice(opts.home, opts.noUpdateCheck)

	doc, err := launchGuidanceDoc(opts.home, opts.manifestName, root)
	if err != nil {
		return err
	}
	check, err := guidance.Check(root, doc.ref.LocalPath, doc.doc)
	if err != nil {
		return err
	}
	if check.Status != "ok" {
		if !opts.onboard {
			a.printLaunchGuidanceBlock(check)
			return errAlreadyPrinted
		}
		if err := a.runOnboard(onboardArgsForLaunch(opts.home, doc.ref.Name, root, opts.noRefresh, opts.noUpdateCheck)); err != nil {
			return err
		}
		doc, err = loadSingleRegisteredDoc(opts.home, doc.ref.Name)
		if err != nil {
			return err
		}
		check, err = guidance.Check(root, doc.ref.LocalPath, doc.doc)
		if err != nil {
			return err
		}
		if check.Status != "ok" {
			a.printLaunchGuidanceBlock(check)
			return errAlreadyPrinted
		}
	}

	if err := a.ensureLaunchSelfSkill(h, opts.home); err != nil {
		return err
	}

	targetDir, err := a.launchTargetDir(opts, root)
	if err != nil {
		return a.maybePrintStructuredCommandError(err)
	}
	line := shellCommandLine(targetDir, commandName, harnessArgs)
	binary, err := a.lookupPath(commandName)
	if err != nil {
		fmt.Fprintf(a.stderr, "%s not found on PATH; run:\n%s\n", commandName, line)
		return errAlreadyPrinted
	}
	return a.runHarness(binary, harnessArgs, targetDir)
}

func validateLaunchSessionOptions(opts launchCommandOpts) error {
	if opts.noSession && opts.sessionID != "" {
		return fmt.Errorf("--session cannot be combined with --no-session")
	}
	if opts.legacyProduct != "" {
		return structuredCommandError{
			code:        "product_flag_removed",
			message:     "products are business catalog entries, not checkouts; --product was removed from our ai",
			remediation: "use our ai --no-session --repo " + opts.legacyProduct + " (see our repos list)",
		}
	}
	if opts.repoID != "" && !opts.noSession {
		if opts.sessionID != "" {
			return structuredCommandError{
				code:        "repo_requires_no_session",
				message:     "our ai --repo cannot be combined with --session; repo worktrees are not included in work sessions yet",
				remediation: "pass --no-session for a base repo launch, or omit --repo to resume the content session",
			}
		}
		return structuredCommandError{
			code:        "repo_requires_no_session",
			message:     "our ai --repo launches the base repo checkout; default session mode does not include repo worktrees yet",
			remediation: "pass --no-session for a base repo launch, or omit --repo to start a content session",
		}
	}
	return nil
}

func (a app) printStructuredCommandError(err structuredCommandError) {
	fmt.Fprintln(a.stderr, err.message)
	if err.remediation != "" {
		fmt.Fprintln(a.stderr, err.remediation)
	}
}

func (a app) maybePrintStructuredCommandError(err error) error {
	var structured structuredCommandError
	if errors.As(err, &structured) {
		a.printStructuredCommandError(structured)
		return errAlreadyPrinted
	}
	return err
}

func (a app) launchTargetDir(opts launchCommandOpts, root string) (string, error) {
	if opts.noSession {
		if opts.repoID != "" {
			return umbrella.RepoPath(root, opts.repoID), nil
		}
		return root, nil
	}
	if opts.sessionID != "" {
		session, err := worksession.Load(root, opts.sessionID)
		if err != nil {
			return "", err
		}
		if session.Status != worksession.StatusActive {
			return "", fmt.Errorf("session %s is %s; choose an active session or pass --no-session", session.ID, session.Status)
		}
		return session.Path, nil
	}
	specs, err := sessionMountSpecs(opts.home, opts.manifestName, root)
	if err != nil {
		return "", err
	}
	if len(specs) == 0 {
		return "", structuredCommandError{
			code:        "no_session_mounts",
			message:     "no synced content mounts eligible for a session worktree under " + root,
			remediation: "run our setup to clone the manifest's content mounts first, or pass --no-session for a base umbrella launch",
		}
	}
	session, err := worksession.Start(worksession.StartOptions{
		Root:   root,
		Mounts: specs,
	})
	if err != nil {
		return "", err
	}
	return session.Path, nil
}

func (a app) ensureLaunchSelfSkill(h harness.Harness, home string) error {
	if !h.IsFilesystem() {
		return nil
	}
	rows, err := selfskill.Inspect([]harness.Harness{h}, selfskill.Options{Home: home})
	if err != nil {
		return err
	}
	if len(rows) == 1 && rows[0].Status == "installed" {
		return nil
	}
	results, err := selfskill.Install([]harness.Harness{h}, selfskill.Options{Home: home, Link: true})
	if err != nil {
		return err
	}
	if len(results) != 1 {
		return fmt.Errorf("unexpected self-skill install result count: %d", len(results))
	}
	result := results[0]
	switch result.Status {
	case skills.StatusInstalled, skills.StatusUpdated:
		return nil
	default:
		a.printLaunchSelfSkillBlock(result)
		return errAlreadyPrinted
	}
}

func (a app) printLaunchSelfSkillBlock(result skills.Result) {
	fmt.Fprintf(a.stderr, "our self-skill %s for %s", result.Status, result.Harness)
	if result.TargetPath != "" {
		fmt.Fprintf(a.stderr, " at %s", result.TargetPath)
	}
	fmt.Fprintln(a.stderr)
	if result.Message != "" {
		fmt.Fprintln(a.stderr, result.Message)
	}
	if result.Err != nil {
		fmt.Fprintln(a.stderr, result.Err)
	}
	fmt.Fprintf(a.stderr, "run: our skills self install %s --force\n", result.Harness)
}

func (a app) printLaunchUsage() {
	fmt.Fprintln(a.stderr, `Usage of our ai:
  our ai [--session ID|--no-session] [--repo ID] [--setup] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check] [harness] [-- harness args...]

Harness defaults to claude-code. By default, filesystem harnesses launch from a
fresh work session. Harness flags go after the harness name.

Examples:
  our ai claude-code
  our ai codex --model gpt-5
  our ai --session 2026-06-11-work-ab12 codex
  our ai --no-session --repo sample-service codex
  our ai --print codex

Options:
  --home DIR        override home directory
  --manifest NAME   use a registered manifest when no umbrella is found
  --umbrella DIR    override umbrella root
  --session ID      resume an active work session instead of creating one
  --no-session      launch from the base umbrella or product checkout
  --repo ID         with --no-session, run from repos/<id> under the umbrella
  --setup           reconcile the umbrella first if guidance is stale or missing
  --no-refresh      skip best-effort auto-refresh
  --no-update-check skip best-effort update notice
  --print           print the resolved launch command without execing; in session mode this creates the session`)
}

func parseLaunchArgs(args []string) (launchCommandOpts, string, []string, bool, error) {
	var opts launchCommandOpts
	harnessName := "claude-code"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return opts, "", nil, true, nil
		case arg == "--":
			if i+1 >= len(args) {
				return opts, "", nil, false, fmt.Errorf("usage: our ai [harness]")
			}
			return opts, args[i+1], args[i+2:], false, nil
		case arg == "--setup":
			opts.onboard = true
		case arg == "--onboard":
			opts.onboard = true
		case arg == "--print":
			opts.printOnly = true
		case arg == "--no-session":
			opts.noSession = true
		case arg == "--no-refresh":
			opts.noRefresh = true
		case arg == "--no-update-check":
			opts.noUpdateCheck = true
		case arg == "--home" || arg == "--manifest" || arg == "--umbrella" || arg == "--repo" || arg == "--product" || arg == "--session":
			i++
			if i >= len(args) {
				return opts, "", nil, false, fmt.Errorf("missing value for %s", arg)
			}
			setLaunchValue(&opts, arg[2:], args[i])
		case strings.HasPrefix(arg, "--home="):
			opts.home = strings.TrimPrefix(arg, "--home=")
		case strings.HasPrefix(arg, "--manifest="):
			opts.manifestName = strings.TrimPrefix(arg, "--manifest=")
		case strings.HasPrefix(arg, "--umbrella="):
			opts.umbrellaRoot = strings.TrimPrefix(arg, "--umbrella=")
		case strings.HasPrefix(arg, "--repo="):
			opts.repoID = strings.TrimPrefix(arg, "--repo=")
		case strings.HasPrefix(arg, "--product="):
			opts.legacyProduct = strings.TrimPrefix(arg, "--product=")
		case strings.HasPrefix(arg, "--session="):
			opts.sessionID = strings.TrimPrefix(arg, "--session=")
		case isFlagArg(arg):
			return opts, "", nil, false, fmt.Errorf("unknown our ai flag %q; put harness flags after the harness name", arg)
		default:
			return opts, arg, args[i+1:], false, nil
		}
	}
	return opts, harnessName, nil, false, nil
}

func setLaunchValue(opts *launchCommandOpts, name, value string) {
	switch name {
	case "home":
		opts.home = value
	case "manifest":
		opts.manifestName = value
	case "umbrella":
		opts.umbrellaRoot = value
	case "repo":
		opts.repoID = value
	case "product":
		opts.legacyProduct = value
	case "session":
		opts.sessionID = value
	}
}

func resolveOurRoot(home, manifestName, explicit string) (string, error) {
	if manifestName != "" {
		doc, err := loadSingleRegisteredDoc(home, manifestName)
		if err != nil {
			return "", err
		}
		return umbrella.ResolveRoot(home, ".", explicit, doc.doc)
	}
	if explicit != "" {
		return resolveUmbrellaRoot(home, explicit)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return root, nil
	}
	doc, err := loadSingleRegisteredDoc(home, "")
	if err != nil {
		return "", err
	}
	return umbrella.ResolveRoot(home, ".", "", doc.doc)
}

func launchGuidanceDoc(home, manifestName, root string) (registeredDoc, error) {
	ws, err := umbrella.LoadWorkspace(root)
	if err == nil {
		if manifestName != "" && ws.ManifestRef != manifestName {
			return registeredDoc{}, fmt.Errorf("umbrella uses manifest %q, not %q", ws.ManifestRef, manifestName)
		}
		return loadSingleRegisteredDoc(home, ws.ManifestRef)
	}
	if !os.IsNotExist(err) {
		return registeredDoc{}, err
	}
	return loadSingleRegisteredDoc(home, manifestName)
}

func onboardArgsForLaunch(home, manifestName, root string, noRefresh, noUpdateCheck bool) []string {
	args := []string{"--manifest", manifestName, "--umbrella", root}
	if home != "" {
		args = append(args, "--home", home)
	}
	if noRefresh {
		args = append(args, "--no-refresh")
	}
	if noUpdateCheck {
		args = append(args, "--no-update-check")
	}
	return args
}

func (a app) printLaunchGuidanceBlock(result guidance.CheckResult) {
	fmt.Fprintf(a.stderr, "workspace guidance %s at %s\n", result.Status, result.AgentsPath)
	if result.Status == "alias-broken" {
		fmt.Fprintf(a.stderr, "CLAUDE.md alias is not current at %s\n", result.ClaudePath)
	}
	if result.Message != "" {
		fmt.Fprintln(a.stderr, result.Message)
	}
}

func shellCommandLine(dir, command string, args []string) string {
	parts := []string{"cd", shellQuote(dir), "&&", shellQuote(command)}
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
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

func (a app) lookupPath(name string) (string, error) {
	if a.lookPath != nil {
		return a.lookPath(name)
	}
	return exec.LookPath(name)
}

func (a app) runHarness(path string, args []string, dir string) error {
	if a.execHarness != nil {
		return a.execHarness(path, args, dir)
	}
	cmd := exec.Command(path, args...)
	cmd.Dir = dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitCodeError{code: exitErr.ExitCode()}
		}
		return err
	}
	return nil
}

func (a app) runVersion(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("version does not accept arguments")
	}
	fmt.Fprintln(a.stdout, bundle.Version())
	return nil
}

func (a app) runUpdate(args []string) error {
	var checkOnly bool
	var jsonOut bool
	var yes bool
	var targetVersion string
	fs := newFlagSet("our update", a.stderr)
	fs.BoolVar(&checkOnly, "check", false, "check for an update without installing it")
	fs.StringVar(&targetVersion, "version", "", "install a specific release version")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.BoolVar(&yes, "yes", false, "accept the update operation")
	fs.Usage = func() {
		a.printUpdateUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{"version": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("update does not accept positional arguments")
	}
	_ = yes
	result, err := selfupdate.Update(context.Background(), selfupdate.Options{
		CurrentVersion: a.currentOurVersion(),
		TargetVersion:  targetVersion,
		CheckOnly:      checkOnly,
		TargetPath:     a.updateTargetPath,
		Source:         a.updateSource,
	})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintln(a.stdout, result.Message)
	return nil
}

func (a app) printUpdateUsage() {
	fmt.Fprintln(a.stderr, `Usage of our update:
  our update [--check] [--version X.Y.Z] [--json] [--yes]

Options:`)
}

func (a app) currentOurVersion() string {
	if a.updateCurrentVersion != "" {
		return a.updateCurrentVersion
	}
	return bundle.Version()
}

func (a app) maybeUpdateNotice(home string, noUpdateCheck bool) {
	if noUpdateCheck || os.Getenv("OUR_NO_UPDATE_CHECK") != "" {
		return
	}
	notice, err := selfupdate.CheckNotice(context.Background(), selfupdate.NoticeOptions{
		CurrentVersion: a.currentOurVersion(),
		Home:           home,
		Source:         a.updateSource,
		TTL:            selfupdate.UpdateCheckTTLFromEnv(),
		Now:            a.updateNow,
	})
	if err != nil || !notice.UpdateAvailable {
		return
	}
	fmt.Fprintf(a.stderr, "a newer our (v%s) is available; run `our update`\n", notice.LatestVersion)
}

func (a app) runSync(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var backend string
	var publish string
	var scope string
	var message string
	var printOnly bool
	var noDerived bool
	var jsonOut bool
	fs := newFlagSet("our sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.StringVar(&backend, "backend", "auto", "sync backend: auto, gnit, or builtin")
	fs.StringVar(&publish, "publish", "auto", "publish mode: auto, never, direct, or pr")
	fs.StringVar(&scope, "scope", "all", "sync scope: all, local, content, manifest, or repos")
	fs.StringVar(&message, "message", "", "commit message for newly committed content")
	fs.BoolVar(&printOnly, "print", false, "print planned actions without changing files or remotes")
	fs.BoolVar(&noDerived, "no-derived", false, "skip derived skill/guidance reconciliation after manifest changes")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		a.printSyncUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
		"backend":  true,
		"publish":  true,
		"scope":    true,
		"message":  true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("sync does not accept positional arguments")
	}
	if !validSyncBackend(backend) {
		return fmt.Errorf("--backend must be one of auto, gnit, or builtin")
	}
	if !validSyncPublish(publish) {
		return fmt.Errorf("--publish must be one of auto, never, direct, or pr")
	}
	if !validSyncScope(scope) {
		return fmt.Errorf("--scope must be one of all, local, content, manifest, or repos")
	}
	publishExplicit := flagWasSet(fs, "publish")
	if !publishExplicit {
		var err error
		publish, err = a.defaultSyncPublish(home, manifestName, publish)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	}
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, scope)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	var localMountBlocks []syncer.Result
	if publish != "never" && syncScopeAllowsDerived(scope) {
		localMountBlocks, err = a.localMountSyncBlocks(home, manifestName)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
		entries = withoutBlockedManifestEntries(entries, localMountBlocks)
	}
	gnitRoot := ""
	var sessionHolds []syncer.SessionHold
	if root, err := resolveOurRoot(home, manifestName, umbrellaRoot); err == nil {
		gnitRoot = findGnitWorkspaceRoot(root)
		sessionHolds, err = collectSessionHolds(root)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	}
	effectiveBackend := backend
	backendMessage := ""
	if effectiveBackend == "auto" {
		if publish != "pr" && gnitRoot != "" {
			effectiveBackend = "gnit"
		} else {
			effectiveBackend = "builtin"
			if publish == "pr" {
				backendMessage = "PR mode is handled by Our AI/gh; Gnit remains the publish substrate after PR support lands"
			} else {
				backendMessage = "Gnit workspace not initialized; using Our AI guard backend"
			}
		}
	}
	report := syncer.Run(entries, syncer.Options{
		Backend:      effectiveBackend,
		GnitRoot:     gnitRoot,
		Publish:      publish,
		DryRun:       printOnly,
		Message:      message,
		Visibility:   a.githubRepoVisibility,
		SessionHolds: sessionHolds,
	})
	if backendMessage != "" && report.BackendMessage == "" {
		report.BackendMessage = backendMessage
	}
	report.Results = append(report.Results, localMountBlocks...)
	var derived *derivedReconcileReport
	if !printOnly && !noDerived && syncScopeAllowsDerived(scope) {
		if changedManifest, ok := changedManifestForDerived(report); ok {
			if root, hasRoot, err := existingUmbrellaRoot(home, changedManifest, umbrellaRoot); err != nil {
				return a.maybeJSONError(jsonOut, err)
			} else if hasRoot {
				derivedReport, err := a.reconcileDerived(home, changedManifest, root)
				if err != nil {
					return a.maybeJSONError(jsonOut, err)
				}
				derived = &derivedReport
			}
		}
	}
	if !printOnly {
		if err := a.saveLastSyncReport(home, manifestName, umbrellaRoot, report); err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, syncCommandReport{Report: report, Derived: derived}); err != nil {
			return err
		}
	} else {
		a.printSyncReport(report)
		if derived != nil {
			a.printDerivedReconcileReport(*derived)
		}
	}
	if syncReportFailed(report) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more sync operations failed"))
	}
	if derivedReportFailed(derived) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more derived reconciliation operations failed"))
	}
	return nil
}

func (a app) localMountSyncBlocks(home, manifestName string) ([]syncer.Result, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var out []syncer.Result
	for _, doc := range docs {
		localMounts := localMountGitURLs(doc.doc)
		if len(localMounts) == 0 {
			continue
		}
		out = append(out, syncer.Result{
			Manifest:  doc.ref.Name,
			ID:        doc.ref.Name,
			Role:      "manifest",
			Kind:      "manifest",
			GitURL:    doc.ref.GitURL,
			LocalPath: doc.ref.LocalPath,
			Status:    "held back",
			Message:   "manifest has local mount URL(s): " + strings.Join(localMounts, ", ") + "; run our publish --manifest " + doc.ref.Name,
		})
	}
	return out, nil
}

func withoutBlockedManifestEntries(entries []syncer.Entry, blocks []syncer.Result) []syncer.Entry {
	if len(blocks) == 0 {
		return entries
	}
	blocked := map[string]bool{}
	for _, block := range blocks {
		name := block.Manifest
		if name == "" {
			name = block.ID
		}
		blocked[name] = true
	}
	out := entries[:0]
	for _, entry := range entries {
		name := entry.Manifest
		if name == "" {
			name = entry.ID
		}
		if entry.Role == "manifest" && blocked[name] {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func localMountGitURLs(doc manifest.Document) []string {
	var out []string
	for _, mount := range manifest.EffectiveMounts(doc) {
		if localMountGitURL(mount.GitURL) {
			out = append(out, mount.ID+"="+mount.GitURL)
		}
	}
	sort.Strings(out)
	return out
}

type syncCommandReport struct {
	syncer.Report
	Derived *derivedReconcileReport `json:"derived,omitempty"`
}

func (a app) printSyncUsage() {
	fmt.Fprintln(a.stderr, `Usage of our sync:
  our sync [--backend auto|gnit|builtin] [--publish auto|never|direct|pr] [--scope all|local|content|manifest|repos] [--manifest NAME] [--home DIR] [--umbrella DIR] [--message TEXT] [--no-derived] [--print] [--json]

Synchronizes registered Our AI repositories in both directions. The default
backend uses Gnit when the umbrella is a Gnit workspace; otherwise Our AI uses a
guarded Git fallback until bootstrap/canonicalization is complete. The default
publish mode only pushes private content-only changes when sibling checkouts of
the same remote are clean. Direct mode can push existing commits, but dirty
non-content changes still require an explicit admin or review workflow.
Non-print runs write .our/last-sync.json when an umbrella is present. When a
manifest checkout changes, sync also reconciles generated guidance and
manifest skills unless --no-derived is passed.`)
}

func syncScopeAllowsDerived(scope string) bool {
	switch scope {
	case "all", "local", "manifest":
		return true
	default:
		return false
	}
}

func changedManifestForDerived(report syncer.Report) (string, bool) {
	seen := map[string]bool{}
	var names []string
	for _, result := range report.Results {
		if result.Role != "manifest" {
			continue
		}
		if !syncManifestResultChanged(result) {
			continue
		}
		name := result.Manifest
		if name == "" {
			name = result.ID
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) != 1 {
		return "", false
	}
	return names[0], true
}

func syncManifestResultChanged(result syncer.Result) bool {
	if result.Status == "pulled" || result.Status == "pushed" {
		return true
	}
	return len(result.Changed) != 0 && result.Status != "dry-run"
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func (a app) defaultSyncPublish(home, manifestName, fallback string) (string, error) {
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return fallback, nil
	}
	if doc.doc.Sync.PublishPolicy == "" {
		return fallback, nil
	}
	return doc.doc.Sync.PublishPolicy, nil
}

func validSyncBackend(value string) bool {
	switch value {
	case "auto", "gnit", "builtin":
		return true
	default:
		return false
	}
}

func validSyncPublish(value string) bool {
	switch value {
	case "auto", "never", "direct", "pr":
		return true
	default:
		return false
	}
}

func validSyncScope(value string) bool {
	switch value {
	case "all", "local", "content", "manifest", "repos":
		return true
	default:
		return false
	}
}

func (a app) collectSyncEntries(home, manifestName, umbrellaRoot, scope string) ([]syncer.Entry, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var entries []syncer.Entry
	for _, doc := range docs {
		manifestWanted := scope == "all" || scope == "local" || scope == "manifest"
		contentWanted := scope == "all" || scope == "local" || scope == "content"
		if manifestWanted {
			entries = append(entries, syncer.Entry{
				Manifest:  doc.ref.Name,
				ID:        doc.ref.Name,
				Role:      "manifest",
				Kind:      "manifest",
				GitURL:    doc.ref.GitURL,
				LocalPath: doc.ref.LocalPath,
			})
		}
		if contentWanted {
			mounts, err := workspace.ListMounts(home, doc.ref.Name, umbrellaRoot)
			if err != nil {
				return nil, err
			}
			for _, mount := range mounts {
				entries = append(entries, syncer.Entry{
					Manifest:     mount.Manifest,
					ID:           mount.ID,
					Role:         "content",
					Kind:         mount.Kind,
					GitURL:       mount.GitURL,
					LocalPath:    mount.LocalPath,
					ContentPaths: syncContentPaths(mount),
				})
			}
		}
		if scope == "all" || scope == "local" || scope == "repos" {
			productEntries, err := a.collectSyncRepoEntries(home, doc, umbrellaRoot)
			if err != nil {
				return nil, err
			}
			entries = append(entries, productEntries...)
		}
	}
	entries = dedupeSyncEntries(entries)
	if scope == "local" {
		entries = existingSyncEntries(entries)
	}
	return entries, nil
}

func (a app) collectSyncRepoEntries(home string, doc registeredDoc, umbrellaRoot string) ([]syncer.Entry, error) {
	root, err := umbrella.ResolveRoot(home, "", umbrellaRoot, doc.doc)
	if err != nil {
		return nil, err
	}
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []syncer.Entry
	for _, id := range state.SelectedRepos {
		repo, ok, err := manifest.FindRepo(home, doc.ref.Name, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		entry := repoEntry(doc, root, repo)
		entries = append(entries, syncer.Entry{
			Manifest:  entry.Manifest,
			ID:        entry.ID,
			Role:      "repo",
			Kind:      entry.Kind,
			GitURL:    entry.GitURL,
			LocalPath: entry.LocalPath,
		})
	}
	return entries, nil
}

func syncContentPaths(entry workspace.Entry) []string {
	return mountContentPaths(entry.Kind, entry.IncludePaths)
}

func mountContentPaths(kind string, includePaths []string) []string {
	if len(includePaths) != 0 {
		return append([]string(nil), includePaths...)
	}
	switch kind {
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

func dedupeSyncEntries(entries []syncer.Entry) []syncer.Entry {
	seen := map[string]bool{}
	var out []syncer.Entry
	for _, entry := range entries {
		key := entry.Role + "\x00" + entry.LocalPath
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	return out
}

func existingSyncEntries(entries []syncer.Entry) []syncer.Entry {
	var out []syncer.Entry
	for _, entry := range entries {
		if _, err := os.Stat(entry.LocalPath); err == nil {
			out = append(out, entry)
		}
	}
	return out
}

func (a app) githubRepoVisibility(gitURL string) (string, error) {
	slug, ok := githubRepoSlug(gitURL)
	if !ok {
		return "", fmt.Errorf("not a GitHub repository")
	}
	cmd := exec.Command("gh", "repo", "view", slug, "--json", "visibility", "--jq", ".visibility")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func githubRepoSlug(gitURL string) (string, bool) {
	value := strings.TrimSpace(gitURL)
	value = strings.TrimSuffix(value, ".git")
	switch {
	case strings.HasPrefix(value, "https://github.com/"):
		return strings.TrimPrefix(value, "https://github.com/"), true
	case strings.HasPrefix(value, "git@github.com:"):
		return strings.TrimPrefix(value, "git@github.com:"), true
	case strings.HasPrefix(value, "ssh://git@github.com/"):
		return strings.TrimPrefix(value, "ssh://git@github.com/"), true
	default:
		return "", false
	}
}

func (a app) printSyncReport(report syncer.Report) {
	if report.Backend != "" {
		line := "# backend: " + report.Backend
		if report.GnitRoot != "" {
			line += "\tgnit_root=" + report.GnitRoot
		}
		if report.BackendMessage != "" {
			line += "\t" + report.BackendMessage
		}
		fmt.Fprintln(a.stdout, line)
	}
	for _, result := range report.Results {
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s", result.Manifest, result.ID, result.Role, result.Status, result.LocalPath)
		if result.Ahead != 0 || result.Behind != 0 {
			line += fmt.Sprintf("\tahead=%d behind=%d", result.Ahead, result.Behind)
		}
		if len(result.Dirty) != 0 {
			line += "\tdirty=" + strings.Join(result.Dirty, ",")
		}
		if len(result.Changed) != 0 {
			line += "\tchanged=" + strings.Join(result.Changed, ",")
		}
		if result.Message != "" {
			line += "\t" + strings.ReplaceAll(result.Message, "\n", " ")
		}
		if result.Error != "" {
			line += "\t" + result.Error
		}
		fmt.Fprintln(a.stdout, line)
	}
}

func syncReportFailed(report syncer.Report) bool {
	for _, result := range report.Results {
		if result.Status == "failed" {
			return true
		}
	}
	return false
}

const lastSyncFile = "last-sync.json"
const autoRefreshFile = "auto-refresh.json"
const defaultRefreshTTL = 6 * time.Hour

type lastSyncAudit struct {
	SchemaVersion int           `json:"schema_version"`
	SavedAt       string        `json:"saved_at"`
	Report        syncer.Report `json:"report"`
}

type autoRefreshState struct {
	SchemaVersion int                          `json:"schema_version"`
	Repos         map[string]autoRefreshRecord `json:"repos,omitempty"`
}

type autoRefreshRecord struct {
	LastAutoRefresh string `json:"last_auto_refresh"`
}

func (a app) saveLastSyncReport(home, manifestName, umbrellaRoot string, report syncer.Report) error {
	root, ok, err := existingUmbrellaRoot(home, manifestName, umbrellaRoot)
	if err != nil || !ok {
		return err
	}
	audit := lastSyncAudit{
		SchemaVersion: 1,
		SavedAt:       time.Now().UTC().Format(time.RFC3339),
		Report:        report,
	}
	data, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(root, umbrella.DirName, lastSyncFile), data, 0o644)
}

func existingUmbrellaRoot(home, manifestName, explicit string) (string, bool, error) {
	if explicit != "" {
		root, err := resolveUmbrellaRoot(home, explicit)
		if err != nil {
			return "", false, err
		}
		return existingUmbrellaRootStatus(root, manifestName)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return existingUmbrellaRootStatus(root, manifestName)
	}
	root, err := resolveOurRoot(home, manifestName, "")
	if err != nil {
		return "", false, nil
	}
	return existingUmbrellaRootStatus(root, manifestName)
}

type umbrellaManifestMismatchError struct {
	Root string
	Have string
	Want string
}

func (e umbrellaManifestMismatchError) Error() string {
	return fmt.Sprintf("umbrella %s uses manifest %q, not %q", e.Root, e.Have, e.Want)
}

func existingUmbrellaRootStatus(root, manifestName string) (string, bool, error) {
	if !hasOurDir(root) {
		return root, false, nil
	}
	if manifestName == "" {
		return root, true, nil
	}
	ws, err := umbrella.LoadWorkspace(root)
	if err != nil {
		return root, false, err
	}
	if ws.ManifestRef != "" && ws.ManifestRef != manifestName {
		return root, false, umbrellaManifestMismatchError{
			Root: root,
			Have: ws.ManifestRef,
			Want: manifestName,
		}
	}
	return root, true, nil
}

func hasOurDir(root string) bool {
	info, err := os.Stat(filepath.Join(root, umbrella.DirName))
	return err == nil && info.IsDir()
}

func loadLastSyncAudit(root string) (lastSyncAudit, bool, error) {
	path := filepath.Join(root, umbrella.DirName, lastSyncFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return lastSyncAudit{}, false, nil
	}
	if err != nil {
		return lastSyncAudit{}, false, err
	}
	var audit lastSyncAudit
	if err := json.Unmarshal(data, &audit); err != nil {
		return lastSyncAudit{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	return audit, true, nil
}

func (a app) maybeAutoRefresh(home, manifestName, umbrellaRoot, root string, noRefresh bool) {
	if noRefresh || os.Getenv("OUR_NO_AUTO_REFRESH") != "" || root == "" || !hasOurDir(root) {
		return
	}
	ttl := autoRefreshTTL()
	state, err := loadAutoRefreshState(root)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: auto-refresh skipped: %v\n", err)
		return
	}
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, "local")
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: auto-refresh skipped: %v\n", err)
		return
	}
	now := time.Now().UTC()
	changed := false
	for _, entry := range entries {
		if !autoRefreshEntryAllowed(entry) {
			continue
		}
		key := autoRefreshKey(entry)
		if !autoRefreshDue(state, key, now, ttl) {
			continue
		}
		result := syncer.Inspect([]syncer.Entry{entry}, syncer.InspectOptions{Fetch: true})[0]
		item, _, include := doctorFixFreshnessItem(entry, result)
		state.Repos[key] = autoRefreshRecord{LastAutoRefresh: now.Format(time.RFC3339)}
		changed = true
		if include && item.Status == "fixed" {
			fmt.Fprintf(a.stderr, "refresh\t%s\tfixed\t%s\n", item.Name, item.Message)
		} else if include && item.Status == "error" {
			fmt.Fprintf(a.stderr, "warning: auto-refresh %s failed: %s\n", item.Name, item.Message)
		} else if notice, ok := freshnessNotice(result); ok {
			fmt.Fprintf(a.stderr, "notice\t%s\t%s\n", doctorFreshnessName(result), notice)
		}
	}
	if changed {
		if err := saveAutoRefreshState(root, state); err != nil {
			fmt.Fprintf(a.stderr, "warning: auto-refresh state not saved: %v\n", err)
		}
	}
}

// freshnessNotice describes a checkout the auto-refresh could not converge,
// with the command that reconciles it. Failed and unknown inspections are
// reported elsewhere; converged checkouts return false.
func freshnessNotice(result syncer.Result) (string, bool) {
	if result.Status == "failed" || result.Status == "unknown" || result.BehindUnknown {
		return "", false
	}
	dirty := len(result.Dirty)
	switch {
	case result.Ahead > 0 && result.Behind > 0:
		return fmt.Sprintf("diverged from remote (ahead %d, behind %d); run `our doctor` and reconcile manually", result.Ahead, result.Behind), true
	case dirty > 0 && result.Behind > 0:
		return fmt.Sprintf("behind remote by %d with %d uncommitted file(s); commit or stash, then run `our sync`", result.Behind, dirty), true
	case dirty > 0:
		return fmt.Sprintf("%d uncommitted file(s); run `our sync` to review and publish", dirty), true
	case result.Ahead > 0:
		return fmt.Sprintf("ahead of remote by %d unpublished commit(s); run `our sync` to publish", result.Ahead), true
	case result.Behind > 0:
		return fmt.Sprintf("behind remote by %d; run `our sync`", result.Behind), true
	}
	return "", false
}

func autoRefreshTTL() time.Duration {
	value := strings.TrimSpace(os.Getenv("OUR_REFRESH_TTL"))
	if value == "" {
		return defaultRefreshTTL
	}
	ttl, err := time.ParseDuration(value)
	if err != nil {
		return defaultRefreshTTL
	}
	return ttl
}

func loadAutoRefreshState(root string) (autoRefreshState, error) {
	path := filepath.Join(root, umbrella.DirName, autoRefreshFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return autoRefreshState{SchemaVersion: 1, Repos: map[string]autoRefreshRecord{}}, nil
	}
	if err != nil {
		return autoRefreshState{}, err
	}
	var state autoRefreshState
	if err := json.Unmarshal(data, &state); err != nil {
		return autoRefreshState{}, fmt.Errorf("read %s: %w", path, err)
	}
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	if state.Repos == nil {
		state.Repos = map[string]autoRefreshRecord{}
	}
	return state, nil
}

func saveAutoRefreshState(root string, state autoRefreshState) error {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	if state.Repos == nil {
		state.Repos = map[string]autoRefreshRecord{}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(root, umbrella.DirName, autoRefreshFile), data, 0o644)
}

func autoRefreshEntryAllowed(entry syncer.Entry) bool {
	return entry.Role == "manifest" || entry.Role == "content"
}

func autoRefreshKey(entry syncer.Entry) string {
	manifestName := entry.Manifest
	if manifestName == "" {
		manifestName = entry.ID
	}
	return entry.Role + ":" + manifestName + ":" + entry.ID
}

func autoRefreshDue(state autoRefreshState, key string, now time.Time, ttl time.Duration) bool {
	record, ok := state.Repos[key]
	if !ok || record.LastAutoRefresh == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, record.LastAutoRefresh)
	if err != nil {
		return true
	}
	return !last.Add(ttl).After(now)
}

func findGnitWorkspaceRoot(start string) string {
	for dir := filepath.Clean(start); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".gnit", "roster.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}

func (a app) runMeetings(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing meetings subcommand")
	}
	switch args[0] {
	case "list":
		return a.runMeetingsList(args[1:])
	case "search":
		return a.runMeetingsSearch(args[1:])
	case "get":
		return a.runMeetingsGet(args[1:])
	case "add":
		return a.runMeetingsAdd(args[1:])
	case "-h", "--help", "help":
		a.printMeetingsUsage()
		return nil
	default:
		return fmt.Errorf("unknown meetings subcommand %q", args[0])
	}
}

func (a app) printMeetingsUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our meetings list [--manifest NAME] [--workspace ID] [--since DATE] [--customer ID] [--partner ID] [--product ID] [--json]
  our meetings search <text> [--manifest NAME] [--workspace ID] [--customer ID] [--partner ID] [--product ID] [--json]
  our meetings get <id|path> [--manifest NAME] [--workspace ID] [--json]
  our meetings add <slug> [--manifest NAME] [--workspace ID] [--date DATE] [--title TEXT] [--customer ID] [--attendees NAME] [--partner ID] [--product ID] [--source-id ID] [--print] [--json]

Meeting commands use local markdown files under workspace meetings/ directories.`)
}

func (a app) runMeetingsList(args []string) error {
	opts, rest, err := parseMeetingReadOpts("our meetings list", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("meetings list does not accept positional arguments")
	}
	roots, err := meetingRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter := a.resolveMeetingFilter(opts.home, opts.manifestName, opts.umbrellaRoot, opts.filter())
	found, err := meetings.List(roots, filter)
	if err != nil {
		return err
	}
	return a.printMeetings(found, opts.jsonOut)
}

func (a app) runMeetingsSearch(args []string) error {
	opts, rest, err := parseMeetingReadOpts("our meetings search", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our meetings search <text>")
	}
	roots, err := meetingRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter := a.resolveMeetingFilter(opts.home, opts.manifestName, opts.umbrellaRoot, opts.filter())
	found, err := meetingSearch(roots, rest[0], filter)
	if err != nil {
		return err
	}
	return a.printMeetings(found, opts.jsonOut)
}

func (a app) runMeetingsGet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("our meetings get", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our meetings get <id|path>")
	}
	roots, err := meetingRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	meeting, content, err := meetings.Get(roots, rest[0])
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Meeting meetings.Meeting `json:"meeting"`
			Content string           `json:"content"`
		}{Meeting: meeting, Content: content})
	}
	fmt.Fprint(a.stdout, content)
	return nil
}

func (a app) runMeetingsAdd(args []string) error {
	var opts meetingAddOpts
	fs := newFlagSet("our meetings add", a.stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.date, "date", "", "meeting date, YYYY-MM-DD")
	fs.StringVar(&opts.title, "title", "", "meeting title")
	fs.StringVar(&opts.customer, "customer", "", "customer ID")
	fs.Var(&opts.attendees, "attendees", "meeting attendee (repeatable)")
	fs.Var(&opts.partners, "partner", "partner ID (repeatable)")
	fs.StringVar(&opts.product, "product", "", "product ID")
	fs.StringVar(&opts.sourceID, "source-id", "", "source system identifier")
	fs.StringVar(&opts.status, "status", "", "meeting status")
	fs.BoolVar(&opts.printOnly, "print", false, "print the scaffold without writing a file")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":      true,
		"manifest":  true,
		"workspace": true,
		"umbrella":  true,
		"date":      true,
		"title":     true,
		"customer":  true,
		"attendees": true,
		"partner":   true,
		"product":   true,
		"source-id": true,
		"status":    true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our meetings add <slug>")
	}
	roots, err := meetingRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(roots) != 1 {
		return fmt.Errorf("meetings add requires exactly one workspace; pass --manifest and --workspace")
	}
	customer := a.resolveCustomerForWrite(opts.home, opts.manifestName, opts.umbrellaRoot, opts.customer)
	meeting, content, err := meetings.Add(roots[0], rest[0], meetings.AddOptions{
		Date:      opts.date,
		Title:     opts.title,
		Customer:  customer,
		Attendees: []string(opts.attendees),
		Partners:  []string(opts.partners),
		Product:   opts.product,
		SourceID:  opts.sourceID,
		Status:    opts.status,
		DryRun:    opts.printOnly,
	})
	if err != nil {
		return err
	}
	if !opts.printOnly {
		if err := markRecordIntentToAdd(roots[0], meeting.Path); err != nil {
			return err
		}
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Meeting meetings.Meeting `json:"meeting"`
			Content string           `json:"content,omitempty"`
		}{Meeting: meeting, Content: content})
	}
	if opts.printOnly {
		fmt.Fprintf(a.stdout, "# path: %s\n%s", meeting.Path, content)
		return nil
	}
	fmt.Fprintln(a.stdout, meeting.Path)
	return nil
}

func (a app) runSupport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing support subcommand")
	}
	switch args[0] {
	case "list":
		return a.runSupportList(args[1:])
	case "search":
		return a.runSupportSearch(args[1:])
	case "get":
		return a.runSupportGet(args[1:])
	case "add":
		return a.runSupportAdd(args[1:])
	case "-h", "--help", "help":
		a.printSupportUsage()
		return nil
	default:
		return fmt.Errorf("unknown support subcommand %q", args[0])
	}
}

func (a app) printSupportUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our support list [--manifest NAME] [--workspace ID] [--since DATE] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
  our support search <text> [--manifest NAME] [--workspace ID] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
  our support get <id|path> [--manifest NAME] [--workspace ID] [--json]
  our support add <slug> [--manifest NAME] [--workspace ID] [--date DATE] [--title TEXT] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--observed-by MEMBER] [--approved-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--status open|workaround|resolved] [--feature-candidate] [--print] [--json]

Support commands use local markdown files under workspace support/ directories.
Repeat --identifier for each device, order, or asset identifier (for example a
workstation name, equipment ID, functional location, or sales order number) so
records can be linked to the same equipment later. --claimed-by names the org
member who worked the problem, --observed-by (repeatable) anyone else involved,
and --approved-by the member who signed off on the record; agents should leave
approved_by empty unless the operator explicitly approves.`)
}

func (a app) runSupportList(args []string) error {
	opts, rest, err := parseSupportReadOpts("our support list", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("support list does not accept positional arguments")
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter := a.resolveSupportFilter(opts.home, opts.manifestName, opts.umbrellaRoot, opts.filter())
	found, err := support.List(roots, filter)
	if err != nil {
		return err
	}
	return a.printSupport(found, opts.jsonOut)
}

func (a app) runSupportSearch(args []string) error {
	opts, rest, err := parseSupportReadOpts("our support search", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our support search <text>")
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter := a.resolveSupportFilter(opts.home, opts.manifestName, opts.umbrellaRoot, opts.filter())
	found, err := supportSearch(roots, rest[0], filter)
	if err != nil {
		return err
	}
	return a.printSupport(found, opts.jsonOut)
}

func (a app) runSupportGet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("our support get", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our support get <id|path>")
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	record, content, err := support.Get(roots, rest[0])
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record  support.Record `json:"record"`
			Content string         `json:"content"`
		}{Record: record, Content: content})
	}
	fmt.Fprint(a.stdout, content)
	return nil
}

func (a app) runSupportAdd(args []string) error {
	var opts supportAddOpts
	fs := newFlagSet("our support add", a.stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.date, "date", "", "support record date, YYYY-MM-DD")
	fs.StringVar(&opts.title, "title", "", "support record title")
	fs.StringVar(&opts.customer, "customer", "", "canonical customer ID")
	fs.Var(&opts.identifiers, "identifier", "device, order, or asset identifier (repeatable)")
	fs.StringVar(&opts.claimedBy, "claimed-by", "", "org member who worked the problem")
	fs.Var(&opts.observedBy, "observed-by", "org member who was involved or observed (repeatable)")
	fs.StringVar(&opts.approvedBy, "approved-by", "", "org member who approved this record")
	fs.StringVar(&opts.product, "product", "", "product ID")
	fs.StringVar(&opts.area, "area", "", "product or problem area")
	fs.Var(&opts.tags, "tag", "support tag (repeatable)")
	fs.StringVar(&opts.status, "status", "", "support status: open, workaround, or resolved")
	fs.BoolVar(&opts.featureCandidate, "feature-candidate", false, "mark as a feature candidate")
	fs.BoolVar(&opts.printOnly, "print", false, "print the scaffold without writing a file")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":        true,
		"manifest":    true,
		"workspace":   true,
		"umbrella":    true,
		"date":        true,
		"title":       true,
		"customer":    true,
		"identifier":  true,
		"claimed-by":  true,
		"observed-by": true,
		"approved-by": true,
		"product":     true,
		"area":        true,
		"tag":         true,
		"status":      true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our support add <slug>")
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(roots) != 1 {
		return fmt.Errorf("support add requires exactly one workspace; pass --manifest and --workspace")
	}
	customer := a.resolveCustomerForWrite(opts.home, opts.manifestName, opts.umbrellaRoot, opts.customer)
	record, content, err := support.Add(roots[0], rest[0], support.AddOptions{
		Date:             opts.date,
		Title:            opts.title,
		Customer:         customer,
		Identifiers:      []string(opts.identifiers),
		ClaimedBy:        opts.claimedBy,
		ObservedBy:       []string(opts.observedBy),
		ApprovedBy:       opts.approvedBy,
		Product:          opts.product,
		Area:             opts.area,
		Tags:             []string(opts.tags),
		Status:           opts.status,
		FeatureCandidate: opts.featureCandidate,
		DryRun:           opts.printOnly,
	})
	if err != nil {
		return err
	}
	a.warnUnknownFleetIdentifiers(opts.meetingCommonOpts, []string(opts.identifiers))
	if !opts.printOnly {
		if err := markRecordIntentToAdd(roots[0], record.Path); err != nil {
			return err
		}
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record  support.Record `json:"record"`
			Content string         `json:"content,omitempty"`
		}{Record: record, Content: content})
	}
	if opts.printOnly {
		fmt.Fprintf(a.stdout, "# path: %s\n%s", record.Path, content)
		return nil
	}
	fmt.Fprintln(a.stdout, record.Path)
	return nil
}

func (a app) runFleet(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing fleet subcommand")
	}
	switch args[0] {
	case "list":
		return a.runFleetList(args[1:])
	case "search":
		return a.runFleetSearch(args[1:])
	case "get":
		return a.runFleetGet(args[1:])
	case "add":
		return a.runFleetAdd(args[1:])
	case "set":
		return a.runFleetSet(args[1:])
	case "-h", "--help", "help":
		a.printFleetUsage()
		return nil
	default:
		return fmt.Errorf("unknown fleet subcommand %q", args[0])
	}
}

func (a app) printFleetUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our fleet list [--manifest NAME] [--workspace ID] [--status TEXT] [--customer ID] [--partner ID] [--identifier ID] [--branch NAME] [--where KEY=VALUE] [--json]
  our fleet search <text> [--manifest NAME] [--workspace ID] [--status TEXT] [--customer ID] [--partner ID] [--identifier ID] [--branch NAME] [--where KEY=VALUE] [--json]
  our fleet get <id|identifier|path> [--manifest NAME] [--workspace ID] [--json]
  our fleet add <id> [--manifest NAME] [--workspace ID] [--customer ID] [--partner ID] [--status TEXT] [--device TEXT] [--serial TEXT] [--identifier ID] [--config-repo NAME] [--config-branch NAME] [--deployed-site TEXT] [--ship-to TEXT] [--contact TEXT] [--install-date DATE] [--print] [--json]
  our fleet set <id|identifier> KEY=VALUE... [--manifest NAME] [--workspace ID] [--json]

Fleet commands use local markdown files under workspace fleet/ directories:
one registry record per deployed instance, keyed by a stable id (use the
hostname or node name). get resolves any identifier — a sales order, purchase
order, functional location, or serial from the record's identifiers list —
and reports related support records that share an identifier. set updates
scalar frontmatter fields in place (for example status=live) and preserves
every other line; state history is the record's git history, so publish each
meaningful transition with our sync --message. status vocabulary is
organization-defined. --where filters on any top-level frontmatter field.`)
}

func (a app) runFleetList(args []string) error {
	opts, rest, err := parseFleetReadOpts("our fleet list", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("fleet list does not accept positional arguments")
	}
	filter, err := opts.filter()
	if err != nil {
		return err
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter = a.resolveFleetFilter(opts.home, opts.manifestName, opts.umbrellaRoot, filter)
	found, err := fleet.List(roots, filter)
	if err != nil {
		return err
	}
	return a.printFleet(found, opts.jsonOut)
}

func (a app) runFleetSearch(args []string) error {
	opts, rest, err := parseFleetReadOpts("our fleet search", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our fleet search <text>")
	}
	filter, err := opts.filter()
	if err != nil {
		return err
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter = a.resolveFleetFilter(opts.home, opts.manifestName, opts.umbrellaRoot, filter)
	found, err := fleetSearch(roots, rest[0], filter)
	if err != nil {
		return err
	}
	return a.printFleet(found, opts.jsonOut)
}

func (a app) runFleetGet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("our fleet get", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our fleet get <id|identifier|path>")
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	rec, content, err := fleet.Get(roots, rest[0])
	if err != nil {
		return err
	}
	related := a.relatedSupportRecords(opts.home, opts.manifestName, opts.umbrellaRoot, rec)
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record         fleet.Record     `json:"record"`
			Content        string           `json:"content"`
			RelatedSupport []support.Record `json:"related_support,omitempty"`
		}{Record: rec, Content: content, RelatedSupport: related})
	}
	fmt.Fprint(a.stdout, content)
	if len(related) != 0 {
		fmt.Fprintf(a.stdout, "\n# Related support records\n\n")
		for _, sr := range related {
			fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", sr.Date, sr.ID, sr.Title, sr.Path)
		}
	}
	return nil
}

func (a app) runFleetAdd(args []string) error {
	var opts fleetAddOpts
	fs := newFlagSet("our fleet add", a.stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.customer, "customer", "", "canonical customer ID")
	fs.StringVar(&opts.partner, "partner", "", "resale or service partner ID")
	fs.StringVar(&opts.status, "status", "", "workflow status (organization-defined; default new)")
	fs.StringVar(&opts.device, "device", "", "device or instance model")
	fs.StringVar(&opts.serial, "serial", "", "device serial number")
	fs.Var(&opts.identifiers, "identifier", "order, invoice, location, or asset identifier (repeatable)")
	fs.StringVar(&opts.configRepo, "config-repo", "", "configuration repository")
	fs.StringVar(&opts.configBranch, "config-branch", "", "configuration branch")
	fs.StringVar(&opts.deployedSite, "deployed-site", "", "where the instance runs")
	fs.StringVar(&opts.shipTo, "ship-to", "", "where the instance shipped (may differ from deployed site)")
	fs.Var(&opts.contacts, "contact", "site contact (repeatable)")
	fs.StringVar(&opts.installDate, "install-date", "", "install date, YYYY-MM-DD")
	fs.BoolVar(&opts.printOnly, "print", false, "print the scaffold without writing a file")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":          true,
		"manifest":      true,
		"workspace":     true,
		"umbrella":      true,
		"customer":      true,
		"partner":       true,
		"status":        true,
		"device":        true,
		"serial":        true,
		"identifier":    true,
		"config-repo":   true,
		"config-branch": true,
		"deployed-site": true,
		"ship-to":       true,
		"contact":       true,
		"install-date":  true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our fleet add <id>")
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(roots) != 1 {
		return fmt.Errorf("fleet add requires exactly one workspace; pass --manifest and --workspace")
	}
	customer := a.resolveCustomerForWrite(opts.home, opts.manifestName, opts.umbrellaRoot, opts.customer)
	rec, content, err := fleet.Add(roots[0], rest[0], fleet.AddOptions{
		Customer:     customer,
		Partner:      opts.partner,
		Status:       opts.status,
		Device:       opts.device,
		Serial:       opts.serial,
		Identifiers:  []string(opts.identifiers),
		ConfigRepo:   opts.configRepo,
		ConfigBranch: opts.configBranch,
		DeployedSite: opts.deployedSite,
		ShipTo:       opts.shipTo,
		Contacts:     []string(opts.contacts),
		InstallDate:  opts.installDate,
		DryRun:       opts.printOnly,
	})
	if err != nil {
		return err
	}
	if !opts.printOnly {
		if err := markRecordIntentToAdd(roots[0], rec.Path); err != nil {
			return err
		}
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record  fleet.Record `json:"record"`
			Content string       `json:"content,omitempty"`
		}{Record: rec, Content: content})
	}
	if opts.printOnly {
		fmt.Fprintf(a.stdout, "# path: %s\n%s", rec.Path, content)
		return nil
	}
	fmt.Fprintln(a.stdout, rec.Path)
	return nil
}

func (a app) runFleetSet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("our fleet set", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: our fleet set <id|identifier> KEY=VALUE...")
	}
	updates := map[string]string{}
	for _, pair := range rest[1:] {
		key, value, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return fmt.Errorf("fleet set arguments must be KEY=VALUE, got %q", pair)
		}
		updates[key] = strings.TrimSpace(value)
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	rec, changes, err := fleet.Set(roots, rest[0], updates)
	if err != nil {
		return err
	}
	syncCommand := ""
	if len(changes) != 0 {
		var parts []string
		for _, change := range changes {
			parts = append(parts, change.Key+"="+change.New)
		}
		syncCommand = fmt.Sprintf("our sync --message %s", shellQuote(fmt.Sprintf("Update fleet %s: %s", rec.ID, strings.Join(parts, ", "))))
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record      fleet.Record         `json:"record"`
			Changes     []record.FieldChange `json:"changes"`
			SyncCommand string               `json:"sync_command,omitempty"`
		}{Record: rec, Changes: changes, SyncCommand: syncCommand})
	}
	if len(changes) == 0 {
		fmt.Fprintf(a.stdout, "no changes for %s\n", rec.Path)
		return nil
	}
	fmt.Fprintf(a.stdout, "updated %s\n", rec.Path)
	for _, change := range changes {
		fmt.Fprintf(a.stdout, "  %s: %q -> %q\n", change.Key, change.Old, change.New)
	}
	fmt.Fprintf(a.stdout, "publish this transition with: %s\n", syncCommand)
	return nil
}

func (a app) runRecord(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing record subcommand")
	}
	switch args[0] {
	case "adopt":
		return a.runRecordAdopt(args[1:])
	case "-h", "--help", "help":
		a.printRecordUsage()
		return nil
	default:
		return fmt.Errorf("unknown record subcommand %q", args[0])
	}
}

func (a app) printRecordUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our record adopt <path> [--manifest NAME] [--workspace ID] [--home DIR] [--umbrella DIR] [--json]

Record commands operate on local markdown records under declared content
mounts. adopt marks an existing untracked file as intentional publish content
using Git intent-to-add; our sync still stages the final content when it
publishes.`)
}

type recordAdoptResult struct {
	Path         string `json:"path"`
	Repo         string `json:"repo"`
	RelativePath string `json:"relative_path"`
	Status       string `json:"status"`
}

func (a app) runRecordAdopt(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("our record adopt", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our record adopt <path>")
	}
	path, err := filepath.Abs(rest[0])
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("record adopt requires a file path, got directory %s", path)
	}
	entry, rel, err := a.recordAdoptTarget(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot, path)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := gitIntentToAdd(entry.LocalPath, path, true); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	result := recordAdoptResult{
		Path:         path,
		Repo:         entry.LocalPath,
		RelativePath: rel,
		Status:       "adopted",
	}
	if opts.jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\n", path)
	return nil
}

func (a app) recordAdoptTarget(home, manifestName, workspaceID, umbrellaRoot, path string) (syncer.Entry, string, error) {
	entries, err := a.collectAdoptEntries(home, manifestName, umbrellaRoot)
	if err != nil {
		return syncer.Entry{}, "", err
	}
	var matches []struct {
		entry syncer.Entry
		rel   string
	}
	for _, entry := range entries {
		if workspaceID != "" && entry.ID != workspaceID {
			continue
		}
		rel, ok := relativePathUnder(entry.LocalPath, path)
		if !ok {
			continue
		}
		rel = filepath.ToSlash(rel)
		if !pathsWithinContent([]string{rel}, entry.ContentPaths) {
			continue
		}
		matches = append(matches, struct {
			entry syncer.Entry
			rel   string
		}{entry: entry, rel: rel})
	}
	if len(matches) == 0 {
		return syncer.Entry{}, "", fmt.Errorf("path %s is not under a declared content path; run our mounts list or pass --workspace", path)
	}
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i].entry.LocalPath) > len(matches[j].entry.LocalPath)
	})
	return matches[0].entry, matches[0].rel, nil
}

func (a app) collectAdoptEntries(home, manifestName, umbrellaRoot string) ([]syncer.Entry, error) {
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, "content")
	if err != nil {
		return nil, err
	}
	var out []syncer.Entry
	for _, entry := range entries {
		if entry.Role == "content" {
			out = append(out, entry)
		}
	}
	return dedupeSyncEntries(out), nil
}

func markRecordIntentToAdd(root record.Root, path string) error {
	return gitIntentToAdd(root.Path, path, false)
}

func gitIntentToAdd(repo, path string, requireGit bool) error {
	if !isGitWorkTree(repo) {
		if requireGit {
			return fmt.Errorf("content root %s is not a git checkout", repo)
		}
		return nil
	}
	rel, ok := relativePathUnder(repo, path)
	if !ok {
		return fmt.Errorf("path %s is outside content root %s", path, repo)
	}
	return runGit(repo, "add", "-N", "--", filepath.FromSlash(filepath.ToSlash(rel)))
}

func isGitWorkTree(path string) bool {
	out, err := gitCmdOutput(path, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func relativePathUnder(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	rel = filepath.Clean(rel)
	if rel == "." || strings.HasPrefix(filepath.ToSlash(rel), "../") || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func pathsWithinContent(paths, prefixes []string) bool {
	if len(paths) == 0 || len(prefixes) == 0 {
		return false
	}
	for _, path := range paths {
		matched := false
		for _, prefix := range prefixes {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// relatedSupportRecords best-effort lists support records sharing an
// identifier with the fleet record. Resolution failures mean no related
// records, never an error: the support noun may not be adopted.
func (a app) relatedSupportRecords(home, manifestName, umbrellaRoot string, rec fleet.Record) []support.Record {
	roots, err := supportRoots(home, manifestName, "", umbrellaRoot)
	if err != nil {
		return nil
	}
	all, err := support.List(roots, support.Filter{})
	if err != nil {
		return nil
	}
	keys := map[string]bool{strings.ToLower(rec.ID): true}
	for _, id := range rec.Identifiers {
		keys[strings.ToLower(id)] = true
	}
	var out []support.Record
	for _, sr := range all {
		for _, id := range sr.Identifiers {
			if keys[strings.ToLower(id)] {
				out = append(out, sr)
				break
			}
		}
	}
	return out
}

// warnUnknownFleetIdentifiers notes identifiers absent from the fleet
// registry. The registry may legitimately lag reality, so this warns and
// never blocks; without a populated registry it stays silent.
func (a app) warnUnknownFleetIdentifiers(opts meetingCommonOpts, identifiers []string) {
	if len(identifiers) == 0 {
		return
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, "", opts.umbrellaRoot)
	if err != nil {
		return
	}
	all, err := fleet.List(roots, fleet.Filter{})
	if err != nil || len(all) == 0 {
		return
	}
	known := map[string]bool{}
	for _, rec := range all {
		known[strings.ToLower(rec.ID)] = true
		for _, id := range rec.Identifiers {
			known[strings.ToLower(id)] = true
		}
	}
	for _, id := range identifiers {
		if !known[strings.ToLower(id)] {
			fmt.Fprintf(a.stderr, "note: identifier %q is not in the fleet registry; it may be new or misspelled\n", id)
		}
	}
}

type fleetReadOpts struct {
	meetingCommonOpts
	status     string
	customer   string
	partner    string
	identifier string
	branch     string
	where      stringListFlag
}

type fleetAddOpts struct {
	meetingCommonOpts
	customer     string
	partner      string
	status       string
	device       string
	serial       string
	identifiers  stringListFlag
	configRepo   string
	configBranch string
	deployedSite string
	shipTo       string
	contacts     stringListFlag
	installDate  string
	printOnly    bool
}

func parseFleetReadOpts(name string, stderr io.Writer, args []string) (fleetReadOpts, []string, error) {
	var opts fleetReadOpts
	fs := newFlagSet(name, stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.status, "status", "", "workflow status")
	fs.StringVar(&opts.customer, "customer", "", "customer ID")
	fs.StringVar(&opts.partner, "partner", "", "partner ID")
	fs.StringVar(&opts.identifier, "identifier", "", "order, invoice, location, or asset identifier")
	fs.StringVar(&opts.branch, "branch", "", "configuration branch")
	fs.Var(&opts.where, "where", "KEY=VALUE frontmatter filter (repeatable)")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":       true,
		"manifest":   true,
		"workspace":  true,
		"umbrella":   true,
		"status":     true,
		"customer":   true,
		"partner":    true,
		"identifier": true,
		"branch":     true,
		"where":      true,
	})
	return opts, rest, err
}

func (opts fleetReadOpts) filter() (fleet.Filter, error) {
	filter := fleet.Filter{
		Status:     opts.status,
		Customer:   opts.customer,
		Partner:    opts.partner,
		Identifier: opts.identifier,
		Branch:     opts.branch,
	}
	for _, pair := range opts.where {
		key, value, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return fleet.Filter{}, fmt.Errorf("--where must be KEY=VALUE, got %q", pair)
		}
		if filter.Where == nil {
			filter.Where = map[string]string{}
		}
		filter.Where[key] = strings.TrimSpace(value)
	}
	return filter, nil
}

type meetingCommonOpts struct {
	home         string
	manifestName string
	workspaceID  string
	umbrellaRoot string
	jsonOut      bool
}

type meetingReadOpts struct {
	meetingCommonOpts
	since    string
	customer string
	partner  string
	product  string
}

type meetingAddOpts struct {
	meetingCommonOpts
	date      string
	title     string
	customer  string
	attendees stringListFlag
	partners  stringListFlag
	product   string
	sourceID  string
	status    string
	printOnly bool
}

type supportReadOpts struct {
	meetingCommonOpts
	since            string
	customer         string
	identifier       string
	claimedBy        string
	product          string
	area             string
	tag              string
	featureCandidate bool
}

type supportAddOpts struct {
	meetingCommonOpts
	date             string
	title            string
	customer         string
	identifiers      stringListFlag
	claimedBy        string
	observedBy       stringListFlag
	approvedBy       string
	product          string
	area             string
	tags             stringListFlag
	status           string
	featureCandidate bool
	printOnly        bool
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	cleaned := strings.TrimSpace(value)
	if cleaned != "" {
		*f = append(*f, cleaned)
	}
	return nil
}

func parseMeetingReadOpts(name string, stderr io.Writer, args []string) (meetingReadOpts, []string, error) {
	var opts meetingReadOpts
	fs := newFlagSet(name, stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.since, "since", "", "minimum meeting date, YYYY-MM-DD")
	fs.StringVar(&opts.customer, "customer", "", "customer ID")
	fs.StringVar(&opts.partner, "partner", "", "partner ID")
	fs.StringVar(&opts.product, "product", "", "product ID")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":      true,
		"manifest":  true,
		"workspace": true,
		"umbrella":  true,
		"since":     true,
		"customer":  true,
		"partner":   true,
		"product":   true,
	})
	return opts, rest, err
}

func parseSupportReadOpts(name string, stderr io.Writer, args []string) (supportReadOpts, []string, error) {
	var opts supportReadOpts
	fs := newFlagSet(name, stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.since, "since", "", "minimum support record date, YYYY-MM-DD")
	fs.StringVar(&opts.customer, "customer", "", "customer ID")
	fs.StringVar(&opts.identifier, "identifier", "", "device, order, or asset identifier")
	fs.StringVar(&opts.claimedBy, "claimed-by", "", "org member who worked the problem")
	fs.StringVar(&opts.product, "product", "", "product ID")
	fs.StringVar(&opts.area, "area", "", "product or problem area")
	fs.StringVar(&opts.tag, "tag", "", "support tag")
	fs.BoolVar(&opts.featureCandidate, "feature-candidate", false, "show only feature candidates")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":       true,
		"manifest":   true,
		"workspace":  true,
		"umbrella":   true,
		"since":      true,
		"customer":   true,
		"identifier": true,
		"claimed-by": true,
		"product":    true,
		"area":       true,
		"tag":        true,
	})
	return opts, rest, err
}

func bindMeetingCommonFlags(fs *flag.FlagSet, opts *meetingCommonOpts) {
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&opts.workspaceID, "workspace", "", "limit to one workspace ID")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON")
}

func meetingValueFlags() map[string]bool {
	return map[string]bool{
		"home":      true,
		"manifest":  true,
		"workspace": true,
		"umbrella":  true,
	}
}

func (opts meetingReadOpts) filter() meetings.Filter {
	return meetings.Filter{
		Since:    opts.since,
		Customer: opts.customer,
		Partner:  opts.partner,
		Product:  opts.product,
	}
}

func (opts supportReadOpts) filter() support.Filter {
	return support.Filter{
		Since:       opts.since,
		Customer:    opts.customer,
		Identifier:  opts.identifier,
		ClaimedBy:   opts.claimedBy,
		Product:     opts.product,
		Area:        opts.area,
		Tag:         opts.tag,
		FeatureOnly: opts.featureCandidate,
	}
}

func meetingRoots(home, manifestName, workspaceID, umbrellaRoot string) ([]meetings.Root, error) {
	return contentRoots(home, manifestName, workspaceID, umbrellaRoot, "meeting", []string{"handbook", "meetings"})
}

func supportRoots(home, manifestName, workspaceID, umbrellaRoot string) ([]support.Root, error) {
	return contentRoots(home, manifestName, workspaceID, umbrellaRoot, "support", []string{"handbook", "support"})
}

func fleetRoots(home, manifestName, workspaceID, umbrellaRoot string) ([]fleet.Root, error) {
	return contentRoots(home, manifestName, workspaceID, umbrellaRoot, "fleet", []string{"handbook", "fleet"})
}

// contentRoots resolves the workspace roots for one content noun. The noun
// only changes which mount kinds participate and how empty results read.
func contentRoots(home, manifestName, workspaceID, umbrellaRoot, noun string, kinds []string) ([]record.Root, error) {
	if umbrellaRoot != "" {
		return umbrellaContentRoots(home, workspaceID, umbrellaRoot, noun, kinds)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return umbrellaContentRoots(home, workspaceID, root, noun, kinds)
	}
	if roots, ok, err := configuredUmbrellaContentRoots(home, manifestName, workspaceID, noun, kinds); ok || err != nil {
		return roots, err
	}
	if manifestName == "" {
		return nil, noUmbrellaError("no our umbrella found; run our setup or pass --umbrella", "run our setup or pass --umbrella <path>")
	}
	entries, err := workspace.List(home, manifestName)
	if err != nil {
		return nil, err
	}
	var roots []record.Root
	for _, entry := range entries {
		if workspaceID != "" && entry.ID != workspaceID {
			continue
		}
		roots = append(roots, record.Root{
			Manifest:  entry.Manifest,
			Workspace: entry.ID,
			Path:      entry.LocalPath,
		})
	}
	if len(roots) == 0 {
		if workspaceID != "" {
			return nil, fmt.Errorf("workspace %q is not declared by any selected manifest", workspaceID)
		}
		return nil, fmt.Errorf("no workspaces declared by selected manifests")
	}
	return roots, nil
}

func configuredUmbrellaContentRoots(home, manifestName, workspaceID, noun string, kinds []string) ([]record.Root, bool, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil || len(docs) == 0 {
		return nil, false, nil
	}
	type candidate struct {
		root string
		ref  string
	}
	var candidates []candidate
	var configured []candidate
	for _, doc := range docs {
		root, err := umbrella.ResolveRoot(home, "", "", doc.doc)
		if err != nil {
			return nil, true, err
		}
		configured = append(configured, candidate{root: root, ref: doc.ref.Name})
		if _, err := umbrella.LoadWorkspace(root); err == nil {
			candidates = append(candidates, candidate{root: root, ref: doc.ref.Name})
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, true, err
		}
	}
	if len(candidates) == 1 {
		roots, err := umbrellaContentRoots(home, workspaceID, candidates[0].root, noun, kinds)
		return roots, true, err
	}
	if len(candidates) > 1 {
		return nil, true, fmt.Errorf("multiple our umbrellas configured; pass --manifest or --umbrella")
	}
	if manifestName == "" && len(configured) == 1 {
		return nil, true, noUmbrellaError(
			fmt.Sprintf("no our umbrella found; configured umbrella is %s", configured[0].root),
			fmt.Sprintf("run our setup --manifest %s or pass --umbrella %s", configured[0].ref, configured[0].root),
		)
	}
	return nil, false, nil
}

func umbrellaContentRoots(home, workspaceID, umbrellaRoot, noun string, kinds []string) ([]record.Root, error) {
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return nil, err
	}
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, fmt.Errorf("read umbrella state: %w", err)
	}
	var roots []record.Root
	for _, mount := range state.Mounts {
		if workspaceID != "" && mount.ID != workspaceID {
			continue
		}
		// local-only mounts (no origin yet, pre-publish) are present and
		// writable; recording must work before the org is published.
		if mount.Status != "synced" && mount.Status != "local-only" {
			continue
		}
		if !record.ContainsValue(kinds, mount.Kind) {
			continue
		}
		roots = append(roots, record.Root{
			Manifest:  mount.SourceRef,
			Workspace: mount.ID,
			Path:      umbrella.MountPath(root, mount.ID),
		})
	}
	if len(roots) == 0 {
		if workspaceID != "" {
			return nil, fmt.Errorf("workspace %q is not mounted in umbrella %s", workspaceID, root)
		}
		return nil, fmt.Errorf("no %s mounts synced in umbrella %s", noun, root)
	}
	return roots, nil
}

func (a app) printMeetings(found []meetings.Meeting, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, found)
	}
	for _, meeting := range found {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			meeting.Date,
			meeting.ID,
			meeting.Title,
			meeting.Customer,
			strings.Join(meeting.Partners, ","),
			meeting.Product,
			meeting.Snippet,
			meeting.Path,
		)
	}
	return nil
}

var meetingSearch = defaultMeetingSearch
var qmdMeetingSearch = runQMDMeetingSearch

func defaultMeetingSearch(roots []meetings.Root, query string, filter meetings.Filter) ([]meetings.Meeting, error) {
	qmdFound, qmdOK := qmdMeetingSearch(roots, query, filter)
	fallback, err := meetings.Search(roots, query, filter)
	if err != nil {
		return nil, err
	}
	if qmdOK && len(qmdFound) != 0 {
		return mergeMeetingResults(qmdFound, fallback), nil
	}
	return fallback, nil
}

type qmdSearchResult struct {
	File    string  `json:"file"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

func (a app) printSupport(found []support.Record, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, found)
	}
	for _, record := range found {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			record.Date,
			record.ID,
			record.Title,
			record.Customer,
			strings.Join(record.Identifiers, ","),
			record.ClaimedBy,
			record.Product,
			record.Area,
			record.Status,
			strings.Join(record.Tags, ","),
			record.FeatureCandidate,
			record.Snippet,
			record.Path,
		)
	}
	return nil
}

var supportSearch = defaultSupportSearch
var qmdSupportSearch = runQMDSupportSearch

func defaultSupportSearch(roots []support.Root, query string, filter support.Filter) ([]support.Record, error) {
	qmdFound, qmdOK := qmdSupportSearch(roots, query, filter)
	fallback, err := support.Search(roots, query, filter)
	if err != nil {
		return nil, err
	}
	if qmdOK && len(qmdFound) != 0 {
		return mergeSupportResults(qmdFound, fallback), nil
	}
	return fallback, nil
}

var fleetSearch = defaultFleetSearch
var qmdFleetSearch = runQMDFleetSearch

func defaultFleetSearch(roots []fleet.Root, query string, filter fleet.Filter) ([]fleet.Record, error) {
	qmdFound, qmdOK := qmdFleetSearch(roots, query, filter)
	fallback, err := fleet.Search(roots, query, filter)
	if err != nil {
		return nil, err
	}
	if qmdOK && len(qmdFound) != 0 {
		return mergeFleetResults(qmdFound, fallback), nil
	}
	return fallback, nil
}

func (a app) printFleet(found []fleet.Record, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, found)
	}
	for _, record := range found {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			record.ID,
			record.Customer,
			record.Partner,
			record.Status,
			record.Device,
			record.ConfigBranch,
			strings.Join(record.Identifiers, ","),
			record.DeployedSite,
			record.Snippet,
			record.Path,
		)
	}
	return nil
}

func runQMDSupportSearch(roots []support.Root, query string, filter support.Filter) ([]support.Record, bool) {
	found, err := support.List(roots, filter)
	if err != nil {
		return nil, false
	}
	return runQMDContentSearch(found, query, "support",
		func(record support.Record) string { return record.Path },
		func(record support.Record, snippet string) support.Record { record.Snippet = snippet; return record })
}

func runQMDMeetingSearch(roots []meetings.Root, query string, filter meetings.Filter) ([]meetings.Meeting, bool) {
	found, err := meetings.List(roots, filter)
	if err != nil {
		return nil, false
	}
	return runQMDContentSearch(found, query, "meetings",
		func(meeting meetings.Meeting) string { return meeting.Path },
		func(meeting meetings.Meeting, snippet string) meetings.Meeting {
			meeting.Snippet = snippet
			return meeting
		})
}

func runQMDFleetSearch(roots []fleet.Root, query string, filter fleet.Filter) ([]fleet.Record, bool) {
	found, err := fleet.List(roots, filter)
	if err != nil {
		return nil, false
	}
	return runQMDContentSearch(found, query, "fleet",
		func(record fleet.Record) string { return record.Path },
		func(record fleet.Record, snippet string) fleet.Record { record.Snippet = snippet; return record })
}

// runQMDContentSearch runs one qmd query over pre-filtered records of any
// content noun, matching qmd result paths back to records by path keys.
func runQMDContentSearch[T any](items []T, query, dirToken string, path func(T) string, withSnippet func(T, string) T) ([]T, bool) {
	qmd, err := exec.LookPath("qmd")
	if err != nil {
		return nil, false
	}
	index := map[string]T{}
	for _, item := range items {
		for _, key := range qmdContentKeys(path(item)) {
			if _, exists := index[key]; !exists {
				index[key] = item
			}
		}
	}
	if len(index) == 0 {
		return nil, false
	}
	cmd := exec.Command(qmd, "search", query, "--json", "-n", "100")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || strings.HasPrefix(trimmed, "No results found") {
		return nil, true
	}
	var results []qmdSearchResult
	if err := json.Unmarshal([]byte(trimmed), &results); err != nil {
		return nil, false
	}
	var found []T
	seen := map[string]bool{}
	for _, result := range results {
		item, ok := matchQMDContent(index, result.File, dirToken)
		if !ok || seen[path(item)] {
			continue
		}
		if snippet := cleanQMDSnippet(result.Snippet); snippet != "" {
			item = withSnippet(item, snippet)
		}
		found = append(found, item)
		seen[path(item)] = true
	}
	return found, true
}

func qmdContentKeys(path string) []string {
	var keys []string
	add := func(value string) {
		value = strings.ToLower(filepath.ToSlash(value))
		if value != "" {
			keys = append(keys, value)
		}
	}
	add(path)
	if abs, err := filepath.Abs(path); err == nil {
		add(abs)
	}
	root := filepath.Dir(filepath.Dir(path))
	if rel, err := filepath.Rel(root, path); err == nil {
		add(rel)
	}
	if rel, err := filepath.Rel(filepath.Dir(root), path); err == nil {
		add(rel)
	}
	return keys
}

func matchQMDContent[T any](index map[string]T, file, dirToken string) (T, bool) {
	for _, key := range qmdContentResultKeys(file, dirToken) {
		if item, ok := index[key]; ok {
			return item, true
		}
	}
	var zero T
	return zero, false
}

func qmdContentResultKeys(file, dirToken string) []string {
	file = strings.TrimSpace(file)
	var keys []string
	add := func(value string) {
		value = strings.ToLower(filepath.ToSlash(value))
		if value != "" {
			keys = append(keys, value)
		}
	}
	add(file)
	withoutScheme := strings.TrimPrefix(file, "qmd://")
	add(withoutScheme)
	if i := strings.Index(withoutScheme, "/"); i >= 0 {
		rel := withoutScheme[i+1:]
		add(rel)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for i, part := range parts {
			if part != dirToken {
				continue
			}
			add(strings.Join(parts[i:], "/"))
			if i > 0 {
				add(strings.Join(parts[i-1:], "/"))
			}
		}
	}
	return keys
}

func cleanQMDSnippet(snippet string) string {
	for _, line := range strings.Split(snippet, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "@@") {
			continue
		}
		return line
	}
	return ""
}

func mergeMeetingResults(primary, fallback []meetings.Meeting) []meetings.Meeting {
	return mergeContentResults(primary, fallback, func(meeting meetings.Meeting) string { return meeting.Path })
}

func mergeSupportResults(primary, fallback []support.Record) []support.Record {
	return mergeContentResults(primary, fallback, func(record support.Record) string { return record.Path })
}

func mergeFleetResults(primary, fallback []fleet.Record) []fleet.Record {
	return mergeContentResults(primary, fallback, func(record fleet.Record) string { return record.Path })
}

func mergeContentResults[T any](primary, fallback []T, path func(T) string) []T {
	out := append([]T(nil), primary...)
	seen := map[string]bool{}
	for _, item := range primary {
		seen[path(item)] = true
	}
	for _, item := range fallback {
		if !seen[path(item)] {
			out = append(out, item)
			seen[path(item)] = true
		}
	}
	return out
}

func (a app) resolveMeetingFilter(home, manifestName, umbrellaRoot string, filter meetings.Filter) meetings.Filter {
	if filter.Customer == "" {
		return filter
	}
	customer, ok, err := a.findCustomer(home, manifestName, umbrellaRoot, filter.Customer)
	if err != nil || !ok {
		return filter
	}
	filter.CustomerValues = uniqueStrings(append(append([]string{filter.Customer, customer.ID, customer.Domain}, customer.Aliases...), customer.Name))
	return filter
}

func (a app) resolveSupportFilter(home, manifestName, umbrellaRoot string, filter support.Filter) support.Filter {
	if filter.Customer == "" {
		return filter
	}
	customer, ok, err := a.findCustomer(home, manifestName, umbrellaRoot, filter.Customer)
	if err != nil || !ok {
		return filter
	}
	filter.CustomerValues = uniqueStrings(append(append([]string{filter.Customer, customer.ID, customer.Domain}, customer.Aliases...), customer.Name))
	return filter
}

func (a app) resolveFleetFilter(home, manifestName, umbrellaRoot string, filter fleet.Filter) fleet.Filter {
	if filter.Customer == "" {
		return filter
	}
	customer, ok, err := a.findCustomer(home, manifestName, umbrellaRoot, filter.Customer)
	if err != nil || !ok {
		return filter
	}
	filter.CustomerValues = uniqueStrings(append(append([]string{filter.Customer, customer.ID, customer.Domain}, customer.Aliases...), customer.Name))
	return filter
}

func (a app) resolveCustomerForWrite(home, manifestName, umbrellaRoot, value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	customer, ok, err := a.findCustomer(home, manifestName, umbrellaRoot, value)
	if err == nil && ok {
		return customer.ID
	}
	customers, loadErr := a.loadCustomers(home, manifestName, umbrellaRoot)
	if loadErr == nil && len(customers) != 0 {
		fmt.Fprintf(a.stderr, "warning: unknown customer %q; keeping literal value; add it with our admin customers add %s --manifest-dir <checkout>\n", value, shellQuote(value))
	}
	return value
}

func (a app) findCustomer(home, manifestName, umbrellaRoot, value string) (manifest.Customer, bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return manifest.Customer{}, false, nil
	}
	customers, err := a.loadCustomers(home, manifestName, umbrellaRoot)
	if err != nil {
		return manifest.Customer{}, false, err
	}
	for _, customer := range customers {
		if strings.ToLower(customer.ID) == value || strings.ToLower(customer.Domain) == value || strings.ToLower(customer.Name) == value {
			return customer, true, nil
		}
		for _, alias := range customer.Aliases {
			if strings.ToLower(alias) == value {
				return customer, true, nil
			}
		}
	}
	return manifest.Customer{}, false, nil
}

func (a app) loadCustomers(home, manifestName, umbrellaRoot string) ([]manifest.Customer, error) {
	catalogCustomers, err := manifest.LoadCustomers(home, manifestName)
	if err != nil {
		return nil, err
	}
	root, ok, err := customerUmbrellaRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		return nil, err
	}
	if !ok {
		return catalogCustomers, nil
	}
	registryCustomers, err := handbookRegistryCustomers(root)
	if err != nil {
		return nil, err
	}
	return mergeCustomers(catalogCustomers, registryCustomers), nil
}

func customerUmbrellaRoot(home, manifestName, umbrellaRoot string) (string, bool, error) {
	if umbrellaRoot != "" {
		root, err := resolveUmbrellaRoot(home, umbrellaRoot)
		return root, err == nil, err
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return root, true, nil
	}
	roots, ok, err := configuredUmbrellaRoots(home, manifestName)
	if err != nil || !ok || len(roots) == 0 {
		return "", false, err
	}
	if len(roots) > 1 {
		return "", false, nil
	}
	return roots[0], true, nil
}

func configuredUmbrellaRoots(home, manifestName string) ([]string, bool, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil || len(docs) == 0 {
		return nil, false, nil
	}
	var roots []string
	for _, doc := range docs {
		root, err := umbrella.ResolveRoot(home, "", "", doc.doc)
		if err != nil {
			return nil, true, err
		}
		if _, err := umbrella.LoadWorkspace(root); err == nil {
			roots = append(roots, root)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, true, err
		}
	}
	return roots, true, nil
}

func handbookRegistryCustomers(root string) ([]manifest.Customer, error) {
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, nil
	}
	var out []manifest.Customer
	for _, mount := range state.Mounts {
		if mount.Status != "synced" || mount.Kind != "handbook" {
			continue
		}
		path := filepath.Join(umbrella.MountPath(root, mount.ID), "customers", "registry.md")
		customers, err := parseCustomerRegistry(path)
		if err != nil {
			return nil, err
		}
		out = append(out, customers...)
	}
	return out, nil
}

func parseCustomerRegistry(path string) ([]manifest.Customer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []manifest.Customer
	confirmed := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "## registry") && strings.Contains(lower, "confirmed") {
			confirmed = !strings.Contains(lower, "unconfirmed")
			continue
		}
		if !strings.HasPrefix(trimmed, "|") || strings.Contains(trimmed, "---") {
			continue
		}
		cells := markdownTableCells(trimmed)
		if len(cells) < 4 || strings.Contains(strings.ToLower(cells[0]), "canonical id") || strings.Contains(strings.ToLower(cells[0]), "placeholder id") {
			continue
		}
		id := cleanMarkdownCell(cells[0])
		if id == "" {
			continue
		}
		customer := manifest.Customer{
			ID:              id,
			Name:            cleanMarkdownCell(cells[1]),
			Partners:        splitCustomerPartners(cleanMarkdownCell(cells[2])),
			DomainConfirmed: confirmed,
			Aliases:         registryAliases(id, cells[3]),
		}
		if confirmed && strings.Contains(id, ".") {
			customer.Domain = id
		}
		out = append(out, customer)
	}
	return out, nil
}

func markdownTableCells(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func cleanMarkdownCell(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "`", "")
	value = strings.ReplaceAll(value, "**", "")
	if value == "\u2014" {
		return ""
	}
	return value
}

func splitCustomerPartners(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" && part != "\u2014" {
			out = append(out, part)
		}
	}
	return out
}

func registryAliases(id, notes string) []string {
	var out []string
	for _, part := range strings.Split(notes, "`") {
		part = strings.TrimSpace(part)
		if part == "" || part == id {
			continue
		}
		if strings.ContainsAny(part, " /,()") {
			continue
		}
		out = append(out, part)
	}
	return uniqueStrings(out)
}

func mergeCustomers(primary, secondary []manifest.Customer) []manifest.Customer {
	out := append([]manifest.Customer(nil), primary...)
	seen := map[string]int{}
	for i, customer := range out {
		seen[customer.ID] = i
	}
	for _, customer := range secondary {
		if i, ok := seen[customer.ID]; ok {
			out[i].Aliases = uniqueStrings(append(out[i].Aliases, customer.Aliases...))
			out[i].Partners = uniqueStrings(append(out[i].Partners, customer.Partners...))
			if out[i].Name == "" {
				out[i].Name = customer.Name
			}
			if out[i].Domain == "" {
				out[i].Domain = customer.Domain
			}
			out[i].DomainConfirmed = out[i].DomainConfirmed || customer.DomainConfirmed
			continue
		}
		seen[customer.ID] = len(out)
		out = append(out, customer)
	}
	return out
}

func uniqueStrings(values []string) []string {
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
	return out
}

type registeredDoc struct {
	ref manifest.Ref
	doc manifest.Document
}

func (a app) runDoctor(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var noFetch bool
	var fix bool
	var jsonOut bool
	fs := newFlagSet("our doctor", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&noFetch, "no-fetch", false, "use local tracking refs without fetching remotes")
	fs.BoolVar(&fix, "fix", false, "fast-forward safe stale checkouts and reconcile derived artifacts")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("doctor does not accept positional arguments")
	}
	if fix && noFetch {
		return fmt.Errorf("--fix requires fetched freshness; omit --no-fetch")
	}
	report := a.buildDoctorReport(home, manifestName, umbrellaRoot, doctorOptions{NoFetch: noFetch, Fix: fix})
	if jsonOut {
		if err := printJSON(a.stdout, report); err != nil {
			return err
		}
	} else {
		a.printDoctorReport(report)
	}
	if fix && doctorFixFailed(report.Fixes) {
		return fmt.Errorf("one or more doctor fixes failed")
	}
	return nil
}

type doctorOptions struct {
	NoFetch bool
	Fix     bool
}

type doctorReport struct {
	Version    []doctorItem `json:"version,omitempty"`
	Legacy     []doctorItem `json:"legacy,omitempty"`
	Umbrella   []doctorItem `json:"umbrella,omitempty"`
	Manifests  []doctorItem `json:"manifests"`
	Freshness  []doctorItem `json:"freshness,omitempty"`
	Derived    []doctorItem `json:"derived,omitempty"`
	Fixes      []doctorItem `json:"fixes,omitempty"`
	LastSync   []doctorItem `json:"last_sync,omitempty"`
	Workspaces []doctorItem `json:"workspaces"`
	Tools      []doctorItem `json:"tools"`
}

type doctorItem struct {
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message,omitempty"`
	WouldFix string   `json:"would_fix,omitempty"`
	Details  []string `json:"details,omitempty"`
}

func (a app) buildDoctorReport(home, manifestName, umbrellaRoot string, opts doctorOptions) doctorReport {
	var report doctorReport
	report.Version = append(report.Version, a.doctorVersion(home))
	var root string
	if umbrellaRoot != "" {
		resolved, err := resolveUmbrellaRoot(home, umbrellaRoot)
		if err != nil {
			report.Umbrella = append(report.Umbrella, doctorItem{Name: umbrellaRoot, Status: "error", Message: err.Error()})
		} else {
			root = resolved
			report.Umbrella = append(report.Umbrella, doctorUmbrella(home, root)...)
		}
	} else if found, ok := umbrella.FindRoot("."); ok {
		root = found
		report.Umbrella = append(report.Umbrella, doctorUmbrella(home, root)...)
	}
	report.Legacy = append(report.Legacy, doctorLegacy(home, root)...)
	refs, err := manifestRefs(home, manifestName)
	if err != nil {
		report.Manifests = append(report.Manifests, doctorItem{Name: manifestName, Status: "error", Message: err.Error()})
		return report
	}
	for _, ref := range refs {
		result := manifest.ValidateFile(ref.LocalPath)
		item := doctorItem{Name: ref.Name, Path: result.Path}
		switch {
		case len(result.Errors) != 0:
			item.Status = "error"
			item.Details = append(item.Details, result.Errors...)
		case len(result.Warnings) != 0:
			item.Status = "warning"
			item.Details = append(item.Details, result.Warnings...)
		default:
			item.Status = "ok"
		}
		report.Manifests = append(report.Manifests, item)
		if len(result.Errors) != 0 {
			continue
		}
		doc, _, err := manifest.LoadDocument(ref.LocalPath)
		if err != nil {
			continue
		}
		report.Manifests = append(report.Manifests, doctorLocalMountURLs(ref, doc)...)
		report.Workspaces = append(report.Workspaces, doctorWorkspaces(home, ref.Name, doc.Workspaces)...)
		report.Tools = append(report.Tools, doctorTools(ref.Name, doc.Tools)...)
	}
	report.Freshness = append(report.Freshness, a.doctorFreshness(home, manifestName, umbrellaRoot, !opts.NoFetch, root)...)
	report.Derived = append(report.Derived, a.doctorDerived(home, manifestName, root)...)
	if root != "" {
		for i := range report.Derived {
			item := &report.Derived[i]
			if item.Status == "ok" || item.Status == "error" {
				continue
			}
			if item.Name == "selfskill" {
				item.WouldFix = "reinstall the our self-skill"
			} else {
				item.WouldFix = "reconcile derived guidance and skills"
			}
		}
	}
	if opts.Fix {
		clearDoctorWouldFix(report.Freshness)
		clearDoctorWouldFix(report.Derived)
		report.Fixes = append(report.Fixes, a.doctorFix(home, manifestName, umbrellaRoot, root, report.Derived)...)
	}
	if root != "" {
		report.LastSync = append(report.LastSync, doctorLastSync(root))
	}
	return report
}

func doctorLocalMountURLs(ref manifest.Ref, doc manifest.Document) []doctorItem {
	var items []doctorItem
	for _, mount := range manifest.EffectiveMounts(doc) {
		if !localMountGitURL(mount.GitURL) {
			continue
		}
		items = append(items, doctorItem{
			Name:    ref.Name + ":mount:" + mount.ID,
			Status:  "local-only",
			Path:    mount.GitURL,
			Message: "mount git_url is local-only; run our publish --manifest " + ref.Name,
			Details: []string{
				"manifest=" + ref.LocalPath,
			},
		})
	}
	return items
}

func (a app) doctorVersion(home string) doctorItem {
	current := a.currentOurVersion()
	item := doctorItem{Name: "our", Status: "ok", Message: "current=v" + strings.TrimPrefix(current, "v")}
	notice, err := selfupdate.CheckNotice(context.Background(), selfupdate.NoticeOptions{
		CurrentVersion: current,
		Home:           home,
		Source:         a.updateSource,
		TTL:            selfupdate.UpdateCheckTTLFromEnv(),
		Now:            a.updateNow,
	})
	if err != nil {
		item.Status = "unknown"
		item.Message = item.Message + " update_check=unavailable"
		item.Details = append(item.Details, err.Error())
		return item
	}
	item.Message = fmt.Sprintf("current=v%s latest=v%s", notice.CurrentVersion, notice.LatestVersion)
	if notice.UpdateAvailable {
		item.Status = "stale"
		item.Message += " run our update"
	}
	return item
}

func doctorLegacy(home, root string) []doctorItem {
	homeDir, err := resolveHome(home)
	if err != nil {
		return []doctorItem{{Name: "legacy", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	if root != "" {
		legacyUmbrella := filepath.Join(root, ".flux")
		if info, err := os.Stat(legacyUmbrella); err == nil && info.IsDir() {
			items = append(items, doctorItem{
				Name:    ".flux",
				Status:  "warning",
				Path:    legacyUmbrella,
				Message: "legacy Flux workspace marker found; migrate state to .our or re-run our setup in the intended umbrella",
			})
		}
	}
	legacyShare := filepath.Join(homeDir, ".local", "share", "flux")
	if info, err := os.Stat(legacyShare); err == nil && info.IsDir() {
		items = append(items, doctorItem{
			Name:    "flux data",
			Status:  "warning",
			Path:    legacyShare,
			Message: "legacy Flux data directory found; Our AI uses ~/.local/share/our",
		})
	}
	legacyManifestRegistry := filepath.Join(homeDir, ".config", "flux", "manifests.json")
	if info, err := os.Stat(legacyManifestRegistry); err == nil && !info.IsDir() {
		items = append(items, doctorItem{
			Name:    "flux manifest registry",
			Status:  "warning",
			Path:    legacyManifestRegistry,
			Message: "legacy Flux manifest registry found; Our AI uses ~/.config/our/manifests.json",
		})
	}
	var legacyEnv []string
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		if strings.HasPrefix(key, "FLUX_") {
			legacyEnv = append(legacyEnv, key)
		}
	}
	if len(legacyEnv) != 0 {
		sort.Strings(legacyEnv)
		items = append(items, doctorItem{
			Name:    "FLUX_* env",
			Status:  "warning",
			Message: "legacy Flux environment variables are set; rename them to OUR_*",
			Details: legacyEnv,
		})
	}
	if path, err := exec.LookPath("flux"); err == nil {
		items = append(items, doctorItem{
			Name:    "flux binary",
			Status:  "warning",
			Path:    path,
			Message: "legacy flux binary is still on PATH; remove it or replace workflows with our",
		})
	}
	for _, h := range []harness.Harness{harness.ClaudeCode, harness.Codex, harness.OpenCode} {
		target := h.SkillTargetPath(homeDir, "flux")
		if _, err := os.Lstat(target); err == nil {
			items = append(items, doctorItem{
				Name:    string(h) + ":flux skill",
				Status:  "warning",
				Path:    target,
				Message: "legacy flux self-skill is installed; run our skills self install " + string(h),
			})
		}
	}
	return items
}

func (a app) doctorFreshness(home, manifestName, umbrellaRoot string, fetch bool, root string) []doctorItem {
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, "local")
	if err != nil {
		return []doctorItem{{Name: "git", Status: "error", Message: err.Error()}}
	}
	if len(entries) == 0 {
		return nil
	}
	refreshes := doctorMountRefreshes(root)
	results := syncer.Inspect(entries, syncer.InspectOptions{Fetch: fetch})
	items := make([]doctorItem, 0, len(results))
	for _, result := range results {
		items = append(items, doctorFreshnessItem(result, fetch, refreshes))
	}
	return items
}

func doctorMountRefreshes(root string) map[string]string {
	if root == "" {
		return nil
	}
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, mount := range state.Mounts {
		if mount.LastSync == "" {
			continue
		}
		out["content\x00"+mount.ID] = mount.LastSync
		out["product\x00"+mount.ID] = mount.LastSync
	}
	return out
}

func doctorFreshnessItem(result syncer.Result, fetch bool, refreshes map[string]string) doctorItem {
	item := doctorItem{
		Name: doctorFreshnessName(result),
		Path: result.LocalPath,
	}
	switch result.Status {
	case "already landed":
		item.Status = "ok"
	case "pending":
		item.Status = doctorPendingFreshnessStatus(result)
	case "held back":
		if strings.Contains(result.Message, "not cloned") {
			item.Status = "missing"
		} else {
			item.Status = "held back"
		}
	case "unknown":
		item.Status = "unknown"
	case "failed":
		item.Status = "error"
	default:
		item.Status = result.Status
	}
	item.Message = doctorFreshnessMessage(result, fetch)
	if result.Branch != "" {
		item.Details = append(item.Details, "branch="+result.Branch)
	}
	if result.Head != "" {
		item.Details = append(item.Details, "head="+result.Head)
	}
	if lastRefresh := refreshes[result.Role+"\x00"+result.ID]; lastRefresh != "" {
		item.Details = append(item.Details, "last_refresh="+lastRefresh)
	}
	if len(result.Dirty) != 0 {
		item.Details = append(item.Details, "dirty="+strings.Join(result.Dirty, ","))
	}
	if len(result.Changed) != 0 {
		item.Details = append(item.Details, "changed="+strings.Join(result.Changed, ","))
	}
	if result.FetchError != "" {
		item.Details = append(item.Details, "fetch_error="+result.FetchError)
	}
	if result.Error != "" {
		item.Details = append(item.Details, result.Error)
	}
	if item.Status == "stale" && !result.BehindUnknown && len(result.Dirty) == 0 &&
		result.Ahead == 0 && result.Behind > 0 &&
		(result.Role == "manifest" || result.Role == "content") {
		item.WouldFix = "fast-forward"
	}
	return item
}

// clearDoctorWouldFix drops dry-run plans once --fix is actually applying them.
func clearDoctorWouldFix(items []doctorItem) {
	for i := range items {
		items[i].WouldFix = ""
	}
}

func doctorFreshnessName(result syncer.Result) string {
	name := result.Role + ":" + result.ID
	if result.Manifest != "" && result.Role != "manifest" {
		return result.Manifest + ":" + name
	}
	return name
}

func doctorPendingFreshnessStatus(result syncer.Result) string {
	if result.BehindUnknown {
		return "unknown"
	}
	if len(result.Dirty) != 0 {
		return "dirty"
	}
	if result.Ahead != 0 && result.Behind != 0 {
		return "diverged"
	}
	if result.Ahead != 0 {
		return "ahead"
	}
	if result.Behind != 0 {
		return "stale"
	}
	return "warning"
}

func doctorFreshnessMessage(result syncer.Result, fetch bool) string {
	if result.Status == "held back" || result.Status == "failed" {
		if result.Message != "" {
			return result.Message
		}
		return result.Error
	}
	if result.Status == "already landed" {
		if fetch {
			return "up to date"
		}
		return "up to date (as of last fetch)"
	}
	parts := []string{}
	if result.BehindUnknown {
		parts = append(parts, "behind=unknown (remote unreachable)")
	} else {
		parts = append(parts, fmt.Sprintf("behind=%d", result.Behind))
	}
	parts = append(parts, fmt.Sprintf("ahead=%d", result.Ahead))
	if len(result.Dirty) != 0 {
		parts = append(parts, fmt.Sprintf("dirty=%d", len(result.Dirty)))
	}
	if !fetch {
		parts = append(parts, "as of last fetch")
	}
	if result.Message != "" {
		parts = append(parts, result.Message)
	}
	return strings.Join(parts, " ")
}

func (a app) doctorFix(home, manifestName, umbrellaRoot, root string, derived []doctorItem) []doctorItem {
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, "local")
	if err != nil {
		return []doctorItem{{Name: "git", Status: "error", Message: err.Error()}}
	}
	entryByPath := map[string]syncer.Entry{}
	for _, entry := range entries {
		entryByPath[entry.LocalPath] = entry
	}
	results := syncer.Inspect(entries, syncer.InspectOptions{Fetch: true})
	var items []doctorItem
	manifestFixed := false
	for _, result := range results {
		entry, ok := entryByPath[result.LocalPath]
		if !ok {
			continue
		}
		item, fixedManifest, include := doctorFixFreshnessItem(entry, result)
		if !include {
			continue
		}
		if fixedManifest {
			manifestFixed = true
		}
		items = append(items, item)
	}
	if manifestFixed || doctorDerivedHasDrift(derived) {
		items = append(items, a.doctorFixDerived(home, manifestName, root)...)
	}
	items = append(items, a.doctorFixSelfSkill(home)...)
	return items
}

func (a app) doctorFixSelfSkill(home string) []doctorItem {
	rows, err := selfskill.Inspect(harness.All(), selfskill.Options{Home: home})
	if err != nil {
		return []doctorItem{{Name: "selfskill", Status: "error", Message: err.Error()}}
	}
	var hs []harness.Harness
	for _, row := range rows {
		if row.Status == "absent" || row.Status == "stale" {
			hs = append(hs, row.Harness)
		}
	}
	if len(hs) == 0 {
		return nil
	}
	results, err := selfskill.Install(hs, selfskill.Options{Home: home, Link: true, SkipMissing: true})
	if err != nil {
		return []doctorItem{{Name: "selfskill", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	for _, result := range results {
		item := doctorItem{
			Name:    "selfskill:" + string(result.Harness),
			Status:  doctorFixStatusFromSkill(result.Status),
			Path:    result.TargetPath,
			Message: result.Message,
		}
		if item.Message == "" {
			item.Message = "reinstalled our self-skill"
		}
		if result.Err != nil {
			item.Details = append(item.Details, result.Err.Error())
		}
		items = append(items, item)
	}
	return items
}

func doctorFixFreshnessItem(entry syncer.Entry, result syncer.Result) (doctorItem, bool, bool) {
	item := doctorItem{Name: doctorFreshnessName(result), Path: result.LocalPath}
	if result.BehindUnknown || result.Status == "unknown" {
		item.Status = "skipped"
		item.Message = "remote freshness unknown"
		return item, false, true
	}
	if result.Status == "failed" {
		item.Status = "error"
		item.Message = result.Error
		return item, false, true
	}
	if result.Role == "repo" && result.Behind > 0 {
		item.Status = "skipped"
		item.Message = "repo checkouts are never fixed by doctor"
		return item, false, true
	}
	if len(result.Dirty) != 0 {
		item.Status = "skipped"
		item.Message = "dirty checkout; commit or stash before fixing"
		item.Details = append(item.Details, "dirty="+strings.Join(result.Dirty, ","))
		return item, false, true
	}
	if result.Ahead != 0 && result.Behind != 0 {
		item.Status = "skipped"
		item.Message = "diverged checkout; reconcile manually"
		return item, false, true
	}
	if result.Behind == 0 {
		return doctorItem{}, false, false
	}
	if result.Role != "manifest" && result.Role != "content" {
		item.Status = "skipped"
		item.Message = "only manifest and read-mostly content checkouts are fixed"
		return item, false, true
	}
	fixed := syncer.FastForward(entry, syncer.FastForwardOptions{})
	item.Message = fixed.Message
	switch fixed.Status {
	case "pulled":
		item.Status = "fixed"
		item.Message = "pulled --ff-only"
		if fixed.Head != "" {
			item.Details = append(item.Details, "head="+fixed.Head)
		}
		return item, result.Role == "manifest", true
	case "already landed":
		item.Status = "skipped"
		item.Message = "already up to date"
	case "failed":
		item.Status = "error"
		item.Message = fixed.Error
	default:
		item.Status = "skipped"
		if item.Message == "" {
			item.Message = fixed.Status
		}
	}
	return item, false, true
}

func doctorDerivedHasDrift(items []doctorItem) bool {
	for _, item := range items {
		if item.Status != "ok" {
			return true
		}
	}
	return false
}

func (a app) doctorFixDerived(home, manifestName, root string) []doctorItem {
	if root == "" {
		return []doctorItem{{Name: "derived", Status: "skipped", Message: "no our umbrella found; run our setup or pass --umbrella"}}
	}
	report, err := a.reconcileDerived(home, manifestName, root)
	if err != nil {
		return []doctorItem{{Name: "derived", Status: "error", Message: err.Error()}}
	}
	return doctorDerivedFixItems(report)
}

func doctorDerivedFixItems(report derivedReconcileReport) []doctorItem {
	items := []doctorItem{{
		Name:    "guidance",
		Status:  doctorFixStatusFromGuidance(report.Guidance.Status),
		Path:    report.Guidance.TargetPath,
		Message: report.Guidance.Message,
	}}
	if report.Guidance.ClaudePath != "" {
		items[0].Details = append(items[0].Details, "claude_path="+report.Guidance.ClaudePath)
	}
	for _, result := range report.Skills {
		item := doctorItem{
			Name:    "skill:" + string(result.Harness) + ":" + result.Skill,
			Status:  doctorFixStatusFromSkill(result.Status),
			Path:    result.TargetPath,
			Message: result.Message,
		}
		if result.CanonicalID != "" {
			item.Details = append(item.Details, "canonical_id="+result.CanonicalID)
		}
		if result.Err != nil {
			item.Details = append(item.Details, result.Err.Error())
		}
		items = append(items, item)
	}
	return items
}

func doctorFixStatusFromGuidance(status string) string {
	switch status {
	case "installed", "updated":
		return "fixed"
	case "blocked":
		return "error"
	default:
		return status
	}
}

func doctorFixStatusFromSkill(status string) string {
	switch status {
	case skills.StatusInstalled, skills.StatusUpdated, skills.StatusRemoved:
		return "fixed"
	case skills.StatusFailed, skills.StatusBlocked:
		return "error"
	case skills.StatusSkipped, skills.StatusDryRun, skills.StatusNotInstalled:
		return "skipped"
	default:
		return status
	}
}

func doctorFixFailed(items []doctorItem) bool {
	for _, item := range items {
		if item.Status == "error" {
			return true
		}
	}
	return false
}

func (a app) doctorDerived(home, manifestName, root string) []doctorItem {
	var items []doctorItem
	items = append(items, a.doctorSkillDrift(home, manifestName)...)
	items = append(items, a.doctorSelfSkill(home)...)
	if root != "" {
		items = append(items, doctorDerivedGuidance(home, root, manifestName))
	}
	if len(items) == 0 {
		items = append(items, doctorItem{Name: "derived", Status: "ok", Message: "no derived drift detected"})
	}
	return items
}

func (a app) doctorSkillDrift(home, manifestName string) []doctorItem {
	opts := skillsCommandOpts{home: home, manifestName: manifestName, quietSource: true, allowMissingToolSkills: true}
	bundled, sourceRoots, _, err := a.discoverSkills(opts)
	if err != nil {
		return []doctorItem{{Name: "skills", Status: "error", Message: err.Error()}}
	}
	if len(bundled) == 0 {
		return nil
	}
	rows, err := a.skillStatusRows(opts, bundled, sourceRoots)
	if err != nil {
		return []doctorItem{{Name: "skills", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	for _, row := range rows {
		if !doctorSkillHarnessPresent(row) {
			continue
		}
		if doctorSkillStatusOK(row.Status) {
			continue
		}
		item := doctorItem{
			Name:   "skill:" + string(row.Harness) + ":" + row.Skill,
			Status: row.Status,
			Path:   row.TargetPath,
		}
		if row.Message != "" {
			item.Message = row.Message
		} else {
			item.Message = row.Remedy
		}
		if row.CanonicalID != "" {
			item.Details = append(item.Details, "canonical_id="+row.CanonicalID)
		}
		if row.SourcePath != "" {
			item.Details = append(item.Details, "source="+row.SourcePath)
		}
		if row.LinkTarget != "" {
			item.Details = append(item.Details, "link_target="+row.LinkTarget)
		}
		if row.Remedy != "" && row.Remedy != item.Message {
			item.Details = append(item.Details, "remedy="+row.Remedy)
		}
		if row.Error != "" {
			item.Details = append(item.Details, row.Error)
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		items = append(items, doctorItem{Name: "skills", Status: "ok", Message: "no present harness skill drift detected"})
	}
	return items
}

func (a app) doctorSelfSkill(home string) []doctorItem {
	rows, err := selfskill.Inspect(harness.All(), selfskill.Options{Home: home})
	if err != nil {
		return []doctorItem{{Name: "selfskill", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	for _, row := range rows {
		if row.Status == "installed" || row.Status == "missing-harness" {
			continue
		}
		item := doctorItem{
			Name:   "selfskill:" + string(row.Harness),
			Status: row.Status,
			Path:   row.TargetPath,
		}
		if row.Message != "" {
			item.Message = row.Message
		} else {
			item.Message = row.Remedy
		}
		if row.CanonicalID != "" {
			item.Details = append(item.Details, "canonical_id="+row.CanonicalID)
		}
		if row.LinkTarget != "" {
			item.Details = append(item.Details, "link_target="+row.LinkTarget)
		}
		if row.Remedy != "" && row.Remedy != item.Message {
			item.Details = append(item.Details, "remedy="+row.Remedy)
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		items = append(items, doctorItem{Name: "selfskill", Status: "ok", Message: "our self-skill installed for present harnesses"})
	}
	return items
}

func doctorSkillHarnessPresent(row skillStatusRow) bool {
	if !row.Harness.IsFilesystem() {
		return true
	}
	if row.TargetPath == "" {
		return false
	}
	info, err := os.Stat(filepath.Dir(row.TargetPath))
	return err == nil && info.IsDir()
}

func doctorSkillStatusOK(status string) bool {
	return status == "installed" || status == "managed-by-gemini"
}

func doctorDerivedGuidance(home, root, manifestName string) doctorItem {
	if manifestName == "" {
		if ws, err := umbrella.LoadWorkspace(root); err == nil {
			manifestName = ws.ManifestRef
		}
	}
	item := doctorGuidance(home, root, manifestName)
	item.Name = "guidance"
	if item.Status == "ok" && item.Message == "" {
		item.Message = "workspace guidance matches current manifest"
	}
	return item
}

type derivedReconcileReport struct {
	Guidance guidance.Result `json:"guidance"`
	Skills   []skills.Result `json:"skills,omitempty"`
}

func (a app) reconcileDerived(home, manifestName, root string) (derivedReconcileReport, error) {
	if manifestName == "" {
		if ws, err := umbrella.LoadWorkspace(root); err == nil {
			manifestName = ws.ManifestRef
		}
	}
	if root == "" {
		return derivedReconcileReport{}, fmt.Errorf("no our umbrella found; run our setup or pass --umbrella")
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return derivedReconcileReport{}, err
	}
	guidanceResult, err := guidance.Ensure(root, doc.ref.LocalPath, doc.doc, guidance.Options{})
	if err != nil {
		return derivedReconcileReport{}, err
	}
	opts := skillsCommandOpts{
		all:                    true,
		home:                   home,
		manifestName:           doc.ref.Name,
		quietSource:            true,
		allowMissingToolSkills: true,
	}
	skillResults, err := a.collectSkillSyncResults(opts, harness.All(), false)
	if err != nil {
		return derivedReconcileReport{}, err
	}
	return derivedReconcileReport{Guidance: guidanceResult, Skills: skillResults}, nil
}

func (a app) printDerivedReconcileReport(report derivedReconcileReport) {
	line := fmt.Sprintf("derived\tguidance\t%s\t%s", report.Guidance.Status, report.Guidance.TargetPath)
	if report.Guidance.Message != "" {
		line += "\t" + report.Guidance.Message
	}
	fmt.Fprintln(a.stdout, line)
	for _, result := range report.Skills {
		line := fmt.Sprintf("derived-skill\t%s\t%s\t%s", result.Harness, result.Skill, result.Status)
		if result.TargetPath != "" {
			line += "\t" + result.TargetPath
		}
		if result.Message != "" {
			line += "\t" + result.Message
		}
		if result.Err != nil {
			line += "\t" + result.Err.Error()
		}
		fmt.Fprintln(a.stdout, line)
	}
}

func derivedReportFailed(report *derivedReconcileReport) bool {
	if report == nil {
		return false
	}
	return derivedReconcileFailed(*report)
}

func derivedReconcileFailed(report derivedReconcileReport) bool {
	if report.Guidance.Status == "blocked" {
		return true
	}
	for _, result := range report.Skills {
		if result.Status == skills.StatusFailed || result.Status == skills.StatusBlocked {
			return true
		}
	}
	return false
}

func doctorLastSync(root string) doctorItem {
	path := filepath.Join(root, umbrella.DirName, lastSyncFile)
	audit, ok, err := loadLastSyncAudit(root)
	if err != nil {
		return doctorItem{Name: "last publish", Status: "error", Path: path, Message: err.Error()}
	}
	if !ok {
		return doctorItem{Name: "last publish", Status: "missing", Path: path, Message: "run our sync to record an audit"}
	}
	item := doctorItem{
		Name:    "last publish",
		Status:  "ok",
		Path:    path,
		Message: lastSyncSummary(audit),
	}
	if syncReportFailed(audit.Report) {
		item.Status = "warning"
	}
	for _, result := range audit.Report.Results {
		item.Details = append(item.Details, lastSyncResultDetail(result))
	}
	return item
}

func lastSyncSummary(audit lastSyncAudit) string {
	parts := []string{"saved_at=" + audit.SavedAt}
	if audit.Report.Publish != "" {
		parts = append(parts, "publish="+audit.Report.Publish)
	}
	if audit.Report.Backend != "" {
		parts = append(parts, "backend="+audit.Report.Backend)
	}
	for _, part := range syncStatusCounts(audit.Report.Results) {
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

func syncStatusCounts(results []syncer.Result) []string {
	order := []string{"pushed", "pulled", "held back", "dry-run", "already landed", "failed"}
	counts := map[string]int{}
	for _, result := range results {
		counts[result.Status]++
	}
	var out []string
	for _, status := range order {
		if counts[status] == 0 {
			continue
		}
		out = append(out, strings.ReplaceAll(status, " ", "_")+"="+strconv.Itoa(counts[status]))
		delete(counts, status)
	}
	var rest []string
	for status := range counts {
		rest = append(rest, status)
	}
	sort.Strings(rest)
	for _, status := range rest {
		out = append(out, strings.ReplaceAll(status, " ", "_")+"="+strconv.Itoa(counts[status]))
	}
	return out
}

func lastSyncResultDetail(result syncer.Result) string {
	parts := []string{result.Role + ":" + result.ID, "status=" + strings.ReplaceAll(result.Status, " ", "_")}
	if result.Manifest != "" {
		parts = append(parts, "manifest="+result.Manifest)
	}
	if result.GitURL != "" {
		parts = append(parts, "remote="+result.GitURL)
	}
	if result.Branch != "" {
		parts = append(parts, "branch="+result.Branch)
	}
	if result.Head != "" {
		parts = append(parts, "head="+result.Head)
	}
	if result.Direction != "" {
		parts = append(parts, "direction="+result.Direction)
	}
	return strings.Join(parts, " ")
}

func doctorWorkspaces(home, manifestName string, declared []manifest.Workspace) []doctorItem {
	homeDir, err := resolveHome(home)
	if err != nil {
		return []doctorItem{{Name: manifestName, Status: "error", Message: err.Error()}}
	}
	out := make([]doctorItem, 0, len(declared))
	for _, w := range declared {
		path := expandUserPath(homeDir, w.LocalPath)
		item := doctorItem{Name: manifestName + ":" + w.ID, Path: path}
		if path == "" {
			item.Status = "error"
			item.Message = "local_path is required"
			out = append(out, item)
			continue
		}
		if info, err := os.Stat(path); err != nil {
			item.Status = "missing"
			item.Message = "run our workspaces sync " + w.ID + " --manifest " + manifestName
		} else if !info.IsDir() {
			item.Status = "error"
			item.Message = "target exists and is not a directory"
		} else if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			item.Status = "warning"
			item.Message = "target exists but is not a git repository"
		} else {
			item.Status = "ok"
		}
		out = append(out, item)
	}
	return out
}

func doctorTools(manifestName string, tools []manifest.Tool) []doctorItem {
	out := make([]doctorItem, 0, len(tools))
	for _, tool := range tools {
		item := doctorItem{Name: manifestName + ":" + tool.ID}
		if path, err := exec.LookPath(tool.ID); err == nil {
			item.Status = "ok"
			item.Path = path
		} else {
			item.Status = "missing"
			item.Message = tool.Purpose
			item.Details = append(item.Details, tool.Install.Commands...)
			if tool.Install.DocsURL != "" {
				item.Details = append(item.Details, tool.Install.DocsURL)
			}
		}
		out = append(out, item)
	}
	return out
}

func (a app) printDoctorReport(report doctorReport) {
	fixable := 0
	printItems := func(kind string, items []doctorItem) {
		for _, item := range items {
			line := fmt.Sprintf("%s\t%s\t%s", kind, item.Name, item.Status)
			if item.Path != "" {
				line += "\t" + item.Path
			}
			if item.Message != "" {
				line += "\t" + item.Message
			}
			if item.WouldFix != "" {
				fixable++
				line += "\twould " + item.WouldFix
			}
			fmt.Fprintln(a.stdout, line)
			for _, detail := range item.Details {
				fmt.Fprintf(a.stdout, "%s\t%s\tdetail\t%s\n", kind, item.Name, detail)
			}
		}
	}
	printItems("manifest", report.Manifests)
	printItems("version", report.Version)
	printItems("legacy", report.Legacy)
	printItems("umbrella", report.Umbrella)
	printItems("freshness", report.Freshness)
	printItems("derived", report.Derived)
	printItems("fix", report.Fixes)
	printItems("last-sync", report.LastSync)
	printItems("workspace", report.Workspaces)
	printItems("tool", report.Tools)
	if fixable > 0 {
		fmt.Fprintf(a.stdout, "fixable\t%d\trun `our doctor --fix` to apply\n", fixable)
	}
}

func doctorUmbrella(home, root string) []doctorItem {
	ws, err := umbrella.LoadWorkspace(root)
	if err != nil {
		return []doctorItem{{Name: root, Status: "error", Path: root, Message: err.Error()}}
	}
	items := []doctorItem{{
		Name:    ws.Organization,
		Status:  "ok",
		Path:    root,
		Message: "manifest " + ws.ManifestRef,
	}}
	items = append(items, doctorGuidance(home, root, ws.ManifestRef))
	state, err := umbrella.LoadState(root)
	if err != nil {
		items = append(items, doctorItem{Name: "state", Status: "error", Path: filepath.Join(root, umbrella.DirName, umbrella.StateFile), Message: err.Error()})
		return items
	}
	for _, mount := range state.Mounts {
		item := doctorItem{
			Name:    mount.ID,
			Status:  mount.Status,
			Path:    stateMountPath(root, mount),
			Message: mount.Kind,
		}
		if mount.LastError != "" {
			item.Details = append(item.Details, mount.LastError)
		}
		items = append(items, item)
	}
	return items
}

func doctorGuidance(home, root, manifestName string) doctorItem {
	item := doctorItem{Name: "guidance"}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		item.Status = "error"
		item.Message = err.Error()
		return item
	}
	result, err := guidance.Check(root, doc.ref.LocalPath, doc.doc)
	if err != nil {
		item.Status = "error"
		item.Path = result.AgentsPath
		item.Message = err.Error()
		return item
	}
	item.Status = result.Status
	item.Path = result.AgentsPath
	item.Message = result.Message
	if result.ClaudePath != "" {
		item.Details = append(item.Details, "claude_path="+result.ClaudePath)
	}
	return item
}

func (a app) runTools(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing tools subcommand")
	}
	switch args[0] {
	case "list":
		return a.runToolsList(args[1:])
	case "info":
		return a.runToolsInfo(args[1:])
	case "-h", "--help", "help":
		a.printToolsUsage()
		return nil
	default:
		return fmt.Errorf("unknown tools subcommand %q", args[0])
	}
}

func (a app) printToolsUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our tools list [--manifest NAME] [--home DIR] [--json]
  our tools info <name> [--manifest NAME] [--home DIR] [--json]

Tool entries are operator-facing hints from synced organization manifests.`)
}

type toolInfo struct {
	Manifest string        `json:"manifest"`
	Tool     manifest.Tool `json:"tool"`
}

func (a app) runToolsList(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("our tools list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("tools list does not accept positional arguments")
	}
	infos, err := a.listToolInfo(home, manifestName)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, infos)
	}
	for _, info := range infos {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", info.Manifest, info.Tool.ID, info.Tool.Mode, info.Tool.Purpose)
	}
	return nil
}

func (a app) runToolsInfo(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("our tools info", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our tools info <name>")
	}
	infos, err := a.findToolInfo(home, manifestName, rest[0])
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, infos)
	}
	for _, info := range infos {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", info.Manifest, info.Tool.ID, info.Tool.Mode, info.Tool.Purpose)
		for _, command := range info.Tool.Install.Commands {
			fmt.Fprintf(a.stdout, "install\t%s\n", command)
		}
		if info.Tool.Install.DocsURL != "" {
			fmt.Fprintf(a.stdout, "docs\t%s\n", info.Tool.Install.DocsURL)
		}
	}
	return nil
}

func (a app) runCustomers(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing customers subcommand")
	}
	switch args[0] {
	case "list":
		return a.runCustomersList(args[1:])
	case "-h", "--help", "help":
		a.printCustomersUsage()
		return nil
	default:
		return fmt.Errorf("unknown customers subcommand %q", args[0])
	}
}

func (a app) printCustomersUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our customers list [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]

Customer data comes from catalog/customers.json and mounted handbook customers/registry.md files.`)
}

func (a app) runCustomersList(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var jsonOut bool
	fs := newFlagSet("our customers list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("customers list does not accept positional arguments")
	}
	customers, err := a.loadCustomers(home, manifestName, umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if jsonOut {
		return printJSON(a.stdout, customers)
	}
	for _, customer := range customers {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%t\t%s\t%s\n",
			customer.ID,
			customer.Name,
			customer.Domain,
			customer.DomainConfirmed,
			strings.Join(customer.Aliases, ","),
			strings.Join(customer.Partners, ","),
		)
	}
	return nil
}

func (a app) runProducts(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing products subcommand")
	}
	switch args[0] {
	case "list":
		return a.runProductsList(args[1:])
	case "-h", "--help", "help":
		a.printProductsUsage()
		return nil
	default:
		return fmt.Errorf("unknown products subcommand %q", args[0])
	}
}

func (a app) printProductsUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our products list [--manifest NAME] [--home DIR] [--json]

Catalog data comes from synced organization manifests.`)
}

func (a app) runProductsList(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("our products list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: our products list")
	}
	products, err := manifest.LoadCatalog(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if jsonOut {
		return printJSON(a.stdout, products)
	}
	a.printProducts(products)
	return nil
}

func (a app) printProducts(products []manifest.Product) {
	for i, product := range products {
		if i != 0 {
			fmt.Fprintln(a.stdout)
		}
		fmt.Fprintf(a.stdout, "%s", product.ID)
		if product.Name != "" {
			fmt.Fprintf(a.stdout, " - %s", product.Name)
		}
		fmt.Fprintln(a.stdout)
		if len(product.Repos) != 0 {
			printHumanField(a.stdout, "repos", strings.Join(product.Repos, ", "))
		}
		if product.Purpose != "" {
			printHumanField(a.stdout, "purpose", product.Purpose)
		} else if product.Description != "" {
			printHumanField(a.stdout, "description", product.Description)
		}
		if len(product.RelatedSkills) != 0 {
			printHumanField(a.stdout, "skills", strings.Join(product.RelatedSkills, ", "))
		}
	}
}

func printHumanField(w io.Writer, label, value string) {
	const width = 88
	text := strings.Join(strings.Fields(value), " ")
	if text == "" {
		return
	}
	firstPrefix := "  " + label + ": "
	nextPrefix := strings.Repeat(" ", len(firstPrefix))
	line := firstPrefix
	for _, word := range strings.Fields(text) {
		if line == firstPrefix {
			line += word
			continue
		}
		if len(line)+1+len(word) <= width {
			line += " " + word
			continue
		}
		fmt.Fprintln(w, line)
		line = nextPrefix + word
	}
	fmt.Fprintln(w, line)
}

func (a app) listToolInfo(home, manifestName string) ([]toolInfo, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var out []toolInfo
	for _, doc := range docs {
		for _, tool := range doc.doc.Tools {
			out = append(out, toolInfo{Manifest: doc.ref.Name, Tool: tool})
		}
	}
	return out, nil
}

func (a app) findToolInfo(home, manifestName, toolID string) ([]toolInfo, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var out []toolInfo
	for _, doc := range docs {
		for _, tool := range doc.doc.Tools {
			if tool.ID == toolID {
				out = append(out, toolInfo{Manifest: doc.ref.Name, Tool: tool})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tool %q is not declared by any selected manifest", toolID)
	}
	return out, nil
}

func loadRegisteredDocs(home, manifestName string) ([]registeredDoc, error) {
	refs, err := manifestRefs(home, manifestName)
	if err != nil {
		return nil, err
	}
	docs := make([]registeredDoc, 0, len(refs))
	for _, ref := range refs {
		doc, _, err := manifest.LoadDocument(ref.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("manifest %q is not synced; run our manifests sync %s: %w", ref.Name, ref.Name, err)
		}
		result := manifest.ValidateFile(ref.LocalPath)
		if len(result.Errors) != 0 {
			return nil, fmt.Errorf("manifest %q is invalid: %s", ref.Name, strings.Join(result.Errors, "; "))
		}
		docs = append(docs, registeredDoc{ref: ref, doc: doc})
	}
	return docs, nil
}

func loadSingleRegisteredDoc(home, manifestName string) (registeredDoc, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return registeredDoc{}, err
	}
	if len(docs) == 0 {
		return registeredDoc{}, fmt.Errorf("our requires a registered manifest")
	}
	if len(docs) != 1 {
		return registeredDoc{}, fmt.Errorf("our requires exactly one manifest; pass --manifest")
	}
	return docs[0], nil
}

func manifestRefs(home, manifestName string) ([]manifest.Ref, error) {
	if manifestName != "" {
		ref, ok, err := manifest.Find(home, manifestName)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("manifest %q is not registered", manifestName)
		}
		return []manifest.Ref{ref}, nil
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return nil, err
	}
	return reg.Manifests, nil
}

func expandUserPath(home, path string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return os.UserHomeDir()
}

func (a app) runWorkspace(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing workspace subcommand")
	}

	switch args[0] {
	case "list":
		return a.runWorkspaceList(args[1:])
	case "sync":
		return a.runWorkspaceSync(args[1:])
	case "-h", "--help", "help":
		a.printWorkspaceUsage()
		return nil
	default:
		return fmt.Errorf("unknown workspace subcommand %q", args[0])
	}
}

func (a app) printWorkspaceUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our workspaces list [--manifest NAME] [--home DIR] [--json]
  our workspaces sync <workspace...> | --all [--manifest NAME] [--home DIR] [--print] [--json]

Workspace data comes from synced organization manifests. Use manifest:workspace
to disambiguate duplicate workspace IDs across manifests.`)
}

func (a app) runWorkspaceList(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("our workspaces list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("workspace list does not accept positional arguments")
	}
	entries, err := workspace.List(home, manifestName)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, entries)
	}
	for _, entry := range entries {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", entry.Manifest, entry.ID, entry.GitURL, entry.LocalPath)
	}
	return nil
}

func (a app) runWorkspaceSync(args []string) error {
	var home string
	var manifestName string
	var all bool
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("our workspaces sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&all, "all", false, "sync every selected workspace")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	results, err := workspace.Sync(home, manifestName, rest, all, printOnly, nil)
	if err != nil {
		return err
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
			return err
		}
	} else {
		a.printWorkspaceResults(results)
	}
	if workspaceResultsFailed(results) {
		return fmt.Errorf("one or more workspace syncs failed")
	}
	return nil
}

func (a app) printWorkspaceResults(results []workspace.SyncResult) {
	for _, r := range results {
		line := fmt.Sprintf("%s\t%s\t%s\t%s", r.Manifest, r.Workspace, r.Status, r.LocalPath)
		if r.Message != "" {
			line += "\t" + r.Message
		}
		if r.Error != "" {
			line += "\t" + r.Error
		}
		fmt.Fprintln(a.stdout, line)
	}
}

func recordMountResults(root string, results []workspace.SyncResult) error {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		state = umbrella.State{SchemaVersion: umbrella.SchemaVersion}
	} else if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, result := range results {
		status := result.Status
		lastSync := ""
		lastError := result.Error
		if result.Status == "synced" {
			lastSync = now
			lastError = ""
		} else if result.Status == "inaccessible" || result.Status == "failed" && strings.Contains(result.Error, "gh auth login") {
			status = "inaccessible"
		}
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        result.Workspace,
			Kind:      result.Kind,
			SourceRef: result.SourceRef,
			Status:    status,
			LastSync:  lastSync,
			LastError: lastError,
		})
	}
	return umbrella.SaveState(root, state)
}

func recordMountResultsByRoot(results []workspace.SyncResult) error {
	byRoot := map[string][]workspace.SyncResult{}
	for _, result := range results {
		if result.UmbrellaRoot == "" {
			continue
		}
		byRoot[result.UmbrellaRoot] = append(byRoot[result.UmbrellaRoot], result)
	}
	for root, rootResults := range byRoot {
		if err := recordMountResults(root, rootResults); err != nil {
			return err
		}
	}
	return nil
}

func recordRepoResults(root string, ids []string, results []workspace.SyncResult) error {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		state = umbrella.State{SchemaVersion: umbrella.SchemaVersion}
	} else if err != nil {
		return err
	}
	for _, id := range ids {
		state = umbrella.AddSelectedRepo(state, id)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, result := range results {
		status := result.Status
		lastSync := ""
		lastError := result.Error
		if result.Status == "synced" {
			lastSync = now
			lastError = ""
		}
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        result.Workspace,
			Kind:      "repo",
			SourceRef: result.SourceRef,
			Status:    status,
			LastSync:  lastSync,
			LastError: lastError,
		})
	}
	return umbrella.SaveState(root, state)
}

func removeMountsFromState(root string, mountIDs []string, repoIDs []string) error {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, id := range mountIDs {
		state = umbrella.RemoveMount(state, id)
	}
	for _, id := range repoIDs {
		state = umbrella.RemoveSelectedRepo(state, id)
		state = umbrella.RemoveMount(state, repoMountID(id))
	}
	return umbrella.SaveState(root, state)
}

func addStateMountEntries(home, manifestName, umbrellaRoot string, entries []workspace.Entry) ([]workspace.Entry, error) {
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		if strings.Contains(err.Error(), "no our umbrella found") {
			return entries, nil
		}
		return nil, err
	}
	ws, err := umbrella.LoadWorkspace(root)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	if manifestName != "" && manifestName != ws.ManifestRef {
		return entries, nil
	}
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	orgByManifest := map[string]string{}
	for _, entry := range entries {
		seen[entry.ID] = true
		if entry.Manifest != "" && entry.Organization != "" {
			orgByManifest[entry.Manifest] = entry.Organization
		}
	}
	organization := orgByManifest[ws.ManifestRef]
	if organization == "" {
		if docs, err := loadRegisteredDocs(home, ws.ManifestRef); err == nil && len(docs) == 1 {
			organization = docs[0].doc.Organization.ID
		}
	}
	for _, mount := range state.Mounts {
		if seen[mount.ID] {
			continue
		}
		entries = append(entries, workspace.Entry{
			Manifest:     ws.ManifestRef,
			Organization: organization,
			ID:           mount.ID,
			Kind:         mount.Kind,
			Mode:         "optional",
			GitURL:       mount.SourceRef,
			LocalPath:    stateMountPath(root, mount),
			UmbrellaRoot: root,
			SourceRef:    mount.SourceRef,
		})
		seen[mount.ID] = true
	}
	return entries, nil
}

func (a app) runMount(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing mount subcommand")
	}
	switch args[0] {
	case "list":
		return a.runMountList(args[1:])
	case "add":
		return a.runMountAdd(args[1:])
	case "sync":
		return a.runMountSync(args[1:])
	case "remove":
		return a.runMountRemove(args[1:])
	case "-h", "--help", "help":
		a.printMountUsage()
		return nil
	default:
		return fmt.Errorf("unknown mount subcommand %q", args[0])
	}
}

func (a app) printMountUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our mounts list [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]
  our mounts add <kind:id|id> [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
  our mounts sync <mount...> | --all [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
  our mounts remove <mount...> [--home DIR] [--umbrella DIR] [--print] [--force] [--json]

Mounts are detached content sources inside the local organization umbrella.`)
}

func (a app) runMountList(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var jsonOut bool
	fs := newFlagSet("our mounts list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("mount list does not accept positional arguments")
	}
	entries, err := workspace.ListMounts(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	entries, err = addStateMountEntries(home, manifestName, umbrellaRoot, entries)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, entries)
	}
	for _, entry := range entries {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", entry.Manifest, entry.ID, entry.Kind, entry.Mode, entry.GitURL, entry.LocalPath)
	}
	return nil
}

func (a app) runMountAdd(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("our mounts add", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our mounts add <kind:id|id>")
	}
	kind, id := splitMountRef(rest[0])
	if kind == "product" {
		return a.maybeJSONError(jsonOut, structuredCommandError{
			code:        "product_mounts_removed",
			message:     "products are business catalog entries, not checkouts; mount product:" + id + " was removed",
			remediation: "use our repos add " + id + " (declared in catalog/repos.json)",
		})
	}
	if kind == "repo" {
		return a.repoAddByID(home, manifestName, umbrellaRoot, id, printOnly, jsonOut)
	}
	entries, err := workspace.ListMounts(home, manifestName, umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	entry, err := selectMountEntry(entries, kind, id)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	results, err := workspace.SyncMounts(home, entry.Manifest, umbrellaRoot, []string{entry.ID}, false, nil, printOnly, nil)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !printOnly {
		if err := recordMountResultsByRoot(results); err != nil {
			return err
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
			return err
		}
	} else {
		a.printWorkspaceResults(results)
	}
	if workspaceResultsFailed(results) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more mount syncs failed"))
	}
	return nil
}

func (a app) repoAddByID(home, manifestName, umbrellaRoot, id string, printOnly bool, jsonOut bool) error {
	if !portableMountID(id) {
		return fmt.Errorf("repo id %q must be lowercase kebab-case", id)
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	repo, ok, err := manifest.FindRepo(home, doc.ref.Name, id)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !ok {
		return a.maybeJSONError(jsonOut, structuredCommandError{
			code:        "unknown_repo",
			message:     fmt.Sprintf("repo %q is not in catalog/repos.json for manifest %q", id, doc.ref.Name),
			remediation: "our repos list --manifest " + doc.ref.Name,
		})
	}
	if err := checkRepoCloneTarget(umbrella.RepoPath(root, id), repo.GitURL); err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !printOnly {
		if _, _, err := umbrella.Ensure(root, doc.doc.Organization.ID, doc.ref.Name); err != nil {
			return err
		}
	}
	results := []workspace.SyncResult{workspace.SyncEntry(repoEntry(doc, root, repo), printOnly, nil)}
	normalizeRepoResults(results)
	if !printOnly {
		if err := recordRepoResults(root, []string{id}, results); err != nil {
			return err
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
			return err
		}
	} else {
		a.printWorkspaceResults(results)
	}
	if workspaceResultsFailed(results) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more mount syncs failed"))
	}
	return nil
}

func (a app) runMountSync(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var all bool
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("our mounts sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&all, "all", false, "sync every selected mount")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	mountRefs, repoIDs, err := splitMountSyncRefs(rest, all)
	if err != nil {
		return err
	}
	var results []workspace.SyncResult
	if all || len(mountRefs) != 0 {
		results, err = workspace.SyncMounts(home, manifestName, umbrellaRoot, mountRefs, all, nil, printOnly, nil)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	} else if len(repoIDs) == 0 {
		return fmt.Errorf("select a mount ID or pass --all")
	}
	repoResults, err := a.syncRepoMounts(home, manifestName, umbrellaRoot, repoIDs, all, printOnly)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	results = append(results, repoResults...)
	if !printOnly {
		if err := recordMountResultsByRoot(results); err != nil {
			return err
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
			return err
		}
	} else {
		a.printWorkspaceResults(results)
	}
	if workspaceResultsFailed(results) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more mount syncs failed"))
	}
	return nil
}

func (a app) syncRepoMounts(home, manifestName, umbrellaRoot string, repoIDs []string, all bool, printOnly bool) ([]workspace.SyncResult, error) {
	if !all && len(repoIDs) == 0 {
		return nil, nil
	}
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		if all && strings.Contains(err.Error(), "no our umbrella found") {
			return nil, nil
		}
		return nil, err
	}
	ws, err := umbrella.LoadWorkspace(root)
	if err != nil {
		return nil, err
	}
	if manifestName != "" && manifestName != ws.ManifestRef {
		return nil, fmt.Errorf("umbrella uses manifest %q, not %q", ws.ManifestRef, manifestName)
	}
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, err
	}
	if all {
		repoIDs = append([]string(nil), state.SelectedRepos...)
	}
	if len(repoIDs) == 0 {
		return nil, nil
	}
	entries := make([]workspace.Entry, 0, len(repoIDs))
	for _, id := range repoIDs {
		entry, err := repoEntryFromState(home, ws.ManifestRef, root, state, id)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	results := workspace.SyncEntries(entries, printOnly, nil)
	normalizeRepoResults(results)
	if !printOnly {
		if err := recordRepoResults(root, repoIDs, results); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// syncSelectedRepos clones or refreshes the umbrella's selected catalog
// repos at setup time, plus any catalog repo marked default that is not yet
// selected.
func (a app) syncSelectedRepos(home string, doc registeredDoc, root string, printOnly bool) ([]workspace.SyncResult, error) {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	catalog, err := manifest.LoadRepoCatalog(home, doc.ref.Name)
	if err != nil {
		return nil, err
	}
	ids := append([]string(nil), state.SelectedRepos...)
	for _, repo := range catalog {
		if repo.Default && !stringInSlice(ids, repo.ID) {
			ids = append(ids, repo.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	entries := make([]workspace.Entry, 0, len(ids))
	for _, id := range ids {
		repo, ok, err := manifest.FindRepo(home, doc.ref.Name, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, structuredCommandError{
				code:        "unknown_repo",
				message:     fmt.Sprintf("repo %q is not in catalog/repos.json for manifest %q", id, doc.ref.Name),
				remediation: "our repos list --manifest " + doc.ref.Name,
			}
		}
		entries = append(entries, repoEntry(doc, root, repo))
	}
	results := workspace.SyncEntries(entries, printOnly, nil)
	normalizeRepoResults(results)
	if !printOnly {
		if err := recordRepoResults(root, ids, results); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func repoEntryFromState(home, manifestName, root string, state umbrella.State, id string) (workspace.Entry, error) {
	mountID := repoMountID(id)
	for _, mount := range state.Mounts {
		if mount.ID == mountID && mount.Kind == "repo" && mount.SourceRef != "" {
			return workspace.Entry{
				Manifest:     manifestName,
				ID:           mountID,
				Kind:         "repo",
				Mode:         "optional",
				GitURL:       mount.SourceRef,
				LocalPath:    umbrella.RepoPath(root, id),
				UmbrellaRoot: root,
				SourceRef:    mount.SourceRef,
			}, nil
		}
	}
	repo, ok, err := manifest.FindRepo(home, manifestName, id)
	if err != nil {
		return workspace.Entry{}, err
	}
	if !ok {
		return workspace.Entry{}, structuredCommandError{
			code:        "unknown_repo",
			message:     fmt.Sprintf("repo %q is not in catalog/repos.json for manifest %q", id, manifestName),
			remediation: "our repos list --manifest " + manifestName,
		}
	}
	return workspace.Entry{
		Manifest:     manifestName,
		ID:           mountID,
		Kind:         "repo",
		Mode:         "optional",
		GitURL:       repo.GitURL,
		LocalPath:    umbrella.RepoPath(root, id),
		UmbrellaRoot: root,
		SourceRef:    repo.GitURL,
	}, nil
}

func (a app) runMountRemove(args []string) error {
	var home string
	var umbrellaRoot string
	var printOnly bool
	var force bool
	var jsonOut bool
	fs := newFlagSet("our mounts remove", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&printOnly, "print", false, "print planned removals without changing files")
	fs.BoolVar(&force, "force", false, "remove mount directories")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: our mounts remove <mount...>")
	}
	if !force && !printOnly {
		return fmt.Errorf("mount remove requires --force or --print")
	}
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return err
	}
	type removeResult struct {
		Mount      string `json:"mount"`
		TargetPath string `json:"target_path"`
		Status     string `json:"status"`
	}
	var results []removeResult
	var removedMountIDs []string
	var removedRepoIDs []string
	for _, ref := range rest {
		kind, id := splitMountRef(ref)
		if kind == "product" {
			return fmt.Errorf("products are business catalog entries, not checkouts; use repo:%s (see our repos list)", id)
		}
		if !portableMountID(id) {
			return fmt.Errorf("mount id %q must be lowercase kebab-case", id)
		}
		target := mountRemoveTarget(root, kind, id)
		result := removeResult{Mount: id, TargetPath: target}
		if printOnly {
			result.Status = "dry-run"
		} else if err := os.RemoveAll(target); err != nil {
			result.Status = "failed"
		} else {
			result.Status = "removed"
			if kind == "repo" {
				removedRepoIDs = append(removedRepoIDs, id)
			} else {
				removedMountIDs = append(removedMountIDs, id)
			}
		}
		results = append(results, result)
	}
	if !printOnly {
		if err := removeMountsFromState(root, removedMountIDs, removedRepoIDs); err != nil {
			return err
		}
	}
	if jsonOut {
		return printJSON(a.stdout, results)
	}
	for _, result := range results {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", result.Mount, result.Status, result.TargetPath)
	}
	return nil
}

func portableMountID(value string) bool {
	if value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func splitMountRef(ref string) (kind, id string) {
	if i := strings.Index(ref, ":"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

func splitMountSyncRefs(refs []string, all bool) ([]string, []string, error) {
	if all && len(refs) != 0 {
		return nil, nil, fmt.Errorf("--all cannot be combined with explicit mount IDs")
	}
	var mountRefs []string
	var repoIDs []string
	for _, ref := range refs {
		kind, id := splitMountRef(ref)
		if kind == "product" {
			return nil, nil, fmt.Errorf("products are business catalog entries, not checkouts; use repo:%s (see our repos list)", id)
		}
		if kind == "repo" {
			if !portableMountID(id) {
				return nil, nil, fmt.Errorf("repo id %q must be lowercase kebab-case", id)
			}
			repoIDs = append(repoIDs, id)
			continue
		}
		mountRefs = append(mountRefs, ref)
	}
	return mountRefs, repoIDs, nil
}

// checkRepoCloneTarget guards an idempotent repo add: an existing clone of
// the same remote is adopted, anything else at the path is a structured hold.
func checkRepoCloneTarget(path, gitURL string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return structuredCommandError{
			code:        "repo_path_conflict",
			message:     fmt.Sprintf("repo path %s exists and is not a directory", path),
			remediation: "move the conflicting file aside, then rerun our repos add",
		}
	}
	if !isGitCheckout(path) {
		if entries, readErr := os.ReadDir(path); readErr == nil && len(entries) == 0 {
			return nil
		}
		return structuredCommandError{
			code:        "repo_path_conflict",
			message:     fmt.Sprintf("repo path %s exists but is not a git checkout", path),
			remediation: "move the conflicting directory aside, then rerun our repos add",
		}
	}
	origin, _ := gitCmdOutput(path, "remote", "get-url", "origin")
	if origin != "" && origin != gitURL {
		return structuredCommandError{
			code:        "repo_remote_mismatch",
			message:     fmt.Sprintf("repo path %s tracks %s, not the declared %s", path, origin, gitURL),
			remediation: "reconcile or move the existing checkout, then rerun our repos add",
		}
	}
	return nil
}

func repoMountID(id string) string {
	return "repo:" + id
}

func mountRemoveTarget(root, kind, id string) string {
	if kind == "repo" {
		return umbrella.RepoPath(root, id)
	}
	return filepath.Join(root, id)
}

func stateMountPath(root string, mount umbrella.MountStatus) string {
	if mount.Kind == "repo" && strings.HasPrefix(mount.ID, "repo:") {
		return umbrella.RepoPath(root, strings.TrimPrefix(mount.ID, "repo:"))
	}
	return umbrella.MountPath(root, mount.ID)
}

func repoEntry(doc registeredDoc, root string, repo manifest.Repo) workspace.Entry {
	return workspace.Entry{
		Manifest:     doc.ref.Name,
		Organization: doc.doc.Organization.ID,
		ID:           repoMountID(repo.ID),
		Kind:         "repo",
		Mode:         "optional",
		GitURL:       repo.GitURL,
		LocalPath:    umbrella.RepoPath(root, repo.ID),
		UmbrellaRoot: root,
		SourceRef:    repo.GitURL,
	}
}

func normalizeRepoResults(results []workspace.SyncResult) {
	for i := range results {
		if results[i].Status == "failed" && strings.Contains(results[i].Error, "gh auth login") {
			results[i].Status = "inaccessible"
		}
	}
}

func selectMountEntry(entries []workspace.Entry, kind, id string) (workspace.Entry, error) {
	var matches []workspace.Entry
	for _, entry := range entries {
		if entry.ID != id {
			continue
		}
		if kind != "" && entry.Kind != kind {
			continue
		}
		matches = append(matches, entry)
	}
	if len(matches) == 0 {
		if kind != "" {
			return workspace.Entry{}, fmt.Errorf("mount %q is not declared for kind %q", id, kind)
		}
		return workspace.Entry{}, fmt.Errorf("mount %q is not declared by any selected manifest", id)
	}
	if len(matches) > 1 {
		return workspace.Entry{}, fmt.Errorf("mount %q is ambiguous; pass --manifest", id)
	}
	return matches[0], nil
}

func resolveUmbrellaRoot(home, explicit string) (string, error) {
	if explicit != "" {
		if explicit == "~" {
			resolved, err := resolveHome(home)
			if err != nil {
				return "", err
			}
			return resolved, nil
		}
		resolvedHome, err := resolveHome(home)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(explicit, "~/") {
			return filepath.Join(resolvedHome, explicit[2:]), nil
		}
		return filepath.Abs(explicit)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return root, nil
	}
	return "", fmt.Errorf("no our umbrella found; run our setup or pass --umbrella")
}

func (a app) runAdmin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin subcommand")
	}
	switch args[0] {
	case "skills":
		return a.runAdminSkills(args[1:])
	case "setup":
		return a.runOnboard(args[1:])
	case "manifests":
		return a.runAdminManifest(args[1:])
	case "mounts":
		return a.runAdminMount(args[1:])
	case "meetings":
		return a.runAdminMeetings(args[1:])
	case "support":
		return a.runAdminSupport(args[1:])
	case "customers":
		return a.runAdminCustomers(args[1:])
	case "tools":
		return a.runAdminTools(args[1:])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin subcommand %q", args[0])
	}
}

func (a app) printAdminUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our admin skills add <skill-dir> --id namespace:name --manifest-dir DIR [--install-slug SLUG] [--keep-original|--remove-original] [--force] [--json]
  our admin skills remove <id|slug> --manifest-dir DIR [--delete-source] [--prune-related] [--prune-orphans] [--force] [--json]
  our admin setup ...                      (alias of our setup)
  our admin manifests add|sync|validate ...   (alias of our manifests ...)
  our admin mounts add|remove|sync ...        (alias of our mounts ...)
  our admin meetings add ...                 (alias of our meetings add)
  our admin support add ...                  (alias of our support add)
  our admin customers add|edit ...           (edit manifest customer catalog)
  our admin tools add <id> --manifest-dir DIR --mode required|optional --purpose TEXT [--install-command CMD] [--docs-url URL] [--skill-install-command CMD] [--skill-install-arg ARG] [--force] [--json]
  our admin tools edit <id> --manifest-dir DIR [--mode required|optional] [--purpose TEXT] [--install-command CMD] [--clear-install-commands] [--docs-url URL|--clear-docs-url] [--skill-install-command CMD] [--skill-install-arg ARG] [--clear-skill-install] [--force] [--json]
  our admin tools remove <id> --manifest-dir DIR [--force] [--json]

admin groups shared/workspace configuration. The top-level command forms remain
as compatibility aliases. Admin aliases are limited to mutating/configuration
subcommands; operational reads (skills list/show/status, manifests list, mounts
list, tools list/info, meetings/support list/search/get) stay under their
top-level commands.`)
}

func adminOperationalReadError(group, subcommand string) error {
	return fmt.Errorf("our admin %s %s is operational; use our %s %s", group, subcommand, group, subcommand)
}

func (a app) runAdminManifest(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin manifests subcommand")
	}
	switch args[0] {
	case "add", "sync", "validate":
		return a.runManifest(args)
	case "list":
		return adminOperationalReadError("manifests", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin manifests subcommand %q", args[0])
	}
}

func (a app) runAdminMount(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin mounts subcommand")
	}
	switch args[0] {
	case "add", "sync", "remove":
		return a.runMount(args)
	case "list":
		return adminOperationalReadError("mounts", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin mounts subcommand %q", args[0])
	}
}

func (a app) runAdminMeetings(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin meetings subcommand")
	}
	switch args[0] {
	case "add":
		return a.runMeetings(args)
	case "list", "search", "get":
		return adminOperationalReadError("meetings", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin meetings subcommand %q", args[0])
	}
}

func (a app) runAdminSupport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin support subcommand")
	}
	switch args[0] {
	case "add":
		return a.runSupport(args)
	case "list", "search", "get":
		return adminOperationalReadError("support", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin support subcommand %q", args[0])
	}
}

func (a app) runAdminCustomers(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin customers subcommand")
	}
	switch args[0] {
	case "add":
		return a.runAdminCustomersAdd(args[1:])
	case "edit":
		return a.runAdminCustomersEdit(args[1:])
	case "list":
		return adminOperationalReadError("customers", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin customers subcommand %q", args[0])
	}
}

func (a app) runAdminTools(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin tools subcommand")
	}
	switch args[0] {
	case "add":
		return a.runAdminToolsAdd(args[1:])
	case "edit":
		return a.runAdminToolsEdit(args[1:])
	case "remove":
		return a.runAdminToolsRemove(args[1:])
	case "list", "info":
		return adminOperationalReadError("tools", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin tools subcommand %q", args[0])
	}
}

func (a app) runAdminSkills(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin skills subcommand")
	}
	switch args[0] {
	case "add":
		return a.runAdminSkillsAdd(args[1:])
	case "remove":
		return a.runAdminSkillsRemove(args[1:])
	case "list", "show", "status":
		return adminOperationalReadError("skills", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin skills subcommand %q", args[0])
	}
}

type adminSkillResult struct {
	Action          string   `json:"action"`
	ID              string   `json:"id"`
	InstallSlug     string   `json:"install_slug,omitempty"`
	ManifestPath    string   `json:"manifest_path"`
	SourcePath      string   `json:"source_path,omitempty"`
	RemovedOriginal bool     `json:"removed_original,omitempty"`
	DeletedSource   bool     `json:"deleted_source,omitempty"`
	PrunedProducts  []string `json:"pruned_products,omitempty"`
	OrphanedTools   []string `json:"orphaned_tools,omitempty"`
	OrphanedNS      []string `json:"orphaned_allowed_namespaces,omitempty"`
	PrunedTools     []string `json:"pruned_tools,omitempty"`
	PrunedNS        []string `json:"pruned_allowed_namespaces,omitempty"`
	Message         string   `json:"message,omitempty"`
	NextCommands    []string `json:"next_commands,omitempty"`
}

type adminCustomerResult struct {
	Action       string            `json:"action"`
	ID           string            `json:"id"`
	CatalogPath  string            `json:"catalog_path"`
	Customer     manifest.Customer `json:"customer"`
	Message      string            `json:"message,omitempty"`
	NextCommands []string          `json:"next_commands,omitempty"`
}

type adminToolResult struct {
	Action       string        `json:"action"`
	ID           string        `json:"id"`
	ManifestPath string        `json:"manifest_path"`
	Tool         manifest.Tool `json:"tool,omitempty"`
	Message      string        `json:"message,omitempty"`
	NextCommands []string      `json:"next_commands,omitempty"`
}

type adminCustomerOpts struct {
	manifestDir     string
	name            optionalStringFlag
	domain          optionalStringFlag
	aliases         stringListFlag
	partners        stringListFlag
	domainConfirmed optionalBoolFlag
	force           bool
	jsonOut         bool
}

type adminToolOpts struct {
	manifestDir          string
	mode                 optionalStringFlag
	purpose              optionalStringFlag
	installCommands      stringListFlag
	docsURL              optionalStringFlag
	skillInstallCommand  optionalStringFlag
	skillInstallArgs     stringListFlag
	clearInstallCommands bool
	clearDocsURL         bool
	clearSkillInstall    bool
	force                bool
	jsonOut              bool
}

type optionalStringFlag struct {
	value string
	set   bool
}

func (f *optionalStringFlag) String() string {
	return f.value
}

func (f *optionalStringFlag) Set(value string) error {
	f.value = strings.TrimSpace(value)
	f.set = true
	return nil
}

type optionalBoolFlag struct {
	value bool
	set   bool
}

func (f *optionalBoolFlag) String() string {
	return strconv.FormatBool(f.value)
}

func (f *optionalBoolFlag) Set(value string) error {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	f.value = parsed
	f.set = true
	return nil
}

func (f *optionalBoolFlag) IsBoolFlag() bool {
	return true
}

func (a app) runAdminCustomersAdd(args []string) error {
	opts, rest, err := parseAdminCustomerOpts("our admin customers add", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" {
		return fmt.Errorf("usage: our admin customers add <id> --manifest-dir DIR")
	}
	result, err := a.adminCustomersAdd(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminCustomerResult(result, opts.jsonOut)
}

func (a app) runAdminCustomersEdit(args []string) error {
	opts, rest, err := parseAdminCustomerOpts("our admin customers edit", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" {
		return fmt.Errorf("usage: our admin customers edit <id> --manifest-dir DIR")
	}
	result, err := a.adminCustomersEdit(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminCustomerResult(result, opts.jsonOut)
}

func parseAdminCustomerOpts(name string, stderr io.Writer, args []string) (adminCustomerOpts, []string, error) {
	var opts adminCustomerOpts
	fs := newFlagSet(name, stderr)
	fs.StringVar(&opts.manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.Var(&opts.name, "name", "customer display name")
	fs.Var(&opts.domain, "domain", "customer domain")
	fs.Var(&opts.aliases, "alias", "customer alias (repeatable)")
	fs.Var(&opts.partners, "partner", "customer partner (repeatable)")
	fs.Var(&opts.domainConfirmed, "domain-confirmed", "mark the customer domain as confirmed")
	fs.BoolVar(&opts.force, "force", false, "allow dirty checkout")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"manifest-dir": true,
		"name":         true,
		"domain":       true,
		"alias":        true,
		"partner":      true,
	})
	return opts, rest, err
}

func (a app) printAdminCustomerResult(result adminCustomerResult, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", result.Action, result.ID, result.CatalogPath)
	if result.Message != "" {
		fmt.Fprintln(a.stdout, result.Message)
	}
	printAdminNextCommands(a.stdout, result.NextCommands)
	return nil
}

func (a app) runAdminToolsAdd(args []string) error {
	opts, rest, err := parseAdminToolOpts("our admin tools add", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" || !opts.mode.set || !opts.purpose.set {
		return fmt.Errorf("usage: our admin tools add <id> --manifest-dir DIR --mode required|optional --purpose TEXT")
	}
	result, err := a.adminToolsAdd(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminToolResult(result, opts.jsonOut)
}

func (a app) runAdminToolsEdit(args []string) error {
	opts, rest, err := parseAdminToolOpts("our admin tools edit", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" {
		return fmt.Errorf("usage: our admin tools edit <id> --manifest-dir DIR")
	}
	result, err := a.adminToolsEdit(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminToolResult(result, opts.jsonOut)
}

func (a app) runAdminToolsRemove(args []string) error {
	var manifestDir string
	var force bool
	var jsonOut bool
	fs := newFlagSet("our admin tools remove", a.stderr)
	fs.StringVar(&manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.BoolVar(&force, "force", false, "allow dirty checkout")
	fs.BoolVar(&jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{"manifest-dir": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 || manifestDir == "" {
		return fmt.Errorf("usage: our admin tools remove <id> --manifest-dir DIR")
	}
	result, err := a.adminToolsRemove(rest[0], manifestDir, force)
	if err != nil {
		return err
	}
	return a.printAdminToolResult(result, jsonOut)
}

func parseAdminToolOpts(name string, stderr io.Writer, args []string) (adminToolOpts, []string, error) {
	var opts adminToolOpts
	fs := newFlagSet(name, stderr)
	fs.StringVar(&opts.manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.Var(&opts.mode, "mode", "tool mode: required or optional")
	fs.Var(&opts.purpose, "purpose", "tool purpose")
	fs.Var(&opts.installCommands, "install-command", "install command hint (repeatable)")
	fs.Var(&opts.docsURL, "docs-url", "tool documentation URL")
	fs.Var(&opts.skillInstallCommand, "skill-install-command", "command that materializes tool-provided skills")
	fs.Var(&opts.skillInstallArgs, "skill-install-arg", "argument for the skill install command (repeatable)")
	fs.BoolVar(&opts.clearInstallCommands, "clear-install-commands", false, "remove install command hints")
	fs.BoolVar(&opts.clearDocsURL, "clear-docs-url", false, "remove the docs URL")
	fs.BoolVar(&opts.clearSkillInstall, "clear-skill-install", false, "remove skill_install")
	fs.BoolVar(&opts.force, "force", false, "allow dirty checkout or replace an existing declaration")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"manifest-dir":          true,
		"mode":                  true,
		"purpose":               true,
		"install-command":       true,
		"docs-url":              true,
		"skill-install-command": true,
		"skill-install-arg":     true,
	})
	if err != nil {
		return opts, rest, err
	}
	if opts.clearInstallCommands && len(opts.installCommands) != 0 {
		return opts, rest, fmt.Errorf("--clear-install-commands cannot be combined with --install-command")
	}
	if opts.clearDocsURL && opts.docsURL.set {
		return opts, rest, fmt.Errorf("--clear-docs-url cannot be combined with --docs-url")
	}
	if opts.clearSkillInstall && (opts.skillInstallCommand.set || len(opts.skillInstallArgs) != 0) {
		return opts, rest, fmt.Errorf("--clear-skill-install cannot be combined with --skill-install-command or --skill-install-arg")
	}
	if opts.mode.set && !validToolMode(opts.mode.value) {
		return opts, rest, fmt.Errorf("--mode must be required or optional")
	}
	return opts, rest, nil
}

func (a app) printAdminToolResult(result adminToolResult, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", result.Action, result.ID, result.ManifestPath)
	if result.Message != "" {
		fmt.Fprintln(a.stdout, result.Message)
	}
	printAdminNextCommands(a.stdout, result.NextCommands)
	return nil
}

func (a app) adminCustomersAdd(id string, opts adminCustomerOpts) (adminCustomerResult, error) {
	root, customers, path, err := loadAdminCustomerCatalogFromCheckout(opts.manifestDir)
	if err != nil {
		return adminCustomerResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminCustomerResult{}, err
	}
	id = strings.TrimSpace(id)
	if !manifest.ValidCustomerID(id) {
		return adminCustomerResult{}, fmt.Errorf("customer id %q must be lowercase FQDN-style or kebab-case", id)
	}
	if customerIndex(customers, id) != -1 {
		return adminCustomerResult{}, fmt.Errorf("customer %q already exists", id)
	}
	customer := manifest.Customer{ID: id}
	applyCustomerOpts(&customer, opts, true)
	customers = append(customers, customer)
	if err := manifest.ValidateCustomers(path, customers); err != nil {
		return adminCustomerResult{}, err
	}
	if err := saveAdminCustomerCatalog(path, customers); err != nil {
		return adminCustomerResult{}, err
	}
	return adminCustomerResult{
		Action:       "added",
		ID:           customer.ID,
		CatalogPath:  path,
		Customer:     customer,
		Message:      "added customer catalog entry",
		NextCommands: adminNextCommands(root),
	}, nil
}

func (a app) adminCustomersEdit(id string, opts adminCustomerOpts) (adminCustomerResult, error) {
	root, customers, path, err := loadAdminCustomerCatalogFromCheckout(opts.manifestDir)
	if err != nil {
		return adminCustomerResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminCustomerResult{}, err
	}
	id = strings.TrimSpace(id)
	if !manifest.ValidCustomerID(id) {
		return adminCustomerResult{}, fmt.Errorf("customer id %q must be lowercase FQDN-style or kebab-case", id)
	}
	idx := customerIndex(customers, id)
	if idx == -1 {
		return adminCustomerResult{}, fmt.Errorf("customer %q does not exist", id)
	}
	applyCustomerOpts(&customers[idx], opts, false)
	if err := manifest.ValidateCustomers(path, customers); err != nil {
		return adminCustomerResult{}, err
	}
	if err := saveAdminCustomerCatalog(path, customers); err != nil {
		return adminCustomerResult{}, err
	}
	return adminCustomerResult{
		Action:       "edited",
		ID:           customers[idx].ID,
		CatalogPath:  path,
		Customer:     customers[idx],
		Message:      "updated customer catalog entry",
		NextCommands: adminNextCommands(root),
	}, nil
}

func (a app) adminToolsAdd(id string, opts adminToolOpts) (adminToolResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminToolResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminToolResult{}, err
	}
	id = strings.TrimSpace(id)
	if !portableKebab(id) {
		return adminToolResult{}, fmt.Errorf("tool id %q must be lowercase kebab-case", id)
	}
	idx := toolIndex(doc.Tools, id)
	if idx != -1 && !opts.force {
		return adminToolResult{}, fmt.Errorf("tool %q already exists; re-run with --force to replace it", id)
	}
	tool := manifest.Tool{ID: id}
	applyAdminToolOpts(&tool, opts)
	if idx == -1 {
		doc.Tools = append(doc.Tools, tool)
	} else {
		doc.Tools[idx] = tool
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminToolResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminToolResult{}, err
	}
	action := "added"
	message := "added tool declaration"
	if idx != -1 {
		action = "edited"
		message = "replaced tool declaration"
	}
	return adminToolResult{
		Action:       action,
		ID:           tool.ID,
		ManifestPath: manifestPath,
		Tool:         tool,
		Message:      message,
		NextCommands: adminNextCommands(root),
	}, nil
}

func (a app) adminToolsEdit(id string, opts adminToolOpts) (adminToolResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminToolResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminToolResult{}, err
	}
	id = strings.TrimSpace(id)
	idx := toolIndex(doc.Tools, id)
	if idx == -1 {
		return adminToolResult{}, fmt.Errorf("tool %q does not exist", id)
	}
	applyAdminToolOpts(&doc.Tools[idx], opts)
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminToolResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminToolResult{}, err
	}
	return adminToolResult{
		Action:       "edited",
		ID:           doc.Tools[idx].ID,
		ManifestPath: manifestPath,
		Tool:         doc.Tools[idx],
		Message:      "updated tool declaration",
		NextCommands: adminNextCommands(root),
	}, nil
}

func (a app) adminToolsRemove(id, manifestDir string, force bool) (adminToolResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminToolResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminToolResult{}, err
	}
	id = strings.TrimSpace(id)
	idx := toolIndex(doc.Tools, id)
	if idx == -1 {
		return adminToolResult{}, fmt.Errorf("tool %q does not exist", id)
	}
	refs := adminToolSkillReferences(doc, id)
	if len(refs) != 0 {
		return adminToolResult{}, fmt.Errorf("tool %q is referenced by skills: %s", id, strings.Join(refs, ", "))
	}
	removed := doc.Tools[idx]
	doc.Tools = append(doc.Tools[:idx], doc.Tools[idx+1:]...)
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminToolResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminToolResult{}, err
	}
	return adminToolResult{
		Action:       "removed",
		ID:           removed.ID,
		ManifestPath: manifestPath,
		Tool:         removed,
		Message:      "removed tool declaration",
		NextCommands: adminNextCommands(root),
	}, nil
}

func applyAdminToolOpts(tool *manifest.Tool, opts adminToolOpts) {
	if opts.mode.set {
		tool.Mode = opts.mode.value
	}
	if opts.purpose.set {
		tool.Purpose = opts.purpose.value
	}
	if opts.clearInstallCommands {
		tool.Install.Commands = nil
	} else if len(opts.installCommands) != 0 {
		tool.Install.Commands = append([]string(nil), opts.installCommands...)
	}
	if opts.clearDocsURL {
		tool.Install.DocsURL = ""
	} else if opts.docsURL.set {
		tool.Install.DocsURL = opts.docsURL.value
	}
	if opts.clearSkillInstall {
		tool.SkillInstall = manifest.SkillInstall{}
		return
	}
	if opts.skillInstallCommand.set {
		tool.SkillInstall.Command = opts.skillInstallCommand.value
	}
	if len(opts.skillInstallArgs) != 0 {
		tool.SkillInstall.Args = append([]string(nil), opts.skillInstallArgs...)
	}
}

func toolIndex(tools []manifest.Tool, id string) int {
	for i, tool := range tools {
		if tool.ID == id {
			return i
		}
	}
	return -1
}

func adminToolSkillReferences(doc manifest.Document, toolID string) []string {
	var refs []string
	for _, skill := range doc.Skills {
		if adminSkillToolRefs(skill)[toolID] {
			refs = append(refs, skill.ID)
		}
	}
	return refs
}

func validToolMode(value string) bool {
	switch value {
	case "required", "optional":
		return true
	default:
		return false
	}
}

func applyCustomerOpts(customer *manifest.Customer, opts adminCustomerOpts, add bool) {
	if opts.name.set {
		customer.Name = opts.name.value
	}
	if opts.domain.set {
		customer.Domain = opts.domain.value
	}
	if len(opts.aliases) != 0 {
		customer.Aliases = append([]string(nil), opts.aliases...)
	}
	if len(opts.partners) != 0 {
		customer.Partners = append([]string(nil), opts.partners...)
	}
	if opts.domainConfirmed.set {
		customer.DomainConfirmed = opts.domainConfirmed.value
	} else if add {
		customer.DomainConfirmed = false
	}
}

func customerIndex(customers []manifest.Customer, id string) int {
	for i, customer := range customers {
		if customer.ID == id {
			return i
		}
	}
	return -1
}

func (a app) runAdminSkillsAdd(args []string) error {
	var id string
	var manifestDir string
	var installSlug string
	var keepOriginal bool
	var removeOriginal bool
	var force bool
	var jsonOut bool
	fs := newFlagSet("our admin skills add", a.stderr)
	fs.StringVar(&id, "id", "", "canonical skill id namespace:name")
	fs.StringVar(&manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.StringVar(&installSlug, "install-slug", "", "portable harness install slug")
	fs.BoolVar(&keepOriginal, "keep-original", false, "explicitly keep a harness-visible original skill directory")
	fs.BoolVar(&removeOriginal, "remove-original", false, "delete the original skill directory after import")
	fs.BoolVar(&force, "force", false, "allow dirty checkout or replace an existing declaration/source")
	fs.BoolVar(&jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"id":           true,
		"manifest-dir": true,
		"install-slug": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 || id == "" || manifestDir == "" {
		return fmt.Errorf("usage: our admin skills add <skill-dir> --id namespace:name --manifest-dir DIR")
	}
	if keepOriginal && removeOriginal {
		return fmt.Errorf("--keep-original and --remove-original are mutually exclusive")
	}
	result, err := a.adminSkillsAdd(rest[0], id, installSlug, manifestDir, keepOriginal, removeOriginal, force)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", result.Action, result.ID, result.InstallSlug, result.ManifestPath)
	if result.Message != "" {
		fmt.Fprintln(a.stdout, result.Message)
	}
	printAdminNextCommands(a.stdout, result.NextCommands)
	return nil
}

func (a app) adminSkillsAdd(skillDir, id, installSlug, manifestDir string, keepOriginal, removeOriginal, force bool) (adminSkillResult, error) {
	source, err := filepath.Abs(skillDir)
	if err != nil {
		return adminSkillResult{}, err
	}
	if resolved, err := filepath.EvalSymlinks(source); err == nil {
		source = resolved
	}
	if _, err := os.Stat(filepath.Join(source, "SKILL.md")); err != nil {
		return adminSkillResult{}, fmt.Errorf("skill directory %s must contain SKILL.md: %w", source, err)
	}
	if visible := harnessVisibleSkillSource(source); visible != "" && !keepOriginal && !removeOriginal {
		return adminSkillResult{}, fmt.Errorf("source skill is already visible to %s; pass --keep-original or --remove-original explicitly", visible)
	}
	doc, manifestPath, root, err := loadAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminSkillResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminSkillResult{}, err
	}
	if installSlug == "" {
		installSlug = filepath.Base(source)
	}
	if !portableKebab(installSlug) {
		return adminSkillResult{}, fmt.Errorf("install slug %q must be lowercase kebab-case", installSlug)
	}
	if err := validateAdminSkillID(id, doc); err != nil {
		return adminSkillResult{}, err
	}
	if meta, err := discoverSingleSkill(source); err == nil {
		for _, warning := range meta.Warnings {
			fmt.Fprintf(a.stderr, "warning: %s\n", warning)
		}
		if meta.SkillName != "" && meta.SkillName != installSlug {
			fmt.Fprintf(a.stderr, "warning: SKILL.md name %q does not match install slug %q\n", meta.SkillName, installSlug)
		}
	}

	replaced := false
	for _, existing := range doc.Skills {
		if existing.ID == id || existing.InstallSlug == installSlug {
			if !force {
				return adminSkillResult{}, fmt.Errorf("skill id or install slug already exists; re-run with --force to replace it")
			}
			replaced = true
		}
	}
	relPath := filepath.ToSlash(filepath.Join("skills", installSlug))
	target := filepath.Join(root, "skills", installSlug)
	nextSkills := make([]manifest.Skill, 0, len(doc.Skills)+1)
	for _, existing := range doc.Skills {
		if existing.ID == id || existing.InstallSlug == installSlug {
			continue
		}
		nextSkills = append(nextSkills, existing)
	}
	doc.Skills = append(nextSkills, manifest.Skill{
		ID:          id,
		InstallSlug: installSlug,
		Path:        relPath,
		Source:      manifest.Source{Type: "static"},
	})
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminSkillResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if !samePath(source, target) {
		if _, err := os.Stat(target); err == nil {
			if !force {
				return adminSkillResult{}, fmt.Errorf("manifest skill source %s already exists; re-run with --force to replace it", target)
			}
			if err := os.RemoveAll(target); err != nil {
				return adminSkillResult{}, err
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return adminSkillResult{}, err
		}
		if err := skills.CopyDir(source, target); err != nil {
			return adminSkillResult{}, fmt.Errorf("copy skill into manifest: %w", err)
		}
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminSkillResult{}, err
	}
	removedOriginal := false
	if removeOriginal {
		if samePath(source, target) {
			return adminSkillResult{}, fmt.Errorf("--remove-original would delete the imported manifest source")
		}
		if err := os.RemoveAll(source); err != nil {
			return adminSkillResult{}, err
		}
		removedOriginal = true
	}
	message := "imported skill"
	if replaced {
		message = "replaced skill declaration"
	}
	return adminSkillResult{
		Action:          "added",
		ID:              id,
		InstallSlug:     installSlug,
		ManifestPath:    manifestPath,
		SourcePath:      target,
		RemovedOriginal: removedOriginal,
		Message:         message,
		NextCommands:    adminNextCommands(root),
	}, nil
}

func (a app) runAdminSkillsRemove(args []string) error {
	var manifestDir string
	var deleteSource bool
	var pruneRelated bool
	var pruneOrphans bool
	var force bool
	var jsonOut bool
	fs := newFlagSet("our admin skills remove", a.stderr)
	fs.StringVar(&manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.BoolVar(&deleteSource, "delete-source", false, "delete the static manifest source directory")
	fs.BoolVar(&pruneRelated, "prune-related", false, "remove product catalog related_skills references")
	fs.BoolVar(&pruneOrphans, "prune-orphans", false, "remove now-unreferenced tools and allowed skill namespaces")
	fs.BoolVar(&force, "force", false, "allow dirty checkout")
	fs.BoolVar(&jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{"manifest-dir": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 || manifestDir == "" {
		return fmt.Errorf("usage: our admin skills remove <id|slug> --manifest-dir DIR")
	}
	result, err := a.adminSkillsRemove(rest[0], manifestDir, deleteSource, pruneRelated, pruneOrphans, force)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", result.Action, result.ID, result.ManifestPath)
	if result.Message != "" {
		fmt.Fprintln(a.stdout, result.Message)
	}
	printAdminNextCommands(a.stdout, result.NextCommands)
	return nil
}

func (a app) adminSkillsRemove(ref, manifestDir string, deleteSource, pruneRelated, pruneOrphans, force bool) (adminSkillResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminSkillResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminSkillResult{}, err
	}
	idx := -1
	for i, skill := range doc.Skills {
		if skill.ID == ref || skill.InstallSlug == ref {
			if idx != -1 {
				return adminSkillResult{}, fmt.Errorf("skill %q is ambiguous; match by canonical id", ref)
			}
			idx = i
		}
	}
	if idx == -1 {
		return adminSkillResult{}, fmt.Errorf("skill %q is not declared by the manifest", ref)
	}
	removed := doc.Skills[idx]
	products, productPath, productsFound, err := loadAdminProductCatalog(root)
	if err != nil {
		return adminSkillResult{}, err
	}
	prunedProducts := productRefsSkill(products, removed.ID)
	if len(prunedProducts) != 0 && !pruneRelated {
		return adminSkillResult{}, fmt.Errorf("skill %q is referenced by product related_skills: %s; re-run with --prune-related to remove those references", removed.ID, strings.Join(prunedProducts, ", "))
	}
	if pruneRelated && len(prunedProducts) != 0 {
		products = pruneProductSkillRefs(products, removed.ID)
	}
	sourcePath := ""
	if removed.Path != "" {
		sourcePath = filepath.Join(root, filepath.FromSlash(removed.Path))
	}
	if deleteSource {
		sourceType := removed.Source.Type
		if sourceType == "" {
			sourceType = "static"
		}
		if sourceType != "static" {
			return adminSkillResult{}, fmt.Errorf("--delete-source is only valid for static manifest-owned skills")
		}
		if removed.Path == "" {
			return adminSkillResult{}, fmt.Errorf("refusing to delete empty skill source path")
		}
		if !pathWithinRoot(sourcePath, root) {
			return adminSkillResult{}, fmt.Errorf("refusing to delete skill source outside manifest checkout: %s", sourcePath)
		}
	}
	doc.Skills = append(doc.Skills[:idx], doc.Skills[idx+1:]...)
	orphanedTools, orphanedNS := adminSkillOrphans(doc, removed)
	prunedTools := []string(nil)
	prunedNS := []string(nil)
	if pruneOrphans {
		if len(orphanedTools) != 0 {
			doc.Tools = pruneManifestTools(doc.Tools, stringSet(orphanedTools))
			prunedTools = orphanedTools
		}
		if len(orphanedNS) != 0 {
			doc.AllowedExternalNamespaces = pruneStrings(doc.AllowedExternalNamespaces, stringSet(orphanedNS))
			prunedNS = orphanedNS
		}
	}
	if result := manifest.ValidateDocument("", doc); len(result.Errors) != 0 {
		return adminSkillResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	// Write the product catalog before manifest.json so a failure between the
	// two writes still leaves a consistent checkout: a pruned related_skills
	// reference with the skill still declared validates fine, whereas a removed
	// skill that a product still references would not.
	if pruneRelated && len(prunedProducts) != 0 && productsFound {
		if err := saveAdminProductCatalog(productPath, products); err != nil {
			return adminSkillResult{}, err
		}
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminSkillResult{}, err
	}
	deletedSource := false
	if deleteSource {
		if err := os.RemoveAll(sourcePath); err != nil {
			return adminSkillResult{}, err
		}
		deletedSource = true
	}
	return adminSkillResult{
		Action:         "removed",
		ID:             removed.ID,
		InstallSlug:    removed.InstallSlug,
		ManifestPath:   manifestPath,
		SourcePath:     sourcePath,
		DeletedSource:  deletedSource,
		PrunedProducts: prunedProducts,
		OrphanedTools:  orphanedTools,
		OrphanedNS:     orphanedNS,
		PrunedTools:    prunedTools,
		PrunedNS:       prunedNS,
		Message:        adminSkillRemoveMessage(orphanedTools, orphanedNS, prunedTools, prunedNS),
		NextCommands:   adminNextCommands(root),
	}, nil
}

func adminSkillOrphans(doc manifest.Document, removed manifest.Skill) ([]string, []string) {
	candidateTools := adminSkillToolRefs(removed)
	referencedTools := adminReferencedToolIDs(doc)
	orphanedTools := []string(nil)
	for _, tool := range doc.Tools {
		if candidateTools[tool.ID] && !referencedTools[tool.ID] {
			orphanedTools = append(orphanedTools, tool.ID)
		}
	}

	orphanedNS := []string(nil)
	ns := skillNamespace(removed.ID)
	if ns != "" && ns != doc.Organization.ID && stringInSlice(doc.AllowedExternalNamespaces, ns) && !skillNamespaceUsed(doc.Skills, ns) {
		orphanedNS = append(orphanedNS, ns)
	}
	return orphanedTools, orphanedNS
}

func adminSkillToolRefs(skill manifest.Skill) map[string]bool {
	refs := map[string]bool{}
	if skill.Source.Tool != "" {
		refs[skill.Source.Tool] = true
	}
	for _, req := range skill.Requires {
		typ, id, ok := strings.Cut(req, ":")
		if ok && typ == "tool" && id != "" {
			refs[id] = true
		}
	}
	return refs
}

func adminReferencedToolIDs(doc manifest.Document) map[string]bool {
	refs := map[string]bool{}
	for _, skill := range doc.Skills {
		for id := range adminSkillToolRefs(skill) {
			refs[id] = true
		}
	}
	return refs
}

func skillNamespace(skillID string) string {
	ns, _, ok := strings.Cut(skillID, ":")
	if !ok {
		return ""
	}
	return ns
}

func skillNamespaceUsed(skills []manifest.Skill, ns string) bool {
	for _, skill := range skills {
		if skillNamespace(skill.ID) == ns {
			return true
		}
	}
	return false
}

func stringInSlice(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func pruneManifestTools(tools []manifest.Tool, remove map[string]bool) []manifest.Tool {
	out := tools[:0]
	for _, tool := range tools {
		if !remove[tool.ID] {
			out = append(out, tool)
		}
	}
	return out
}

func pruneStrings(values []string, remove map[string]bool) []string {
	out := values[:0]
	for _, value := range values {
		if !remove[value] {
			out = append(out, value)
		}
	}
	return out
}

func adminSkillRemoveMessage(orphanedTools, orphanedNS, prunedTools, prunedNS []string) string {
	lines := []string{"removed skill declaration"}
	if len(prunedTools) != 0 {
		lines = append(lines, "pruned orphaned tools: "+strings.Join(prunedTools, ", "))
	} else if len(orphanedTools) != 0 {
		lines = append(lines, "orphaned tools remain: "+strings.Join(orphanedTools, ", ")+" (re-run with --prune-orphans to remove)")
	}
	if len(prunedNS) != 0 {
		lines = append(lines, "pruned orphaned allowed namespaces: "+strings.Join(prunedNS, ", "))
	} else if len(orphanedNS) != 0 {
		lines = append(lines, "orphaned allowed namespaces remain: "+strings.Join(orphanedNS, ", ")+" (re-run with --prune-orphans to remove)")
	}
	return strings.Join(lines, "\n")
}

func printAdminNextCommands(w io.Writer, commands []string) {
	for _, command := range commands {
		fmt.Fprintf(w, "next\t%s\n", command)
	}
}

func adminNextCommands(root string) []string {
	return []string{
		"git -C " + shellQuote(root) + " status --short",
		"git -C " + shellQuote(root) + " diff -- manifest.json catalog skills",
	}
}

func loadAdminManifestCheckout(dir string) (manifest.Document, string, string, error) {
	doc, manifestPath, err := manifest.LoadDocument(dir)
	if err != nil {
		return manifest.Document{}, "", "", err
	}
	root := filepath.Dir(manifestPath)
	return doc, manifestPath, root, nil
}

func ensureAdminManifestClean(root string, force bool) error {
	if force {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", root, "status", "--porcelain")
	out, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("check manifest git status: %s", message)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("manifest checkout %s has uncommitted changes; commit or stash them, or re-run with --force", root)
	}
	return nil
}

func validateAdminSkillID(id string, doc manifest.Document) error {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 || !portableKebab(parts[0]) || !portableKebab(parts[1]) {
		return fmt.Errorf("skill id %q must be namespace:name with lowercase kebab-case parts", id)
	}
	if parts[0] == doc.Organization.ID {
		return nil
	}
	for _, allowed := range doc.AllowedExternalNamespaces {
		if parts[0] == allowed {
			return nil
		}
	}
	return fmt.Errorf("skill id namespace %q is not organization.id %q or an allowed external namespace", parts[0], doc.Organization.ID)
}

func portableKebab(value string) bool {
	if value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func discoverSingleSkill(dir string) (skills.Skill, error) {
	parent := filepath.Dir(dir)
	base := filepath.Base(dir)
	found, err := skills.Discover(parent)
	if err != nil {
		return skills.Skill{}, err
	}
	for _, skill := range found {
		if skill.Name == base {
			return skill, nil
		}
	}
	return skills.Skill{}, fmt.Errorf("skill %s was not discovered", dir)
}

func harnessVisibleSkillSource(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	clean := filepath.Clean(abs)
	parent := filepath.Dir(clean)
	grandparent := filepath.Dir(parent)
	if filepath.Base(parent) == "skills" {
		switch filepath.Base(grandparent) {
		case ".claude":
			return "Claude Code"
		case ".codex":
			return "Codex"
		case ".agents":
			return "agent-compatible harnesses"
		case "opencode":
			if filepath.Base(filepath.Dir(grandparent)) == ".config" {
				return "OpenCode"
			}
		}
	}
	if filepath.Base(grandparent) == ".opencode" && filepath.Base(parent) == "skill" {
		return "OpenCode"
	}
	return ""
}

func loadAdminProductCatalog(root string) ([]manifest.Product, string, bool, error) {
	path := filepath.Join(root, "catalog", "products.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, path, false, nil
	}
	if err != nil {
		return nil, path, false, err
	}
	var products []manifest.Product
	if err := json.Unmarshal(data, &products); err != nil {
		return nil, path, false, fmt.Errorf("read product catalog %s: %w", path, err)
	}
	return products, path, true, nil
}

func loadAdminCustomerCatalogFromCheckout(manifestDir string) (string, []manifest.Customer, string, error) {
	_, _, root, err := loadAdminManifestCheckout(manifestDir)
	if err != nil {
		return "", nil, "", err
	}
	customers, path, _, err := loadAdminCustomerCatalog(root)
	if err != nil {
		return "", nil, "", err
	}
	return root, customers, path, nil
}

func loadAdminCustomerCatalog(root string) ([]manifest.Customer, string, bool, error) {
	path := filepath.Join(root, "catalog", "customers.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return []manifest.Customer{}, path, false, nil
	}
	if err != nil {
		return nil, path, false, err
	}
	var customers []manifest.Customer
	if err := json.Unmarshal(data, &customers); err != nil {
		return nil, path, false, fmt.Errorf("read customer catalog %s: %w", path, err)
	}
	if err := manifest.ValidateCustomers(path, customers); err != nil {
		return nil, path, false, err
	}
	return customers, path, true, nil
}

func saveAdminCustomerCatalog(path string, customers []manifest.Customer) error {
	data, err := json.MarshalIndent(customers, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func saveAdminProductCatalog(path string, products []manifest.Product) error {
	data, err := json.MarshalIndent(products, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func productRefsSkill(products []manifest.Product, skillID string) []string {
	var refs []string
	for _, product := range products {
		for _, related := range product.RelatedSkills {
			if related == skillID {
				refs = append(refs, product.ID)
				break
			}
		}
	}
	return refs
}

func pruneProductSkillRefs(products []manifest.Product, skillID string) []manifest.Product {
	for i := range products {
		var kept []string
		for _, related := range products[i].RelatedSkills {
			if related != skillID {
				kept = append(kept, related)
			}
		}
		products[i].RelatedSkills = kept
	}
	return products
}

func samePath(a, b string) bool {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absA); err == nil {
		absA = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absB); err == nil {
		absB = resolved
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func pathWithinRoot(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func (a app) runManifest(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing manifest subcommand")
	}

	switch args[0] {
	case "add":
		return a.runManifestAdd(args[1:])
	case "list":
		return a.runManifestList(args[1:])
	case "sync":
		return a.runManifestSync(args[1:])
	case "validate":
		return a.runManifestValidate(args[1:])
	case "-h", "--help", "help":
		a.printManifestUsage()
		return nil
	default:
		return fmt.Errorf("unknown manifest subcommand %q", args[0])
	}
}

func (a app) printManifestUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our manifests add <name> <git-url> [--home DIR] [--json]
  our manifests list [--home DIR] [--json]
  our manifests sync <name...> | --all [--home DIR] [--umbrella DIR] [--no-derived] [--print] [--json]
  our manifests validate <name|path> [--home DIR] [--json]`)
}

func (a app) runManifestAdd(args []string) error {
	var home string
	var jsonOut bool
	fs := newFlagSet("our manifests add", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return fmt.Errorf("usage: our manifests add <name> <git-url>")
	}
	ref, err := manifest.Add(home, rest[0], rest[1])
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, ref)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", ref.Name, ref.GitURL, ref.LocalPath)
	return nil
}

func (a app) runManifestList(args []string) error {
	var home string
	var jsonOut bool
	fs := newFlagSet("our manifests list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("manifest list does not accept positional arguments")
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, reg)
	}
	for _, ref := range reg.Manifests {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", ref.Name, ref.GitURL, ref.LocalPath)
	}
	return nil
}

func (a app) runManifestSync(args []string) error {
	var home string
	var umbrellaRoot string
	var all bool
	var noDerived bool
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("our manifests sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root for derived reconciliation")
	fs.BoolVar(&all, "all", false, "sync every registered manifest")
	fs.BoolVar(&noDerived, "no-derived", false, "skip guidance and skill reconciliation after manifest changes")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "umbrella": true})
	if err != nil {
		return err
	}
	results, err := manifest.Sync(home, rest, all, printOnly, nil)
	if err != nil {
		return err
	}
	derivedManifest, derived, derivedNotices, derivedErr := a.manifestSyncDerived(home, umbrellaRoot, results, printOnly || noDerived)
	wrapped := wrapManifestSyncResults(results, derivedManifest, derived, derivedNotices)
	if jsonOut {
		if err := printJSON(a.stdout, wrapped); err != nil {
			return err
		}
	} else {
		for _, r := range results {
			line := fmt.Sprintf("%s\t%s\t%s", r.Name, r.Status, r.LocalPath)
			if r.Message != "" {
				line += "\t" + r.Message
			}
			if r.Error != "" {
				line += "\t" + r.Error
			}
			fmt.Fprintln(a.stdout, line)
		}
		if derived != nil {
			a.printDerivedReconcileReport(*derived)
		}
		printManifestSyncDerivedNotices(a.stdout, results, derivedNotices)
	}
	if manifestResultsFailed(results) {
		return fmt.Errorf("one or more manifest syncs failed")
	}
	if derivedErr != nil {
		return derivedErr
	}
	if derivedReportFailed(derived) {
		return fmt.Errorf("one or more derived reconciliation operations failed")
	}
	return nil
}

type manifestSyncCommandResult struct {
	manifest.SyncResult
	Derived        *derivedReconcileReport `json:"derived,omitempty"`
	DerivedStatus  string                  `json:"derived_status,omitempty"`
	DerivedMessage string                  `json:"derived_message,omitempty"`
}

type manifestSyncDerivedNotice struct {
	Status  string
	Message string
}

func wrapManifestSyncResults(results []manifest.SyncResult, derivedManifest string, derived *derivedReconcileReport, notices map[string]manifestSyncDerivedNotice) []manifestSyncCommandResult {
	wrapped := make([]manifestSyncCommandResult, 0, len(results))
	attached := false
	for _, result := range results {
		item := manifestSyncCommandResult{SyncResult: result}
		if derived != nil && !attached && result.Name == derivedManifest {
			item.Derived = derived
			attached = true
		}
		if notice, ok := notices[result.Name]; ok {
			item.DerivedStatus = notice.Status
			item.DerivedMessage = notice.Message
		}
		wrapped = append(wrapped, item)
	}
	return wrapped
}

func printManifestSyncDerivedNotices(w io.Writer, results []manifest.SyncResult, notices map[string]manifestSyncDerivedNotice) {
	if len(notices) == 0 {
		return
	}
	printed := map[string]bool{}
	for _, result := range results {
		if printed[result.Name] {
			continue
		}
		notice, ok := notices[result.Name]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "derived\tmanifest:%s\t%s\t%s\n", result.Name, notice.Status, notice.Message)
		printed[result.Name] = true
	}
}

func (a app) manifestSyncDerived(home, umbrellaRoot string, results []manifest.SyncResult, skip bool) (string, *derivedReconcileReport, map[string]manifestSyncDerivedNotice, error) {
	notices := map[string]manifestSyncDerivedNotice{}
	if skip || manifestResultsFailed(results) {
		return "", nil, notices, nil
	}
	changed := changedManifestSyncResults(results)
	if len(changed) == 0 {
		return "", nil, notices, nil
	}
	if len(changed) != 1 {
		for _, manifestName := range changed {
			message, err := manifestSyncDerivedSkipMessage(home, umbrellaRoot, manifestName, "multiple changed manifests")
			if err != nil {
				return manifestName, nil, notices, err
			}
			notices[manifestName] = manifestSyncDerivedNotice{Status: "skipped", Message: message}
		}
		return "", nil, notices, nil
	}
	manifestName := changed[0]
	root, hasRoot, err := existingUmbrellaRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		if message, ok := manifestSyncUmbrellaMismatchMessage(err); ok {
			notices[manifestName] = manifestSyncDerivedNotice{Status: "skipped", Message: message}
			return manifestName, nil, notices, nil
		}
		return manifestName, nil, notices, err
	}
	if !hasRoot {
		notices[manifestName] = manifestSyncDerivedNotice{
			Status:  "skipped",
			Message: manifestSyncSetupRemediation("no existing umbrella found", manifestName, root),
		}
		return manifestName, nil, notices, nil
	}
	report, err := a.reconcileDerived(home, manifestName, root)
	if err != nil {
		return manifestName, nil, notices, err
	}
	return manifestName, &report, notices, nil
}

func manifestSyncDerivedSkipMessage(home, umbrellaRoot, manifestName, reason string) (string, error) {
	root, hasRoot, err := existingUmbrellaRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		if message, ok := manifestSyncUmbrellaMismatchMessage(err); ok {
			return reason + "; " + message, nil
		}
		return "", err
	}
	if !hasRoot {
		return manifestSyncSetupRemediation(reason+"; no existing umbrella found", manifestName, root), nil
	}
	return manifestSyncSetupRemediation(reason, manifestName, root), nil
}

func manifestSyncUmbrellaMismatchMessage(err error) (string, bool) {
	var mismatch umbrellaManifestMismatchError
	if !errors.As(err, &mismatch) {
		return "", false
	}
	return fmt.Sprintf("umbrella %s uses manifest %q, not %q; pass --umbrella for the %s umbrella or run our setup --manifest %s", mismatch.Root, mismatch.Have, mismatch.Want, mismatch.Want, mismatch.Want), true
}

func manifestSyncSetupRemediation(reason, manifestName, root string) string {
	args := []string{"our", "setup", "--manifest", manifestName}
	if root != "" {
		args = append(args, "--umbrella", root)
	}
	return reason + "; run " + strings.Join(args, " ")
}

func changedManifestSyncResults(results []manifest.SyncResult) []string {
	seen := map[string]bool{}
	var names []string
	for _, result := range results {
		if !result.Changed || result.Name == "" {
			continue
		}
		if seen[result.Name] {
			continue
		}
		seen[result.Name] = true
		names = append(names, result.Name)
	}
	return names
}

func (a app) runManifestValidate(args []string) error {
	var home string
	var jsonOut bool
	fs := newFlagSet("our manifests validate", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our manifests validate <name|path>")
	}
	path := rest[0]
	if ref, ok, err := manifest.Find(home, rest[0]); err != nil {
		return err
	} else if ok {
		path = ref.LocalPath
	}
	result := manifest.ValidateFile(path)
	if jsonOut {
		if err := printJSON(a.stdout, result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.stdout, "%s\n", result.Path)
		for _, warning := range result.Warnings {
			fmt.Fprintf(a.stdout, "warning\t%s\n", warning)
		}
		for _, validationErr := range result.Errors {
			fmt.Fprintf(a.stdout, "error\t%s\n", validationErr)
		}
		if len(result.Errors) == 0 {
			fmt.Fprintln(a.stdout, "ok")
		}
	}
	if len(result.Errors) != 0 {
		return fmt.Errorf("manifest validation failed")
	}
	return nil
}

func (a app) runSkills(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing skills subcommand")
	}

	switch args[0] {
	case "self":
		return a.runSkillsSelf(args[1:])
	case "install":
		return a.runSkillsInstall(args[1:])
	case "uninstall":
		return a.runSkillsUninstall(args[1:])
	case "sync":
		return a.runSkillsSync(args[1:])
	case "purge":
		return a.runSkillsPurge(args[1:])
	case "list":
		return a.runSkillsList(args[1:])
	case "show":
		return a.runSkillsShow(args[1:])
	case "status":
		return a.runSkillsStatus(args[1:])
	case "-h", "--help", "help":
		a.printSkillsUsage()
		return nil
	default:
		return fmt.Errorf("unknown skills subcommand %q", args[0])
	}
}

func (a app) printSkillsUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our skills self install [harness...] | --all [--print] [--copy] [--link] [--force] [--json] [--home DIR]
  our skills self uninstall [harness...] | --all [--print] [--force] [--json] [--home DIR]
  our skills self status [harness...] | --all [--json] [--home DIR]
  our skills install [harness...] | --all [--skill ID_OR_SLUG] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
  our skills uninstall <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
  our skills sync [harness...] | --all [--skill ID_OR_SLUG] [--no-prune] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
  our skills purge <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
  our skills list [--json] [--source DIR] [--manifest NAME] [--home DIR]
  our skills show <id|slug> [--json] [--source DIR] [--manifest NAME] [--home DIR]
  our skills status [--skill ID_OR_SLUG] [--json] [--source DIR] [--manifest NAME] [--home DIR]

Harnesses:
  claude-code, codex, opencode, gemini

With no harnesses, install targets all supported harnesses and silently skips
missing ones. If synced manifests are registered, skills commands use them by
default; --source forces a local skills directory.

Manifest skill commands only refresh harness skill directories. Run our
onboard to regenerate workspace guidance such as AGENTS.md. Self-skill commands
install Our AI's bundled CLI guidance into harness skill directories.`)
}

func (a app) runSkillsSelf(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing skills self subcommand")
	}
	switch args[0] {
	case "install":
		return a.runSkillsSelfInstall(args[1:])
	case "uninstall":
		return a.runSkillsSelfUninstall(args[1:])
	case "status":
		return a.runSkillsSelfStatus(args[1:])
	case "-h", "--help", "help":
		a.printSkillsSelfUsage()
		return nil
	default:
		return fmt.Errorf("unknown skills self subcommand %q", args[0])
	}
}

func (a app) printSkillsSelfUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our skills self install [harness...] | --all [--print] [--copy] [--link] [--force] [--json] [--home DIR]
  our skills self uninstall [harness...] | --all [--print] [--force] [--json] [--home DIR]
  our skills self status [harness...] | --all [--json] [--home DIR]

Installs Our AI's bundled CLI self-skill. This is separate from manifest-backed
organization skills.`)
}

func (a app) runSkillsSelfInstall(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our skills self install", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "install into every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.copyMode, "copy", false, "copy skill directories instead of symlinking")
	fs.BoolVar(&opts.linkMode, "link", false, "symlink skill directories")
	fs.BoolVar(&opts.force, "force", false, "replace non-Our AI-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if opts.copyMode && opts.linkMode {
		return fmt.Errorf("--copy and --link are mutually exclusive")
	}
	if len(rest) == 0 && !opts.all {
		opts.all = true
	}
	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	results, err := selfskill.Install(hs, selfskill.Options{
		Home:        opts.home,
		Link:        !opts.copyMode,
		DryRun:      opts.print,
		SkipMissing: opts.all,
		Force:       opts.force,
	})
	if err != nil {
		return err
	}
	return a.printResults(results, opts.jsonOut)
}

func (a app) runSkillsSelfUninstall(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our skills self uninstall", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "uninstall from every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.force, "force", false, "remove non-Our AI-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) == 0 && !opts.all {
		opts.all = true
	}
	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	results, err := selfskill.Uninstall(hs, selfskill.Options{
		Home:        opts.home,
		DryRun:      opts.print,
		SkipMissing: opts.all,
		Force:       opts.force,
	})
	if err != nil {
		return err
	}
	return a.printResults(results, opts.jsonOut)
}

func (a app) runSkillsSelfStatus(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our skills self status", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "inspect every supported harness")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) == 0 && !opts.all {
		opts.all = true
	}
	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	rows, err := selfskill.Inspect(hs, selfskill.Options{Home: opts.home})
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return printJSON(a.stdout, rows)
	}
	a.printSelfSkillStatus(rows)
	if selfSkillStatusFailed(rows) {
		return fmt.Errorf("one or more operations failed")
	}
	return nil
}

func (a app) printSelfSkillStatus(rows []selfskill.Status) {
	for _, row := range rows {
		line := fmt.Sprintf("%s\t%s\t%s", row.Harness, row.Skill, row.Status)
		if row.Kind != "" {
			line += "\t" + row.Kind
		}
		if row.TargetPath != "" {
			line += "\t" + row.TargetPath
		}
		if row.Message != "" {
			line += "\t" + row.Message
		}
		if row.Remedy != "" {
			line += "\t" + row.Remedy
		}
		fmt.Fprintln(a.stdout, line)
	}
}

func selfSkillStatusFailed(rows []selfskill.Status) bool {
	for _, row := range rows {
		if row.Status == skills.StatusFailed {
			return true
		}
	}
	return false
}

func (a app) runOnboard(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our setup", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "install into every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.copyMode, "copy", false, "copy skill directories instead of symlinking")
	fs.BoolVar(&opts.linkMode, "link", false, "symlink skill directories")
	fs.BoolVar(&opts.force, "force", false, "replace non-Our AI-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.BoolVar(&opts.noRefresh, "no-refresh", false, "skip best-effort auto-refresh")
	fs.BoolVar(&opts.noUpdateCheck, "no-update-check", false, "skip best-effort update notice")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use skills declared by a synced manifest")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if opts.copyMode && opts.linkMode {
		return fmt.Errorf("--copy and --link are mutually exclusive")
	}
	if len(rest) == 0 && !opts.all {
		opts.all = true
	}
	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	docs, ok, err := a.skillManifestDocs(opts.home, opts.manifestName)
	if err != nil {
		return err
	}
	if !ok || len(docs) == 0 {
		return fmt.Errorf("our setup requires a registered manifest")
	}
	if len(docs) != 1 {
		return fmt.Errorf("our setup requires exactly one manifest; pass --manifest")
	}
	doc := docs[0]
	root, err := umbrella.ResolveRoot(opts.home, ".", opts.umbrellaRoot, doc.doc)
	if err != nil {
		return err
	}
	var ws umbrella.Workspace
	if opts.print {
		fmt.Fprintf(a.stderr, "# umbrella: %s\n", root)
		ws = umbrella.Workspace{
			SchemaVersion: umbrella.SchemaVersion,
			Organization:  doc.doc.Organization.ID,
			ManifestRef:   doc.ref.Name,
			WorkspaceRoot: root,
		}
	} else {
		ensured, _, err := umbrella.Ensure(root, doc.doc.Organization.ID, doc.ref.Name)
		if err != nil {
			return err
		}
		ws = ensured
		a.maybeAutoRefresh(opts.home, doc.ref.Name, root, root, opts.noRefresh)
		a.maybeUpdateNotice(opts.home, opts.noUpdateCheck)
		refreshed, err := loadSingleRegisteredDoc(opts.home, doc.ref.Name)
		if err != nil {
			return err
		}
		doc = refreshed
	}
	guidanceResult, err := guidance.Ensure(root, doc.ref.LocalPath, doc.doc, guidance.Options{
		Force:  opts.force,
		DryRun: opts.print,
	})
	if err != nil {
		return err
	}
	results, err := workspace.SyncMounts(opts.home, doc.ref.Name, root, nil, false, []string{"required", "default"}, opts.print, nil)
	if err != nil {
		return err
	}
	if !opts.print {
		if err := recordMountResults(root, results); err != nil {
			return err
		}
	}
	for _, result := range results {
		if (result.Status == "failed" || result.Status == "inaccessible") && result.Mode == "required" {
			return fmt.Errorf("required mount %q failed: %s", result.Workspace, result.Error)
		}
	}
	repoResults, err := a.syncSelectedRepos(opts.home, doc, root, opts.print)
	if err != nil {
		return err
	}
	results = append(results, repoResults...)
	skillResults, err := a.collectSkillInstallResults(opts, hs, false)
	if err != nil {
		return err
	}
	selfSkillResults, err := selfskill.Install(hs, selfskill.Options{
		Home:        opts.home,
		Link:        !opts.copyMode,
		DryRun:      opts.print,
		SkipMissing: opts.all,
		Force:       opts.force,
	})
	if err != nil {
		return err
	}
	skillResults = append(skillResults, selfSkillResults...)
	if opts.jsonOut {
		if err := printJSON(a.stdout, struct {
			Umbrella umbrella.Workspace     `json:"umbrella"`
			Guidance guidance.Result        `json:"guidance"`
			Mounts   []workspace.SyncResult `json:"mounts"`
			Skills   []skills.Result        `json:"skills"`
		}{Umbrella: ws, Guidance: guidanceResult, Mounts: results, Skills: skillResults}); err != nil {
			return err
		}
		if guidanceResult.Status == "blocked" || resultsFailed(skillResults) {
			return fmt.Errorf("one or more operations failed")
		}
		return nil
	}
	a.printGuidanceResult(guidanceResult)
	a.printWorkspaceResults(results)
	if err := a.printResults(skillResults, false); err != nil {
		return err
	}
	if guidanceResult.Status == "blocked" {
		return fmt.Errorf("one or more operations failed")
	}
	a.printLaunchHints(root)
	return nil
}

func (a app) printGuidanceResult(result guidance.Result) {
	line := fmt.Sprintf("workspace-guidance\t%s\t%s", result.Status, result.TargetPath)
	if result.Message != "" {
		line += "\t" + result.Message
	}
	fmt.Fprintln(a.stdout, line)
}

func (a app) printLaunchHints(root string) {
	fmt.Fprintf(a.stdout, "launch\tclaude-code\t%s\n", shellCommandLine(root, "claude", nil))
	fmt.Fprintf(a.stdout, "launch\tcodex\t%s\n", shellCommandLine(root, "codex", nil))
}

func (a app) runSkillsInstall(args []string) error {
	return a.runSkillsInstallNamed("our skills install", args)
}

func (a app) runSkillsInstallNamed(commandName string, args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet(commandName, a.stderr)
	fs.BoolVar(&opts.all, "all", false, "install into every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.copyMode, "copy", false, "copy skill directories instead of symlinking")
	fs.BoolVar(&opts.linkMode, "link", false, "symlink skill directories")
	fs.BoolVar(&opts.force, "force", false, "replace non-Our AI-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.StringVar(&opts.source, "source", "", "skills source directory")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use skills declared by a synced manifest")
	fs.Var(&opts.skillRefs, "skill", "limit to one skill id or install slug; repeatable")
	fs.Usage = func() {
		fmt.Fprintf(a.stderr, `Usage of %s:
  %s [harness...] | --all [--skill ID_OR_SLUG] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]

Skills install only changes harness skill directories. Run our setup to
regenerate workspace guidance such as AGENTS.md.

Options:
`, commandName, commandName)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"source":   true,
		"home":     true,
		"manifest": true,
		"skill":    true,
	})
	if err != nil {
		return err
	}
	if opts.copyMode && opts.linkMode {
		return fmt.Errorf("--copy and --link are mutually exclusive")
	}
	if len(rest) == 0 && !opts.all {
		opts.all = true
	}

	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	return a.installSkills(opts, hs, true)
}

func (a app) installSkills(opts skillsCommandOpts, hs []harness.Harness, syncLegacyWorkspaces bool) error {
	results, err := a.collectSkillInstallResults(opts, hs, syncLegacyWorkspaces)
	if err != nil {
		return err
	}
	return a.printResults(results, opts.jsonOut)
}

func (a app) collectSkillInstallResults(opts skillsCommandOpts, hs []harness.Harness, syncLegacyWorkspaces bool) ([]skills.Result, error) {
	if err := a.prepareManifestSkillSources(opts); err != nil {
		return nil, err
	}
	bundled, sourceRoots, manifestBacked, err := a.discoverSkills(opts)
	if err != nil {
		return nil, err
	}
	bundled, err = selectSkills(bundled, opts.skillRefs)
	if err != nil {
		return nil, err
	}
	a.printSkillWarnings(bundled)
	if manifestBacked && syncLegacyWorkspaces {
		a.syncSkillWorkspaces(opts.home, opts.manifestName, opts.print)
	}

	installOpts := skills.InstallOpts{
		Link:        !opts.copyMode,
		DryRun:      opts.print,
		SkipMissing: opts.all,
		Home:        opts.home,
		Force:       opts.force,
		SourceRoots: sourceRoots,
	}
	var results []skills.Result
	for _, h := range hs {
		for _, s := range bundled {
			results = append(results, skills.Install(s, h, installOpts))
		}
	}
	return results, nil
}

func (a app) runSkillsUninstall(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our skills uninstall", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "uninstall from every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.force, "force", false, "remove non-Our AI-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.StringVar(&opts.source, "source", "", "skills source directory")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use skills declared by a synced manifest")
	fs.Var(&opts.skillRefs, "skill", "limit to one skill id or install slug; repeatable")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"source":   true,
		"home":     true,
		"manifest": true,
		"skill":    true,
	})
	if err != nil {
		return err
	}
	opts.allowMissingToolSkills = true

	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	bundled, sourceRoots, _, err := a.discoverSkills(opts)
	if err != nil {
		return err
	}
	bundled, err = selectSkills(bundled, opts.skillRefs)
	if err != nil {
		return err
	}
	a.printSkillWarnings(bundled)

	installOpts := skills.InstallOpts{
		DryRun:      opts.print,
		SkipMissing: opts.all,
		Home:        opts.home,
		Force:       opts.force,
		SourceRoots: sourceRoots,
	}
	var results []skills.Result
	for _, h := range hs {
		for _, s := range bundled {
			results = append(results, skills.Uninstall(s.Name, h, installOpts))
		}
	}
	return a.printResults(results, opts.jsonOut)
}

func (a app) runSkillsSync(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our skills sync", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "sync every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.copyMode, "copy", false, "copy skill directories instead of symlinking")
	fs.BoolVar(&opts.linkMode, "link", false, "symlink skill directories")
	fs.BoolVar(&opts.force, "force", false, "replace non-Our AI-managed targets during install")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.BoolVar(&opts.noPrune, "no-prune", false, "skip removal of stale Our AI-managed skill materializations")
	fs.StringVar(&opts.source, "source", "", "skills source directory")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use skills declared by a synced manifest")
	fs.Var(&opts.skillRefs, "skill", "limit install/update to one skill id or install slug; repeatable")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"source":   true,
		"home":     true,
		"manifest": true,
		"skill":    true,
	})
	if err != nil {
		return err
	}
	if opts.copyMode && opts.linkMode {
		return fmt.Errorf("--copy and --link are mutually exclusive")
	}
	if len(rest) == 0 && !opts.all {
		opts.all = true
	}
	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	results, err := a.collectSkillSyncResults(opts, hs, true)
	if err != nil {
		return err
	}
	return a.printResults(results, opts.jsonOut)
}

func (a app) collectSkillSyncResults(opts skillsCommandOpts, hs []harness.Harness, syncLegacyWorkspaces bool) ([]skills.Result, error) {
	if err := a.prepareManifestSkillSources(opts); err != nil {
		return nil, err
	}
	bundled, sourceRoots, manifestBacked, err := a.discoverSkills(opts)
	if err != nil {
		return nil, err
	}
	selected, err := selectSkills(bundled, opts.skillRefs)
	if err != nil {
		return nil, err
	}
	a.printSkillWarnings(selected)
	if manifestBacked && syncLegacyWorkspaces {
		a.syncSkillWorkspaces(opts.home, opts.manifestName, opts.print)
	}

	installOpts := skills.InstallOpts{
		Link:        !opts.copyMode,
		DryRun:      opts.print,
		SkipMissing: opts.all,
		Home:        opts.home,
		Force:       opts.force,
		SourceRoots: sourceRoots,
	}
	var results []skills.Result
	for _, h := range hs {
		for _, s := range selected {
			results = append(results, skills.Install(s, h, installOpts))
		}
	}
	if !opts.noPrune && len(opts.skillRefs) == 0 {
		prune, err := collectStaleSkillRemovalResults(opts, hs, bundled, sourceRoots)
		if err != nil {
			return nil, err
		}
		results = append(results, prune...)
	}
	return results, nil
}

func (a app) runSkillsPurge(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our skills purge", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "purge from every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.force, "force", false, "remove explicitly selected non-Our AI-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.StringVar(&opts.source, "source", "", "skills source directory")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use skills declared by a synced manifest")
	fs.Var(&opts.skillRefs, "skill", "limit to one skill id or install slug; repeatable")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"source":   true,
		"home":     true,
		"manifest": true,
		"skill":    true,
	})
	if err != nil {
		return err
	}
	opts.allowMissingToolSkills = true
	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	results, err := a.collectSkillPurgeResults(opts, hs)
	if err != nil {
		return err
	}
	return a.printResults(results, opts.jsonOut)
}

func (a app) collectSkillPurgeResults(opts skillsCommandOpts, hs []harness.Harness) ([]skills.Result, error) {
	bundled, sourceRoots, _, err := a.discoverSkills(opts)
	if err != nil {
		return nil, err
	}
	a.printSkillWarnings(bundled)
	installOpts := skills.InstallOpts{
		DryRun:      opts.print,
		SkipMissing: opts.all,
		Home:        opts.home,
		Force:       opts.force,
		SourceRoots: sourceRoots,
	}
	var results []skills.Result
	for _, h := range hs {
		if h == harness.Gemini {
			targets, err := geminiPurgeTargets(bundled, opts.skillRefs)
			if err != nil {
				return nil, err
			}
			for _, target := range targets {
				res := skills.Uninstall(target.Name, h, installOpts)
				res.CanonicalID = target.CanonicalID
				results = append(results, res)
			}
			continue
		}
		installed, err := skills.ListInstalled(h, installOpts)
		if err != nil {
			results = append(results, skills.Result{Harness: h, Skill: "*", Status: skills.StatusFailed, Err: err})
			continue
		}
		targets, err := filesystemPurgeTargets(bundled, installed, opts.skillRefs)
		if err != nil {
			return nil, err
		}
		for _, target := range targets {
			res := skills.Uninstall(target.Name, h, installOpts)
			if target.CanonicalID != "" {
				res.CanonicalID = target.CanonicalID
			}
			results = append(results, res)
		}
	}
	return results, nil
}

func (a app) runSkillsList(args []string) error {
	var source string
	var manifestName string
	var home string
	var jsonOut bool
	fs := newFlagSet("our skills list", a.stderr)
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.StringVar(&source, "source", "", "skills source directory")
	fs.StringVar(&manifestName, "manifest", "", "use skills declared by a synced manifest")
	fs.StringVar(&home, "home", "", "override home directory")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"source":   true,
		"manifest": true,
		"home":     true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("skills list does not accept positional arguments")
	}

	bundled, _, _, err := a.discoverSkills(skillsCommandOpts{
		source: source, manifestName: manifestName, home: home, quietSource: true, allowMissingToolSkills: true,
	})
	if err != nil {
		return err
	}
	a.printSkillWarnings(bundled)

	if jsonOut {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(bundled)
	}
	a.printSkillsList(bundled)
	return nil
}

func (a app) runSkillsShow(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our skills show", a.stderr)
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON")
	fs.StringVar(&opts.source, "source", "", "skills source directory")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use skills declared by a synced manifest")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"source":   true,
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our skills show <id|slug>")
	}

	bundled, _, _, err := a.discoverSkills(skillsCommandOpts{
		source: opts.source, manifestName: opts.manifestName, home: opts.home, quietSource: true, allowMissingToolSkills: true,
	})
	if err != nil {
		return err
	}
	selected, err := selectSkills(bundled, []string{rest[0]})
	if err != nil {
		return err
	}
	a.printSkillWarnings(selected)
	s := selected[0]

	if opts.jsonOut {
		return printJSON(a.stdout, s)
	}
	a.printSkillShow(s)
	return nil
}

func (a app) printSkillShow(s skills.Skill) {
	fmt.Fprintln(a.stdout, s.Name)
	if s.CanonicalID != "" {
		printHumanField(a.stdout, "id", s.CanonicalID)
	}
	if s.SkillName != "" && s.SkillName != s.Name {
		printHumanField(a.stdout, "skill", s.SkillName)
	}
	if s.Description != "" {
		printHumanField(a.stdout, "description", s.Description)
	}
	if s.SourcePath != "" {
		printHumanField(a.stdout, "source", s.SourcePath)
	}
	if s.SourceRoot != "" {
		printHumanField(a.stdout, "source root", s.SourceRoot)
	}
}

type skillStatusRow struct {
	Harness     harness.Harness `json:"harness"`
	Skill       string          `json:"skill"`
	CanonicalID string          `json:"canonical_id,omitempty"`
	Status      string          `json:"status"`
	Kind        string          `json:"kind,omitempty"`
	TargetPath  string          `json:"target_path,omitempty"`
	SourcePath  string          `json:"source_path,omitempty"`
	LinkTarget  string          `json:"link_target,omitempty"`
	Remedy      string          `json:"remedy,omitempty"`
	Message     string          `json:"message,omitempty"`
	Error       string          `json:"error,omitempty"`
}

func (a app) runSkillsStatus(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("our skills status", a.stderr)
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON")
	fs.StringVar(&opts.source, "source", "", "skills source directory")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use skills declared by a synced manifest")
	fs.Var(&opts.skillRefs, "skill", "limit to one skill id or install slug; repeatable")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"source":   true,
		"home":     true,
		"manifest": true,
		"skill":    true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("skills status does not accept positional arguments")
	}
	opts.allowMissingToolSkills = true

	bundled, sourceRoots, _, err := a.discoverSkills(skillsCommandOpts{
		source: opts.source, manifestName: opts.manifestName, home: opts.home, quietSource: true, skillRefs: opts.skillRefs, allowMissingToolSkills: true,
	})
	if err != nil {
		return err
	}
	bundled, err = selectSkills(bundled, opts.skillRefs)
	if err != nil {
		return err
	}
	a.printSkillWarnings(bundled)
	rows, err := a.skillStatusRows(opts, bundled, sourceRoots)
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return printJSON(a.stdout, rows)
	}
	a.printSkillsStatus(rows)
	return nil
}

func (a app) skillStatusRows(opts skillsCommandOpts, bundled []skills.Skill, sourceRoots []string) ([]skillStatusRow, error) {
	home, err := resolveHome(opts.home)
	if err != nil {
		return nil, err
	}
	installOpts := skills.InstallOpts{Home: opts.home, SourceRoots: sourceRoots}
	var rows []skillStatusRow
	for _, h := range harness.All() {
		for _, s := range bundled {
			row := skillStatusRow{
				Harness:     h,
				Skill:       s.Name,
				CanonicalID: s.CanonicalID,
				SourcePath:  s.SourcePath,
			}
			if h.IsFilesystem() {
				row.TargetPath = h.SkillTargetPath(home, s.Name)
			} else {
				row.TargetPath = "(gemini CLI)"
			}
			inspection, err := skills.InspectDeclared(s, h, installOpts)
			if err != nil {
				row.Status = "error"
				row.Error = err.Error()
				rows = append(rows, row)
				continue
			}
			kind := inspection.Kind
			row.Kind = kind.Kind
			row.LinkTarget = kind.Target
			switch kind.Kind {
			case "absent":
				row.Status = "absent"
				row.Remedy = skillInstallRemedy(opts, h, s)
			case "managed-by-gemini":
				row.Status = "managed-by-gemini"
			case "copy":
				if inspection.Stale {
					row.Status = "stale"
					row.Remedy = skillSyncRemedy(opts, h, s)
					row.Message = inspection.StaleReason
				} else {
					row.Status = "installed"
				}
			default:
				row.Status = "installed"
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func skillSyncRemedy(opts skillsCommandOpts, h harness.Harness, s skills.Skill) string {
	ref := s.CanonicalID
	if ref == "" {
		ref = s.Name
	}
	parts := []string{"our", "skills", "sync", string(h), "--skill", ref}
	if opts.manifestName != "" {
		parts = append(parts, "--manifest", opts.manifestName)
	}
	if opts.source != "" {
		parts = append(parts, "--source", opts.source)
	}
	if opts.home != "" {
		parts = append(parts, "--home", opts.home)
	}
	for i, part := range parts {
		parts[i] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}

func skillInstallRemedy(opts skillsCommandOpts, h harness.Harness, s skills.Skill) string {
	ref := s.CanonicalID
	if ref == "" {
		ref = s.Name
	}
	parts := []string{"our", "skills", "install", string(h), "--skill", ref}
	if opts.manifestName != "" {
		parts = append(parts, "--manifest", opts.manifestName)
	}
	if opts.source != "" {
		parts = append(parts, "--source", opts.source)
	}
	if opts.home != "" {
		parts = append(parts, "--home", opts.home)
	}
	for i, part := range parts {
		parts[i] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}

func (a app) printSkillsStatus(rows []skillStatusRow) {
	for _, row := range rows {
		fields := []string{string(row.Harness), row.Skill, row.Status}
		if row.CanonicalID != "" {
			fields = append(fields, row.CanonicalID)
		}
		if row.Kind != "" && row.Kind != row.Status {
			fields = append(fields, row.Kind)
		}
		if row.TargetPath != "" {
			fields = append(fields, row.TargetPath)
		}
		if row.LinkTarget != "" {
			fields = append(fields, "-> "+row.LinkTarget)
		}
		if row.Remedy != "" {
			fields = append(fields, row.Remedy)
		}
		if row.Message != "" {
			fields = append(fields, row.Message)
		}
		if row.Error != "" {
			fields = append(fields, row.Error)
		}
		fmt.Fprintln(a.stdout, strings.Join(fields, "\t"))
	}
}

func (a app) printSkillsList(bundled []skills.Skill) {
	for i, s := range bundled {
		if i != 0 {
			fmt.Fprintln(a.stdout)
		}
		fmt.Fprintln(a.stdout, s.Name)
		if s.CanonicalID != "" {
			printHumanField(a.stdout, "id", s.CanonicalID)
		}
		if s.SkillName != "" && s.SkillName != s.Name {
			printHumanField(a.stdout, "skill", s.SkillName)
		}
		if s.Description != "" {
			printHumanField(a.stdout, "description", s.Description)
		}
	}
}

type skillsCommandOpts struct {
	all                    bool
	print                  bool
	copyMode               bool
	linkMode               bool
	force                  bool
	jsonOut                bool
	noPrune                bool
	noRefresh              bool
	noUpdateCheck          bool
	source                 string
	home                   string
	manifestName           string
	umbrellaRoot           string
	quietSource            bool
	skillRefs              stringListFlag
	allowMissingToolSkills bool
}

func selectSkills(all []skills.Skill, refs []string) ([]skills.Skill, error) {
	if len(refs) == 0 {
		return all, nil
	}
	var out []skills.Skill
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		var matches []skills.Skill
		for _, s := range all {
			if skillMatchesRef(s, ref) {
				matches = append(matches, s)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("skill %q is not available from the selected source", ref)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("skill %q is ambiguous; matches %s; use a canonical id", ref, skillMatchNames(matches))
		}
		key := skillSelectionKey(matches[0])
		if !seen[key] {
			out = append(out, matches[0])
			seen[key] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no skills selected")
	}
	return out, nil
}

func skillMatchesRef(s skills.Skill, ref string) bool {
	return s.Name == ref || s.SkillName == ref || s.CanonicalID == ref
}

func skillSelectionKey(s skills.Skill) string {
	return s.CanonicalID + "\x00" + s.Name + "\x00" + s.SourcePath
}

func skillMatchNames(matches []skills.Skill) string {
	var names []string
	for _, s := range matches {
		name := s.Name
		if s.CanonicalID != "" {
			name = s.CanonicalID + " (" + s.Name + ")"
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

type skillRemovalTarget struct {
	Name        string
	CanonicalID string
}

func collectStaleSkillRemovalResults(opts skillsCommandOpts, hs []harness.Harness, declared []skills.Skill, sourceRoots []string) ([]skills.Result, error) {
	declaredNames := map[string]bool{}
	for _, s := range declared {
		declaredNames[s.Name] = true
	}
	installOpts := skills.InstallOpts{
		DryRun:      opts.print,
		SkipMissing: opts.all,
		Home:        opts.home,
		Force:       opts.force,
		SourceRoots: sourceRoots,
	}
	var results []skills.Result
	for _, h := range hs {
		if !h.IsFilesystem() {
			continue
		}
		installed, err := skills.ListInstalled(h, installOpts)
		if err != nil {
			results = append(results, skills.Result{Harness: h, Skill: "*", Status: skills.StatusFailed, Err: err})
			continue
		}
		for _, existing := range installed {
			if declaredNames[existing.Skill] || !existing.Managed || isSelfSkillMaterialization(existing) {
				continue
			}
			res := skills.Uninstall(existing.Skill, h, installOpts)
			res.CanonicalID = existing.CanonicalID
			res.Message = staleRemovalMessage(res.Message)
			results = append(results, res)
		}
	}
	return results, nil
}

func staleRemovalMessage(message string) string {
	if message == "" {
		return "stale Our AI-managed skill not declared by selected source"
	}
	return "stale Our AI-managed skill not declared by selected source; " + message
}

func geminiPurgeTargets(declared []skills.Skill, refs []string) ([]skillRemovalTarget, error) {
	if len(refs) == 0 {
		targets := make([]skillRemovalTarget, 0, len(declared))
		for _, s := range declared {
			targets = append(targets, skillRemovalTarget{Name: s.Name, CanonicalID: s.CanonicalID})
		}
		return targets, nil
	}
	return declaredOrRawRemovalTargets(declared, refs), nil
}

func filesystemPurgeTargets(declared []skills.Skill, installed []skills.InstalledSkill, refs []string) ([]skillRemovalTarget, error) {
	if len(refs) == 0 {
		var targets []skillRemovalTarget
		for _, existing := range installed {
			if existing.Managed && !isSelfSkillMaterialization(existing) {
				targets = append(targets, skillRemovalTarget{Name: existing.Skill, CanonicalID: existing.CanonicalID})
			}
		}
		return dedupeRemovalTargets(targets), nil
	}

	var targets []skillRemovalTarget
	for _, ref := range refs {
		declaredMatches := declaredMatchesRef(declared, ref)
		if len(declaredMatches) > 1 {
			return nil, fmt.Errorf("skill %q is ambiguous; matches %s; use a canonical id", ref, skillMatchNames(declaredMatches))
		}
		installedMatches := installedMatchesRef(installed, ref)
		if len(installedMatches) > 1 {
			return nil, fmt.Errorf("skill %q is ambiguous; matches installed %s", ref, installedMatchNames(installedMatches))
		}
		for _, s := range declaredMatches {
			targets = append(targets, skillRemovalTarget{Name: s.Name, CanonicalID: s.CanonicalID})
		}
		for _, existing := range installedMatches {
			if isSelfSkillMaterialization(existing) {
				continue
			}
			targets = append(targets, skillRemovalTarget{Name: existing.Skill, CanonicalID: existing.CanonicalID})
		}
		if len(declaredMatches) == 0 && len(installedMatches) == 0 {
			targets = append(targets, skillRemovalTarget{Name: ref})
		}
	}
	return dedupeRemovalTargets(targets), nil
}

func isSelfSkillMaterialization(existing skills.InstalledSkill) bool {
	return existing.CanonicalID == selfskill.CanonicalID
}

func declaredOrRawRemovalTargets(declared []skills.Skill, refs []string) []skillRemovalTarget {
	var targets []skillRemovalTarget
	for _, ref := range refs {
		matches := declaredMatchesRef(declared, ref)
		if len(matches) == 0 {
			targets = append(targets, skillRemovalTarget{Name: ref})
			continue
		}
		for _, s := range matches {
			targets = append(targets, skillRemovalTarget{Name: s.Name, CanonicalID: s.CanonicalID})
		}
	}
	return dedupeRemovalTargets(targets)
}

func declaredMatchesRef(declared []skills.Skill, ref string) []skills.Skill {
	ref = strings.TrimSpace(ref)
	var matches []skills.Skill
	for _, s := range declared {
		if skillMatchesRef(s, ref) {
			matches = append(matches, s)
		}
	}
	return matches
}

func installedMatchesRef(installed []skills.InstalledSkill, ref string) []skills.InstalledSkill {
	ref = strings.TrimSpace(ref)
	var matches []skills.InstalledSkill
	for _, s := range installed {
		if s.Skill == ref || s.CanonicalID == ref {
			matches = append(matches, s)
		}
	}
	return matches
}

func installedMatchNames(matches []skills.InstalledSkill) string {
	var names []string
	for _, s := range matches {
		name := s.Skill
		if s.CanonicalID != "" {
			name = s.CanonicalID + " (" + s.Skill + ")"
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func dedupeRemovalTargets(targets []skillRemovalTarget) []skillRemovalTarget {
	var out []skillRemovalTarget
	seen := map[string]bool{}
	for _, target := range targets {
		if target.Name == "" || seen[target.Name] {
			continue
		}
		out = append(out, target)
		seen[target.Name] = true
	}
	return out
}

func selectedHarnesses(all bool, names []string) ([]harness.Harness, error) {
	if all {
		if len(names) != 0 {
			return nil, fmt.Errorf("--all cannot be combined with explicit harnesses")
		}
		return harness.All(), nil
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("select at least one harness or pass --all")
	}

	out := make([]harness.Harness, 0, len(names))
	for _, name := range names {
		h, err := harness.Parse(name)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

func (a app) resolveSkillsSource(explicit, home string) (bundle.Source, error) {
	source, err := bundle.ResolveSkillsSource(bundle.ResolveOptions{
		ExplicitSource: explicit,
		Home:           home,
	})
	if err != nil {
		return bundle.Source{}, err
	}
	fmt.Fprintln(a.stderr, source.Description())
	return source, nil
}

func (a app) discoverSkills(opts skillsCommandOpts) ([]skills.Skill, []string, bool, error) {
	if opts.source != "" && opts.manifestName != "" {
		return nil, nil, false, fmt.Errorf("--source and --manifest are mutually exclusive")
	}
	allowMissingToolSkills := opts.print || opts.allowMissingToolSkills
	if opts.manifestName != "" {
		found, sourceRoots, err := a.discoverManifestSkills(opts.home, opts.manifestName, allowMissingToolSkills, !opts.quietSource, opts.skillRefs)
		return found, sourceRoots, true, err
	}
	if opts.source == "" {
		if found, sourceRoots, ok, err := a.discoverDefaultManifestSkills(opts.home, allowMissingToolSkills, !opts.quietSource, opts.skillRefs); err != nil {
			return nil, nil, false, err
		} else if ok {
			return found, sourceRoots, true, nil
		}
	}
	source, err := a.resolveSkillsSource(opts.source, opts.home)
	if err != nil {
		return nil, nil, false, err
	}
	bundled, err := skills.Discover(source.SkillsDir)
	if err != nil {
		return nil, nil, false, err
	}
	return bundled, []string{source.SkillsDir}, false, nil
}

func (a app) prepareManifestSkillSources(opts skillsCommandOpts) error {
	if opts.source != "" {
		return nil
	}
	docs, ok, err := a.skillManifestDocs(opts.home, opts.manifestName)
	if err != nil || !ok {
		return err
	}
	return a.installToolSkills(opts.home, docs, opts.print, opts.skillRefs)
}

func (a app) skillManifestDocs(home, manifestName string) ([]registeredDoc, bool, error) {
	if manifestName != "" {
		docs, err := loadRegisteredDocs(home, manifestName)
		return docs, true, err
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return nil, false, err
	}
	if len(reg.Manifests) == 0 {
		return nil, false, nil
	}
	docs, err := loadRegisteredDocs(home, "")
	return docs, true, err
}

func (a app) installToolSkills(home string, docs []registeredDoc, dryRun bool, refs []string) error {
	needed := manifestToolSkillIDs(docs, refs)
	if len(needed) == 0 {
		return nil
	}
	skillsRoot, err := bundle.SkillsRoot(home)
	if err != nil {
		return err
	}
	if !dryRun {
		if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, doc := range docs {
		for _, tool := range doc.doc.Tools {
			if !needed[tool.ID] || tool.SkillInstall.Command == "" {
				continue
			}
			key := doc.ref.Name + ":" + tool.ID
			if seen[key] {
				continue
			}
			seen[key] = true
			args := skillInstallArgs(tool.SkillInstall.Args, skillsRoot)
			label := doc.ref.Name + ":" + tool.ID
			if dryRun {
				fmt.Fprintf(a.stderr, "# tool-skill: %s dry-run %s\n", label, commandLine(tool.SkillInstall.Command, args))
				continue
			}
			commandPath, err := exec.LookPath(tool.SkillInstall.Command)
			if err != nil {
				fmt.Fprintf(a.stderr, "warning: tool-skill: %s skipped; %s not in PATH\n", label, tool.SkillInstall.Command)
				continue
			}
			cmd := exec.Command(commandPath, args...)
			out, err := cmd.CombinedOutput()
			message := strings.TrimSpace(string(out))
			if err != nil {
				if message == "" {
					message = err.Error()
				}
				fmt.Fprintf(a.stderr, "warning: tool-skill: %s failed: %s\n", label, message)
				continue
			}
			line := fmt.Sprintf("# tool-skill: %s installed via %s", label, commandLine(tool.SkillInstall.Command, args))
			if message != "" {
				line += "\t" + message
			}
			fmt.Fprintln(a.stderr, line)
		}
	}
	return nil
}

func manifestToolSkillIDs(docs []registeredDoc, refs []string) map[string]bool {
	needed := map[string]bool{}
	for _, doc := range docs {
		for _, skill := range doc.doc.Skills {
			if len(refs) != 0 && !manifestSkillMatchesRefs(skill, refs) {
				continue
			}
			if skill.Source.Type == "tool" && skill.Source.Tool != "" {
				needed[skill.Source.Tool] = true
			}
		}
	}
	return needed
}

func manifestSkillMatchesRefs(skill manifest.Skill, refs []string) bool {
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if skill.ID == ref || skill.InstallSlug == ref {
			return true
		}
	}
	return false
}

func skillInstallArgs(args []string, skillsRoot string) []string {
	out := make([]string, 0, len(args))
	replacer := strings.NewReplacer("{{ skills_root }}", skillsRoot, "{{skills_root}}", skillsRoot)
	for _, arg := range args {
		out = append(out, replacer.Replace(arg))
	}
	return out
}

func commandLine(command string, args []string) string {
	parts := append([]string{command}, args...)
	return strings.Join(parts, " ")
}

func (a app) discoverManifestSkills(home, manifestName string, allowMissingToolSkills, showSource bool, refs []string) ([]skills.Skill, []string, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, nil, err
	}
	return a.discoverManifestSkillDocs(home, docs, allowMissingToolSkills, showSource, refs)
}

func (a app) discoverDefaultManifestSkills(home string, allowMissingToolSkills, showSource bool, refs []string) ([]skills.Skill, []string, bool, error) {
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return nil, nil, false, err
	}
	if len(reg.Manifests) == 0 {
		return nil, nil, false, nil
	}
	docs, err := loadRegisteredDocs(home, "")
	if err != nil {
		return nil, nil, false, err
	}
	found, sourceRoots, err := a.discoverManifestSkillDocs(home, docs, allowMissingToolSkills, showSource, refs)
	if err != nil {
		return nil, nil, false, err
	}
	return found, sourceRoots, true, nil
}

func (a app) discoverManifestSkillDocs(home string, docs []registeredDoc, allowMissingToolSkills, showSource bool, refs []string) ([]skills.Skill, []string, error) {
	var out []skills.Skill
	var sourceRoots []string
	materializedRoot, err := bundle.SkillsRoot(home)
	if err != nil {
		return nil, nil, err
	}
	for _, doc := range docs {
		toolsByID := manifestToolsByID(doc.doc.Tools)
		declared := make([]skills.DeclaredSkill, 0, len(doc.doc.Skills))
		for _, skill := range doc.doc.Skills {
			if len(refs) != 0 && !manifestSkillMatchesRefs(skill, refs) {
				continue
			}
			sourceType := skill.Source.Type
			if sourceType == "" {
				sourceType = "static"
			}
			sourceRoot := doc.ref.LocalPath
			sourceLabel := "manifest root"
			path := skill.Path
			if sourceType == "tool" {
				sourceRoot = materializedRoot
				sourceLabel = "materialized skills root"
				if path == "" {
					path = skill.InstallSlug
				}
				if !allowMissingToolSkills {
					skillPath := filepath.Join(sourceRoot, filepath.FromSlash(path), "SKILL.md")
					if _, err := os.Stat(skillPath); err != nil {
						tool := toolsByID[skill.Source.Tool]
						if strings.EqualFold(tool.Mode, "required") {
							return nil, nil, fmt.Errorf("manifest %q: required tool-sourced skill %q missing SKILL.md at %s: %w", doc.ref.Name, skill.ID, filepath.Dir(skillPath), err)
						}
						fmt.Fprintf(a.stderr, "warning: tool-skill: %s skipped; generated skill missing at %s\n", skill.ID, filepath.Dir(skillPath))
						continue
					}
				}
			}
			declared = append(declared, skills.DeclaredSkill{
				ID:           skill.ID,
				InstallSlug:  skill.InstallSlug,
				Path:         path,
				SourceRoot:   sourceRoot,
				SourceLabel:  sourceLabel,
				AllowMissing: sourceType == "tool" && allowMissingToolSkills,
			})
		}
		found, err := skills.DiscoverDeclared(doc.ref.LocalPath, declared)
		if err != nil {
			return nil, nil, fmt.Errorf("manifest %q: %w", doc.ref.Name, err)
		}
		if showSource {
			fmt.Fprintf(a.stderr, "# source: manifest %s -> %s\n", doc.ref.Name, doc.ref.LocalPath)
		}
		out = append(out, found...)
		sourceRoots = append(sourceRoots, doc.ref.LocalPath)
	}
	if len(manifestToolSkillIDs(docs, refs)) != 0 {
		sourceRoots = append(sourceRoots, materializedRoot)
	}
	return out, sourceRoots, nil
}

func manifestToolsByID(tools []manifest.Tool) map[string]manifest.Tool {
	out := make(map[string]manifest.Tool, len(tools))
	for _, tool := range tools {
		out[tool.ID] = tool
	}
	return out
}

func (a app) syncSkillWorkspaces(home, manifestName string, dryRun bool) {
	results, err := workspace.Sync(home, manifestName, nil, true, dryRun, nil)
	if err != nil {
		fmt.Fprintf(a.stderr, "warning: workspace sync skipped: %v\n", err)
		return
	}
	for _, result := range results {
		prefix := "# workspace"
		message := result.Message
		if result.Status == "failed" {
			prefix = "warning: workspace"
			message = result.Error
		}
		line := fmt.Sprintf("%s: %s:%s %s %s", prefix, result.Manifest, result.Workspace, result.Status, result.LocalPath)
		if message != "" {
			line += "\t" + message
		}
		fmt.Fprintln(a.stderr, line)
	}
}

func (a app) printResults(results []skills.Result, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
		if resultsFailed(results) {
			return fmt.Errorf("one or more operations failed")
		}
		return nil
	}

	for _, r := range results {
		line := fmt.Sprintf("%s\t%s\t%s", r.Harness, r.Skill, r.Status)
		if r.CanonicalID != "" {
			line += "\t" + r.CanonicalID
		}
		if r.TargetPath != "" {
			line += "\t" + r.TargetPath
		}
		if r.Message != "" {
			line += "\t" + r.Message
		}
		if r.Err != nil {
			line += "\t" + r.Err.Error()
		}
		fmt.Fprintln(a.stdout, line)
	}
	if resultsFailed(results) {
		return fmt.Errorf("one or more operations failed")
	}
	return nil
}

func resultsFailed(results []skills.Result) bool {
	for _, r := range results {
		if r.Status == skills.StatusFailed || r.Status == skills.StatusBlocked {
			return true
		}
	}
	return false
}

func manifestResultsFailed(results []manifest.SyncResult) bool {
	for _, r := range results {
		if r.Status == "failed" {
			return true
		}
	}
	return false
}

func workspaceResultsFailed(results []workspace.SyncResult) bool {
	for _, r := range results {
		if r.Status == "failed" {
			return true
		}
	}
	return false
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type commandErrorPayload struct {
	Error       string `json:"error"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type structuredCommandError struct {
	code        string
	message     string
	remediation string
}

func (e structuredCommandError) Error() string {
	return e.message
}

func noUmbrellaError(message, remediation string) error {
	return structuredCommandError{
		code:        "no_umbrella",
		message:     message,
		remediation: remediation,
	}
}

func (a app) maybeJSONError(jsonOut bool, err error) error {
	if !jsonOut {
		return err
	}
	payload := commandErrorPayload{
		Error:   "command_failed",
		Message: err.Error(),
	}
	var structured structuredCommandError
	if errors.As(err, &structured) {
		payload.Error = structured.code
		payload.Message = structured.message
		payload.Remediation = structured.remediation
	} else if strings.Contains(err.Error(), "no our umbrella found") {
		payload.Error = "no_umbrella"
		payload.Remediation = "run our setup or pass --umbrella <path>"
	}
	if printErr := printJSON(a.stdout, payload); printErr != nil {
		return printErr
	}
	return errAlreadyPrinted
}

func (a app) printSkillWarnings(bundled []skills.Skill) {
	for _, s := range bundled {
		for _, warning := range s.Warnings {
			fmt.Fprintf(a.stderr, "warning: %s: %s\n", s.Name, warning)
		}
	}
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func parseInterspersed(fs *flag.FlagSet, args []string, valueFlags map[string]bool) ([]string, error) {
	var flagArgs []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if isFlagArg(arg) {
			flagArgs = append(flagArgs, arg)
			name := flagName(arg)
			if valueFlags[name] && !strings.Contains(arg, "=") {
				i++
				if i >= len(args) {
					return nil, fmt.Errorf("missing value for %s", arg)
				}
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positional, nil
}

func isFlagArg(arg string) bool {
	return strings.HasPrefix(arg, "-") && arg != "-"
}

func flagName(arg string) string {
	arg = strings.TrimLeft(arg, "-")
	if i := strings.Index(arg, "="); i >= 0 {
		return arg[:i]
	}
	return arg
}
