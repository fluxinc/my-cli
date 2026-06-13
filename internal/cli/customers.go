package cli

import (
	"fmt"
	"strings"

	"github.com/fluxinc/our-ai/internal/customers"
)

func (a app) resolveCustomerForWrite(home, manifestName, umbrellaRoot, value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	customer, ok, err := a.findCustomer(home, manifestName, umbrellaRoot, value)
	if err == nil && ok {
		return customer.ID
	}
	found, loadErr := a.loadCustomers(home, manifestName, umbrellaRoot)
	if loadErr == nil && len(found) != 0 {
		fmt.Fprintf(a.stderr, "warning: unknown customer %q; keeping literal value; add a mounted customers/%s.md record if this should be canonical\n", value, customersRecordName(value))
	}
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
	case "-h", "--help", "help":
		a.printCustomersUsage()
		return nil
	default:
		return fmt.Errorf("unknown customers subcommand %q", args[0])
	}
}

func (a app) printCustomersUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  our customers list [--manifest NAME] [--workspace ID] [--home DIR] [--umbrella DIR] [--json]

Customer commands use local markdown files under workspace customers/ directories.`)
}

func (a app) runCustomersList(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("our customers list", a.stderr)
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

func customersRecordName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "-")
	if value == "" {
		return "customer"
	}
	return value
}
