package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/fluxinc/my-cli/internal/fleet"
	"github.com/fluxinc/my-cli/internal/record"
	"github.com/fluxinc/my-cli/internal/support"
)

func (a app) runFleet(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing fleet subcommand")
	}
	switch args[0] {
	case "list":
		return a.runFleetList(args[1:])
	case "search":
		return a.runFleetSearch(args[1:])
	case "get":
		return a.runFleetGet(args[1:])
	case "add":
		return a.runFleetAdd(args[1:])
	case "set":
		return a.runFleetSet(args[1:])
	case "-h", "--help", "help":
		a.printFleetUsage()
		return nil
	default:
		return fmt.Errorf("unknown fleet subcommand %q", args[0])
	}
}

func (a app) printFleetUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my fleet list [--manifest NAME] [--workspace ID] [--status TEXT] [--customer ID] [--partner ID] [--identifier ID] [--branch NAME] [--where KEY=VALUE] [--json]
  my fleet search <text> [--manifest NAME] [--workspace ID] [--status TEXT] [--customer ID] [--partner ID] [--identifier ID] [--branch NAME] [--where KEY=VALUE] [--json]
  my fleet get <id|identifier|path> [--manifest NAME] [--workspace ID] [--json]
  my fleet add <id> [--manifest NAME] [--workspace ID] [--customer ID] [--partner ID] [--status TEXT] [--device TEXT] [--serial TEXT] [--identifier ID] [--config-repo NAME] [--config-branch NAME] [--deployed-site TEXT] [--ship-to TEXT] [--contact TEXT] [--install-date DATE] [--print] [--json]
  my fleet set <id|identifier> KEY=VALUE... [--manifest NAME] [--workspace ID] [--json]

Fleet commands use local markdown files under workspace fleet/ directories:
one registry record per deployed instance, keyed by a stable id (use the
hostname or node name). get resolves any identifier — a sales order, purchase
order, functional location, or serial from the record's identifiers list —
and reports related support records that share an identifier. set updates
scalar frontmatter fields in place (for example status=live) and preserves
every other line; state history is the record's git history, so publish each
	meaningful transition with my sync --push --message. status vocabulary is
organization-defined. --where filters on any top-level frontmatter field.`)
}

func (a app) runFleetList(args []string) error {
	opts, rest, err := parseFleetReadOpts("my fleet list", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("fleet list does not accept positional arguments")
	}
	filter, err := opts.filter()
	if err != nil {
		return err
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter = a.resolveFleetFilter(opts.home, opts.manifestName, opts.umbrellaRoot, filter)
	found, err := fleet.List(roots, filter)
	if err != nil {
		return err
	}
	return a.printFleet(found, opts.jsonOut)
}

func (a app) runFleetSearch(args []string) error {
	opts, rest, err := parseFleetReadOpts("my fleet search", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my fleet search <text>")
	}
	filter, err := opts.filter()
	if err != nil {
		return err
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter = a.resolveFleetFilter(opts.home, opts.manifestName, opts.umbrellaRoot, filter)
	found, err := fleetSearch(roots, rest[0], filter)
	if err != nil {
		return err
	}
	return a.printFleet(found, opts.jsonOut)
}

func (a app) runFleetGet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my fleet get", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my fleet get <id|identifier|path>")
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	rec, content, err := fleet.Get(roots, rest[0])
	if err != nil {
		return err
	}
	related := a.relatedSupportRecords(opts.home, opts.manifestName, opts.umbrellaRoot, rec)
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record         fleet.Record     `json:"record"`
			Content        string           `json:"content"`
			RelatedSupport []support.Record `json:"related_support,omitempty"`
		}{Record: rec, Content: content, RelatedSupport: related})
	}
	fmt.Fprint(a.stdout, content)
	if len(related) != 0 {
		fmt.Fprintf(a.stdout, "\n# Related support records\n\n")
		for _, sr := range related {
			fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", sr.Date, sr.ID, sr.Title, sr.Path)
		}
	}
	fmt.Fprint(a.stdout, fleetSupportNextStep(rec, related))
	return nil
}

func fleetSupportNextStep(rec fleet.Record, related []support.Record) string {
	var b strings.Builder
	b.WriteString("\n# Support record next step\n\n")
	if len(related) != 0 {
		b.WriteString("Continue a relevant support record above, or create a new dated support record for a distinct incident:\n\n")
	} else {
		b.WriteString("No related support records were found. Create a dated support record before substantive fleet work:\n\n")
	}
	b.WriteString("`")
	b.WriteString(fleetSupportAddCommand(rec))
	b.WriteString("`\n")
	return b.String()
}

func fleetSupportAddCommand(rec fleet.Record) string {
	parts := []string{"my", "support", "add", "<slug>"}
	if rec.Customer != "" {
		parts = append(parts, "--customer", rec.Customer)
	}
	seen := map[string]bool{}
	for _, id := range append([]string{rec.ID}, rec.Identifiers...) {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if seen[key] {
			continue
		}
		seen[key] = true
		parts = append(parts, "--identifier", id)
	}
	for i, part := range parts {
		parts[i] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}

func (a app) runFleetAdd(args []string) error {
	var opts fleetAddOpts
	fs := newFlagSet("my fleet add", a.stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.customer, "customer", "", "canonical customer ID")
	fs.StringVar(&opts.partner, "partner", "", "resale or service partner ID")
	fs.StringVar(&opts.status, "status", "", "workflow status (organization-defined; default new)")
	fs.StringVar(&opts.device, "device", "", "device or instance model")
	fs.StringVar(&opts.serial, "serial", "", "device serial number")
	fs.Var(&opts.identifiers, "identifier", "order, invoice, location, or asset identifier (repeatable)")
	fs.StringVar(&opts.configRepo, "config-repo", "", "configuration repository")
	fs.StringVar(&opts.configBranch, "config-branch", "", "configuration branch")
	fs.StringVar(&opts.deployedSite, "deployed-site", "", "where the instance runs")
	fs.StringVar(&opts.shipTo, "ship-to", "", "where the instance shipped (may differ from deployed site)")
	fs.Var(&opts.contacts, "contact", "site contact (repeatable)")
	fs.StringVar(&opts.installDate, "install-date", "", "install date, YYYY-MM-DD")
	fs.BoolVar(&opts.printOnly, "print", false, "print the scaffold without writing a file")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":          true,
		"manifest":      true,
		"workspace":     true,
		"umbrella":      true,
		"customer":      true,
		"partner":       true,
		"status":        true,
		"device":        true,
		"serial":        true,
		"identifier":    true,
		"config-repo":   true,
		"config-branch": true,
		"deployed-site": true,
		"ship-to":       true,
		"contact":       true,
		"install-date":  true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my fleet add <id>")
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(roots) != 1 {
		return fmt.Errorf("fleet add requires exactly one workspace; pass --manifest and --workspace")
	}
	customer := a.resolveCustomerForWrite(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot, opts.customer)
	rec, content, err := fleet.Add(roots[0], rest[0], fleet.AddOptions{
		Customer:     customer,
		Partner:      opts.partner,
		Status:       opts.status,
		Device:       opts.device,
		Serial:       opts.serial,
		Identifiers:  []string(opts.identifiers),
		ConfigRepo:   opts.configRepo,
		ConfigBranch: opts.configBranch,
		DeployedSite: opts.deployedSite,
		ShipTo:       opts.shipTo,
		Contacts:     []string(opts.contacts),
		InstallDate:  opts.installDate,
		DryRun:       opts.printOnly,
	})
	if err != nil {
		return err
	}
	if !opts.printOnly {
		if err := markRecordIntentToAdd(roots[0], rec.Path); err != nil {
			return err
		}
		warnRecordOutsidePublishPaths(a.stderr, roots[0], rec.Path)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record  fleet.Record `json:"record"`
			Content string       `json:"content,omitempty"`
		}{Record: rec, Content: content})
	}
	if opts.printOnly {
		fmt.Fprintf(a.stdout, "# path: %s\n%s", rec.Path, content)
		return nil
	}
	fmt.Fprintln(a.stdout, rec.Path)
	return nil
}

func (a app) runFleetSet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my fleet set", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: my fleet set <id|identifier> KEY=VALUE...")
	}
	updates := map[string]string{}
	for _, pair := range rest[1:] {
		key, value, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return fmt.Errorf("fleet set arguments must be KEY=VALUE, got %q", pair)
		}
		updates[key] = strings.TrimSpace(value)
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	rec, changes, err := fleet.Set(roots, rest[0], updates)
	if err != nil {
		return err
	}
	syncCommand := ""
	if len(changes) != 0 {
		var parts []string
		for _, change := range changes {
			parts = append(parts, change.Key+"="+change.New)
		}
		syncCommand = fmt.Sprintf("my sync --push --message %s", shellQuote(fmt.Sprintf("Update fleet %s: %s", rec.ID, strings.Join(parts, ", "))))
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record      fleet.Record         `json:"record"`
			Changes     []record.FieldChange `json:"changes"`
			SyncCommand string               `json:"sync_command,omitempty"`
		}{Record: rec, Changes: changes, SyncCommand: syncCommand})
	}
	if len(changes) == 0 {
		fmt.Fprintf(a.stdout, "no changes for %s\n", rec.Path)
		return nil
	}
	fmt.Fprintf(a.stdout, "updated %s\n", rec.Path)
	for _, change := range changes {
		fmt.Fprintf(a.stdout, "  %s: %q -> %q\n", change.Key, change.Old, change.New)
	}
	fmt.Fprintf(a.stdout, "publish this transition with: %s\n", syncCommand)
	return nil
}

// relatedSupportRecords best-effort lists support records sharing an
// identifier with the fleet record. Resolution failures mean no related
// records, never an error: the support noun may not be adopted.
func (a app) relatedSupportRecords(home, manifestName, umbrellaRoot string, rec fleet.Record) []support.Record {
	roots, err := supportRoots(home, manifestName, "", umbrellaRoot)
	if err != nil {
		return nil
	}
	all, err := support.List(roots, support.Filter{})
	if err != nil {
		return nil
	}
	keys := map[string]bool{strings.ToLower(rec.ID): true}
	for _, id := range rec.Identifiers {
		keys[strings.ToLower(id)] = true
	}
	var out []support.Record
	for _, sr := range all {
		for _, id := range sr.Identifiers {
			if keys[strings.ToLower(id)] {
				out = append(out, sr)
				break
			}
		}
	}
	return out
}

// warnUnknownFleetIdentifiers notes identifiers absent from the fleet
// registry. The registry may legitimately lag reality, so this warns and
// never blocks; without a populated registry it stays silent.

// warnUnknownFleetIdentifiers notes identifiers absent from the fleet
// registry. The registry may legitimately lag reality, so this warns and
// never blocks; without a populated registry it stays silent.
func (a app) warnUnknownFleetIdentifiers(opts meetingCommonOpts, identifiers []string) {
	if len(identifiers) == 0 {
		return
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, "", opts.umbrellaRoot)
	if err != nil {
		return
	}
	all, err := fleet.List(roots, fleet.Filter{})
	if err != nil || len(all) == 0 {
		return
	}
	known := map[string]bool{}
	for _, rec := range all {
		known[strings.ToLower(rec.ID)] = true
		for _, id := range rec.Identifiers {
			known[strings.ToLower(id)] = true
		}
	}
	for _, id := range identifiers {
		if !known[strings.ToLower(id)] {
			fmt.Fprintf(a.stderr, "note: identifier %q is not in the fleet registry; it may be new or misspelled\n", id)
		}
	}
}

type fleetReadOpts struct {
	meetingCommonOpts
	status     string
	customer   string
	partner    string
	identifier string
	branch     string
	where      stringListFlag
}

type fleetAddOpts struct {
	meetingCommonOpts
	customer     string
	partner      string
	status       string
	device       string
	serial       string
	identifiers  stringListFlag
	configRepo   string
	configBranch string
	deployedSite string
	shipTo       string
	contacts     stringListFlag
	installDate  string
	printOnly    bool
}

func parseFleetReadOpts(name string, stderr io.Writer, args []string) (fleetReadOpts, []string, error) {
	var opts fleetReadOpts
	fs := newFlagSet(name, stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.status, "status", "", "workflow status")
	fs.StringVar(&opts.customer, "customer", "", "customer ID")
	fs.StringVar(&opts.partner, "partner", "", "partner ID")
	fs.StringVar(&opts.identifier, "identifier", "", "order, invoice, location, or asset identifier")
	fs.StringVar(&opts.branch, "branch", "", "configuration branch")
	fs.Var(&opts.where, "where", "KEY=VALUE frontmatter filter (repeatable)")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":       true,
		"manifest":   true,
		"workspace":  true,
		"umbrella":   true,
		"status":     true,
		"customer":   true,
		"partner":    true,
		"identifier": true,
		"branch":     true,
		"where":      true,
	})
	return opts, rest, err
}

func (opts fleetReadOpts) filter() (fleet.Filter, error) {
	filter := fleet.Filter{
		Status:     opts.status,
		Customer:   opts.customer,
		Partner:    opts.partner,
		Identifier: opts.identifier,
		Branch:     opts.branch,
	}
	for _, pair := range opts.where {
		key, value, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return fleet.Filter{}, fmt.Errorf("--where must be KEY=VALUE, got %q", pair)
		}
		if filter.Where == nil {
			filter.Where = map[string]string{}
		}
		filter.Where[key] = strings.TrimSpace(value)
	}
	return filter, nil
}

func fleetRoots(home, manifestName, workspaceID, umbrellaRoot string) ([]fleet.Root, error) {
	return contentRoots(home, manifestName, workspaceID, umbrellaRoot, "fleet", []string{"handbook", "fleet"})
}

// contentRoots resolves the workspace roots for one content noun. The noun
// only changes which mount kinds participate and how empty results read.

var fleetSearch = defaultFleetSearch

var qmdFleetSearch = runQMDFleetSearch

func defaultFleetSearch(roots []fleet.Root, query string, filter fleet.Filter) ([]fleet.Record, error) {
	qmdFound, qmdOK := qmdFleetSearch(roots, query, filter)
	fallback, err := fleet.Search(roots, query, filter)
	if err != nil {
		return nil, err
	}
	if qmdOK && len(qmdFound) != 0 {
		return mergeFleetResults(qmdFound, fallback), nil
	}
	return fallback, nil
}

func (a app) printFleet(found []fleet.Record, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, found)
	}
	for _, record := range found {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			record.ID,
			record.Customer,
			record.Partner,
			record.Status,
			record.Device,
			record.ConfigBranch,
			strings.Join(record.Identifiers, ","),
			record.DeployedSite,
			record.Snippet,
			record.Path,
		)
	}
	return nil
}

func runQMDFleetSearch(roots []fleet.Root, query string, filter fleet.Filter) ([]fleet.Record, bool) {
	found, err := fleet.List(roots, filter)
	if err != nil {
		return nil, false
	}
	return runQMDContentSearch(found, query, "fleet",
		func(record fleet.Record) string { return record.Path },
		func(record fleet.Record, snippet string) fleet.Record { record.Snippet = snippet; return record })
}

// runQMDContentSearch runs one qmd query over pre-filtered records of any
// content noun, matching qmd result paths back to records by path keys.

func mergeFleetResults(primary, fallback []fleet.Record) []fleet.Record {
	return mergeContentResults(primary, fallback, func(record fleet.Record) string { return record.Path })
}

func (a app) resolveFleetFilter(home, manifestName, umbrellaRoot string, filter fleet.Filter) fleet.Filter {
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
