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
	docs, ok, err := a.skillManifestDocs(opts.home, opts.manifestName)
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
	if err := a.printResults(skillResults, false); err != nil {
		return err
	}
	if guidanceResult.Status == "blocked" || mcpResult.Status == "blocked" {
		return fmt.Errorf("one or more operations failed")
	}
	a.printLaunchHints(root)
	return nil
}

type onboardOptions struct {
	home          string
	manifestName  string
	umbrellaRoot  string
	agent         bool
	harnessName   string
	noRefresh     bool
	noUpdateCheck bool
}

func (a app) runOnboard(args []string) error {
	var opts onboardOptions
	fs := newFlagSet("my onboard", a.stderr)
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use a registered manifest")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&opts.agent, "agent", false, "launch model-driven onboarding in a harness")
	fs.StringVar(&opts.harnessName, "harness", "", "harness for --agent (claude-code, codex, opencode, antigravity)")
	fs.BoolVar(&opts.noRefresh, "no-refresh", false, "skip best-effort auto-refresh during setup")
	fs.BoolVar(&opts.noUpdateCheck, "no-update-check", false, "skip best-effort update notice during setup")
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
		return fmt.Errorf("my onboard does not accept positional arguments")
	}
	if opts.harnessName != "" && !opts.agent {
		return fmt.Errorf("--harness requires --agent")
	}
	if opts.agent {
		return a.runOnboardAgent(opts)
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
		return fmt.Errorf("my onboard requires exactly one manifest; pass --manifest")
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
	if tourMarked(state) {
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
		if err := markTourComplete(root); err != nil {
			return err
		}
		fmt.Fprintln(a.stdout, "onboard\tcomplete")
		return nil
	}
	a.printOnboardUnmarkedSetup(opts, doc.ref.Name, root, configured, "setup review declined")
	return nil
}

func (a app) runOnboardAgent(opts onboardOptions) error {
	docs, ok, err := a.skillManifestDocs(opts.home, opts.manifestName)
	if err != nil {
		return err
	}
	if ok && len(docs) != 0 && len(docs) != 1 {
		return fmt.Errorf("my onboard --agent requires exactly one manifest; pass --manifest")
	}
	h, err := a.selectOnboardHarness(opts.harnessName)
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
	args := onboardAgentLaunchArgs(opts, doc.ref.Name, root, h)
	return a.runLaunchWithInitialPrompt(args, prompt)
}

func (a app) runOnboardAgentAuthor(opts onboardOptions, h harness.Harness) error {
	if err := a.ensureLaunchSelfSkill(h, opts.home); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	commandName := h.CommandName()
	args, err := initialPromptArgs(h, onboardAgentPrompt("AUTHOR", "", ""))
	if err != nil {
		return err
	}
	binary, err := a.lookupPath(commandName)
	if err != nil {
		a.printLaunchMissingHarness(commandName, cwd, args, false)
		return errAlreadyPrinted
	}
	return a.runHarness(binary, args, cwd)
}

func (a app) selectOnboardHarness(name string) (harness.Harness, error) {
	if name != "" {
		return harness.Parse(name)
	}
	all := harness.All()
	fmt.Fprintln(a.stdout, "Select a harness:")
	for i, h := range all {
		fmt.Fprintf(a.stdout, "  %d) %s\n", i+1, h)
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
			if n >= 1 && n <= len(all) {
				return all[n-1], nil
			}
			fmt.Fprintf(a.stdout, "Enter a number from 1 to %d, or a harness name.\n", len(all))
			continue
		}
		h, err := harness.Parse(line)
		if err == nil {
			return h, nil
		}
		fmt.Fprintln(a.stdout, err)
	}
}

func onboardAgentLaunchArgs(opts onboardOptions, manifestName, root string, h harness.Harness) []string {
	args := []string{"--manifest", manifestName, "--umbrella", root, "--setup", "--no-session"}
	if opts.home != "" {
		args = append(args, "--home", opts.home)
	}
	if opts.noRefresh {
		args = append(args, "--no-refresh")
	}
	if opts.noUpdateCheck {
		args = append(args, "--no-update-check")
	}
	return append(args, string(h))
}

func onboardAgentPrompt(branch, manifestName, root string) string {
	var b strings.Builder
	b.WriteString("Use the bundled `my` skill, section `Agent-Operated Onboarding`, to run model-driven onboarding.\n")
	switch branch {
	case "AUTHOR":
		b.WriteString("Branch: AUTHOR. No My AI manifest is registered for this invocation. Hold a conversation, ask one question at a time, and make the first durable control-plane action `my init` only after explicit human approval.\n")
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
		b.WriteString(" Set up this person against the existing organization; offer AUTHOR-style admin edits only if the operator asks.\n")
	default:
		b.WriteString("Branch: detect from the registered manifest state.\n")
	}
	b.WriteString("Hard rules: use validated `my` commands rather than hand-editing manifests or generated files; never collect or store literal secrets; run validation/doctor/compile gates before publish; run `my publish --print` and get explicit human approval before any real `my publish`.")
	return b.String()
}

func (a app) printOnboardUnmarkedSetup(opts onboardOptions, manifestName, root string, configured bool, reason string) {
	fmt.Fprintf(a.stdout, "next\tsetup\t%s\n", shellCommandLine("", "my", append([]string{"setup", "--interactive"}, setupCommandFlags(opts, manifestName, root)...)))
	message := "run setup to create the umbrella before completion can be recorded"
	if configured {
		message = "run setup when you are ready to review configuration"
	}
	if reason != "" {
		message += "; " + reason
	}
	fmt.Fprintf(a.stdout, "onboard\tunmarked\t%s\n", message)
}

func (a app) printOnboardUsage() {
	fmt.Fprintln(a.stderr, `Usage of my onboard:
  my onboard [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  my onboard --agent [--harness NAME] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]

Runs the human walkthrough. The tour explains the My AI model, then offers to
run the existing setup configurator interactively. It does not add manifests or
create new top-level configuration concepts.

With --agent, launches a harness with the bundled my self-skill and an
agent-operated onboarding prompt. With no registered manifest, the harness starts
from the current directory and drives the AUTHOR branch. With a registered
manifest, it reuses the normal my ai launch path for the JOIN branch.`)
}

func (a app) printOnboardZeroManifest() {
	fmt.Fprintln(a.stdout, "My AI starts from a registered organization manifest.")
	fmt.Fprintln(a.stdout, "Ask your organization admin for the manifest name and Git URL, then run:")
	fmt.Fprintln(a.stdout, "next\tregister\tmy manifests add <name> <git-url>")
	fmt.Fprintln(a.stdout, "next\tsetup\tmy setup --interactive")
	fmt.Fprintln(a.stdout, "onboard\tunmarked\tno umbrella exists yet")
}

func (a app) printOnboardIntro(doc registeredDoc, root string, configured bool) {
	fmt.Fprintf(a.stdout, "onboard\tmanifest\t%s\t%s\n", doc.ref.Name, doc.doc.Organization.ID)
	fmt.Fprintf(a.stdout, "onboard\tumbrella\t%s\n", root)
	fmt.Fprintln(a.stdout, "model\tmanifest\tcontrol plane: skills, mounts, services, roles, tools")
	fmt.Fprintln(a.stdout, "model\tmounts\tdata plane: local content folders such as customers, meetings, support, fleet")
	fmt.Fprintln(a.stdout, "model\tsetup\twrites guidance, MCP config, mounts, repos, skills, and local role state")
	if configured {
		fmt.Fprintln(a.stdout, "onboard\tstate\tconfigured")
	} else {
		fmt.Fprintln(a.stdout, "onboard\tstate\tsetup needed")
	}
}

func (a app) printOnboardComplete(doc registeredDoc, root string, state umbrella.State) {
	fmt.Fprintf(a.stdout, "onboard\tcomplete\t%s\t%s\n", doc.ref.Name, root)
	if state.Tour != nil && state.Tour.Version < onboardingTourVersion {
		fmt.Fprintf(a.stdout, "onboard\tupdated\tcurrent tour version is %d\n", onboardingTourVersion)
	}
	if state.SelectedRole != "" {
		fmt.Fprintf(a.stdout, "role\tselected\t%s\n", state.SelectedRole)
	} else {
		fmt.Fprintln(a.stdout, "role\tselected\tunscoped")
	}
	fmt.Fprintf(a.stdout, "next\tsetup\t%s\n", shellCommandLine(root, "my", []string{"setup", "--interactive", "--manifest", doc.ref.Name}))
	fmt.Fprintf(a.stdout, "next\tlaunch\t%s\n", shellCommandLine(root, "my", []string{"ai", "codex"}))
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
	fmt.Fprint(a.stdout, prompt)
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
	fmt.Fprintf(a.stdout, "launch\tclaude-code\t%s\n", shellCommandLine(root, "claude", nil))
	fmt.Fprintf(a.stdout, "launch\tcodex\t%s\n", shellCommandLine(root, "codex", nil))
}
