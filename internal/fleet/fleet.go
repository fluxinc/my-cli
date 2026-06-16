// Package fleet implements stdlib markdown fleet registry commands. Unlike
// the journal-shaped meetings and support nouns, fleet records are a
// registry: one file per deployed instance keyed by a stable id, updated in
// place, with state history carried by git rather than by dated records.
package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/record"
)

// Root is one workspace root that may contain fleet/.
type Root = record.Root

// Filter limits fleet registry results.
type Filter struct {
	Status         string
	Customer       string
	CustomerValues []string
	Partner        string
	Identifier     string
	Branch         string
	Where          map[string]string
}

// Record is a parsed fleet registry entry for one deployed instance.
type Record struct {
	Manifest     string   `json:"manifest"`
	Workspace    string   `json:"workspace"`
	ID           string   `json:"id"`
	Path         string   `json:"path"`
	Customer     string   `json:"customer,omitempty"`
	Partner      string   `json:"partner,omitempty"`
	Status       string   `json:"status,omitempty"`
	Device       string   `json:"device,omitempty"`
	Serial       string   `json:"serial,omitempty"`
	Identifiers  []string `json:"identifiers,omitempty"`
	ConfigRepo   string   `json:"config_repo,omitempty"`
	ConfigBranch string   `json:"config_branch,omitempty"`
	DeployedSite string   `json:"deployed_site,omitempty"`
	ShipTo       string   `json:"ship_to,omitempty"`
	Contacts     []string `json:"contacts,omitempty"`
	InstallDate  string   `json:"install_date,omitempty"`
	Source       string   `json:"source,omitempty"`
	Snippet      string   `json:"snippet,omitempty"`

	fields map[string]string
}

// AddOptions controls fleet record scaffold creation.
type AddOptions struct {
	Customer     string
	Partner      string
	Status       string
	Device       string
	Serial       string
	Identifiers  []string
	ConfigRepo   string
	ConfigBranch string
	DeployedSite string
	ShipTo       string
	Contacts     []string
	InstallDate  string
	DryRun       bool
}

// List returns fleet records from all roots, sorted by id.
func List(roots []Root, filter Filter) ([]Record, error) {
	records, err := scan(roots)
	if err != nil {
		return nil, err
	}
	records = applyFilter(records, filter)
	sortRecords(records)
	return records, nil
}

// Search returns fleet records whose markdown contains every query term.
func Search(roots []Root, query string, filter Filter) ([]Record, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search text is required")
	}
	terms := record.ParseTerms(query)
	if len(terms) == 0 {
		return nil, fmt.Errorf("search text is required")
	}
	all, err := scan(roots)
	if err != nil {
		return nil, err
	}
	var out []Record
	scores := map[string]int{}
	for _, rec := range applyFilter(all, filter) {
		data, err := os.ReadFile(rec.Path)
		if err != nil {
			return nil, err
		}
		lowerContent := strings.ToLower(string(data))
		if !record.MatchesAllTerms(lowerContent, terms) {
			continue
		}
		rec.Snippet = record.SnippetBodyFirst(data, terms)
		scores[rec.Path] = record.MatchScore(lowerContent, terms)
		out = append(out, rec)
	}
	sortSearchRecords(out, scores)
	return out, nil
}

// Get resolves a fleet record by id, filename stem, path, or any entry in
// its identifiers list, and returns its content.
func Get(roots []Root, key string) (Record, string, error) {
	rec, err := resolve(roots, key)
	if err != nil {
		return Record{}, "", err
	}
	data, err := os.ReadFile(rec.Path)
	if err != nil {
		return Record{}, "", err
	}
	return rec, string(data), nil
}

func resolve(roots []Root, key string) (Record, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return Record{}, fmt.Errorf("fleet record id, identifier, or path is required")
	}
	if info, err := os.Stat(key); err == nil && !info.IsDir() {
		rec, err := parseRecord(Root{}, key)
		return rec, err
	}
	all, err := scan(roots)
	if err != nil {
		return Record{}, err
	}
	var matches []Record
	for _, rec := range all {
		stem := strings.TrimSuffix(filepath.Base(rec.Path), filepath.Ext(rec.Path))
		if rec.ID == key || stem == key {
			matches = append(matches, rec)
		}
	}
	if len(matches) == 0 {
		for _, rec := range all {
			if record.ContainsValue(rec.Identifiers, key) {
				matches = append(matches, rec)
			}
		}
	}
	if len(matches) == 0 {
		lower := strings.ToLower(key)
		for _, rec := range all {
			for _, identifier := range rec.Identifiers {
				if strings.ToLower(identifier) == lower {
					matches = append(matches, rec)
					break
				}
			}
		}
	}
	if len(matches) == 0 {
		return Record{}, fmt.Errorf("fleet record %q not found", key)
	}
	if len(matches) > 1 {
		var ids []string
		for _, match := range matches {
			ids = append(ids, match.ID)
		}
		return Record{}, fmt.Errorf("fleet record %q is ambiguous (matches %s); pass the record id", key, strings.Join(ids, ", "))
	}
	return matches[0], nil
}

// Add creates a markdown fleet record scaffold in root/fleet.
func Add(root Root, id string, opts AddOptions) (Record, string, error) {
	id = record.CleanSlug(id)
	if id == "" {
		return Record{}, "", fmt.Errorf("fleet record id is required")
	}
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "new"
	}
	path := filepath.Join(root.Path, "fleet", id+".md")
	body := scaffold(id, opts, status)
	rec := Record{
		Manifest:     root.Manifest,
		Workspace:    root.Workspace,
		ID:           id,
		Path:         path,
		Customer:     opts.Customer,
		Partner:      opts.Partner,
		Status:       status,
		Device:       opts.Device,
		Serial:       opts.Serial,
		Identifiers:  append([]string(nil), opts.Identifiers...),
		ConfigRepo:   opts.ConfigRepo,
		ConfigBranch: opts.ConfigBranch,
		DeployedSite: opts.DeployedSite,
		ShipTo:       opts.ShipTo,
		Contacts:     append([]string(nil), opts.Contacts...),
		InstallDate:  opts.InstallDate,
		Source:       "fleet",
	}
	if opts.DryRun {
		return rec, body, nil
	}
	if _, err := os.Stat(path); err == nil {
		return Record{}, "", fmt.Errorf("fleet record already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return Record{}, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Record{}, "", err
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return Record{}, "", err
	}
	return rec, body, nil
}

// Set applies scalar frontmatter updates to one fleet record, resolved like
// Get, preserving every untouched line. It returns the updated record and
// the changes actually applied.
func Set(roots []Root, key string, updates map[string]string) (Record, []record.FieldChange, error) {
	rec, err := resolve(roots, key)
	if err != nil {
		return Record{}, nil, err
	}
	changes, err := record.SetScalars(rec.Path, updates)
	if err != nil {
		return Record{}, nil, err
	}
	updated, err := parseRecord(Root{Manifest: rec.Manifest, Workspace: rec.Workspace}, rec.Path)
	if err != nil {
		return Record{}, nil, err
	}
	return updated, changes, nil
}

func scan(roots []Root) ([]Record, error) {
	return record.Scan(roots, "fleet", parseRecord)
}

func parseRecord(root Root, path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	frontmatter, _ := record.SplitFrontmatter(data)
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	rec := Record{
		Manifest:     root.Manifest,
		Workspace:    root.Workspace,
		ID:           record.First(record.FirstValue(frontmatter, "id"), stem),
		Path:         path,
		Customer:     record.FirstValue(frontmatter, "customer"),
		Partner:      record.FirstValue(frontmatter, "partner"),
		Status:       record.FirstValue(frontmatter, "status"),
		Device:       record.FirstValue(frontmatter, "device"),
		Serial:       record.FirstValue(frontmatter, "serial"),
		Identifiers:  record.Values(frontmatter, "identifiers"),
		ConfigRepo:   record.FirstValue(frontmatter, "config_repo"),
		ConfigBranch: record.FirstValue(frontmatter, "config_branch"),
		DeployedSite: record.FirstValue(frontmatter, "deployed_site"),
		ShipTo:       record.FirstValue(frontmatter, "ship_to"),
		Contacts:     record.Values(frontmatter, "contacts"),
		InstallDate:  record.FirstValue(frontmatter, "install_date"),
		Source:       record.FirstValue(frontmatter, "source"),
		fields:       record.ScalarFields(frontmatter),
	}
	return rec, nil
}

func applyFilter(records []Record, filter Filter) []Record {
	var out []Record
	for _, rec := range records {
		if filter.Status != "" && rec.Status != filter.Status {
			continue
		}
		if !record.MatchesFilterValue(rec.Customer, filter.Customer, filter.CustomerValues) {
			continue
		}
		if filter.Partner != "" && rec.Partner != filter.Partner {
			continue
		}
		if filter.Identifier != "" && rec.ID != filter.Identifier && !record.ContainsValue(rec.Identifiers, filter.Identifier) {
			continue
		}
		if filter.Branch != "" && rec.ConfigBranch != filter.Branch {
			continue
		}
		if !matchesWhere(rec, filter.Where) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func matchesWhere(rec Record, where map[string]string) bool {
	for key, want := range where {
		if rec.fields[key] != want {
			return false
		}
	}
	return true
}

func sortRecords(records []Record) {
	sort.Slice(records, func(i, j int) bool {
		return records[i].ID < records[j].ID
	})
}

func sortSearchRecords(records []Record, scores map[string]int) {
	sort.Slice(records, func(i, j int) bool {
		if scores[records[i].Path] != scores[records[j].Path] {
			return scores[records[i].Path] > scores[records[j].Path]
		}
		return records[i].ID < records[j].ID
	})
}

func scaffold(id string, opts AddOptions, status string) string {
	lines := []string{
		"---",
		"id: " + id,
		record.YAMLScalar("customer", opts.Customer),
		record.YAMLScalar("partner", opts.Partner),
		record.YAMLScalar("status", status),
		record.YAMLScalar("device", opts.Device),
		record.YAMLScalar("serial", opts.Serial),
		record.YAMLList("identifiers", opts.Identifiers),
		record.YAMLScalar("config_repo", opts.ConfigRepo),
		record.YAMLScalar("config_branch", opts.ConfigBranch),
		record.YAMLScalar("deployed_site", opts.DeployedSite),
		record.YAMLScalar("ship_to", opts.ShipTo),
		record.YAMLList("contacts", opts.Contacts),
		record.YAMLScalar("install_date", opts.InstallDate),
		"source: fleet",
		"---",
	}
	return strings.Join(lines, "\n") + fmt.Sprintf(`

# %s

## Notes
`, id)
}
