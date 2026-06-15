package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/skills"
)

func (a app) runAdmin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin subcommand")
	}
	switch args[0] {
	case "skills":
		return a.runAdminSkills(args[1:])
	case "setup":
		return a.runSetup(args[1:])
	case "manifests":
		return a.runAdminManifest(args[1:])
	case "mounts":
		return a.runAdminMount(args[1:])
	case "meetings":
		return a.runAdminMeetings(args[1:])
	case "support":
		return a.runAdminSupport(args[1:])
	case "tools":
		return a.runAdminTools(args[1:])
	case "contract":
		return a.runAdminContract(args[1:])
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
  our admin contract add "RULE TEXT" --manifest-dir DIR [--force] [--json]
  our admin contract remove <index|"RULE TEXT"> --manifest-dir DIR [--force] [--json]
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

type adminToolResult struct {
	Action       string        `json:"action"`
	ID           string        `json:"id"`
	ManifestPath string        `json:"manifest_path"`
	Tool         manifest.Tool `json:"tool,omitempty"`
	Message      string        `json:"message,omitempty"`
	NextCommands []string      `json:"next_commands,omitempty"`
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
