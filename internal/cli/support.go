package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/fluxinc/our-ai/internal/support"
)

func (a app) runSupport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing support subcommand")
	}
	switch args[0] {
	case "list":
		return a.runSupportList(args[1:])
	case "search":
		return a.runSupportSearch(args[1:])
	case "get":
		return a.runSupportGet(args[1:])
	case "add":
		return a.runSupportAdd(args[1:])
	case "-h", "--help", "help":
		a.printSupportUsage()
		return nil
	default:
		return fmt.Errorf("unknown support subcommand %q", args[0])
	}
}

func (a app) printSupportUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our support list [--manifest NAME] [--workspace ID] [--since DATE] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
  our support search <text> [--manifest NAME] [--workspace ID] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--feature-candidate] [--json]
  our support get <id|path> [--manifest NAME] [--workspace ID] [--json]
  our support add <slug> [--manifest NAME] [--workspace ID] [--date DATE] [--title TEXT] [--customer ID] [--identifier ID] [--claimed-by MEMBER] [--observed-by MEMBER] [--approved-by MEMBER] [--product ID] [--area TEXT] [--tag TEXT] [--status open|workaround|resolved] [--feature-candidate] [--print] [--json]

Support commands use local markdown files under workspace support/ directories.
Repeat --identifier for each device, order, or asset identifier (for example a
workstation name, equipment ID, functional location, or sales order number) so
records can be linked to the same equipment later. --claimed-by names the org
member who worked the problem, --observed-by (repeatable) anyone else involved,
and --approved-by the member who signed off on the record; agents should leave
approved_by empty unless the operator explicitly approves.`)
}

func (a app) runSupportList(args []string) error {
	opts, rest, err := parseSupportReadOpts("our support list", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("support list does not accept positional arguments")
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter := a.resolveSupportFilter(opts.home, opts.manifestName, opts.umbrellaRoot, opts.filter())
	found, err := support.List(roots, filter)
	if err != nil {
		return err
	}
	return a.printSupport(found, opts.jsonOut)
}

func (a app) runSupportSearch(args []string) error {
	opts, rest, err := parseSupportReadOpts("our support search", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our support search <text>")
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	filter := a.resolveSupportFilter(opts.home, opts.manifestName, opts.umbrellaRoot, opts.filter())
	found, err := supportSearch(roots, rest[0], filter)
	if err != nil {
		return err
	}
	return a.printSupport(found, opts.jsonOut)
}

func (a app) runSupportGet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("our support get", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our support get <id|path>")
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	record, content, err := support.Get(roots, rest[0])
	if err != nil {
		return err
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record  support.Record `json:"record"`
			Content string         `json:"content"`
		}{Record: record, Content: content})
	}
	fmt.Fprint(a.stdout, content)
	return nil
}

func (a app) runSupportAdd(args []string) error {
	var opts supportAddOpts
	fs := newFlagSet("our support add", a.stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.date, "date", "", "support record date, YYYY-MM-DD")
	fs.StringVar(&opts.title, "title", "", "support record title")
	fs.StringVar(&opts.customer, "customer", "", "canonical customer ID")
	fs.Var(&opts.identifiers, "identifier", "device, order, or asset identifier (repeatable)")
	fs.StringVar(&opts.claimedBy, "claimed-by", "", "org member who worked the problem")
	fs.Var(&opts.observedBy, "observed-by", "org member who was involved or observed (repeatable)")
	fs.StringVar(&opts.approvedBy, "approved-by", "", "org member who approved this record")
	fs.StringVar(&opts.product, "product", "", "product ID")
	fs.StringVar(&opts.area, "area", "", "product or problem area")
	fs.Var(&opts.tags, "tag", "support tag (repeatable)")
	fs.StringVar(&opts.status, "status", "", "support status: open, workaround, or resolved")
	fs.BoolVar(&opts.featureCandidate, "feature-candidate", false, "mark as a feature candidate")
	fs.BoolVar(&opts.printOnly, "print", false, "print the scaffold without writing a file")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":        true,
		"manifest":    true,
		"workspace":   true,
		"umbrella":    true,
		"date":        true,
		"title":       true,
		"customer":    true,
		"identifier":  true,
		"claimed-by":  true,
		"observed-by": true,
		"approved-by": true,
		"product":     true,
		"area":        true,
		"tag":         true,
		"status":      true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: our support add <slug>")
	}
	roots, err := supportRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(roots) != 1 {
		return fmt.Errorf("support add requires exactly one workspace; pass --manifest and --workspace")
	}
	customer := a.resolveCustomerForWrite(opts.home, opts.manifestName, opts.umbrellaRoot, opts.customer)
	record, content, err := support.Add(roots[0], rest[0], support.AddOptions{
		Date:             opts.date,
		Title:            opts.title,
		Customer:         customer,
		Identifiers:      []string(opts.identifiers),
		ClaimedBy:        opts.claimedBy,
		ObservedBy:       []string(opts.observedBy),
		ApprovedBy:       opts.approvedBy,
		Product:          opts.product,
		Area:             opts.area,
		Tags:             []string(opts.tags),
		Status:           opts.status,
		FeatureCandidate: opts.featureCandidate,
		DryRun:           opts.printOnly,
	})
	if err != nil {
		return err
	}
	a.warnUnknownFleetIdentifiers(opts.meetingCommonOpts, []string(opts.identifiers))
	if !opts.printOnly {
		if err := markRecordIntentToAdd(roots[0], record.Path); err != nil {
			return err
		}
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record  support.Record `json:"record"`
			Content string         `json:"content,omitempty"`
		}{Record: record, Content: content})
	}
	if opts.printOnly {
		fmt.Fprintf(a.stdout, "# path: %s\n%s", record.Path, content)
		return nil
	}
	fmt.Fprintln(a.stdout, record.Path)
	return nil
}

type supportReadOpts struct {
	meetingCommonOpts
	since            string
	customer         string
	identifier       string
	claimedBy        string
	product          string
	area             string
	tag              string
	featureCandidate bool
}

type supportAddOpts struct {
	meetingCommonOpts
	date             string
	title            string
	customer         string
	identifiers      stringListFlag
	claimedBy        string
	observedBy       stringListFlag
	approvedBy       string
	product          string
	area             string
	tags             stringListFlag
	status           string
	featureCandidate bool
	printOnly        bool
}

func parseSupportReadOpts(name string, stderr io.Writer, args []string) (supportReadOpts, []string, error) {
	var opts supportReadOpts
	fs := newFlagSet(name, stderr)
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.since, "since", "", "minimum support record date, YYYY-MM-DD")
	fs.StringVar(&opts.customer, "customer", "", "customer ID")
	fs.StringVar(&opts.identifier, "identifier", "", "device, order, or asset identifier")
	fs.StringVar(&opts.claimedBy, "claimed-by", "", "org member who worked the problem")
	fs.StringVar(&opts.product, "product", "", "product ID")
	fs.StringVar(&opts.area, "area", "", "product or problem area")
	fs.StringVar(&opts.tag, "tag", "", "support tag")
	fs.BoolVar(&opts.featureCandidate, "feature-candidate", false, "show only feature candidates")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":       true,
		"manifest":   true,
		"workspace":  true,
		"umbrella":   true,
		"since":      true,
		"customer":   true,
		"identifier": true,
		"claimed-by": true,
		"product":    true,
		"area":       true,
		"tag":        true,
	})
	return opts, rest, err
}

func (opts supportReadOpts) filter() support.Filter {
	return support.Filter{
		Since:       opts.since,
		Customer:    opts.customer,
		Identifier:  opts.identifier,
		ClaimedBy:   opts.claimedBy,
		Product:     opts.product,
		Area:        opts.area,
		Tag:         opts.tag,
		FeatureOnly: opts.featureCandidate,
	}
}

func supportRoots(home, manifestName, workspaceID, umbrellaRoot string) ([]support.Root, error) {
	return contentRoots(home, manifestName, workspaceID, umbrellaRoot, "support", []string{"handbook", "support"})
}

func (a app) printSupport(found []support.Record, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, found)
	}
	for _, record := range found {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%t\t%s\t%s\n",
			record.Date,
			record.ID,
			record.Title,
			record.Customer,
			strings.Join(record.Identifiers, ","),
			record.ClaimedBy,
			record.Product,
			record.Area,
			record.Status,
			strings.Join(record.Tags, ","),
			record.FeatureCandidate,
			record.Snippet,
			record.Path,
		)
	}
	return nil
}

var supportSearch = defaultSupportSearch

var qmdSupportSearch = runQMDSupportSearch

func defaultSupportSearch(roots []support.Root, query string, filter support.Filter) ([]support.Record, error) {
	qmdFound, qmdOK := qmdSupportSearch(roots, query, filter)
	fallback, err := support.Search(roots, query, filter)
	if err != nil {
		return nil, err
	}
	if qmdOK && len(qmdFound) != 0 {
		return mergeSupportResults(qmdFound, fallback), nil
	}
	return fallback, nil
}

func runQMDSupportSearch(roots []support.Root, query string, filter support.Filter) ([]support.Record, bool) {
	found, err := support.List(roots, filter)
	if err != nil {
		return nil, false
	}
	return runQMDContentSearch(found, query, "support",
		func(record support.Record) string { return record.Path },
		func(record support.Record, snippet string) support.Record { record.Snippet = snippet; return record })
}

func mergeSupportResults(primary, fallback []support.Record) []support.Record {
	return mergeContentResults(primary, fallback, func(record support.Record) string { return record.Path })
}

func (a app) resolveSupportFilter(home, manifestName, umbrellaRoot string, filter support.Filter) support.Filter {
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
