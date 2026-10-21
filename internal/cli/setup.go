package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/guidance"
	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/mcpconfig"
	"github.com/fluxinc/my-cli/internal/selfskill"
	"github.com/fluxinc/my-cli/internal/skills"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/workspace"
)

const onboardingTourVersion = 1

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
		return derivedReconcileReport{}, fmt.Errorf("no my umbrella found; run my setup or pass --umbrella")
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
	skillResults, err := a.collectLaunchScopedOrgSkillResults(opts, harness.All())
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

func (a app) printDerivedReconcileReport(report derivedReconcileReport, verbose bool) {
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
		if !skillResultVisible(result, verbose) {
			continue
		}
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

func (a app) runSetup(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("my setup", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "install into every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.copyMode, "copy", false, "copy skill directories instead of symlinking")
	fs.BoolVar(&opts.linkMode, "link", false, "symlink skill directories")
	fs.BoolVar(&opts.force, "force", false, "replace non-My AI-managed targets")
	fs.BoolVar(&opts.interactive, "interactive", false, "prompt for manifest and role choices")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.BoolVar(&opts.verbose, "verbose", false, "show full human-readable setup detail")
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
	if opts.interactive && opts.jsonOut {
		return fmt.Errorf("--interactive and --json are mutually exclusive")
	}
	if opts.interactive && opts.print {
		return fmt.Errorf("--interactive and --print are mutually exclusive")
	}
	opts.roleSet = flagWasSet(fs, "role")
	if len(rest) == 0 && !opts.all {
		opts.all = true
	}
	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	if !opts.interactive && opts.manifestName == "" {
		if name, ok, err := defaultManifestNameIfAny(opts.home, "", opts.umbrellaRoot); err != nil {
			return err
		} else if ok {
			opts.manifestName = name
		}
	}
	if !opts.print {
		if err := a.ensureManifestSynced(opts.home, opts.manifestName); err != nil {
			return err
		}
	}
	docs, ok, err := a.skillManifestDocs(opts.home, opts.manifestName)
	if err == nil && opts.interactive && opts.manifestName == "" {
		docs, ok, err = a.allSkillManifestDocs(opts.home)
	}
	if err != nil {
		return err
	}
	if !ok || len(docs) == 0 {
		return fmt.Errorf("my setup requires a registered manifest")
	}
	if opts.interactive && opts.manifestName == "" && len(docs) > 1 {
		doc, err := a.promptManifestChoice(docs)
		if err != nil {
			return err
		}
		docs = []registeredDoc{doc}
		opts.manifestName = doc.ref.Name
	} else if len(docs) == 1 {
		opts.manifestName = docs[0].ref.Name
	}
	if len(docs) != 1 {
		return fmt.Errorf("my setup requires exactly one manifest; pass --manifest")
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
	if opts.interactive && !opts.roleSet {
		promptedRole, err := a.promptRoleChoice(doc.doc, selectedRole)
		if err != nil {
			return err
		}
		opts.role = promptedRole
		opts.roleSet = true
		selectedRole = promptedRole
	}
	if selectedRole != "" {
		if _, err := roleByID(doc.doc, selectedRole); err != nil {
			return err
		}
	}
	if !opts.print && opts.roleSet {
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
		Role:              guidanceOpts.Role,
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
	// Role-only mounts are listed only when selectedRole names them, so the
	// "role" mode adds nothing for an unscoped umbrella.
	results, err := workspace.SyncMountsForRole(opts.home, doc.ref.Name, root, selectedRole, nil, false, []string{"required", "default", manifest.MountModeRole}, opts.print, nil)
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
	skillResults, err := a.collectLaunchScopedOrgSkillResults(opts, hs)
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
	if err := a.printSetupSkillResults(skillResults, opts.verbose); err != nil {
		return err
	}
	if guidanceResult.Status == "blocked" || mcpResult.Status == "blocked" {
		return fmt.Errorf("one or more operations failed")
	}
	a.printLaunchHints(root)
	return nil
}

func (a app) printSetupSkillResults(results []skills.Result, verbose bool) error {
	for _, r := range results {
		if !skillResultVisible(r, verbose) {
			continue
		}
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

func skillResultVisible(result skills.Result, verbose bool) bool {
	if verbose {
		return true
	}
	return result.Status != skills.StatusSkipped || result.Err != nil
}

type onboardOptions struct {
	home          string
	manifestName  string
	umbrellaRoot  string
	agent         bool
	noAgent       bool
	harnessName   string
	noRefresh     bool
	noUpdateCheck bool
	status        bool
	complete      bool
}

func (a app) runOnboard(args []string) error {
	var opts onboardOptions
	fs := newFlagSet("my onboarding", a.stderr)
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use a registered manifest")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&opts.agent, "agent", false, "force launching model-driven onboarding in a harness")
	fs.BoolVar(&opts.noAgent, "no-agent", false, "run the deterministic walkthrough instead of launching a harness")
	fs.StringVar(&opts.harnessName, "harness", "", "harness to launch ("+harness.Names()+")")
	fs.BoolVar(&opts.noRefresh, "no-refresh", false, "skip best-effort auto-refresh during setup")
	fs.BoolVar(&opts.noUpdateCheck, "no-update-check", false, "skip best-effort update notice during setup")
	fs.BoolVar(&opts.status, "status", false, "report whether the local onboarding tour is complete")
	fs.BoolVar(&opts.complete, "complete", false, "record the local onboarding tour as complete")
	fs.Usage = func() {
		a.printOnboardUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
		"harness":  true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("my onboarding does not accept positional arguments")
	}
	if opts.noAgent && (opts.agent || opts.harnessName != "") {
		return fmt.Errorf("--no-agent cannot be combined with --agent or --harness")
	}
	if opts.status && opts.complete {
		return fmt.Errorf("--status cannot be combined with --complete")
	}
	if (opts.status || opts.complete) && (opts.agent || opts.noAgent || opts.harnessName != "") {
		return fmt.Errorf("--status and --complete cannot be combined with agent selection flags")
	}
	if opts.status || opts.complete {
		return a.runOnboardCompletion(opts)
	}
	// Interactive onboarding launches the model-guided harness by default so the
	// model can introduce itself and configure the workspace conversationally.
	// --agent or --harness force it; --no-agent and non-interactive (scripts,
	// pipes, CI) fall back to the deterministic walkthrough.
	if opts.agent || opts.harnessName != "" || (!opts.noAgent && a.interactive) {
		return a.runOnboardAgent(opts)
	}
	if opts.manifestName == "" {
		if name, ok, err := defaultManifestNameIfAny(opts.home, "", opts.umbrellaRoot); err != nil {
			return err
		} else if ok {
			opts.manifestName = name
		}
	}
	if err := a.ensureManifestSynced(opts.home, opts.manifestName); err != nil {
		return err
	}
	docs, ok, err := a.skillManifestDocs(opts.home, opts.manifestName)
	if err != nil {
		return err
	}
	if !ok || len(docs) == 0 {
		a.printOnboardZeroManifest()
		return nil
	}
	if opts.manifestName == "" && len(docs) > 1 {
		doc, err := a.promptManifestChoice(docs)
		if err != nil {
			return err
		}
		docs = []registeredDoc{doc}
		opts.manifestName = doc.ref.Name
	} else if len(docs) == 1 {
		opts.manifestName = docs[0].ref.Name
	}
	if len(docs) != 1 {
		return fmt.Errorf("my onboarding requires exactly one manifest; pass --manifest")
	}
	doc := docs[0]
	root, err := umbrella.ResolveRoot(opts.home, ".", opts.umbrellaRoot, doc.doc)
	if err != nil {
		return err
	}
	state, stateExists, err := loadOptionalState(root)
	if err != nil {
		return err
	}
	configured := stateExists
	if _, err := umbrella.LoadWorkspace(root); err == nil {
		configured = true
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		// LoadWorkspace returns path errors directly; any non-missing error is real.
		return err
	}
	if !a.interactive && len(requiredPoliciesForRole(doc.doc, state.SelectedRole)) != 0 {
		a.printRequiredPolicyCommands(opts.home, doc, root, requiredPoliciesForRole(doc.doc, state.SelectedRole))
		return nil
	}
	if tourMarked(state) {
		complete, err := a.reviewRequiredPolicies(opts.home, doc, root)
		if err != nil {
			return err
		}
		if !complete {
			return nil
		}
		a.printOnboardComplete(doc, root, state)
		return nil
	}
	a.printOnboardIntro(doc, root, configured)
	runSetup := false
	answered := false
	if configured {
		runSetup, answered, err = a.promptConfirm("Review setup interactively now?", false)
	} else {
		runSetup, answered, err = a.promptConfirm("Run setup interactively now?", true)
	}
	if err != nil {
		return err
	}
	if !answered {
		a.printOnboardUnmarkedSetup(opts, doc.ref.Name, root, configured, "no input received")
		return nil
	}
	if runSetup {
		if err := a.runSetup(onboardSetupArgs(opts, doc.ref.Name, root)); err != nil {
			return err
		}
		doc, err = loadSingleRegisteredDoc(opts.home, doc.ref.Name)
		if err != nil {
			return err
		}
		complete, err := a.reviewRequiredPolicies(opts.home, doc, root)
		if err != nil {
			return err
		}
		if !complete {
			return nil
		}
		if err := markTourComplete(root); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, "Onboarding complete.")
		return nil
	}
	a.printOnboardUnmarkedSetup(opts, doc.ref.Name, root, configured, "setup review declined")
	return nil
}

func (a app) runOnboardCompletion(opts onboardOptions) error {
	if opts.manifestName == "" {
		name, ok, err := defaultManifestNameIfAny(opts.home, "", opts.umbrellaRoot)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("onboarding is incomplete: no organization manifest is registered")
		}
		opts.manifestName = name
	}
	doc, err := loadSingleRegisteredDoc(opts.home, opts.manifestName)
	if err != nil {
		return fmt.Errorf("onboarding is incomplete: %w", err)
	}
	root, err := umbrella.ResolveRoot(opts.home, ".", opts.umbrellaRoot, doc.doc)
	if err != nil {
		return fmt.Errorf("onboarding is incomplete: %w", err)
	}
	state, exists, err := loadOptionalState(root)
	if err != nil {
		return err
	}
	if opts.status {
		if !exists || !tourMarked(state) {
			return fmt.Errorf("onboarding is incomplete for %s; run my onboarding --manifest %s", opts.manifestName, opts.manifestName)
		}
		fmt.Fprintf(a.stdout, "complete\t%s\t%s\n", opts.manifestName, root)
		return nil
	}
	if !exists || (len(doc.doc.Roles) != 0 && state.SelectedRole == "") {
		return fmt.Errorf("cannot complete onboarding before setup; run my setup --interactive --manifest %s", opts.manifestName)
	}
	if err := a.requireGovernedPolicyAcceptances(opts.home, doc, root); err != nil {
		return fmt.Errorf("cannot complete onboarding: %w", err)
	}
	for _, tool := range doc.doc.Tools {
		if tool.Mode != "required" {
			continue
		}
		if _, err := a.lookupPath(tool.ID); err != nil {
			return fmt.Errorf("cannot complete onboarding: required tool %q is missing; inspect my tools info %s --manifest %s", tool.ID, tool.ID, opts.manifestName)
		}
	}
	if err := markTourComplete(root); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Onboarding complete for %s.\n", opts.manifestName)
	return nil
}

func (a app) runOnboardAgent(opts onboardOptions) error {
	if opts.umbrellaRoot != "" {
		root, err := resolveUmbrellaRoot(opts.home, opts.umbrellaRoot)
		if err != nil {
			return err
		}
		opts.umbrellaRoot = root
	}
	if opts.manifestName == "" {
		if name, ok, err := defaultManifestNameIfAny(opts.home, "", opts.umbrellaRoot); err != nil {
			return err
		} else if ok {
			opts.manifestName = name
		}
	}
	docs, ok, err := a.skillManifestDocs(opts.home, opts.manifestName)
	if err != nil {
		if opts.manifestName == "" {
			return err
		}
		ref, found, findErr := manifest.Find(opts.home, opts.manifestName)
		if findErr != nil {
			return findErr
		}
		if !found {
			return err
		}
		h, selectErr := a.selectOnboardHarness(opts.harnessName, opts.home)
		if selectErr != nil {
			return selectErr
		}
		if errors.Is(err, os.ErrNotExist) {
			return a.runOnboardAgentDirect(opts, h, onboardAgentBootstrapPrompt(ref))
		}
		return a.runOnboardAgentDirect(opts, h, onboardAgentRepairPrompt(ref, err))
	}
	if ok && len(docs) != 0 && len(docs) != 1 {
		return fmt.Errorf("my onboarding requires exactly one manifest; pass --manifest")
	}
	h, err := a.selectOnboardHarness(opts.harnessName, opts.home)
	if err != nil {
		return err
	}
	if !ok || len(docs) == 0 {
		return a.runOnboardAgentAuthor(opts, h)
	}
	doc := docs[0]
	root, err := umbrella.ResolveRoot(opts.home, ".", opts.umbrellaRoot, doc.doc)
	if err != nil {
		return err
	}
	prompt := onboardAgentPrompt("JOIN", doc.ref.Name, root)
	opts.umbrellaRoot = root
	return a.runOnboardAgentDirect(opts, h, prompt)
}

func (a app) runOnboardAgentAuthor(opts onboardOptions, h harness.Harness) error {
	return a.runOnboardAgentDirect(opts, h, onboardAgentPrompt("AUTHOR", "", ""))
}

func (a app) runOnboardAgentDirect(opts onboardOptions, h harness.Harness, prompt string) error {
	if err := a.ensureLaunchSelfSkill(h, opts.home); err != nil {
		return err
	}
	// Onboarding must start before Git, provider authentication, setup, and
	// policy acceptance. Never inherit an umbrella's organization instructions
	// or MCP configuration by launching the bootstrap assistant inside it.
	// Keep this directory after exit: a harness may have written useful scratch
	// files or recorded its working directory for conversation resumption.
	cwd, err := os.MkdirTemp("", "my-onboarding-")
	if err != nil {
		return err
	}
	home, err := resolveHome(opts.home)
	if err != nil {
		return err
	}
	prompt += "\nThis is a neutral onboarding workspace, not the organization umbrella or a security sandbox. Run routine local setup commands yourself and explain what each step teaches. Use --home " + shellQuote(home) + " on my commands"
	if opts.manifestName != "" {
		prompt += " and --manifest " + shellQuote(opts.manifestName)
	}
	if opts.umbrellaRoot != "" {
		prompt += "; preserve the requested --umbrella " + shellQuote(opts.umbrellaRoot)
	}
	if opts.noRefresh {
		prompt += "; preserve --no-refresh on commands that support it"
	}
	if opts.noUpdateCheck {
		prompt += "; preserve --no-update-check on commands that support it"
	}
	prompt += ". Apply selectors only to commands that support them: manifests sync takes the manifest name positionally, not --manifest; manifests list takes --home. Do not run organization work or load organization skills here. After setup, guide the operator through the normal my ai launch, including its authentication and policy gates."
	fmt.Fprintf(a.stdout, "Onboarding workspace (kept for resume): %s\n", cwd)
	args, err := initialPromptArgs(h, prompt)
	if err != nil {
		return err
	}
	binary, err := a.lookupHarnessBinary(h)
	if err != nil {
		a.printLaunchMissingHarness(h, cwd, args, false, err)
		return errAlreadyPrinted
	}
	return a.runHarness(binary, args, cwd)
}

func (a app) selectOnboardHarness(name, home string) (harness.Harness, error) {
	if name != "" {
		h, err := harness.Parse(name)
		if err != nil {
			return "", err
		}
		a.printOnboardHarnessChoice(h, "")
		return h, nil
	}
	if h, reason, ok := a.autoDetectHarness(home); ok {
		a.printOnboardHarnessChoice(h, reason)
		return h, nil
	}
	var installed []harness.Harness
	for _, h := range harness.All() {
		if a.harnessInstalled(h) {
			installed = append(installed, h)
		}
	}
	if len(installed) == 0 {
		return "", fmt.Errorf("no supported agent harness found on PATH; install Codex, Claude Code, OpenCode, Antigravity, Grok, or Cursor, then rerun my onboarding")
	}
	fmt.Fprintln(a.stdout, "Select a harness:")
	for i, h := range installed {
		label := string(h)
		switch {
		case a.harnessInstalled(h) && a.harnessHasLogin(h, home):
			label += " (installed, logged in)"
		case a.harnessInstalled(h):
			label += " (installed)"
		}
		fmt.Fprintf(a.stdout, "  %d) %s\n", i+1, label)
	}
	for {
		line, err := a.promptLine("Harness (--harness to skip this prompt): ")
		if err != nil {
			if errors.Is(err, io.EOF) {
				return "", fmt.Errorf("harness selection requires input; pass --harness")
			}
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if n, err := strconv.Atoi(line); err == nil {
			if n >= 1 && n <= len(installed) {
				h := installed[n-1]
				a.printOnboardHarnessChoice(h, "")
				return h, nil
			}
			fmt.Fprintf(a.stdout, "Enter a number from 1 to %d, or an installed harness name.\n", len(installed))
			continue
		}
		h, err := harness.Parse(line)
		if err == nil {
			a.printOnboardHarnessChoice(h, "")
			return h, nil
		}
		fmt.Fprintln(a.stdout, err)
	}
}

func (a app) printOnboardHarnessChoice(h harness.Harness, reason string) {
	if reason == "" {
		fmt.Fprintf(a.stdout, "Onboarding with %s.\n", h)
		return
	}
	fmt.Fprintf(a.stdout, "Onboarding with %s (%s).\n", h, reason)
}

// autoDetectHarness picks a harness without prompting when the choice is
// unambiguous: a single logged-in harness wins, otherwise a single installed
// harness. Anything ambiguous returns ok=false so the caller prompts. Login
// detection is best-effort; the launched assistant's first response is the
// practical check that the chosen harness can actually run.
func (a app) autoDetectHarness(home string) (harness.Harness, string, bool) {
	var installed, loggedIn []harness.Harness
	for _, h := range harness.All() {
		if !a.harnessInstalled(h) {
			continue
		}
		installed = append(installed, h)
		if a.harnessHasLogin(h, home) {
			loggedIn = append(loggedIn, h)
		}
	}
	switch {
	case len(loggedIn) == 1:
		return loggedIn[0], "installed, logged in", true
	case len(loggedIn) == 0 && len(installed) == 1:
		return installed[0], "installed", true
	}
	return "", "", false
}

func (a app) harnessInstalled(h harness.Harness) bool {
	_, err := a.lookupHarnessBinary(h)
	return err == nil
}

func (a app) harnessHasLogin(h harness.Harness, home string) bool {
	resolved, err := resolveHome(home)
	if err != nil {
		return false
	}
	for _, p := range h.LoginMarkers(resolved) {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func onboardAgentPrompt(branch, manifestName, root string) string {
	var b strings.Builder
	b.WriteString("You are this person's My AI onboarding assistant. Start by greeting them in one or two warm sentences, introduce yourself, and immediately begin a learn-by-example walkthrough.\n")
	b.WriteString("Use the bundled `my-cli` skill, section `Agent-Operated Onboarding`, as the source of truth. Run routine local checks and setup commands yourself, explain what they do and teach as you go, diagnose failures and retry the continuation command. Pause only for authentication, privilege/password prompts, policy acceptance, material choices, destructive actions, or publication. Never ask for secrets in chat. Optional tools are opt-in. Offer a manual learn-by-example walkthrough when requested.\n")
	b.WriteString("Keep the walkthrough focused on basic human workflows: setup, launching harnesses, starting/resuming/finishing work sessions, `my sync`, `my sync --push --print`/`my sync --push`, and `my doctor`. For HTTP services, inspect `my services list` and each service auth_setup_url. Teach personal-key setup: the human creates a key in that web UI and saves it in a private password-manager item, then use `my services auth set <id> --ref op://Private/item/credential` or an env:// reference and `my services auth status <id>` to verify it. Never collect a literal key in chat, read the key aloud, or change an organization shared credential. Do not teach content-record, fleet, catalog, or full admin command surfaces during onboarding; humans can paste transcripts or raw context into harness chat and let agents operate deeper CLI surfaces.\n")
	switch branch {
	case "AUTHOR":
		b.WriteString("Branch: AUTHOR. No My AI manifest is registered for this invocation. Guide the operator through authoring a new organization one small command set at a time, and present `my init` only after explicit human approval.\n")
	case "JOIN":
		b.WriteString("Branch: JOIN by default. A manifest is registered")
		if manifestName != "" {
			b.WriteString(": ")
			b.WriteString(manifestName)
		}
		b.WriteString(".")
		if root != "" {
			b.WriteString(" Umbrella: ")
			b.WriteString(root)
			b.WriteString(".")
		}
		b.WriteString(" Set up this person against the existing organization with an agent-operated learn-by-example walkthrough; offer AUTHOR-style admin edits only if the operator asks.\n")
	case "JOIN_BOOTSTRAP":
		b.WriteString("Branch: JOIN_BOOTSTRAP. An existing organization manifest is registered")
		if manifestName != "" {
			b.WriteString(": ")
			b.WriteString(manifestName)
		}
		b.WriteString(", but its checkout is not synced yet. Begin with first-machine prerequisites and GitHub authentication, then sync the manifest, run setup, inspect required organization tools, and continue the JOIN walkthrough. Do not redirect this person into creating a new organization.\n")
	case "JOIN_REPAIR":
		b.WriteString("Branch: JOIN_REPAIR. An existing organization manifest is registered")
		if manifestName != "" {
			b.WriteString(": ")
			b.WriteString(manifestName)
		}
		b.WriteString(", but its local checkout is invalid or unreadable. Diagnose and preserve recoverable state, repair one issue at a time, then continue JOIN. Do not create a replacement organization.\n")
	default:
		b.WriteString("Branch: detect from the registered manifest state.\n")
	}
	b.WriteString("Hard rules: use validated `my` commands rather than hand-editing manifests or generated files; never collect or store literal secrets; if the operator says to file an issue, treat that as authorization to file a public-safe project issue and do not substitute a personal scratch or memory note unless the target repo or privacy boundary is ambiguous; run validation/doctor/compile gates before publish; run `my publish --print` and get explicit human approval before any real `my publish`.")
	return b.String()
}

func onboardAgentBootstrapPrompt(ref manifest.Ref) string {
	prompt := onboardAgentPrompt("JOIN_BOOTSTRAP", ref.Name, "")
	return prompt + "\nRegistered manifest URL: " + ref.GitURL + ". The first deterministic continuation command is `my manifests sync " + ref.Name + "`. If it fails, explain the exact prerequisite or authentication issue, resolve one item at a time, and rerun the same command. For GitHub HTTPS, use `gh auth status` and `gh auth login`; My AI supplies gh as an invocation-local Git credential helper and must not rewrite global Git configuration."
}

func onboardAgentRepairPrompt(ref manifest.Ref, cause error) string {
	prompt := onboardAgentPrompt("JOIN_REPAIR", ref.Name, "")
	return prompt + "\nThe registered manifest checkout could not be loaded: " + cause.Error() + ". Inspect `" + ref.LocalPath + "` without deleting or overwriting unrelated files. Explain the exact issue, preserve recoverable state, and use `my manifests sync " + ref.Name + "` when that is safe. Do not redirect this person into creating a new organization."
}

func (a app) printOnboardUnmarkedSetup(opts onboardOptions, manifestName, root string, configured bool, reason string) {
	message := "Run setup to create the umbrella before onboarding can be recorded as complete."
	if configured {
		message = "Run setup when you are ready to review your configuration."
	}
	if reason != "" {
		message += " (" + reason + ")"
	}
	fmt.Fprintln(a.stdout, message)
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Next step:")
	fmt.Fprintf(a.stdout, "  %s\n", shellCommandLine("", "my", append([]string{"setup", "--interactive"}, setupCommandFlags(opts, manifestName, root)...)))
}

func (a app) printOnboardUsage() {
	fmt.Fprintln(a.stderr, `Usage of my onboarding:
  my onboarding [--agent|--no-agent] [--harness NAME] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  my onboarding --status [--manifest NAME] [--home DIR] [--umbrella DIR]
  my onboarding --complete [--manifest NAME] [--home DIR] [--umbrella DIR]

Run interactively, my onboarding launches a harness with the bundled my-cli self-skill
and an agent-operated onboarding prompt: the model greets the operator, starts a
learn-by-example walkthrough, and runs routine validated my commands itself.
All branches start in a neutral temporary workspace before Git, gh, setup, or
policy acceptance is required. Organization work starts later through normal
my ai, with its usual access and policy gates. A harness is
auto-detected (preferring one that is logged in); pass --harness to choose.

--agent forces the harness launch even when stdout is not a TTY. --no-agent (and
non-interactive runs such as pipes or CI) instead print the deterministic
walkthrough: it explains the model and points at my setup --interactive.

my onboard remains available as a compatibility alias.`)
}

func (a app) printOnboardZeroManifest() {
	fmt.Fprintln(a.stdout, "My AI starts from a registered organization manifest, and none is registered yet.")
	fmt.Fprintln(a.stdout, "Ask your organization admin for the manifest name and Git URL, then run:")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Next steps:")
	fmt.Fprintln(a.stdout, "  Register a manifest:")
	fmt.Fprintln(a.stdout, "    my manifests add <name> <git-url>")
	fmt.Fprintln(a.stdout, "  Configure:")
	fmt.Fprintln(a.stdout, "    my setup --interactive")
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Or run `my onboarding --agent` to have an assistant build a new organization with you.")
}

func (a app) printOnboardIntro(doc registeredDoc, root string, configured bool) {
	fmt.Fprintf(a.stdout, "My AI - %s (%s)\n", doc.ref.Name, doc.doc.Organization.ID)
	fmt.Fprintf(a.stdout, "Umbrella: %s\n", root)
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "How it fits together:")
	fmt.Fprintln(a.stdout, "  - Manifest (control plane): skills, mounts, services, roles, tools")
	fmt.Fprintln(a.stdout, "  - Mounts (data plane): local content folders such as customers, meetings, support, fleet")
	fmt.Fprintln(a.stdout, "  - Setup writes guidance, MCP config, mounts, repos, skills, and local role state")
	for _, service := range doc.doc.Services {
		if service.Kind == "http" && service.AuthSetupURL != "" {
			fmt.Fprintf(a.stdout, "  - Personal access for %s: create a key at %s, save it in a private password-manager item, then run my services auth set %s --ref <op:// or env:// reference>\n", service.ID, service.AuthSetupURL, service.ID)
		}
	}

	fmt.Fprintln(a.stdout)
	if configured {
		fmt.Fprintln(a.stdout, "Status: configured")
	} else {
		fmt.Fprintln(a.stdout, "Status: setup needed")
	}
}

func (a app) printOnboardComplete(doc registeredDoc, root string, state umbrella.State) {
	fmt.Fprintf(a.stdout, "Onboarding complete - %s\n", doc.ref.Name)
	fmt.Fprintf(a.stdout, "Umbrella: %s\n", root)
	if state.SelectedRole != "" {
		fmt.Fprintf(a.stdout, "Role: %s\n", state.SelectedRole)
	} else {
		fmt.Fprintln(a.stdout, "Role: unscoped")
	}
	if state.Tour != nil && state.Tour.Version < onboardingTourVersion {
		fmt.Fprintf(a.stdout, "A newer guided tour is available (version %d); run `my onboarding --agent` to revisit it.\n", onboardingTourVersion)
	}
	fmt.Fprintln(a.stdout)
	fmt.Fprintln(a.stdout, "Next steps:")
	fmt.Fprintln(a.stdout, "  Review setup:")
	fmt.Fprintf(a.stdout, "    %s\n", shellCommandLine(root, "my", []string{"setup", "--interactive", "--manifest", doc.ref.Name}))
	fmt.Fprintln(a.stdout, "  Launch:")
	fmt.Fprintf(a.stdout, "    %s\n", shellCommandLine(root, "my", []string{"ai", "codex"}))
}

func onboardSetupArgs(opts onboardOptions, manifestName, root string) []string {
	args := []string{"--interactive"}
	args = append(args, setupCommandFlags(opts, manifestName, root)...)
	return args
}

func setupCommandFlags(opts onboardOptions, manifestName, root string) []string {
	args := []string{"--manifest", manifestName, "--umbrella", root}
	if opts.home != "" {
		args = append(args, "--home", opts.home)
	}
	if opts.noRefresh {
		args = append(args, "--no-refresh")
	}
	if opts.noUpdateCheck {
		args = append(args, "--no-update-check")
	}
	return args
}

func loadOptionalState(root string) (umbrella.State, bool, error) {
	state, err := umbrella.LoadState(root)
	if err == nil {
		return state, true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return umbrella.State{}, false, nil
	}
	return umbrella.State{}, false, err
}

func tourMarked(state umbrella.State) bool {
	return state.Tour != nil && state.Tour.CompletedAt != ""
}

func markTourComplete(root string) error {
	state, err := umbrella.LoadState(root)
	if err != nil {
		return err
	}
	state.Tour = &umbrella.TourState{
		CompletedAt: utcNowString(),
		Version:     onboardingTourVersion,
	}
	return umbrella.SaveState(root, state)
}

func (a app) promptManifestChoice(docs []registeredDoc) (registeredDoc, error) {
	fmt.Fprintln(a.stdout, "Select a manifest:")
	for i, doc := range docs {
		name := doc.ref.Name
		if doc.doc.Organization.Name != "" {
			name += " - " + doc.doc.Organization.Name
		}
		fmt.Fprintf(a.stdout, "  %d) %s\n", i+1, name)
	}
	for {
		line, err := a.promptLine("Manifest number (--manifest to skip this prompt): ")
		if err != nil {
			if errors.Is(err, io.EOF) {
				return registeredDoc{}, fmt.Errorf("manifest selection requires input; pass --manifest")
			}
			return registeredDoc{}, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		n, err := strconv.Atoi(line)
		if err != nil || n < 1 || n > len(docs) {
			fmt.Fprintf(a.stdout, "Enter a number from 1 to %d.\n", len(docs))
			continue
		}
		return docs[n-1], nil
	}
}

func (a app) promptRoleChoice(doc manifest.Document, current string) (string, error) {
	if len(doc.Roles) == 0 {
		return "", nil
	}
	fmt.Fprintln(a.stdout, "Select a role:")
	fmt.Fprintln(a.stdout, "  none) unscoped")
	for _, role := range doc.Roles {
		line := "  " + role.ID
		if role.Purpose != "" {
			line += " - " + role.Purpose
		}
		fmt.Fprintln(a.stdout, line)
	}
	defaultLabel := "none"
	if current != "" {
		defaultLabel = current
	}
	for {
		line, err := a.promptLine("Role [" + defaultLabel + "]: ")
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return current, nil
		}
		if strings.EqualFold(line, "none") {
			return "", nil
		}
		if _, err := roleByID(doc, line); err != nil {
			fmt.Fprintf(a.stdout, "%s\n", err)
			continue
		}
		return line, nil
	}
}

func (a app) promptConfirm(prompt string, def bool) (bool, bool, error) {
	suffix := " [y/N]: "
	if def {
		suffix = " [Y/n]: "
	}
	for {
		line, err := a.promptLine(prompt + suffix)
		if err != nil && !errors.Is(err, io.EOF) {
			return false, false, err
		}
		eof := errors.Is(err, io.EOF)
		line = strings.TrimSpace(strings.ToLower(line))
		if line == "" {
			return def, !eof, nil
		}
		switch line {
		case "y", "yes":
			return true, true, nil
		case "n", "no":
			return false, true, nil
		default:
			fmt.Fprintln(a.stdout, "Enter y or n.")
		}
	}
}

func (a app) promptLine(prompt string) (string, error) {
	return a.promptLineTo(a.stdout, prompt)
}

func (a app) promptLineTo(out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, prompt)
	if a.stdin == nil {
		return "", io.EOF
	}
	if reader, ok := a.stdin.(interface {
		ReadString(byte) (string, error)
	}); ok {
		line, err := reader.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}
	reader := bufio.NewReader(a.stdin)
	line, err := reader.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

func utcNowString() string {
	return time.Now().UTC().Format(time.RFC3339)
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
	for _, h := range harness.All() {
		fmt.Fprintf(a.stdout, "launch\t%s\t%s\n", h, shellCommandLine(root, h.CommandName(), nil))
	}
}
