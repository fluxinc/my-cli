// Package customers implements mount-backed customer identity records.
package customers

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/our-ai/internal/record"
)

// Root is one workspace root that may contain customers/.
type Root = record.Root

// Customer is a parsed customer identity record.
type Customer struct {
	Manifest        string   `json:"manifest,omitempty"`
	Workspace       string   `json:"workspace,omitempty"`
	ID              string   `json:"id"`
	Path            string   `json:"path,omitempty"`
	Name            string   `json:"name,omitempty"`
	Domain          string   `json:"domain,omitempty"`
	DomainConfirmed bool     `json:"domain_confirmed,omitempty"`
	Aliases         []string `json:"aliases,omitempty"`
	Partners        []string `json:"partners,omitempty"`
}

// List returns customer records from all roots.
func List(roots []Root) ([]Customer, error) {
	customers, err := scan(roots)
	if err != nil {
		return nil, err
	}
	customers, err = mergeCustomers(customers)
	if err != nil {
		return nil, err
	}
	sort.Slice(customers, func(i, j int) bool {
		return customers[i].ID < customers[j].ID
	})
	return customers, nil
}

// Find resolves a customer by id, domain, name, or alias.
func Find(customers []Customer, value string) (Customer, bool) {
	value = normalizeLookup(value)
	if value == "" {
		return Customer{}, false
	}
	for _, customer := range customers {
		if normalizeLookup(customer.ID) == value || normalizeLookup(customer.Domain) == value || normalizeLookup(customer.Name) == value {
			return customer, true
		}
		for _, alias := range customer.Aliases {
			if normalizeLookup(alias) == value {
				return customer, true
			}
		}
	}
	return Customer{}, false
}

func scan(roots []Root) ([]Customer, error) {
	return record.Scan(roots, "customers", func(root Root, path string) (Customer, error) {
		return parseCustomer(root, path)
	})
}

func parseCustomer(root Root, path string) (Customer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Customer{}, err
	}
	frontmatter, _ := record.SplitFrontmatter(data)
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	customer := Customer{
		Manifest:        root.Manifest,
		Workspace:       root.Workspace,
		ID:              record.First(record.FirstValue(frontmatter, "id"), stem),
		Path:            path,
		Name:            record.First(record.FirstValue(frontmatter, "name"), record.FirstValue(frontmatter, "title")),
		Domain:          record.FirstValue(frontmatter, "domain"),
		DomainConfirmed: record.BoolValue(record.FirstValue(frontmatter, "domain_confirmed")),
		Aliases:         record.Values(frontmatter, "aliases"),
		Partners:        record.FirstValues(frontmatter, "partners", "partner"),
	}
	if !ValidID(customer.ID) {
		return Customer{}, fmt.Errorf("customer record %s: id %q must be lowercase FQDN-style or kebab-case", path, customer.ID)
	}
	return customer, nil
}

func mergeCustomers(in []Customer) ([]Customer, error) {
	var out []Customer
	seenID := map[string]int{}
	seenLookup := map[string]string{}
	for _, customer := range in {
		for _, value := range append([]string{customer.Domain}, customer.Aliases...) {
			normalized := normalizeLookup(value)
			if normalized == "" {
				continue
			}
			if existing := seenLookup[normalized]; existing != "" && existing != customer.ID {
				return nil, fmt.Errorf("customer alias/domain %q is used by both %q and %q", value, existing, customer.ID)
			}
			seenLookup[normalized] = customer.ID
		}
		if i, ok := seenID[customer.ID]; ok {
			out[i] = mergeCustomer(out[i], customer)
			continue
		}
		seenID[customer.ID] = len(out)
		out = append(out, customer)
	}
	return out, nil
}

func mergeCustomer(primary, secondary Customer) Customer {
	if primary.Name == "" {
		primary.Name = secondary.Name
	}
	if primary.Domain == "" {
		primary.Domain = secondary.Domain
	}
	primary.DomainConfirmed = primary.DomainConfirmed || secondary.DomainConfirmed
	primary.Aliases = uniqueStrings(append(primary.Aliases, secondary.Aliases...))
	primary.Partners = uniqueStrings(append(primary.Partners, secondary.Partners...))
	return primary
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// ValidID reports whether value is an accepted canonical customer id.
func ValidID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n/\\") {
		return false
	}
	if strings.Contains(value, ".") {
		parts := strings.Split(value, ".")
		for _, part := range parts {
			if !portableID(part) {
				return false
			}
		}
		return true
	}
	return portableID(value)
}

func portableID(value string) bool {
	if value == "" {
		return false
	}
	lastPunct := true
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			lastPunct = false
			continue
		}
		if r == '-' {
			if lastPunct {
				return false
			}
			lastPunct = true
			continue
		}
		return false
	}
	return !lastPunct
}

func normalizeLookup(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
