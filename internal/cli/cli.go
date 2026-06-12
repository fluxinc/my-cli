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
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/our-ai/internal/bundle"
	"github.com/fluxinc/our-ai/internal/guidance"
	"github.com/fluxinc/our-ai/internal/harness"
	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/mcpconfig"
	"github.com/fluxinc/our-ai/internal/selfskill"
	"github.com/fluxinc/our-ai/internal/selfupdate"
	"github.com/fluxinc/our-ai/internal/skills"
	"github.com/fluxinc/our-ai/internal/umbrella"
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
	case "services":
		return a.runServices(args[2:])
	case "roles":
		return a.runRoles(args[2:])
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
  our setup [harness...] | --all [--print] [--copy] [--link] [--force] [--role ROLE] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  our root [--repo ID] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  our ai [--new-session|--session ID|--no-session] [--repo ID] [--setup] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check] [harness] [-- harness args...]
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
  our work list [--all] [--json]
  our work resume [session-id] [--json]
  our work finish [session-id] --land|--publish|--discard [--message TEXT] [--json]
  our customers list
  our products list
  our repos list [--json]
  our repos add <id>
  our repos remove <id> [--force]
  our services list [--manifest NAME] [--json]
  our services get <id> [--manifest NAME] [--json]
  our roles list [--manifest NAME] [--json]
  our roles get <id> [--manifest NAME] [--json]
  our doctor [--no-fetch] [--fix] [--json]
  our version`)
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

type derivedReconcileReport struct {
	Guidance guidance.Result  `json:"guidance"`
	Skills   []skills.Result  `json:"skills,omitempty"`
	MCP      mcpconfig.Result `json:"mcp,omitzero"`
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
	guidanceOpts, err := guidanceOptionsForSelectedRole(root, doc.doc)
	if err != nil {
		return derivedReconcileReport{}, err
	}
	guidanceResult, err := guidance.Ensure(root, doc.ref.LocalPath, doc.doc, guidanceOpts)
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
	selectedRole, err := selectedRoleForRoot(root)
	if err != nil {
		return derivedReconcileReport{}, err
	}
	services, err := visibleServices(doc.doc, selectedRole)
	if err != nil {
		return derivedReconcileReport{}, err
	}
	mcpResult, err := mcpconfig.Ensure(root, doc.ref.LocalPath, services, false)
	if err != nil {
		return derivedReconcileReport{}, err
	}
	return derivedReconcileReport{Guidance: guidanceResult, Skills: skillResults, MCP: mcpResult}, nil
}

func (a app) printDerivedReconcileReport(report derivedReconcileReport) {
	line := fmt.Sprintf("derived\tguidance\t%s\t%s", report.Guidance.Status, report.Guidance.TargetPath)
	if report.Guidance.Message != "" {
		line += "\t" + report.Guidance.Message
	}
	fmt.Fprintln(a.stdout, line)
	if report.MCP.Status != "" {
		line := fmt.Sprintf("derived\tmcp\t%s\t%s", report.MCP.Status, report.MCP.TargetPath)
		if report.MCP.Message != "" {
			line += "\t" + report.MCP.Message
		}
		fmt.Fprintln(a.stdout, line)
	}
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
	if report.MCP.Status == "blocked" {
		return true
	}
	for _, result := range report.Skills {
		if result.Status == skills.StatusFailed || result.Status == skills.StatusBlocked {
			return true
		}
	}
	return false
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

func pruneManifestTools(tools []manifest.Tool, remove map[string]bool) []manifest.Tool {
	out := tools[:0]
	for _, tool := range tools {
		if !remove[tool.ID] {
			out = append(out, tool)
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
	fs.StringVar(&opts.role, "role", "", "select a manifest role for generated guidance and service visibility")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
		"role":     true,
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
	var state umbrella.State
	if opts.print {
		fmt.Fprintf(a.stderr, "# umbrella: %s\n", root)
		ws = umbrella.Workspace{
			SchemaVersion: umbrella.SchemaVersion,
			Organization:  doc.doc.Organization.ID,
			ManifestRef:   doc.ref.Name,
			WorkspaceRoot: root,
		}
		if existing, err := umbrella.LoadState(root); err == nil {
			state = existing
		}
	} else {
		ensured, ensuredState, err := umbrella.Ensure(root, doc.doc.Organization.ID, doc.ref.Name)
		if err != nil {
			return err
		}
		ws = ensured
		state = ensuredState
		a.maybeAutoRefresh(opts.home, doc.ref.Name, root, root, opts.noRefresh)
		a.maybeUpdateNotice(opts.home, opts.noUpdateCheck)
		refreshed, err := loadSingleRegisteredDoc(opts.home, doc.ref.Name)
		if err != nil {
			return err
		}
		doc = refreshed
	}
	selectedRole := state.SelectedRole
	if opts.role != "" {
		selectedRole = opts.role
	}
	if selectedRole != "" {
		if _, err := roleByID(doc.doc, selectedRole); err != nil {
			return err
		}
	}
	if !opts.print && opts.role != "" {
		state.SelectedRole = opts.role
		if err := umbrella.SaveState(root, state); err != nil {
			return err
		}
	}
	guidanceOpts, err := setupGuidanceOptions(root, doc.doc, opts)
	if err != nil {
		return err
	}
	guidanceResult, err := guidance.Ensure(root, doc.ref.LocalPath, doc.doc, guidance.Options{
		Force:             opts.force,
		DryRun:            opts.print,
		RoleGuidancePaths: guidanceOpts.RoleGuidancePaths,
	})
	if err != nil {
		return err
	}
	mcpServices, err := visibleServices(doc.doc, selectedRole)
	if err != nil {
		return err
	}
	mcpResult := mcpconfig.Result{TargetPath: filepath.Join(root, ".mcp.json"), Status: "dry-run"}
	if !opts.print {
		mcpResult, err = mcpconfig.Ensure(root, doc.ref.LocalPath, mcpServices, opts.force)
		if err != nil {
			return err
		}
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
			MCP      mcpconfig.Result       `json:"mcp"`
			Mounts   []workspace.SyncResult `json:"mounts"`
			Skills   []skills.Result        `json:"skills"`
		}{Umbrella: ws, Guidance: guidanceResult, MCP: mcpResult, Mounts: results, Skills: skillResults}); err != nil {
			return err
		}
		if guidanceResult.Status == "blocked" || mcpResult.Status == "blocked" || resultsFailed(skillResults) {
			return fmt.Errorf("one or more operations failed")
		}
		return nil
	}
	a.printGuidanceResult(guidanceResult)
	a.printMCPResult(mcpResult)
	a.printWorkspaceResults(results)
	if err := a.printResults(skillResults, false); err != nil {
		return err
	}
	if guidanceResult.Status == "blocked" || mcpResult.Status == "blocked" {
		return fmt.Errorf("one or more operations failed")
	}
	a.printLaunchHints(root)
	return nil
}

func (a app) printMCPResult(result mcpconfig.Result) {
	if result.Status == "" || result.Status == "skipped" {
		return
	}
	line := fmt.Sprintf("mcp-config\t%s\t%s", result.Status, result.TargetPath)
	if result.Message != "" {
		line += "\t" + result.Message
	}
	fmt.Fprintln(a.stdout, line)
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
	if len(s.Requires) != 0 {
		printHumanField(a.stdout, "requires", strings.Join(s.Requires, ", "))
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
	role                   string
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
				Requires:     skill.Requires,
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

func (a app) printSkillWarnings(bundled []skills.Skill) {
	for _, s := range bundled {
		for _, warning := range s.Warnings {
			fmt.Fprintf(a.stderr, "warning: %s: %s\n", s.Name, warning)
		}
	}
}
