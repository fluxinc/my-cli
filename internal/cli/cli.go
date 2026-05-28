// Package cli implements the flux command-line surface.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxinc/flux/internal/bundle"
	"github.com/fluxinc/flux/internal/guidance"
	"github.com/fluxinc/flux/internal/harness"
	"github.com/fluxinc/flux/internal/manifest"
	"github.com/fluxinc/flux/internal/meetings"
	"github.com/fluxinc/flux/internal/skills"
	"github.com/fluxinc/flux/internal/umbrella"
	"github.com/fluxinc/flux/internal/workspace"
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
		fmt.Fprintf(a.stderr, "flux: %v\n", err)
		return 1
	}
	return 0
}

type app struct {
	stdout      io.Writer
	stderr      io.Writer
	lookPath    func(string) (string, error)
	execHarness func(path string, args []string, dir string) error
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

	switch args[1] {
	case "-h", "--help", "help":
		a.printUsage()
		return nil
	case "-v", "--version", "version":
		return a.runVersion(args[2:])
	case "skills":
		return a.runSkills(args[2:])
	case "onboard":
		return a.runOnboard(args[2:])
	case "root":
		return a.runRoot(args[2:])
	case "launch":
		return a.runLaunch(args[2:])
	case "manifest":
		return a.runManifest(args[2:])
	case "workspace":
		return a.runWorkspace(args[2:])
	case "mount":
		return a.runMount(args[2:])
	case "tools":
		return a.runTools(args[2:])
	case "doctor":
		return a.runDoctor(args[2:])
	case "meetings":
		return a.runMeetings(args[2:])
	case "customers":
		return a.runCustomers(args[2:])
	case "catalog":
		return a.runCatalog(args[2:])
	default:
		return fmt.Errorf("unknown command %q", args[1])
	}
}

func (a app) printUsage() {
	fmt.Fprintln(a.stdout, `flux installs and manages manifest-backed AI workspace tooling.

Usage:
  flux onboard [harness...] | --all [--print] [--copy] [--link] [--force] [--manifest NAME] [--home DIR] [--umbrella DIR]
  flux root [--product ID] [--manifest NAME] [--home DIR] [--umbrella DIR]
  flux launch [--product ID] [--onboard] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [harness] [-- harness args...]
  flux skills install [harness...] | --all [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
  flux skills uninstall <harness...> | --all [--print] [--force] [--source DIR] [--manifest NAME]
  flux skills list [--json] [--source DIR] [--manifest NAME] [--home DIR]
  flux manifest add <name> <git-url>
  flux manifest list
  flux manifest sync <name...> | --all [--print]
  flux manifest validate <name|path>
  flux mount list [--manifest NAME]
  flux mount add <kind:id|id> [--manifest NAME]
  flux mount sync <mount...> | --all [--manifest NAME] [--print]
  flux workspace list [--manifest NAME]
  flux workspace sync <workspace...> | --all [--manifest NAME] [--print]
  flux tools info <name>
  flux meetings list
  flux meetings search <text>
  flux meetings get <id|path>
  flux meetings add <slug>
  flux customers list
  flux catalog list products
  flux doctor
  flux version`)
}

func (a app) runRoot(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var productID string
	fs := newFlagSet("flux root", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "use a registered manifest when no umbrella is found")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.StringVar(&productID, "product", "", "print this product's path under the umbrella")
	fs.Usage = func() {
		a.printRootUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
		"product":  true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("root does not accept positional arguments")
	}
	root, err := resolveFluxRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	target := root
	if productID != "" {
		target = umbrella.ProductPath(root, productID)
	}
	fmt.Fprintln(a.stdout, target)
	return nil
}

func (a app) printRootUsage() {
	fmt.Fprintln(a.stderr, `Usage of flux root:
  flux root [--product ID] [--manifest NAME] [--home DIR] [--umbrella DIR]

Examples:
  cd "$(flux root)" && claude
  cd "$(flux root --product sample-product)" && codex

Options:`)
}

type launchCommandOpts struct {
	home         string
	manifestName string
	umbrellaRoot string
	productID    string
	onboard      bool
	printOnly    bool
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
	root, err := resolveFluxRoot(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return err
	}
	targetDir := root
	if opts.productID != "" {
		targetDir = umbrella.ProductPath(root, opts.productID)
	}
	line := shellCommandLine(targetDir, commandName, harnessArgs)
	if opts.printOnly {
		fmt.Fprintln(a.stdout, line)
		return nil
	}

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
		if err := a.runOnboard(onboardArgsForLaunch(opts.home, doc.ref.Name, root)); err != nil {
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

	binary, err := a.lookupPath(commandName)
	if err != nil {
		fmt.Fprintf(a.stderr, "%s not found on PATH; run:\n%s\n", commandName, line)
		return errAlreadyPrinted
	}
	return a.runHarness(binary, harnessArgs, targetDir)
}

func (a app) printLaunchUsage() {
	fmt.Fprintln(a.stderr, `Usage of flux launch:
  flux launch [--product ID] [--onboard] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [harness] [-- harness args...]

Harness defaults to claude-code. Harness flags go after the harness name.

Examples:
  flux launch claude-code
  flux launch codex --model gpt-5
  flux launch --product sample-product codex
  flux launch --print codex

Options:
  --home DIR        override home directory
  --manifest NAME   use a registered manifest when no umbrella is found
  --umbrella DIR    override umbrella root
  --product ID      run from products/<id> under the umbrella
  --onboard         reconcile the umbrella first if guidance is stale or missing
  --print           print the resolved launch command without checking or execing`)
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
				return opts, "", nil, false, fmt.Errorf("usage: flux launch [harness]")
			}
			return opts, args[i+1], args[i+2:], false, nil
		case arg == "--onboard":
			opts.onboard = true
		case arg == "--print":
			opts.printOnly = true
		case arg == "--home" || arg == "--manifest" || arg == "--umbrella" || arg == "--product":
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
		case strings.HasPrefix(arg, "--product="):
			opts.productID = strings.TrimPrefix(arg, "--product=")
		case isFlagArg(arg):
			return opts, "", nil, false, fmt.Errorf("unknown flux launch flag %q; put harness flags after the harness name", arg)
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
	case "product":
		opts.productID = value
	}
}

func resolveFluxRoot(home, manifestName, explicit string) (string, error) {
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

func onboardArgsForLaunch(home, manifestName, root string) []string {
	args := []string{"--manifest", manifestName, "--umbrella", root}
	if home != "" {
		args = append(args, "--home", home)
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
  flux meetings list [--manifest NAME] [--workspace ID] [--since DATE] [--customer ID] [--partner ID] [--product ID] [--json]
  flux meetings search <text> [--manifest NAME] [--workspace ID] [--customer ID] [--partner ID] [--product ID] [--json]
  flux meetings get <id|path> [--manifest NAME] [--workspace ID] [--json]
  flux meetings add <slug> [--manifest NAME] [--workspace ID] [--date DATE] [--title TEXT] [--customer ID] [--attendees NAME] [--partner ID] [--product ID] [--source-id ID] [--print] [--json]

Meeting commands use local markdown files under workspace meetings/ directories.`)
}

func (a app) runMeetingsList(args []string) error {
	opts, rest, err := parseMeetingReadOpts("flux meetings list", a.stderr, args)
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
	opts, rest, err := parseMeetingReadOpts("flux meetings search", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: flux meetings search <text>")
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
	fs := newFlagSet("flux meetings get", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: flux meetings get <id|path>")
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
	fs := newFlagSet("flux meetings add", a.stderr)
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
		return fmt.Errorf("usage: flux meetings add <slug>")
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

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		cleaned := strings.TrimSpace(part)
		if cleaned != "" {
			*f = append(*f, cleaned)
		}
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

func meetingRoots(home, manifestName, workspaceID, umbrellaRoot string) ([]meetings.Root, error) {
	if umbrellaRoot != "" {
		return umbrellaMeetingRoots(home, workspaceID, umbrellaRoot)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return umbrellaMeetingRoots(home, workspaceID, root)
	}
	if roots, ok, err := configuredUmbrellaMeetingRoots(home, manifestName, workspaceID); ok || err != nil {
		return roots, err
	}
	if manifestName == "" {
		return nil, noUmbrellaError("no flux umbrella found; run flux onboard or pass --umbrella", "run flux onboard or pass --umbrella <path>")
	}
	entries, err := workspace.List(home, manifestName)
	if err != nil {
		return nil, err
	}
	var roots []meetings.Root
	for _, entry := range entries {
		if workspaceID != "" && entry.ID != workspaceID {
			continue
		}
		roots = append(roots, meetings.Root{
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

func configuredUmbrellaMeetingRoots(home, manifestName, workspaceID string) ([]meetings.Root, bool, error) {
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
		roots, err := umbrellaMeetingRoots(home, workspaceID, candidates[0].root)
		return roots, true, err
	}
	if len(candidates) > 1 {
		return nil, true, fmt.Errorf("multiple flux umbrellas configured; pass --manifest or --umbrella")
	}
	if manifestName == "" && len(configured) == 1 {
		return nil, true, noUmbrellaError(
			fmt.Sprintf("no flux umbrella found; configured umbrella is %s", configured[0].root),
			fmt.Sprintf("run flux onboard --manifest %s or pass --umbrella %s", configured[0].ref, configured[0].root),
		)
	}
	return nil, false, nil
}

func umbrellaMeetingRoots(home, workspaceID, umbrellaRoot string) ([]meetings.Root, error) {
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return nil, err
	}
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, fmt.Errorf("read umbrella state: %w", err)
	}
	var roots []meetings.Root
	for _, mount := range state.Mounts {
		if workspaceID != "" && mount.ID != workspaceID {
			continue
		}
		if mount.Status != "synced" {
			continue
		}
		if mount.Kind != "handbook" && mount.Kind != "meetings" {
			continue
		}
		roots = append(roots, meetings.Root{
			Manifest:  mount.SourceRef,
			Workspace: mount.ID,
			Path:      umbrella.MountPath(root, mount.ID),
		})
	}
	if len(roots) == 0 {
		if workspaceID != "" {
			return nil, fmt.Errorf("workspace %q is not mounted in umbrella %s", workspaceID, root)
		}
		return nil, fmt.Errorf("no meeting mounts synced in umbrella %s", root)
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

func runQMDMeetingSearch(roots []meetings.Root, query string, filter meetings.Filter) ([]meetings.Meeting, bool) {
	qmd, err := exec.LookPath("qmd")
	if err != nil {
		return nil, false
	}
	index, err := qmdMeetingIndex(roots, filter)
	if err != nil || len(index) == 0 {
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
	var found []meetings.Meeting
	seen := map[string]bool{}
	for _, result := range results {
		meeting, ok := matchQMDMeeting(index, result.File)
		if !ok || seen[meeting.Path] {
			continue
		}
		if snippet := cleanQMDSnippet(result.Snippet); snippet != "" {
			meeting.Snippet = snippet
		}
		found = append(found, meeting)
		seen[meeting.Path] = true
	}
	return found, true
}

func qmdMeetingIndex(roots []meetings.Root, filter meetings.Filter) (map[string]meetings.Meeting, error) {
	found, err := meetings.List(roots, filter)
	if err != nil {
		return nil, err
	}
	index := map[string]meetings.Meeting{}
	for _, meeting := range found {
		for _, key := range qmdMeetingKeys(meeting) {
			if _, exists := index[key]; !exists {
				index[key] = meeting
			}
		}
	}
	return index, nil
}

func qmdMeetingKeys(meeting meetings.Meeting) []string {
	var keys []string
	add := func(value string) {
		value = strings.ToLower(filepath.ToSlash(value))
		if value != "" {
			keys = append(keys, value)
		}
	}
	add(meeting.Path)
	if abs, err := filepath.Abs(meeting.Path); err == nil {
		add(abs)
	}
	root := filepath.Dir(filepath.Dir(meeting.Path))
	if rel, err := filepath.Rel(root, meeting.Path); err == nil {
		add(rel)
	}
	if rel, err := filepath.Rel(filepath.Dir(root), meeting.Path); err == nil {
		add(rel)
	}
	return keys
}

func matchQMDMeeting(index map[string]meetings.Meeting, file string) (meetings.Meeting, bool) {
	for _, key := range qmdResultKeys(file) {
		if meeting, ok := index[key]; ok {
			return meeting, true
		}
	}
	return meetings.Meeting{}, false
}

func qmdResultKeys(file string) []string {
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
			if part != "meetings" {
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
	out := append([]meetings.Meeting(nil), primary...)
	seen := map[string]bool{}
	for _, meeting := range primary {
		seen[meeting.Path] = true
	}
	for _, meeting := range fallback {
		if !seen[meeting.Path] {
			out = append(out, meeting)
			seen[meeting.Path] = true
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
		fmt.Fprintf(a.stderr, "warning: unknown customer %q; keeping literal value\n", value)
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
	var jsonOut bool
	fs := newFlagSet("flux doctor", a.stderr)
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
		return fmt.Errorf("doctor does not accept positional arguments")
	}
	report := a.buildDoctorReport(home, manifestName, umbrellaRoot)
	if jsonOut {
		return printJSON(a.stdout, report)
	}
	a.printDoctorReport(report)
	return nil
}

type doctorReport struct {
	Umbrella   []doctorItem `json:"umbrella,omitempty"`
	Manifests  []doctorItem `json:"manifests"`
	Workspaces []doctorItem `json:"workspaces"`
	Tools      []doctorItem `json:"tools"`
}

type doctorItem struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"`
	Path    string   `json:"path,omitempty"`
	Message string   `json:"message,omitempty"`
	Details []string `json:"details,omitempty"`
}

func (a app) buildDoctorReport(home, manifestName, umbrellaRoot string) doctorReport {
	var report doctorReport
	if umbrellaRoot != "" {
		root, err := resolveUmbrellaRoot(home, umbrellaRoot)
		if err != nil {
			report.Umbrella = append(report.Umbrella, doctorItem{Name: umbrellaRoot, Status: "error", Message: err.Error()})
		} else {
			report.Umbrella = append(report.Umbrella, doctorUmbrella(home, root)...)
		}
	} else if root, ok := umbrella.FindRoot("."); ok {
		report.Umbrella = append(report.Umbrella, doctorUmbrella(home, root)...)
	}
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
		report.Workspaces = append(report.Workspaces, doctorWorkspaces(home, ref.Name, doc.Workspaces)...)
		report.Tools = append(report.Tools, doctorTools(ref.Name, doc.Tools)...)
	}
	return report
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
			item.Message = "run flux workspace sync " + w.ID + " --manifest " + manifestName
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
	printItems := func(kind string, items []doctorItem) {
		for _, item := range items {
			line := fmt.Sprintf("%s\t%s\t%s", kind, item.Name, item.Status)
			if item.Path != "" {
				line += "\t" + item.Path
			}
			if item.Message != "" {
				line += "\t" + item.Message
			}
			fmt.Fprintln(a.stdout, line)
			for _, detail := range item.Details {
				fmt.Fprintf(a.stdout, "%s\t%s\tdetail\t%s\n", kind, item.Name, detail)
			}
		}
	}
	printItems("manifest", report.Manifests)
	printItems("umbrella", report.Umbrella)
	printItems("workspace", report.Workspaces)
	printItems("tool", report.Tools)
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
  flux tools info <name> [--manifest NAME] [--home DIR] [--json]

Tool entries are operator-facing hints from synced organization manifests.`)
}

type toolInfo struct {
	Manifest string        `json:"manifest"`
	Tool     manifest.Tool `json:"tool"`
}

func (a app) runToolsInfo(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("flux tools info", a.stderr)
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
		return fmt.Errorf("usage: flux tools info <name>")
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
  flux customers list [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]

Customer data comes from catalog/customers.json and mounted handbook customers/registry.md files.`)
}

func (a app) runCustomersList(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var jsonOut bool
	fs := newFlagSet("flux customers list", a.stderr)
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

func (a app) runCatalog(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing catalog subcommand")
	}
	switch args[0] {
	case "list":
		return a.runCatalogList(args[1:])
	case "-h", "--help", "help":
		a.printCatalogUsage()
		return nil
	default:
		return fmt.Errorf("unknown catalog subcommand %q", args[0])
	}
}

func (a app) printCatalogUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  flux catalog list products [--manifest NAME] [--home DIR] [--json]

Catalog data comes from synced organization manifests.`)
}

func (a app) runCatalogList(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("flux catalog list", a.stderr)
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
	if len(rest) != 1 || rest[0] != "products" {
		return fmt.Errorf("usage: flux catalog list products")
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
		printHumanField(a.stdout, "source", product.GitURL)
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
			return nil, fmt.Errorf("manifest %q is not synced; run flux manifest sync %s: %w", ref.Name, ref.Name, err)
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
		return registeredDoc{}, fmt.Errorf("flux requires a registered manifest")
	}
	if len(docs) != 1 {
		return registeredDoc{}, fmt.Errorf("flux requires exactly one manifest; pass --manifest")
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
  flux workspace list [--manifest NAME] [--home DIR] [--json]
  flux workspace sync <workspace...> | --all [--manifest NAME] [--home DIR] [--print] [--json]

Workspace data comes from synced organization manifests. Use manifest:workspace
to disambiguate duplicate workspace IDs across manifests.`)
}

func (a app) runWorkspaceList(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("flux workspace list", a.stderr)
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
	fs := newFlagSet("flux workspace sync", a.stderr)
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

func recordProductResults(root string, ids []string, results []workspace.SyncResult) error {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		state = umbrella.State{SchemaVersion: umbrella.SchemaVersion}
	} else if err != nil {
		return err
	}
	for _, id := range ids {
		state = umbrella.AddSelectedProduct(state, id)
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
			Kind:      "product",
			SourceRef: result.SourceRef,
			Status:    status,
			LastSync:  lastSync,
			LastError: lastError,
		})
	}
	return umbrella.SaveState(root, state)
}

func removeMountsFromState(root string, mountIDs []string, productIDs []string) error {
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
	for _, id := range productIDs {
		state = umbrella.RemoveSelectedProduct(state, id)
		state = umbrella.RemoveMount(state, productMountID(id))
	}
	return umbrella.SaveState(root, state)
}

func addStateMountEntries(home, manifestName, umbrellaRoot string, entries []workspace.Entry) ([]workspace.Entry, error) {
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		if strings.Contains(err.Error(), "no flux umbrella found") {
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
  flux mount list [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]
  flux mount add <kind:id|id> [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
  flux mount sync <mount...> | --all [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
  flux mount remove <mount...> [--home DIR] [--umbrella DIR] [--print] [--force] [--json]

Mounts are detached content sources inside the local organization umbrella.`)
}

func (a app) runMountList(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var jsonOut bool
	fs := newFlagSet("flux mount list", a.stderr)
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
	fs := newFlagSet("flux mount add", a.stderr)
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
		return fmt.Errorf("usage: flux mount add <kind:id|id>")
	}
	kind, id := splitMountRef(rest[0])
	if kind == "product" {
		return a.runMountAddProduct(home, manifestName, umbrellaRoot, id, printOnly, jsonOut)
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

func (a app) runMountAddProduct(home, manifestName, umbrellaRoot, id string, printOnly bool, jsonOut bool) error {
	if !portableMountID(id) {
		return fmt.Errorf("product id %q must be lowercase kebab-case", id)
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	product, ok, err := manifest.FindProduct(home, doc.ref.Name, id)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !ok {
		return a.maybeJSONError(jsonOut, structuredCommandError{
			code:        "unknown_product",
			message:     fmt.Sprintf("product %q is not in catalog for manifest %q", id, doc.ref.Name),
			remediation: "flux catalog list products --manifest " + doc.ref.Name,
		})
	}
	if !printOnly {
		if _, _, err := umbrella.Ensure(root, doc.doc.Organization.ID, doc.ref.Name); err != nil {
			return err
		}
	}
	results := []workspace.SyncResult{workspace.SyncEntry(productEntry(doc, root, product), printOnly, nil)}
	normalizeProductResults(results)
	if !printOnly {
		if err := recordProductResults(root, []string{id}, results); err != nil {
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
	fs := newFlagSet("flux mount sync", a.stderr)
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
	mountRefs, productIDs, err := splitMountSyncRefs(rest, all)
	if err != nil {
		return err
	}
	var results []workspace.SyncResult
	if all || len(mountRefs) != 0 {
		results, err = workspace.SyncMounts(home, manifestName, umbrellaRoot, mountRefs, all, nil, printOnly, nil)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	} else if len(productIDs) == 0 {
		return fmt.Errorf("select a mount ID or pass --all")
	}
	productResults, err := a.syncProductMounts(home, manifestName, umbrellaRoot, productIDs, all, printOnly)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	results = append(results, productResults...)
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

func (a app) syncProductMounts(home, manifestName, umbrellaRoot string, productIDs []string, all bool, printOnly bool) ([]workspace.SyncResult, error) {
	if !all && len(productIDs) == 0 {
		return nil, nil
	}
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		if all && strings.Contains(err.Error(), "no flux umbrella found") {
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
		productIDs = append([]string(nil), state.SelectedProducts...)
	}
	if len(productIDs) == 0 {
		return nil, nil
	}
	entries := make([]workspace.Entry, 0, len(productIDs))
	for _, id := range productIDs {
		entry, err := productEntryFromState(home, ws.ManifestRef, root, state, id)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	results := workspace.SyncEntries(entries, printOnly, nil)
	normalizeProductResults(results)
	if !printOnly {
		if err := recordProductResults(root, productIDs, results); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (a app) syncSelectedProducts(home string, doc registeredDoc, root string, printOnly bool) ([]workspace.SyncResult, error) {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(state.SelectedProducts) == 0 {
		return nil, nil
	}
	entries := make([]workspace.Entry, 0, len(state.SelectedProducts))
	for _, id := range state.SelectedProducts {
		product, ok, err := manifest.FindProduct(home, doc.ref.Name, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, structuredCommandError{
				code:        "unknown_product",
				message:     fmt.Sprintf("product %q is not in catalog for manifest %q", id, doc.ref.Name),
				remediation: "flux catalog list products --manifest " + doc.ref.Name,
			}
		}
		entries = append(entries, productEntry(doc, root, product))
	}
	results := workspace.SyncEntries(entries, printOnly, nil)
	normalizeProductResults(results)
	if !printOnly {
		if err := recordProductResults(root, state.SelectedProducts, results); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func productEntryFromState(home, manifestName, root string, state umbrella.State, id string) (workspace.Entry, error) {
	mountID := productMountID(id)
	for _, mount := range state.Mounts {
		if mount.ID == mountID && mount.Kind == "product" && mount.SourceRef != "" {
			return workspace.Entry{
				Manifest:     manifestName,
				ID:           mountID,
				Kind:         "product",
				Mode:         "optional",
				GitURL:       mount.SourceRef,
				LocalPath:    umbrella.ProductPath(root, id),
				UmbrellaRoot: root,
				SourceRef:    mount.SourceRef,
			}, nil
		}
	}
	product, ok, err := manifest.FindProduct(home, manifestName, id)
	if err != nil {
		return workspace.Entry{}, err
	}
	if !ok {
		return workspace.Entry{}, structuredCommandError{
			code:        "unknown_product",
			message:     fmt.Sprintf("product %q is not in catalog for manifest %q", id, manifestName),
			remediation: "flux catalog list products --manifest " + manifestName,
		}
	}
	return workspace.Entry{
		Manifest:     manifestName,
		ID:           mountID,
		Kind:         "product",
		Mode:         "optional",
		GitURL:       product.GitURL,
		LocalPath:    umbrella.ProductPath(root, id),
		UmbrellaRoot: root,
		SourceRef:    product.GitURL,
	}, nil
}

func (a app) runMountRemove(args []string) error {
	var home string
	var umbrellaRoot string
	var printOnly bool
	var force bool
	var jsonOut bool
	fs := newFlagSet("flux mount remove", a.stderr)
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
		return fmt.Errorf("usage: flux mount remove <mount...>")
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
	var removedProductIDs []string
	for _, ref := range rest {
		kind, id := splitMountRef(ref)
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
			if kind == "product" {
				removedProductIDs = append(removedProductIDs, id)
			} else {
				removedMountIDs = append(removedMountIDs, id)
			}
		}
		results = append(results, result)
	}
	if !printOnly {
		if err := removeMountsFromState(root, removedMountIDs, removedProductIDs); err != nil {
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
	var productIDs []string
	for _, ref := range refs {
		kind, id := splitMountRef(ref)
		if kind == "product" {
			if !portableMountID(id) {
				return nil, nil, fmt.Errorf("product id %q must be lowercase kebab-case", id)
			}
			productIDs = append(productIDs, id)
			continue
		}
		mountRefs = append(mountRefs, ref)
	}
	return mountRefs, productIDs, nil
}

func productMountID(id string) string {
	return "product:" + id
}

func mountRemoveTarget(root, kind, id string) string {
	if kind == "product" {
		return umbrella.ProductPath(root, id)
	}
	return filepath.Join(root, id)
}

func stateMountPath(root string, mount umbrella.MountStatus) string {
	if mount.Kind == "product" && strings.HasPrefix(mount.ID, "product:") {
		return umbrella.ProductPath(root, strings.TrimPrefix(mount.ID, "product:"))
	}
	return umbrella.MountPath(root, mount.ID)
}

func productEntry(doc registeredDoc, root string, product manifest.Product) workspace.Entry {
	return workspace.Entry{
		Manifest:     doc.ref.Name,
		Organization: doc.doc.Organization.ID,
		ID:           productMountID(product.ID),
		Kind:         "product",
		Mode:         "optional",
		GitURL:       product.GitURL,
		LocalPath:    umbrella.ProductPath(root, product.ID),
		UmbrellaRoot: root,
		SourceRef:    product.GitURL,
	}
}

func normalizeProductResults(results []workspace.SyncResult) {
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
	return "", fmt.Errorf("no flux umbrella found; run flux onboard or pass --umbrella")
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
  flux manifest add <name> <git-url> [--home DIR] [--json]
  flux manifest list [--home DIR] [--json]
  flux manifest sync <name...> | --all [--home DIR] [--print] [--json]
  flux manifest validate <name|path> [--home DIR] [--json]`)
}

func (a app) runManifestAdd(args []string) error {
	var home string
	var jsonOut bool
	fs := newFlagSet("flux manifest add", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return fmt.Errorf("usage: flux manifest add <name> <git-url>")
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
	fs := newFlagSet("flux manifest list", a.stderr)
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
	var all bool
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("flux manifest sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&all, "all", false, "sync every registered manifest")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	results, err := manifest.Sync(home, rest, all, printOnly, nil)
	if err != nil {
		return err
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
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
	}
	if manifestResultsFailed(results) {
		return fmt.Errorf("one or more manifest syncs failed")
	}
	return nil
}

func (a app) runManifestValidate(args []string) error {
	var home string
	var jsonOut bool
	fs := newFlagSet("flux manifest validate", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: flux manifest validate <name|path>")
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
	case "install":
		return a.runSkillsInstall(args[1:])
	case "uninstall":
		return a.runSkillsUninstall(args[1:])
	case "list":
		return a.runSkillsList(args[1:])
	case "sync", "purge":
		return fmt.Errorf("skills %s is not implemented yet", args[0])
	case "-h", "--help", "help":
		a.printSkillsUsage()
		return nil
	default:
		return fmt.Errorf("unknown skills subcommand %q", args[0])
	}
}

func (a app) printSkillsUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  flux skills install [harness...] | --all [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
  flux skills uninstall <harness...> | --all [--print] [--force] [--source DIR] [--manifest NAME]
  flux skills list [--json] [--source DIR] [--manifest NAME] [--home DIR]

Harnesses:
  claude-code, codex, opencode, gemini

With no harnesses, install targets all supported harnesses and silently skips
missing ones. If synced manifests are registered, skills commands use them by
default; --source forces a local skills directory.

Skills install only changes harness skill directories. Run flux onboard to
regenerate workspace guidance such as AGENTS.md.`)
}

func (a app) runOnboard(args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet("flux onboard", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "install into every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.copyMode, "copy", false, "copy skill directories instead of symlinking")
	fs.BoolVar(&opts.linkMode, "link", false, "symlink skill directories")
	fs.BoolVar(&opts.force, "force", false, "replace non-Flux-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
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
		return fmt.Errorf("flux onboard requires a registered manifest")
	}
	if len(docs) != 1 {
		return fmt.Errorf("flux onboard requires exactly one manifest; pass --manifest")
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
	productResults, err := a.syncSelectedProducts(opts.home, doc, root, opts.print)
	if err != nil {
		return err
	}
	results = append(results, productResults...)
	skillResults, err := a.collectSkillInstallResults(opts, hs, false)
	if err != nil {
		return err
	}
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
	return a.runSkillsInstallNamed("flux skills install", args)
}

func (a app) runSkillsInstallNamed(commandName string, args []string) error {
	var opts skillsCommandOpts
	fs := newFlagSet(commandName, a.stderr)
	fs.BoolVar(&opts.all, "all", false, "install into every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.copyMode, "copy", false, "copy skill directories instead of symlinking")
	fs.BoolVar(&opts.linkMode, "link", false, "symlink skill directories")
	fs.BoolVar(&opts.force, "force", false, "replace non-Flux-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
	fs.StringVar(&opts.source, "source", "", "skills source directory")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use skills declared by a synced manifest")
	fs.Usage = func() {
		fmt.Fprintf(a.stderr, `Usage of %s:
  %s [harness...] | --all [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]

Skills install only changes harness skill directories. Run flux onboard to
regenerate workspace guidance such as AGENTS.md.

Options:
`, commandName, commandName)
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"source":   true,
		"home":     true,
		"manifest": true,
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
	fs := newFlagSet("flux skills uninstall", a.stderr)
	fs.BoolVar(&opts.all, "all", false, "uninstall from every supported harness")
	fs.BoolVar(&opts.print, "print", false, "print the planned actions without changing files")
	fs.BoolVar(&opts.force, "force", false, "remove non-Flux-managed targets")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON results")
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

	hs, err := selectedHarnesses(opts.all, rest)
	if err != nil {
		return err
	}
	bundled, sourceRoots, _, err := a.discoverSkills(opts)
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

func (a app) runSkillsList(args []string) error {
	var source string
	var manifestName string
	var home string
	var jsonOut bool
	fs := newFlagSet("flux skills list", a.stderr)
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

	bundled, _, _, err := a.discoverSkills(skillsCommandOpts{source: source, manifestName: manifestName, home: home, quietSource: true})
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
	all          bool
	print        bool
	copyMode     bool
	linkMode     bool
	force        bool
	jsonOut      bool
	source       string
	home         string
	manifestName string
	umbrellaRoot string
	quietSource  bool
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
	if opts.manifestName != "" {
		found, sourceRoots, err := a.discoverManifestSkills(opts.home, opts.manifestName, opts.print, !opts.quietSource)
		return found, sourceRoots, true, err
	}
	if opts.source == "" {
		if found, sourceRoots, ok, err := a.discoverDefaultManifestSkills(opts.home, opts.print, !opts.quietSource); err != nil {
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
	return a.installToolSkills(opts.home, docs, opts.print)
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

func (a app) installToolSkills(home string, docs []registeredDoc, dryRun bool) error {
	needed := manifestToolSkillIDs(docs)
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

func manifestToolSkillIDs(docs []registeredDoc) map[string]bool {
	needed := map[string]bool{}
	for _, doc := range docs {
		for _, skill := range doc.doc.Skills {
			if skill.Source.Type == "tool" && skill.Source.Tool != "" {
				needed[skill.Source.Tool] = true
			}
		}
	}
	return needed
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

func (a app) discoverManifestSkills(home, manifestName string, allowMissingToolSkills, showSource bool) ([]skills.Skill, []string, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, nil, err
	}
	return a.discoverManifestSkillDocs(home, docs, allowMissingToolSkills, showSource)
}

func (a app) discoverDefaultManifestSkills(home string, allowMissingToolSkills, showSource bool) ([]skills.Skill, []string, bool, error) {
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
	found, sourceRoots, err := a.discoverManifestSkillDocs(home, docs, allowMissingToolSkills, showSource)
	if err != nil {
		return nil, nil, false, err
	}
	return found, sourceRoots, true, nil
}

func (a app) discoverManifestSkillDocs(home string, docs []registeredDoc, allowMissingToolSkills, showSource bool) ([]skills.Skill, []string, error) {
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
	if len(manifestToolSkillIDs(docs)) != 0 {
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
	} else if strings.Contains(err.Error(), "no flux umbrella found") {
		payload.Error = "no_umbrella"
		payload.Remediation = "run flux onboard or pass --umbrella <path>"
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
