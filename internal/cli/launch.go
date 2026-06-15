package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/fluxinc/our-ai/internal/guidance"
	"github.com/fluxinc/our-ai/internal/harness"
	"github.com/fluxinc/our-ai/internal/selfskill"
	"github.com/fluxinc/our-ai/internal/skills"
	"github.com/fluxinc/our-ai/internal/umbrella"
	"github.com/fluxinc/our-ai/internal/worksession"
)

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
	home           string
	manifestName   string
	umbrellaRoot   string
	repoID         string
	legacyProduct  string
	sessionID      string
	newSession     bool
	noSession      bool
	onboard        bool
	printOnly      bool
	skillsSelector string
	profileID      string
	noRefresh      bool
	noUpdateCheck  bool
}

func (a app) runLaunch(args []string) error {
	return a.runLaunchWithInitialPrompt(args, "")
}

func (a app) runLaunchWithInitialPrompt(args []string, initialPrompt string) error {
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
	if initialPrompt != "" {
		promptArgs, err := initialPromptArgs(h, initialPrompt)
		if err != nil {
			return err
		}
		harnessArgs = append(harnessArgs, promptArgs...)
	}
	commandName := h.CommandName()
	selector, err := launchSelectorFromOpts(opts)
	if err != nil {
		return err
	}
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
		if err := a.runSetup(setupArgsForLaunch(opts.home, doc.ref.Name, root, opts.noRefresh, opts.noUpdateCheck)); err != nil {
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

	createsNewSession := launchCreatesNewSession(opts)
	var targetDir string
	if !createsNewSession {
		targetDir, err = a.launchTargetDir(opts, root)
		if err != nil {
			return a.maybePrintStructuredCommandError(err)
		}
	}
	binary, err := a.lookupPath(commandName)
	if err != nil {
		a.printLaunchMissingHarness(commandName, targetDir, harnessArgs, createsNewSession)
		return errAlreadyPrinted
	}
	if createsNewSession {
		targetDir, err = a.launchTargetDir(opts, root)
		if err != nil {
			return a.maybePrintStructuredCommandError(err)
		}
	}
	if err := a.ensureLaunchOrgSkills(h, opts, doc, root, targetDir, selector); err != nil {
		return err
	}
	return a.runHarness(binary, harnessArgs, targetDir)
}

func launchCreatesNewSession(opts launchCommandOpts) bool {
	return opts.newSession
}

func validateLaunchSessionOptions(opts launchCommandOpts) error {
	if opts.noSession && opts.sessionID != "" {
		return fmt.Errorf("--session cannot be combined with --no-session")
	}
	if opts.noSession && opts.newSession {
		return fmt.Errorf("--new-session cannot be combined with --no-session")
	}
	if opts.sessionID != "" && opts.newSession {
		return fmt.Errorf("--session cannot be combined with --new-session")
	}
	if opts.legacyProduct != "" {
		return structuredCommandError{
			code:        "product_flag_removed",
			message:     "products are business catalog entries, not checkouts; --product was removed from our ai",
			remediation: "use our ai --repo " + opts.legacyProduct + " (see our repos list)",
		}
	}
	if opts.repoID != "" && (opts.sessionID != "" || opts.newSession) {
		if opts.sessionID != "" {
			return structuredCommandError{
				code:        "repo_requires_no_session",
				message:     "our ai --repo cannot be combined with --session; repo worktrees are not included in work sessions yet",
				remediation: "omit session flags for a base repo launch, or omit --repo to resume the content session",
			}
		}
		return structuredCommandError{
			code:        "repo_requires_no_session",
			message:     "our ai --repo launches the base repo checkout; repo worktrees are not included in work sessions yet",
			remediation: "omit --new-session for a base repo launch, or omit --repo to start a content session",
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

func (a app) printLaunchMissingHarness(commandName, targetDir string, args []string, newSession bool) {
	if newSession {
		fmt.Fprintf(a.stderr, "%s not found on PATH; no work session was created\n", commandName)
		fmt.Fprintf(a.stderr, "install %s, then rerun the same our ai command\n", commandName)
		return
	}
	line := shellCommandLine(targetDir, commandName, args)
	fmt.Fprintf(a.stderr, "%s not found on PATH; run:\n%s\n", commandName, line)
}

func initialPromptArgs(h harness.Harness, prompt string) ([]string, error) {
	if prompt == "" {
		return nil, nil
	}
	args := h.InitialPromptArgs(prompt)
	if len(args) == 0 {
		return nil, fmt.Errorf("harness %s does not support interactive initial prompts", h)
	}
	return args, nil
}

func (a app) launchTargetDir(opts launchCommandOpts, root string) (string, error) {
	if opts.repoID != "" {
		return umbrella.RepoPath(root, opts.repoID), nil
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
	if !opts.noSession && !opts.newSession {
		session, ok, err := currentActiveSession(root)
		if err != nil {
			return "", err
		}
		if ok {
			return session.Path, nil
		}
	}
	if !opts.newSession {
		return root, nil
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
  our ai [--new-session|--session ID|--no-session] [--repo ID] [--skills all|none|ID,...] [--profile ID] [--setup] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check] [harness] [-- harness args...]

Harness defaults to claude-code. By default, harnesses launch from the base
umbrella, or from the current work session when run inside one. Harness flags go
after the harness name.

Examples:
  our ai claude-code
  our ai codex --model gpt-5
  our ai --new-session codex
  our ai --session 2026-06-11-work-ab12 codex
  our ai --repo sample-service codex
  our ai --print codex

Options:
  --home DIR        override home directory
  --manifest NAME   use a registered manifest when no umbrella is found
  --umbrella DIR    override umbrella root
  --new-session     create and launch from a fresh work session
  --session ID      resume an active work session
  --no-session      ignore any current work session and launch from the base umbrella
  --repo ID         run from repos/<id> under the umbrella
  --skills VALUE    select org skills: all, none, or comma-separated manifest skill ids
  --profile ID      select a named manifest launch profile
  --setup           reconcile the umbrella first if guidance is stale or missing
  --no-refresh      skip best-effort auto-refresh
  --no-update-check skip best-effort update notice
  --print           print the resolved launch command without execing; with --new-session this creates the session`)
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
		case arg == "--new-session":
			opts.newSession = true
		case arg == "--no-session":
			opts.noSession = true
		case arg == "--no-refresh":
			opts.noRefresh = true
		case arg == "--no-update-check":
			opts.noUpdateCheck = true
		case arg == "--home" || arg == "--manifest" || arg == "--umbrella" || arg == "--repo" || arg == "--product" || arg == "--session" || arg == "--skills" || arg == "--profile":
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
		case strings.HasPrefix(arg, "--skills="):
			opts.skillsSelector = strings.TrimPrefix(arg, "--skills=")
		case strings.HasPrefix(arg, "--profile="):
			opts.profileID = strings.TrimPrefix(arg, "--profile=")
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
	case "skills":
		opts.skillsSelector = value
	case "profile":
		opts.profileID = value
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

func setupArgsForLaunch(home, manifestName, root string, noRefresh, noUpdateCheck bool) []string {
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
	if dir == "" {
		parts := []string{shellQuote(command)}
		for _, arg := range args {
			parts = append(parts, shellQuote(arg))
		}
		return strings.Join(parts, " ")
	}
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
