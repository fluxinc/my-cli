package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fluxinc/my-cli/internal/guidance"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func (a app) runServices(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing services subcommand")
	}
	switch args[0] {
	case "list":
		return a.runServicesList(args[1:])
	case "get":
		return a.runServicesGet(args[1:])
	case "-h", "--help", "help":
		a.printServicesUsage()
		return nil
	default:
		return fmt.Errorf("unknown services subcommand %q", args[0])
	}
}

func (a app) printServicesUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my services list [--manifest NAME] [--home DIR] [--json]
  my services get <id> [--manifest NAME] [--home DIR] [--json]

Services are the organization's remote surfaces declared in the manifest.
Secret material is always referenced (op://, env://, broker://), never stored.`)
}

func (a app) runServicesList(args []string) error {
	var home, manifestName string
	var jsonOut bool
	fs := newFlagSet("my services list", a.stderr)
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
		return fmt.Errorf("usage: my services list")
	}
	services, err := loadManifestServices(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if jsonOut {
		return printJSON(a.stdout, services)
	}
	for i, service := range services {
		if i != 0 {
			fmt.Fprintln(a.stdout)
		}
		a.printService(service)
	}
	return nil
}

func (a app) runServicesGet(args []string) error {
	var home, manifestName string
	var jsonOut bool
	fs := newFlagSet("my services get", a.stderr)
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
		return fmt.Errorf("usage: my services get <id>")
	}
	services, err := loadManifestServices(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	for _, service := range services {
		if service.ID == rest[0] {
			if jsonOut {
				return printJSON(a.stdout, service)
			}
			a.printService(service)
			return nil
		}
	}
	return a.maybeJSONError(jsonOut, fmt.Errorf("service %q not found; run my services list", rest[0]))
}

func (a app) printService(service manifest.Service) {
	fmt.Fprintf(a.stdout, "%s - %s\n", service.ID, service.Purpose)
	printHumanField(a.stdout, "kind", service.Kind)
	printHumanField(a.stdout, "auth", service.AuthRef)
	if service.DescribeRef != "" {
		printHumanField(a.stdout, "describe", service.DescribeRef)
	}
	if !service.Connection.IsZero() {
		if service.Connection.Command != "" {
			printHumanField(a.stdout, "connection", "command "+service.Connection.Command)
		} else if service.Connection.URL != "" {
			printHumanField(a.stdout, "connection", "url "+service.Connection.URL)
		}
	}
}

func loadManifestServices(home, manifestName string) ([]manifest.Service, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	services := []manifest.Service{}
	for _, doc := range docs {
		services = append(services, doc.doc.Services...)
	}
	return services, nil
}

func visibleServices(doc manifest.Document, selectedRole string) ([]manifest.Service, error) {
	role, err := roleByID(doc, selectedRole)
	if err != nil {
		return nil, err
	}
	if selectedRole == "" {
		return append([]manifest.Service(nil), doc.Services...), nil
	}
	selected := make(map[string]bool, len(role.Services))
	for _, id := range role.Services {
		selected[id] = true
	}
	services := make([]manifest.Service, 0, len(role.Services))
	for _, service := range doc.Services {
		if selected[service.ID] {
			services = append(services, service)
		}
	}
	return services, nil
}

func roleByID(doc manifest.Document, selectedRole string) (manifest.Role, error) {
	if selectedRole == "" {
		return manifest.Role{}, nil
	}
	for _, role := range doc.Roles {
		if role.ID == selectedRole {
			return role, nil
		}
	}
	return manifest.Role{}, fmt.Errorf("role %q not found; run my roles list", selectedRole)
}

func guidanceOptionsForSelectedRole(root string, doc manifest.Document) (guidance.Options, error) {
	selectedRole, err := selectedRoleForRoot(root)
	if err != nil {
		return guidance.Options{}, err
	}
	role, err := roleByID(doc, selectedRole)
	if err != nil {
		return guidance.Options{}, err
	}
	return guidance.Options{RoleGuidancePaths: role.GuidancePaths}, nil
}

func selectedRoleForRoot(root string) (string, error) {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return state.SelectedRole, nil
}

func setupGuidanceOptions(root string, doc manifest.Document, opts skillsCommandOpts) (guidance.Options, error) {
	if opts.role != "" {
		role, err := roleByID(doc, opts.role)
		if err != nil {
			return guidance.Options{}, err
		}
		return guidance.Options{RoleGuidancePaths: role.GuidancePaths}, nil
	}
	return guidanceOptionsForSelectedRole(root, doc)
}

func (a app) runRoles(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing roles subcommand")
	}
	switch args[0] {
	case "list":
		return a.runRolesList(args[1:])
	case "get":
		return a.runRolesGet(args[1:])
	case "-h", "--help", "help":
		a.printRolesUsage()
		return nil
	default:
		return fmt.Errorf("unknown roles subcommand %q", args[0])
	}
}

func (a app) printRolesUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my roles list [--manifest NAME] [--home DIR] [--json]
  my roles get <id> [--manifest NAME] [--home DIR] [--json]

Roles are named loadouts declared in the manifest. A role selects generated
guidance and visible services; it never grants authority or prunes mounts.`)
}

func (a app) runRolesList(args []string) error {
	var home, manifestName string
	var jsonOut bool
	fs := newFlagSet("my roles list", a.stderr)
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
		return fmt.Errorf("usage: my roles list")
	}
	roles, err := loadManifestRoles(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if jsonOut {
		return printJSON(a.stdout, roles)
	}
	for i, role := range roles {
		if i != 0 {
			fmt.Fprintln(a.stdout)
		}
		a.printRole(role)
	}
	return nil
}

func (a app) runRolesGet(args []string) error {
	var home, manifestName string
	var jsonOut bool
	fs := newFlagSet("my roles get", a.stderr)
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
		return fmt.Errorf("usage: my roles get <id>")
	}
	roles, err := loadManifestRoles(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	for _, role := range roles {
		if role.ID == rest[0] {
			if jsonOut {
				return printJSON(a.stdout, role)
			}
			a.printRole(role)
			return nil
		}
	}
	return a.maybeJSONError(jsonOut, fmt.Errorf("role %q not found; run my roles list", rest[0]))
}

func (a app) printRole(role manifest.Role) {
	fmt.Fprintf(a.stdout, "%s - %s\n", role.ID, role.Purpose)
	if len(role.Mounts) != 0 {
		printHumanField(a.stdout, "mounts", strings.Join(role.Mounts, ", "))
	}
	if len(role.Skills) != 0 {
		printHumanField(a.stdout, "skills", strings.Join(role.Skills, ", "))
	}
	if len(role.Tools) != 0 {
		printHumanField(a.stdout, "tools", strings.Join(role.Tools, ", "))
	}
	if len(role.Services) != 0 {
		printHumanField(a.stdout, "services", strings.Join(role.Services, ", "))
	}
	if len(role.GuidancePaths) != 0 {
		printHumanField(a.stdout, "guidance", strings.Join(role.GuidancePaths, ", "))
	}
}

func loadManifestRoles(home, manifestName string) ([]manifest.Role, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	roles := []manifest.Role{}
	for _, doc := range docs {
		roles = append(roles, doc.doc.Roles...)
	}
	return roles, nil
}
