package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/umbrella"
)

func (a app) resolveCustomerForWrite(home, manifestName, umbrellaRoot, value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	customer, ok, err := a.findCustomer(home, manifestName, umbrellaRoot, value)
	if err == nil && ok {
		return customer.ID
	}
	customers, loadErr := a.loadCustomers(home, manifestName, umbrellaRoot)
	if loadErr == nil && len(customers) != 0 {
		fmt.Fprintf(a.stderr, "warning: unknown customer %q; keeping literal value; add it with our admin customers add %s --manifest-dir <checkout>\n", value, shellQuote(value))
	}
	return value
}

func (a app) findCustomer(home, manifestName, umbrellaRoot, value string) (manifest.Customer, bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return manifest.Customer{}, false, nil
	}
	customers, err := a.loadCustomers(home, manifestName, umbrellaRoot)
	if err != nil {
		return manifest.Customer{}, false, err
	}
	for _, customer := range customers {
		if strings.ToLower(customer.ID) == value || strings.ToLower(customer.Domain) == value || strings.ToLower(customer.Name) == value {
			return customer, true, nil
		}
		for _, alias := range customer.Aliases {
			if strings.ToLower(alias) == value {
				return customer, true, nil
			}
		}
	}
	return manifest.Customer{}, false, nil
}

func (a app) loadCustomers(home, manifestName, umbrellaRoot string) ([]manifest.Customer, error) {
	catalogCustomers, err := manifest.LoadCustomers(home, manifestName)
	if err != nil {
		return nil, err
	}
	root, ok, err := customerUmbrellaRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		return nil, err
	}
	if !ok {
		return catalogCustomers, nil
	}
	registryCustomers, err := handbookRegistryCustomers(root)
	if err != nil {
		return nil, err
	}
	return mergeCustomers(catalogCustomers, registryCustomers), nil
}

func customerUmbrellaRoot(home, manifestName, umbrellaRoot string) (string, bool, error) {
	if umbrellaRoot != "" {
		root, err := resolveUmbrellaRoot(home, umbrellaRoot)
		return root, err == nil, err
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return root, true, nil
	}
	roots, ok, err := configuredUmbrellaRoots(home, manifestName)
	if err != nil || !ok || len(roots) == 0 {
		return "", false, err
	}
	if len(roots) > 1 {
		return "", false, nil
	}
	return roots[0], true, nil
}

func configuredUmbrellaRoots(home, manifestName string) ([]string, bool, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil || len(docs) == 0 {
		return nil, false, nil
	}
	var roots []string
	for _, doc := range docs {
		root, err := umbrella.ResolveRoot(home, "", "", doc.doc)
		if err != nil {
			return nil, true, err
		}
		if _, err := umbrella.LoadWorkspace(root); err == nil {
			roots = append(roots, root)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, true, err
		}
	}
	return roots, true, nil
}

func handbookRegistryCustomers(root string) ([]manifest.Customer, error) {
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, nil
	}
	var out []manifest.Customer
	for _, mount := range state.Mounts {
		if mount.Status != "synced" || mount.Kind != "handbook" {
			continue
		}
		path := filepath.Join(umbrella.MountPath(root, mount.ID), "customers", "registry.md")
		customers, err := parseCustomerRegistry(path)
		if err != nil {
			return nil, err
		}
		out = append(out, customers...)
	}
	return out, nil
}

func parseCustomerRegistry(path string) ([]manifest.Customer, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []manifest.Customer
	confirmed := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "## registry") && strings.Contains(lower, "confirmed") {
			confirmed = !strings.Contains(lower, "unconfirmed")
			continue
		}
		if !strings.HasPrefix(trimmed, "|") || strings.Contains(trimmed, "---") {
			continue
		}
		cells := markdownTableCells(trimmed)
		if len(cells) < 4 || strings.Contains(strings.ToLower(cells[0]), "canonical id") || strings.Contains(strings.ToLower(cells[0]), "placeholder id") {
			continue
		}
		id := cleanMarkdownCell(cells[0])
		if id == "" {
			continue
		}
		customer := manifest.Customer{
			ID:              id,
			Name:            cleanMarkdownCell(cells[1]),
			Partners:        splitCustomerPartners(cleanMarkdownCell(cells[2])),
			DomainConfirmed: confirmed,
			Aliases:         registryAliases(id, cells[3]),
		}
		if confirmed && strings.Contains(id, ".") {
			customer.Domain = id
		}
		out = append(out, customer)
	}
	return out, nil
}

func markdownTableCells(line string) []string {
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func cleanMarkdownCell(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "`", "")
	value = strings.ReplaceAll(value, "**", "")
	if value == "\u2014" {
		return ""
	}
	return value
}

func splitCustomerPartners(value string) []string {
	if value == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" && part != "\u2014" {
			out = append(out, part)
		}
	}
	return out
}

func registryAliases(id, notes string) []string {
	var out []string
	for _, part := range strings.Split(notes, "`") {
		part = strings.TrimSpace(part)
		if part == "" || part == id {
			continue
		}
		if strings.ContainsAny(part, " /,()") {
			continue
		}
		out = append(out, part)
	}
	return uniqueStrings(out)
}

func mergeCustomers(primary, secondary []manifest.Customer) []manifest.Customer {
	out := append([]manifest.Customer(nil), primary...)
	seen := map[string]int{}
	for i, customer := range out {
		seen[customer.ID] = i
	}
	for _, customer := range secondary {
		if i, ok := seen[customer.ID]; ok {
			out[i].Aliases = uniqueStrings(append(out[i].Aliases, customer.Aliases...))
			out[i].Partners = uniqueStrings(append(out[i].Partners, customer.Partners...))
			if out[i].Name == "" {
				out[i].Name = customer.Name
			}
			if out[i].Domain == "" {
				out[i].Domain = customer.Domain
			}
			out[i].DomainConfirmed = out[i].DomainConfirmed || customer.DomainConfirmed
			continue
		}
		seen[customer.ID] = len(out)
		out = append(out, customer)
	}
	return out
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
  our customers list [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]

Customer data comes from catalog/customers.json and mounted handbook customers/registry.md files.`)
}

func (a app) runCustomersList(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var jsonOut bool
	fs := newFlagSet("our customers list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("customers list does not accept positional arguments")
	}
	customers, err := a.loadCustomers(home, manifestName, umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if jsonOut {
		return printJSON(a.stdout, customers)
	}
	for _, customer := range customers {
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
