package cli

import (
	"fmt"
	"strings"

	"github.com/fluxinc/my-cli/internal/customers"
)

func (a app) resolveCustomerForWrite(home, manifestName, workspaceID, umbrellaRoot, value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	found, err := a.loadCustomers(home, manifestName, umbrellaRoot)
	if err != nil {
		return value
	}
	if customer, ok := customers.Find(found, value); ok {
		return customer.ID
	}
	fmt.Fprintf(a.stderr, "warning: unknown customer %q; keeping literal value; run `%s` if this should be canonical\n", value, customerAddCommand(value, home, manifestName, workspaceID, umbrellaRoot))
	return value
}

func (a app) findCustomer(home, manifestName, umbrellaRoot, value string) (customers.Customer, bool, error) {
	found, err := a.loadCustomers(home, manifestName, umbrellaRoot)
	if err != nil {
		return customers.Customer{}, false, err
	}
	customer, ok := customers.Find(found, value)
	return customer, ok, nil
}

func (a app) loadCustomers(home, manifestName, umbrellaRoot string) ([]customers.Customer, error) {
	roots, err := customerRoots(home, manifestName, "", umbrellaRoot)
	if err != nil {
		return nil, err
	}
	return customers.List(roots)
}

func (a app) runCustomers(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing customers subcommand")
	}
	switch args[0] {
	case "list":
		return a.runCustomersList(args[1:])
	case "add":
		return a.runCustomersAdd(args[1:])
	case "-h", "--help", "help":
		a.printCustomersUsage()
		return nil
	default:
		return fmt.Errorf("unknown customers subcommand %q", args[0])
	}
}

func (a app) printCustomersUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my customers list [--manifest NAME] [--workspace ID] [--home DIR] [--umbrella DIR] [--json]
  my customers add <domain|slug> [--manifest NAME] [--workspace ID] [--home DIR] [--umbrella DIR] [--name TEXT] [--domain DOMAIN] [--domain-confirmed] [--alias TEXT] [--partner ID] [--print] [--json]

Customer commands use local markdown files under workspace customers/ directories.`)
}

func (a app) runCustomersList(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my customers list", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("customers list does not accept positional arguments")
	}
	roots, err := customerRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	found, err := customers.List(roots)
	if err != nil {
		return err
	}
	return a.printCustomers(found, opts.jsonOut)
}

func (a app) runCustomersAdd(args []string) error {
	var opts customerAddOpts
	fs := newFlagSet("my customers add", a.stderr)
	fs.Usage = func() {
		fmt.Fprintln(a.stderr, `Usage of my customers add:
  my customers add <domain|slug> [--manifest NAME] [--workspace ID] [--home DIR] [--umbrella DIR] [--name TEXT] [--domain DOMAIN] [--domain-confirmed] [--alias TEXT] [--partner ID] [--print] [--json]`)
		fs.PrintDefaults()
	}
	bindMeetingCommonFlags(fs, &opts.meetingCommonOpts)
	fs.StringVar(&opts.name, "name", "", "customer display name")
	fs.StringVar(&opts.domain, "domain", "", "customer primary domain")
	fs.BoolVar(&opts.domainConfirmed, "domain-confirmed", false, "mark the domain as confirmed")
	fs.Var(&opts.aliases, "alias", "customer alias (repeatable)")
	fs.Var(&opts.partners, "partner", "partner ID (repeatable)")
	fs.BoolVar(&opts.printOnly, "print", false, "print the scaffold without writing a file")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":      true,
		"manifest":  true,
		"workspace": true,
		"umbrella":  true,
		"name":      true,
		"domain":    true,
		"alias":     true,
		"partner":   true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my customers add <domain|slug>")
	}
	roots, err := customerRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(roots) != 1 {
		return fmt.Errorf("customers add requires exactly one workspace; pass --manifest and --workspace")
	}
	customer, content, err := customers.Add(roots[0], rest[0], customers.AddOptions{
		Name:            opts.name,
		Domain:          opts.domain,
		DomainConfirmed: opts.domainConfirmed,
		Aliases:         []string(opts.aliases),
		Partners:        []string(opts.partners),
		DryRun:          opts.printOnly,
	})
	if err != nil {
		return err
	}
	if !opts.printOnly {
		if err := markRecordIntentToAdd(roots[0], customer.Path); err != nil {
			return err
		}
		warnRecordOutsidePublishPaths(a.stderr, roots[0], customer.Path)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Customer customers.Customer `json:"customer"`
			Content  string             `json:"content,omitempty"`
		}{Customer: customer, Content: content})
	}
	if opts.printOnly {
		fmt.Fprintf(a.stdout, "# path: %s\n%s", customer.Path, content)
		return nil
	}
	fmt.Fprintln(a.stdout, customer.Path)
	return nil
}

func customerRoots(home, manifestName, workspaceID, umbrellaRoot string) ([]customers.Root, error) {
	return contentRoots(home, manifestName, workspaceID, umbrellaRoot, "customer", []string{"handbook", "customers"})
}

func (a app) printCustomers(found []customers.Customer, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, found)
	}
	for _, customer := range found {
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

type customerAddOpts struct {
	meetingCommonOpts
	name            string
	domain          string
	domainConfirmed bool
	aliases         stringListFlag
	partners        stringListFlag
	printOnly       bool
}

func customersRecordName(value string) string {
	if value := customers.CleanID(value); value != "" {
		return value
	}
	if strings.TrimSpace(value) == "" {
		return "customer"
	}
	return "customer"
}

func customerAddCommand(value, home, manifestName, workspaceID, umbrellaRoot string) string {
	parts := []string{"my", "customers", "add", customersRecordName(value)}
	if manifestName != "" {
		parts = append(parts, "--manifest", manifestName)
	}
	if workspaceID != "" {
		parts = append(parts, "--workspace", workspaceID)
	}
	if home != "" {
		parts = append(parts, "--home", home)
	}
	if umbrellaRoot != "" {
		parts = append(parts, "--umbrella", umbrellaRoot)
	}
	for i, part := range parts {
		parts[i] = shellQuote(part)
	}
	return strings.Join(parts, " ")
}
