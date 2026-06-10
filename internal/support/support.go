// Package support implements stdlib markdown support record commands.
package support

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/our-ai/internal/record"
)

// Root is one workspace root that may contain support/.
type Root = record.Root

// Filter limits support record results.
type Filter struct {
	Since          string
	Customer       string
	CustomerValues []string
	Identifier     string
	ClaimedBy      string
	Product        string
	Area           string
	Tag            string
	FeatureOnly    bool
}

// Record is a parsed support note.
type Record struct {
	Manifest         string   `json:"manifest"`
	Workspace        string   `json:"workspace"`
	ID               string   `json:"id"`
	Path             string   `json:"path"`
	Date             string   `json:"date,omitempty"`
	Title            string   `json:"title,omitempty"`
	Customer         string   `json:"customer,omitempty"`
	Identifiers      []string `json:"identifiers,omitempty"`
	ClaimedBy        string   `json:"claimed_by,omitempty"`
	ObservedBy       []string `json:"observed_by,omitempty"`
	ApprovedBy       string   `json:"approved_by,omitempty"`
	Product          string   `json:"product,omitempty"`
	Area             string   `json:"area,omitempty"`
	Status           string   `json:"status,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	FeatureCandidate bool     `json:"feature_candidate,omitempty"`
	Source           string   `json:"source,omitempty"`
	Snippet          string   `json:"snippet,omitempty"`
}

// AddOptions controls support record scaffold creation.
type AddOptions struct {
	Date             string
	Title            string
	Customer         string
	Identifiers      []string
	ClaimedBy        string
	ObservedBy       []string
	ApprovedBy       string
	Product          string
	Area             string
	Tags             []string
	Status           string
	FeatureCandidate bool
	DryRun           bool
}

// List returns support records from all roots.
func List(roots []Root, filter Filter) ([]Record, error) {
	records, err := scan(roots)
	if err != nil {
		return nil, err
	}
	records = applyFilter(records, filter)
	sortRecords(records)
	return records, nil
}

// Search returns support records whose markdown contains every query term.
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

// Get resolves a support record by id, filename stem, or path and returns its
// content.
func Get(roots []Root, idOrPath string) (Record, string, error) {
	idOrPath = strings.TrimSpace(idOrPath)
	if idOrPath == "" {
		return Record{}, "", fmt.Errorf("support record id or path is required")
	}
	if info, err := os.Stat(idOrPath); err == nil && !info.IsDir() {
		rec, content, err := parseRecord(Root{}, idOrPath)
		return rec, content, err
	}
	all, err := scan(roots)
	if err != nil {
		return Record{}, "", err
	}
	var matches []Record
	for _, rec := range all {
		stem := strings.TrimSuffix(filepath.Base(rec.Path), filepath.Ext(rec.Path))
		if rec.ID == idOrPath || stem == idOrPath {
			matches = append(matches, rec)
		}
	}
	if len(matches) == 0 {
		return Record{}, "", fmt.Errorf("support record %q not found", idOrPath)
	}
	if len(matches) > 1 {
		return Record{}, "", fmt.Errorf("support record %q is ambiguous; pass a path", idOrPath)
	}
	data, err := os.ReadFile(matches[0].Path)
	if err != nil {
		return Record{}, "", err
	}
	return matches[0], string(data), nil
}

// Add creates a markdown support record scaffold in root/support.
func Add(root Root, slug string, opts AddOptions) (Record, string, error) {
	slug = record.CleanSlug(slug)
	slugDate, slug := record.SplitDatePrefix(slug)
	if slug == "" {
		return Record{}, "", fmt.Errorf("support record slug is required")
	}
	date := opts.Date
	if date == "" {
		date = slugDate
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	title := opts.Title
	if title == "" {
		title = record.TitleFromSlug(slug)
	}
	status := opts.Status
	if status == "" {
		status = "resolved"
	}
	if !validStatus(status) {
		return Record{}, "", fmt.Errorf("support status %q is unsupported; use open, workaround, or resolved", status)
	}
	id := date + "-" + slug
	path := filepath.Join(root.Path, "support", id+".md")
	body := scaffold(id, date, title, opts, status)
	rec := Record{
		Manifest:         root.Manifest,
		Workspace:        root.Workspace,
		ID:               id,
		Path:             path,
		Date:             date,
		Title:            title,
		Customer:         opts.Customer,
		Identifiers:      append([]string(nil), opts.Identifiers...),
		ClaimedBy:        opts.ClaimedBy,
		ObservedBy:       append([]string(nil), opts.ObservedBy...),
		ApprovedBy:       opts.ApprovedBy,
		Product:          opts.Product,
		Area:             opts.Area,
		Status:           status,
		Tags:             append([]string(nil), opts.Tags...),
		FeatureCandidate: opts.FeatureCandidate,
		Source:           "support",
	}
	if opts.DryRun {
		return rec, body, nil
	}
	if _, err := os.Stat(path); err == nil {
		return Record{}, "", fmt.Errorf("support record already exists: %s", path)
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

func scan(roots []Root) ([]Record, error) {
	return record.Scan(roots, "support", func(root Root, path string) (Record, error) {
		rec, _, err := parseRecord(root, path)
		return rec, err
	})
}

func parseRecord(root Root, path string) (Record, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, "", err
	}
	frontmatter, _ := record.SplitFrontmatter(data)
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	rec := Record{
		Manifest:         root.Manifest,
		Workspace:        root.Workspace,
		ID:               record.First(record.FirstValue(frontmatter, "id"), stem),
		Path:             path,
		Date:             record.First(record.FirstValue(frontmatter, "date"), record.DateFromStem(stem)),
		Title:            record.First(record.FirstValue(frontmatter, "title"), record.TitleFromSlug(stem)),
		Customer:         record.FirstValue(frontmatter, "customer"),
		Identifiers:      record.Values(frontmatter, "identifiers"),
		ClaimedBy:        record.FirstValue(frontmatter, "claimed_by"),
		ObservedBy:       record.Values(frontmatter, "observed_by"),
		ApprovedBy:       record.FirstValue(frontmatter, "approved_by"),
		Product:          record.FirstValue(frontmatter, "product"),
		Area:             record.FirstValue(frontmatter, "area"),
		Status:           record.FirstValue(frontmatter, "status"),
		Tags:             record.Values(frontmatter, "tags"),
		FeatureCandidate: record.BoolValue(record.FirstValue(frontmatter, "feature_candidate")),
		Source:           record.FirstValue(frontmatter, "source"),
	}
	return rec, string(data), nil
}

func applyFilter(records []Record, filter Filter) []Record {
	var out []Record
	for _, rec := range records {
		if filter.Since != "" && rec.Date < filter.Since {
			continue
		}
		if !record.MatchesFilterValue(rec.Customer, filter.Customer, filter.CustomerValues) {
			continue
		}
		if filter.Identifier != "" && !record.ContainsValue(rec.Identifiers, filter.Identifier) {
			continue
		}
		if filter.ClaimedBy != "" && rec.ClaimedBy != filter.ClaimedBy {
			continue
		}
		if filter.Product != "" && rec.Product != filter.Product {
			continue
		}
		if filter.Area != "" && rec.Area != filter.Area {
			continue
		}
		if filter.Tag != "" && !record.ContainsValue(rec.Tags, filter.Tag) {
			continue
		}
		if filter.FeatureOnly && !rec.FeatureCandidate {
			continue
		}
		out = append(out, rec)
	}
	return out
}

func sortRecords(records []Record) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].Date != records[j].Date {
			return records[i].Date > records[j].Date
		}
		return records[i].ID < records[j].ID
	})
}

func sortSearchRecords(records []Record, scores map[string]int) {
	sort.Slice(records, func(i, j int) bool {
		if scores[records[i].Path] != scores[records[j].Path] {
			return scores[records[i].Path] > scores[records[j].Path]
		}
		if records[i].Date != records[j].Date {
			return records[i].Date > records[j].Date
		}
		return records[i].ID < records[j].ID
	})
}

func scaffold(id, date, title string, opts AddOptions, status string) string {
	var frontmatter []string
	frontmatter = append(frontmatter,
		"---",
		"id: "+id,
		"date: "+date,
		`title: "`+record.EscapeValue(title)+`"`,
	)
	if opts.Customer != "" {
		frontmatter = append(frontmatter, "customer: "+opts.Customer)
	}
	frontmatter = append(frontmatter,
		record.YAMLList("identifiers", opts.Identifiers),
		record.YAMLScalar("claimed_by", opts.ClaimedBy),
		record.YAMLList("observed_by", opts.ObservedBy),
		record.YAMLScalar("approved_by", opts.ApprovedBy),
		record.YAMLScalar("product", opts.Product),
		record.YAMLScalar("area", opts.Area),
		"status: "+status,
		record.YAMLList("tags", opts.Tags),
		fmt.Sprintf("feature_candidate: %t", opts.FeatureCandidate),
		"source: support",
		"---",
	)
	return strings.Join(frontmatter, "\n") + fmt.Sprintf(`

# %s

## Problem

## Context

## Diagnosis

## Solution

## Validation

## Feature Signal
`, title)
}

func validStatus(value string) bool {
	switch value {
	case "open", "workaround", "resolved":
		return true
	default:
		return false
	}
}
