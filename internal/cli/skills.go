package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fluxinc/our-ai/internal/bundle"
	"github.com/fluxinc/our-ai/internal/harness"
	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/selfskill"
	"github.com/fluxinc/our-ai/internal/skills"
	"github.com/fluxinc/our-ai/internal/workspace"
)

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

Manifest skill commands only refresh harness skill directories. Run our setup
to regenerate workspace guidance such as AGENTS.md. Self-skill commands
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
	interactive            bool
	source                 string
	home                   string
	manifestName           string
	umbrellaRoot           string
	role                   string
	roleSet                bool
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
