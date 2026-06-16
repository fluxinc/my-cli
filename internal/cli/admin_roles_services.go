package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
)

type adminRoleResult struct {
	Action       string        `json:"action"`
	ID           string        `json:"id"`
	ManifestPath string        `json:"manifest_path"`
	Role         manifest.Role `json:"role,omitempty"`
	Message      string        `json:"message,omitempty"`
	NextCommands []string      `json:"next_commands,omitempty"`
}

type adminRoleOpts struct {
	manifestDir string
	purpose     optionalStringFlag
	guidance    stringListFlag
	mounts      stringListFlag
	skills      stringListFlag
	tools       stringListFlag
	services    stringListFlag

	guidanceSet bool
	mountsSet   bool
	skillsSet   bool
	toolsSet    bool
	servicesSet bool

	clearGuidance bool
	clearMounts   bool
	clearSkills   bool
	clearTools    bool
	clearServices bool
	force         bool
	jsonOut       bool
}

func (a app) runAdminRoles(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin roles subcommand")
	}
	switch args[0] {
	case "add":
		return a.runAdminRolesAdd(args[1:])
	case "edit":
		return a.runAdminRolesEdit(args[1:])
	case "remove":
		return a.runAdminRolesRemove(args[1:])
	case "list", "get":
		return adminOperationalReadError("roles", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin roles subcommand %q", args[0])
	}
}

func (a app) runAdminRolesAdd(args []string) error {
	opts, rest, err := parseAdminRoleOpts("my admin roles add", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" || !opts.purpose.set {
		return fmt.Errorf("usage: my admin roles add <id> --manifest-dir DIR --purpose TEXT")
	}
	result, err := a.adminRolesAdd(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminRoleResult(result, opts.jsonOut)
}

func (a app) runAdminRolesEdit(args []string) error {
	opts, rest, err := parseAdminRoleOpts("my admin roles edit", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" {
		return fmt.Errorf("usage: my admin roles edit <id> --manifest-dir DIR")
	}
	result, err := a.adminRolesEdit(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminRoleResult(result, opts.jsonOut)
}

func (a app) runAdminRolesRemove(args []string) error {
	var manifestDir string
	var force bool
	var jsonOut bool
	fs := newFlagSet("my admin roles remove", a.stderr)
	fs.StringVar(&manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.BoolVar(&force, "force", false, "allow dirty checkout")
	fs.BoolVar(&jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{"manifest-dir": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 || manifestDir == "" {
		return fmt.Errorf("usage: my admin roles remove <id> --manifest-dir DIR")
	}
	result, err := a.adminRolesRemove(rest[0], manifestDir, force)
	if err != nil {
		return err
	}
	return a.printAdminRoleResult(result, jsonOut)
}

func parseAdminRoleOpts(name string, stderr io.Writer, args []string) (adminRoleOpts, []string, error) {
	var opts adminRoleOpts
	fs := newFlagSet(name, stderr)
	fs.StringVar(&opts.manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.Var(&opts.purpose, "purpose", "role purpose")
	fs.Var(&opts.guidance, "guidance", "manifest-relative role guidance path (repeatable)")
	fs.Var(&opts.mounts, "mount", "selected mount id (repeatable)")
	fs.Var(&opts.skills, "skill", "selected skill id namespace:name (repeatable)")
	fs.Var(&opts.tools, "tool", "selected tool id (repeatable)")
	fs.Var(&opts.services, "service", "selected service id (repeatable)")
	fs.BoolVar(&opts.clearGuidance, "clear-guidance", false, "clear role guidance paths")
	fs.BoolVar(&opts.clearMounts, "clear-mounts", false, "clear role mount selections")
	fs.BoolVar(&opts.clearSkills, "clear-skills", false, "clear role skill selections")
	fs.BoolVar(&opts.clearTools, "clear-tools", false, "clear role tool selections")
	fs.BoolVar(&opts.clearServices, "clear-services", false, "clear role service selections")
	fs.BoolVar(&opts.force, "force", false, "allow dirty checkout or replace an existing declaration")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"manifest-dir": true,
		"purpose":      true,
		"guidance":     true,
		"mount":        true,
		"skill":        true,
		"tool":         true,
		"service":      true,
	})
	if err != nil {
		return opts, rest, err
	}
	opts.guidanceSet = flagWasSet(fs, "guidance")
	opts.mountsSet = flagWasSet(fs, "mount")
	opts.skillsSet = flagWasSet(fs, "skill")
	opts.toolsSet = flagWasSet(fs, "tool")
	opts.servicesSet = flagWasSet(fs, "service")
	for _, conflict := range []struct {
		clear bool
		set   bool
		name  string
		flag  string
	}{
		{opts.clearGuidance, opts.guidanceSet, "--clear-guidance", "--guidance"},
		{opts.clearMounts, opts.mountsSet, "--clear-mounts", "--mount"},
		{opts.clearSkills, opts.skillsSet, "--clear-skills", "--skill"},
		{opts.clearTools, opts.toolsSet, "--clear-tools", "--tool"},
		{opts.clearServices, opts.servicesSet, "--clear-services", "--service"},
	} {
		if conflict.clear && conflict.set {
			return opts, rest, fmt.Errorf("%s cannot be combined with %s", conflict.name, conflict.flag)
		}
	}
	return opts, rest, nil
}

func (a app) adminRolesAdd(id string, opts adminRoleOpts) (adminRoleResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminRoleResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminRoleResult{}, err
	}
	id = strings.TrimSpace(id)
	if !portableKebab(id) {
		return adminRoleResult{}, fmt.Errorf("role id %q must be lowercase kebab-case", id)
	}
	idx := roleIndex(doc.Roles, id)
	if idx != -1 && !opts.force {
		return adminRoleResult{}, fmt.Errorf("role %q already exists; re-run with --force to replace it", id)
	}
	role := manifest.Role{ID: id}
	applyAdminRoleOpts(&role, opts)
	if idx == -1 {
		doc.Roles = append(doc.Roles, role)
	} else {
		doc.Roles[idx] = role
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminRoleResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminRoleResult{}, err
	}
	action := "added"
	message := "added role"
	if idx != -1 {
		action = "replaced"
		message = "replaced role"
	}
	return adminRoleResult{Action: action, ID: role.ID, ManifestPath: manifestPath, Role: role, Message: message, NextCommands: adminNextCommands(root)}, nil
}

func (a app) adminRolesEdit(id string, opts adminRoleOpts) (adminRoleResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminRoleResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminRoleResult{}, err
	}
	idx := roleIndex(doc.Roles, strings.TrimSpace(id))
	if idx == -1 {
		return adminRoleResult{}, fmt.Errorf("role %q is not declared by the manifest", id)
	}
	role := doc.Roles[idx]
	applyAdminRoleOpts(&role, opts)
	doc.Roles[idx] = role
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminRoleResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminRoleResult{}, err
	}
	return adminRoleResult{Action: "updated", ID: role.ID, ManifestPath: manifestPath, Role: role, Message: "updated role", NextCommands: adminNextCommands(root)}, nil
}

func (a app) adminRolesRemove(id, manifestDir string, force bool) (adminRoleResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminRoleResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminRoleResult{}, err
	}
	idx := roleIndex(doc.Roles, strings.TrimSpace(id))
	if idx == -1 {
		return adminRoleResult{}, fmt.Errorf("role %q is not declared by the manifest", id)
	}
	removed := doc.Roles[idx]
	doc.Roles = append(doc.Roles[:idx], doc.Roles[idx+1:]...)
	if len(doc.Roles) == 0 {
		doc.Roles = nil
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminRoleResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminRoleResult{}, err
	}
	return adminRoleResult{Action: "removed", ID: removed.ID, ManifestPath: manifestPath, Role: removed, Message: "removed role", NextCommands: adminNextCommands(root)}, nil
}

func applyAdminRoleOpts(role *manifest.Role, opts adminRoleOpts) {
	if opts.purpose.set {
		role.Purpose = opts.purpose.value
	}
	if opts.clearGuidance {
		role.GuidancePaths = nil
	} else if opts.guidanceSet {
		role.GuidancePaths = []string(opts.guidance)
	}
	if opts.clearMounts {
		role.Mounts = nil
	} else if opts.mountsSet {
		role.Mounts = []string(opts.mounts)
	}
	if opts.clearSkills {
		role.Skills = nil
	} else if opts.skillsSet {
		role.Skills = []string(opts.skills)
	}
	if opts.clearTools {
		role.Tools = nil
	} else if opts.toolsSet {
		role.Tools = []string(opts.tools)
	}
	if opts.clearServices {
		role.Services = nil
	} else if opts.servicesSet {
		role.Services = []string(opts.services)
	}
}

func (a app) printAdminRoleResult(result adminRoleResult, jsonOut bool) error {
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

func roleIndex(roles []manifest.Role, id string) int {
	for i, role := range roles {
		if role.ID == id {
			return i
		}
	}
	return -1
}

type adminServiceResult struct {
	Action       string           `json:"action"`
	ID           string           `json:"id"`
	ManifestPath string           `json:"manifest_path"`
	Service      manifest.Service `json:"service,omitempty"`
	PrunedRoles  []string         `json:"pruned_roles,omitempty"`
	Message      string           `json:"message,omitempty"`
	NextCommands []string         `json:"next_commands,omitempty"`
}

type adminServiceOpts struct {
	manifestDir       string
	kind              optionalStringFlag
	purpose           optionalStringFlag
	authRef           optionalStringFlag
	describeRef       optionalStringFlag
	connectionType    optionalStringFlag
	connectionCommand optionalStringFlag
	connectionURL     optionalStringFlag
	connectionArgs    stringListFlag
	connectionEnv     keyValueListFlag
	connectionHeaders keyValueListFlag

	connectionArgsSet    bool
	connectionEnvSet     bool
	connectionHeadersSet bool

	clearDescribeRef bool
	clearConnection  bool
	pruneRoles       bool
	force            bool
	jsonOut          bool
}

type keyValue struct {
	Key   string
	Value string
}

type keyValueListFlag []keyValue

func (f *keyValueListFlag) String() string {
	parts := make([]string, 0, len(*f))
	for _, item := range *f {
		parts = append(parts, item.Key+"="+item.Value)
	}
	return strings.Join(parts, ",")
}

func (f *keyValueListFlag) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("expected KEY=VALUE")
	}
	key = strings.TrimSpace(key)
	val = strings.TrimSpace(val)
	if key == "" || val == "" {
		return fmt.Errorf("expected non-empty KEY=VALUE")
	}
	*f = append(*f, keyValue{Key: key, Value: val})
	return nil
}

func (a app) runAdminServices(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin services subcommand")
	}
	switch args[0] {
	case "add":
		return a.runAdminServicesAdd(args[1:])
	case "edit":
		return a.runAdminServicesEdit(args[1:])
	case "remove":
		return a.runAdminServicesRemove(args[1:])
	case "list", "get":
		return adminOperationalReadError("services", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin services subcommand %q", args[0])
	}
}

func (a app) runAdminServicesAdd(args []string) error {
	opts, rest, err := parseAdminServiceOpts("my admin services add", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" || !opts.kind.set || !opts.purpose.set || !opts.authRef.set {
		return fmt.Errorf("usage: my admin services add <id> --manifest-dir DIR --kind http|mcp --purpose TEXT --auth-ref REF")
	}
	result, err := a.adminServicesAdd(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminServiceResult(result, opts.jsonOut)
}

func (a app) runAdminServicesEdit(args []string) error {
	opts, rest, err := parseAdminServiceOpts("my admin services edit", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || opts.manifestDir == "" {
		return fmt.Errorf("usage: my admin services edit <id> --manifest-dir DIR")
	}
	result, err := a.adminServicesEdit(rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminServiceResult(result, opts.jsonOut)
}

func (a app) runAdminServicesRemove(args []string) error {
	var manifestDir string
	var pruneRoles bool
	var force bool
	var jsonOut bool
	fs := newFlagSet("my admin services remove", a.stderr)
	fs.StringVar(&manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.BoolVar(&pruneRoles, "prune-roles", false, "remove this service from role selections")
	fs.BoolVar(&force, "force", false, "allow dirty checkout")
	fs.BoolVar(&jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{"manifest-dir": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 || manifestDir == "" {
		return fmt.Errorf("usage: my admin services remove <id> --manifest-dir DIR")
	}
	result, err := a.adminServicesRemove(rest[0], manifestDir, pruneRoles, force)
	if err != nil {
		return err
	}
	return a.printAdminServiceResult(result, jsonOut)
}

func parseAdminServiceOpts(name string, stderr io.Writer, args []string) (adminServiceOpts, []string, error) {
	var opts adminServiceOpts
	fs := newFlagSet(name, stderr)
	fs.StringVar(&opts.manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.Var(&opts.kind, "kind", "service kind: http or mcp")
	fs.Var(&opts.purpose, "purpose", "service purpose")
	fs.Var(&opts.authRef, "auth-ref", "service auth reference: none, env://NAME, op://..., or broker://...")
	fs.Var(&opts.describeRef, "describe-ref", "http(s) URL or manifest-relative descriptor path")
	fs.Var(&opts.connectionType, "connection-type", "inline MCP connection type")
	fs.Var(&opts.connectionCommand, "connection-command", "inline MCP command")
	fs.Var(&opts.connectionURL, "connection-url", "inline MCP http(s) URL")
	fs.Var(&opts.connectionArgs, "connection-arg", "inline MCP command argument (repeatable)")
	fs.Var(&opts.connectionEnv, "connection-env", "inline MCP env KEY=REF (repeatable, REF must be ${VAR})")
	fs.Var(&opts.connectionHeaders, "connection-header", "inline MCP header KEY=VALUE (repeatable, VALUE must include ${VAR})")
	fs.BoolVar(&opts.clearDescribeRef, "clear-describe-ref", false, "clear describe_ref")
	fs.BoolVar(&opts.clearConnection, "clear-connection", false, "clear inline connection")
	fs.BoolVar(&opts.force, "force", false, "allow dirty checkout or replace an existing declaration")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"manifest-dir":       true,
		"kind":               true,
		"purpose":            true,
		"auth-ref":           true,
		"describe-ref":       true,
		"connection-type":    true,
		"connection-command": true,
		"connection-url":     true,
		"connection-arg":     true,
		"connection-env":     true,
		"connection-header":  true,
	})
	if err != nil {
		return opts, rest, err
	}
	opts.connectionArgsSet = flagWasSet(fs, "connection-arg")
	opts.connectionEnvSet = flagWasSet(fs, "connection-env")
	opts.connectionHeadersSet = flagWasSet(fs, "connection-header")
	if opts.clearDescribeRef && opts.describeRef.set {
		return opts, rest, fmt.Errorf("--clear-describe-ref cannot be combined with --describe-ref")
	}
	if opts.connectionCommand.set && opts.connectionURL.set {
		return opts, rest, fmt.Errorf("--connection-command cannot be combined with --connection-url")
	}
	if opts.clearConnection && opts.hasConnectionUpdate() {
		return opts, rest, fmt.Errorf("--clear-connection cannot be combined with connection flags")
	}
	if opts.kind.set && opts.kind.value != "http" && opts.kind.value != "mcp" {
		return opts, rest, fmt.Errorf("--kind must be http or mcp")
	}
	for _, item := range opts.connectionEnv {
		if !validConnectionEnvValue(item.Value) {
			return opts, rest, fmt.Errorf("--connection-env %s value must be ${VAR}", item.Key)
		}
	}
	for _, item := range opts.connectionHeaders {
		if !validConnectionHeaderValue(item.Value) {
			return opts, rest, fmt.Errorf("--connection-header %s value must include ${VAR}", item.Key)
		}
	}
	return opts, rest, nil
}

func (o adminServiceOpts) hasConnectionUpdate() bool {
	return o.connectionType.set ||
		o.connectionCommand.set ||
		o.connectionURL.set ||
		o.connectionArgsSet ||
		o.connectionEnvSet ||
		o.connectionHeadersSet
}

func (a app) adminServicesAdd(id string, opts adminServiceOpts) (adminServiceResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminServiceResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminServiceResult{}, err
	}
	id = strings.TrimSpace(id)
	if !portableKebab(id) {
		return adminServiceResult{}, fmt.Errorf("service id %q must be lowercase kebab-case", id)
	}
	idx := serviceIndex(doc.Services, id)
	if idx != -1 && !opts.force {
		return adminServiceResult{}, fmt.Errorf("service %q already exists; re-run with --force to replace it", id)
	}
	service := manifest.Service{ID: id}
	applyAdminServiceOpts(&service, opts)
	if err := validateAdminServiceConnection(service); err != nil {
		return adminServiceResult{}, err
	}
	if idx == -1 {
		doc.Services = append(doc.Services, service)
	} else {
		doc.Services[idx] = service
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminServiceResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminServiceResult{}, err
	}
	action := "added"
	message := "added service"
	if idx != -1 {
		action = "replaced"
		message = "replaced service"
	}
	return adminServiceResult{Action: action, ID: service.ID, ManifestPath: manifestPath, Service: service, Message: message, NextCommands: adminNextCommands(root)}, nil
}

func (a app) adminServicesEdit(id string, opts adminServiceOpts) (adminServiceResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminServiceResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminServiceResult{}, err
	}
	idx := serviceIndex(doc.Services, strings.TrimSpace(id))
	if idx == -1 {
		return adminServiceResult{}, fmt.Errorf("service %q is not declared by the manifest", id)
	}
	service := doc.Services[idx]
	applyAdminServiceOpts(&service, opts)
	if err := validateAdminServiceConnection(service); err != nil {
		return adminServiceResult{}, err
	}
	doc.Services[idx] = service
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminServiceResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminServiceResult{}, err
	}
	return adminServiceResult{Action: "updated", ID: service.ID, ManifestPath: manifestPath, Service: service, Message: "updated service", NextCommands: adminNextCommands(root)}, nil
}

func (a app) adminServicesRemove(id, manifestDir string, pruneRoles, force bool) (adminServiceResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminServiceResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminServiceResult{}, err
	}
	id = strings.TrimSpace(id)
	idx := serviceIndex(doc.Services, id)
	if idx == -1 {
		return adminServiceResult{}, fmt.Errorf("service %q is not declared by the manifest", id)
	}
	referencingRoles := rolesSelectingService(doc.Roles, id)
	if len(referencingRoles) != 0 && !pruneRoles {
		return adminServiceResult{}, fmt.Errorf("service %q is selected by roles: %s; re-run with --prune-roles to remove those references", id, strings.Join(referencingRoles, ", "))
	}
	removed := doc.Services[idx]
	doc.Services = append(doc.Services[:idx], doc.Services[idx+1:]...)
	if len(doc.Services) == 0 {
		doc.Services = nil
	}
	if pruneRoles {
		pruneServiceFromRoles(doc.Roles, id)
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminServiceResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminServiceResult{}, err
	}
	message := "removed service"
	if len(referencingRoles) != 0 {
		message += "; pruned roles: " + strings.Join(referencingRoles, ", ")
	}
	return adminServiceResult{
		Action:       "removed",
		ID:           removed.ID,
		ManifestPath: manifestPath,
		Service:      removed,
		PrunedRoles:  referencingRoles,
		Message:      message,
		NextCommands: adminNextCommands(root),
	}, nil
}

func applyAdminServiceOpts(service *manifest.Service, opts adminServiceOpts) {
	if opts.kind.set {
		service.Kind = opts.kind.value
	}
	if opts.purpose.set {
		service.Purpose = opts.purpose.value
	}
	if opts.authRef.set {
		service.AuthRef = opts.authRef.value
	}
	if opts.clearDescribeRef {
		service.DescribeRef = ""
	} else if opts.describeRef.set {
		service.DescribeRef = opts.describeRef.value
	}
	if opts.clearConnection {
		service.Connection = manifest.ServiceConnection{}
	} else if opts.hasConnectionUpdate() {
		connection := service.Connection
		if opts.connectionType.set {
			connection.Type = opts.connectionType.value
		}
		if opts.connectionCommand.set {
			connection.Command = opts.connectionCommand.value
			connection.URL = ""
		}
		if opts.connectionURL.set {
			connection.URL = opts.connectionURL.value
			connection.Command = ""
			connection.Args = nil
		}
		if opts.connectionArgsSet {
			connection.Args = []string(opts.connectionArgs)
		}
		if opts.connectionEnvSet {
			connection.Env = keyValueMap(opts.connectionEnv)
		}
		if opts.connectionHeadersSet {
			connection.Headers = keyValueMap(opts.connectionHeaders)
		}
		service.Connection = connection
	}
}

func validateAdminServiceConnection(service manifest.Service) error {
	if !service.Connection.IsZero() && service.Kind != "mcp" {
		return fmt.Errorf("connection flags are only valid for mcp services")
	}
	for key, value := range service.Connection.Env {
		if !validConnectionEnvValue(value) {
			return fmt.Errorf("connection env %s value must be ${VAR}", key)
		}
	}
	for key, value := range service.Connection.Headers {
		if !validConnectionHeaderValue(value) {
			return fmt.Errorf("connection header %s value must include ${VAR}", key)
		}
	}
	return nil
}

func keyValueMap(items []keyValue) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[item.Key] = item.Value
	}
	return out
}

func validConnectionEnvValue(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	name, ok := exactEnvPlaceholder(value)
	return ok && validCLIEnvName(name)
}

func validConnectionHeaderValue(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	return containsValidEnvPlaceholder(value)
}

func exactEnvPlaceholder(value string) (string, bool) {
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	return name, true
}

func containsValidEnvPlaceholder(value string) bool {
	foundValid := false
	rest := value
	for {
		_, after, found := strings.Cut(rest, "${")
		if !found {
			break
		}
		name, after, found := strings.Cut(after, "}")
		if !found || !validCLIEnvName(name) {
			return false
		}
		foundValid = true
		rest = after
	}
	return foundValid
}

func validCLIEnvName(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func (a app) printAdminServiceResult(result adminServiceResult, jsonOut bool) error {
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

func serviceIndex(services []manifest.Service, id string) int {
	for i, service := range services {
		if service.ID == id {
			return i
		}
	}
	return -1
}

func rolesSelectingService(roles []manifest.Role, serviceID string) []string {
	var out []string
	for _, role := range roles {
		if stringInSlice(role.Services, serviceID) {
			out = append(out, role.ID)
		}
	}
	return out
}

func pruneServiceFromRoles(roles []manifest.Role, serviceID string) {
	remove := map[string]bool{serviceID: true}
	for i := range roles {
		roles[i].Services = pruneStrings(roles[i].Services, remove)
		if len(roles[i].Services) == 0 {
			roles[i].Services = nil
		}
	}
}
