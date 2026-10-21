package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
)

// my admin manifest-mounts authors the manifest's mounts[] declarations. It
// is distinct from my admin mounts, which aliases umbrella-local my mounts.

type adminManifestMountResult struct {
	Action       string         `json:"action"`
	ID           string         `json:"id"`
	ManifestPath string         `json:"manifest_path"`
	Mount        manifest.Mount `json:"mount,omitzero"`
	Roles        []string       `json:"roles,omitempty"`
	PrunedRoles  []string       `json:"pruned_roles,omitempty"`
	Message      string         `json:"message,omitempty"`
	NextCommands []string       `json:"next_commands,omitempty"`
}

type adminManifestMountOpts struct {
	manifestDir string
	kind        string
	gitURL      string
	mode        string
	include     stringListFlag
	roles       stringListFlag
	force       bool
	jsonOut     bool
}

const adminManifestMountsAddUsage = "usage: my admin manifest-mounts add <id> --manifest-dir DIR --kind KIND --git-url URL --mode required|default|optional|role [--include PATH] [--role ID] [--force] [--json]"

func (a app) runAdminManifestMounts(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin manifest-mounts subcommand")
	}
	switch args[0] {
	case "add":
		return a.runAdminManifestMountsAdd(args[1:])
	case "remove":
		return a.runAdminManifestMountsRemove(args[1:])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin manifest-mounts subcommand %q", args[0])
	}
}

func (a app) runAdminManifestMountsAdd(args []string) error {
	var opts adminManifestMountOpts
	fs := newFlagSet("my admin manifest-mounts add", a.stderr)
	fs.StringVar(&opts.manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.StringVar(&opts.kind, "kind", "", "mount kind")
	fs.StringVar(&opts.gitURL, "git-url", "", "mount content repository URL")
	fs.StringVar(&opts.mode, "mode", "", "mount mode: required, default, optional, or role")
	fs.Var(&opts.include, "include", "sparse include path (repeatable)")
	fs.Var(&opts.roles, "role", "existing role that selects this mount (repeatable)")
	fs.BoolVar(&opts.force, "force", false, "allow dirty checkout or replace an existing declaration")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"manifest-dir": true,
		"kind":         true,
		"git-url":      true,
		"mode":         true,
		"include":      true,
		"role":         true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" || opts.kind == "" || opts.gitURL == "" || opts.mode == "" {
		return fmt.Errorf(adminManifestMountsAddUsage)
	}
	result, err := a.adminManifestMountsAdd(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminManifestMountResult(result, opts.jsonOut)
}

func (a app) runAdminManifestMountsRemove(args []string) error {
	var manifestDir string
	var pruneRoles bool
	var force bool
	var jsonOut bool
	fs := newFlagSet("my admin manifest-mounts remove", a.stderr)
	fs.StringVar(&manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.BoolVar(&pruneRoles, "prune-roles", false, "remove this mount from role selections in the same write")
	fs.BoolVar(&force, "force", false, "allow dirty checkout")
	fs.BoolVar(&jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{"manifest-dir": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 || manifestDir == "" {
		return fmt.Errorf("usage: my admin manifest-mounts remove <id> --manifest-dir DIR [--prune-roles] [--force] [--json]")
	}
	result, err := a.adminManifestMountsRemove(rest[0], manifestDir, pruneRoles, force)
	if err != nil {
		return err
	}
	return a.printAdminManifestMountResult(result, jsonOut)
}

func (a app) adminManifestMountsAdd(id string, opts adminManifestMountOpts) (adminManifestMountResult, error) {
	doc, manifestPath, root, err := a.loadAuthorizedAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminManifestMountResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminManifestMountResult{}, err
	}
	id = strings.TrimSpace(id)
	if !portableKebab(id) {
		return adminManifestMountResult{}, fmt.Errorf("mount id %q must be lowercase kebab-case", id)
	}
	idx := manifestMountIndex(doc.Mounts, id)
	if idx != -1 && !opts.force {
		return adminManifestMountResult{}, fmt.Errorf("mount %q already exists; re-run with --force to replace it", id)
	}
	mount := manifest.Mount{
		ID:           id,
		Kind:         strings.TrimSpace(opts.kind),
		GitURL:       strings.TrimSpace(opts.gitURL),
		Mode:         strings.TrimSpace(opts.mode),
		IncludePaths: []string(opts.include),
	}
	if idx == -1 {
		doc.Mounts = append(doc.Mounts, mount)
	} else {
		doc.Mounts[idx] = mount
	}
	// --role lets a role-only mount be declared together with its first
	// selecting role, since validation rejects an unselected role mount.
	for _, roleID := range opts.roles {
		ri := roleIndex(doc.Roles, strings.TrimSpace(roleID))
		if ri == -1 {
			return adminManifestMountResult{}, fmt.Errorf("role %q is not declared by the manifest; add it with my admin roles add", roleID)
		}
		if !slices.Contains(doc.Roles[ri].Mounts, id) {
			doc.Roles[ri].Mounts = append(doc.Roles[ri].Mounts, id)
		}
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminManifestMountResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminManifestMountResult{}, err
	}
	action, message := "added", "added mount"
	if idx != -1 {
		action, message = "replaced", "replaced mount"
	}
	return adminManifestMountResult{Action: action, ID: id, ManifestPath: manifestPath, Mount: mount, Roles: []string(opts.roles), Message: message, NextCommands: adminNextCommands(root)}, nil
}

// adminManifestMountsRemove removes a mount declaration. With pruneRoles it
// also drops every role's reference in the same validated write, which is the
// only way to remove a role-only mount (a role cannot deselect it first
// without leaving it dangling).
func (a app) adminManifestMountsRemove(id, manifestDir string, pruneRoles, force bool) (adminManifestMountResult, error) {
	doc, manifestPath, root, err := a.loadAuthorizedAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminManifestMountResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminManifestMountResult{}, err
	}
	id = strings.TrimSpace(id)
	idx := manifestMountIndex(doc.Mounts, id)
	if idx == -1 {
		return adminManifestMountResult{}, fmt.Errorf("mount %q is not declared by the manifest", id)
	}
	var selecting []string
	for _, role := range doc.Roles {
		if slices.Contains(role.Mounts, id) {
			selecting = append(selecting, role.ID)
		}
	}
	if len(selecting) != 0 && !pruneRoles {
		return adminManifestMountResult{}, fmt.Errorf("mount %q is selected by roles: %s; re-run with --prune-roles to remove those references", id, strings.Join(selecting, ", "))
	}
	removed := doc.Mounts[idx]
	doc.Mounts = append(doc.Mounts[:idx], doc.Mounts[idx+1:]...)
	if len(doc.Mounts) == 0 {
		doc.Mounts = nil
	}
	if pruneRoles {
		for i := range doc.Roles {
			doc.Roles[i].Mounts = slices.DeleteFunc(doc.Roles[i].Mounts, func(m string) bool { return m == id })
			if len(doc.Roles[i].Mounts) == 0 {
				doc.Roles[i].Mounts = nil
			}
		}
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminManifestMountResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminManifestMountResult{}, err
	}
	message := "removed mount"
	if len(selecting) != 0 {
		message += "; pruned roles: " + strings.Join(selecting, ", ")
	}
	return adminManifestMountResult{Action: "removed", ID: id, ManifestPath: manifestPath, Mount: removed, PrunedRoles: selecting, Message: message, NextCommands: adminNextCommands(root)}, nil
}

func (a app) printAdminManifestMountResult(result adminManifestMountResult, jsonOut bool) error {
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

func manifestMountIndex(mounts []manifest.Mount, id string) int {
	for i, mount := range mounts {
		if mount.ID == id {
			return i
		}
	}
	return -1
}
