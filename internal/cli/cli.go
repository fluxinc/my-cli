// Package cli implements the my command-line surface.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/bundle"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/selfskill"
	"github.com/fluxinc/my-cli/internal/selfupdate"
)

// Run executes the CLI and returns a process exit code.
func Run(args []string) int {
	a := app{
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		stdin:       bufio.NewReader(os.Stdin),
		interactive: isTerminal(os.Stdin) && isTerminal(os.Stdout),
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
		fmt.Fprintf(a.stderr, "my: %v\n", err)
		return 1
	}
	return 0
}

// isTerminal reports whether f is attached to a terminal.
func isTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return isTerminalFD(f.Fd())
}

type app struct {
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	// interactive reports whether stdin/stdout are a TTY. Zero value (false)
	// keeps tests on the deterministic, non-launching path by default.
	interactive          bool
	lookPath             func(string) (string, error)
	execHarness          func(path string, args []string, dir string) error
	updateSource         selfupdate.Source
	updateNow            func() time.Time
	updateCurrentVersion string
	updateTargetPath     string
	// publishRunner overrides external gh invocations during my publish.
	publishRunner manifest.Runner
}

func (a app) runStartupMaintenance(args []string) {
	if !shouldAutoSyncSelfSkill(args) {
		return
	}
	_, _ = selfskill.SyncExisting(selfskill.Options{Link: true, SkipMissing: true})
}

func shouldAutoSyncSelfSkill(args []string) bool {
	if os.Getenv("MYCLI_DISABLE_SELF_SKILL_SYNC") != "" {
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
	for _, key := range []string{"CLAUDECODE", "CODEX_THREAD_ID", "OPENCODE"} {
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
	case "compile":
		return a.runCompile(args[2:])
	case "skills":
		return a.runSkills(args[2:])
	case "setup":
		return a.runSetup(args[2:])
	case "onboarding":
		return a.runOnboard(args[2:])
	case "onboard":
		return a.runOnboard(args[2:])
	case "root":
		return a.runRoot(args[2:])
	case "ai":
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
	case "contract":
		return a.runContract(args[2:])
	case "admin":
		return a.runAdmin(args[2:])
	default:
		return fmt.Errorf("unknown command %q", args[1])
	}
}

func (a app) printUsage() {
	fmt.Fprintln(a.stdout, `my installs and manages manifest-backed AI workspace tooling.

Usage:
  my setup [harness...] | --all [--interactive] [--print] [--copy] [--link] [--force] [--role ROLE] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  my onboarding [--agent|--no-agent] [--harness NAME] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  my root [--repo ID] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check]
  my ai [--new-session|--session ID|--no-session] [--repo ID] [--setup] [--print] [--manifest NAME] [--home DIR] [--umbrella DIR] [--no-refresh] [--no-update-check] [harness] [-- harness args...]
  my update [--check] [--version X.Y.Z] [--json] [--yes]
  my init <org-id> [--name NAME] [--path DIR] [--umbrella DIR] [--home DIR] [--setup] [--json]
  my publish [--manifest NAME] [--home DIR] [--print] [--json]
  my compile --role ROLE [--manifest NAME] [--home DIR]
  my sync [--backend auto|gnit|builtin] [--publish auto|never|direct|pr] [--scope all|local|content|manifest|repos] [--no-derived] [--print] [--json] [--manifest NAME] [--home DIR] [--umbrella DIR]
  my skills self install|uninstall|status ...
  my skills install [harness...] | --all [--skill ID_OR_SLUG] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
  my skills uninstall <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
  my skills sync [harness...] | --all [--skill ID_OR_SLUG] [--no-prune] [--print] [--copy] [--link] [--force] [--source DIR] [--manifest NAME]
  my skills purge <harness...> | --all [--skill ID_OR_SLUG] [--print] [--force] [--source DIR] [--manifest NAME]
  my skills list [--json] [--source DIR] [--manifest NAME] [--home DIR]
  my skills show <id|slug> [--json] [--source DIR] [--manifest NAME] [--home DIR]
  my skills status [--skill ID_OR_SLUG] [--json] [--source DIR] [--manifest NAME] [--home DIR]
  my admin skills add <skill-dir> --id namespace:name --manifest-dir DIR [--install-slug SLUG] [--keep-original|--remove-original] [--force] [--json]
  my admin skills remove <id|slug> --manifest-dir DIR [--delete-source] [--prune-related] [--prune-orphans] [--force] [--json]
  my admin setup ...                      (alias of my setup)
  my admin manifests add|sync|validate ...   (alias of my manifests ...)
  my admin mounts add|remove|sync ...        (alias of my mounts ...)
  my admin meetings add ...                 (alias of my meetings add)
  my admin support add ...                  (alias of my support add)
  my admin tools add|edit|remove ...        (edit manifest tool hints)
  my admin roles add|edit|remove ...        (edit manifest role loadouts)
  my admin services add|edit|remove ...     (edit manifest service surfaces)
  my admin contract add|remove ...          (edit manifest contract rules)
  my manifests add <name> <git-url>
  my manifests list
  my manifests sync <name...> | --all [--umbrella DIR] [--no-derived] [--print]
  my manifests validate <name|path>
  my mounts list [--manifest NAME]
  my mounts add <kind:id|id> [--manifest NAME]
  my mounts sync <mount...> | --all [--manifest NAME] [--print]
  my mounts remove <mount...> [--print] [--force]
  my workspaces list [--manifest NAME]
  my workspaces sync <workspace...> | --all [--manifest NAME] [--print]
  my tools list
  my tools info <name>
  my meetings list
  my meetings search <text>
  my meetings get <id|path>
  my meetings add <slug>
  my support list
  my support search <text>
  my support get <id|path>
  my support add <slug>
  my fleet list
  my fleet search <text>
  my fleet get <id|identifier|path>
  my fleet add <id>
  my fleet set <id> KEY=VALUE...
  my record adopt <path>
  my work start [--slug SLUG] [--json]
  my work status [--all] [--json]
  my work list [--all] [--json]
  my work resume [session-id] [--json]
  my work finish [session-id] --land|--publish|--discard [--message TEXT] [--json]
  my customers list
  my products list
  my repos list [--json]
  my repos add <id>
  my repos remove <id> [--force]
  my services list [--manifest NAME] [--json]
  my services get <id> [--manifest NAME] [--json]
  my roles list [--manifest NAME] [--json]
  my roles get <id> [--manifest NAME] [--json]
  my contract list [--manifest NAME] [--json]
  my doctor [--no-fetch] [--fix] [--json]
  my version`)
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
	fs := newFlagSet("my update", a.stderr)
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
		CurrentVersion: a.currentMyVersion(),
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
	fmt.Fprintln(a.stderr, `Usage of my update:
  my update [--check] [--version X.Y.Z] [--json] [--yes]

Options:`)
}

func (a app) currentMyVersion() string {
	if a.updateCurrentVersion != "" {
		return a.updateCurrentVersion
	}
	return bundle.Version()
}

func (a app) maybeUpdateNotice(home string, noUpdateCheck bool) {
	if noUpdateCheck || os.Getenv("MYCLI_NO_UPDATE_CHECK") != "" {
		return
	}
	notice, err := selfupdate.CheckNotice(context.Background(), selfupdate.NoticeOptions{
		CurrentVersion: a.currentMyVersion(),
		Home:           home,
		Source:         a.updateSource,
		TTL:            selfupdate.UpdateCheckTTLFromEnv(),
		Now:            a.updateNow,
	})
	if err != nil || !notice.UpdateAvailable {
		return
	}
	fmt.Fprintf(a.stderr, "a newer my (v%s) is available; run `my update`\n", notice.LatestVersion)
}
