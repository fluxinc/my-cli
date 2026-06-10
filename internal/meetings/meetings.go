// Package meetings implements stdlib markdown meeting commands.
package meetings

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/our-ai/internal/record"
)

// Root is one workspace root that may contain meetings/.
type Root = record.Root

// Filter limits meeting results.
type Filter struct {
	Since          string
	Customer       string
	CustomerValues []string
	Partner        string
	Product        string
}

// Meeting is a parsed meeting note.
type Meeting struct {
	Manifest  string   `json:"manifest"`
	Workspace string   `json:"workspace"`
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	Date      string   `json:"date,omitempty"`
	Title     string   `json:"title,omitempty"`
	Customer  string   `json:"customer,omitempty"`
	Attendees []string `json:"attendees,omitempty"`
	Partners  []string `json:"partners,omitempty"`
	Product   string   `json:"product,omitempty"`
	SourceID  string   `json:"source_id,omitempty"`
	Status    string   `json:"status,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
}

// AddOptions controls meeting scaffold creation.
type AddOptions struct {
	Date      string
	Title     string
	Customer  string
	Attendees []string
	Partners  []string
	Product   string
	SourceID  string
	Status    string
	DryRun    bool
}

// List returns meeting notes from all roots.
func List(roots []Root, filter Filter) ([]Meeting, error) {
	meetings, err := scan(roots)
	if err != nil {
		return nil, err
	}
	meetings = applyFilter(meetings, filter)
	sortMeetings(meetings)
	return meetings, nil
}

// Search returns meeting notes whose markdown contains every query term.
func Search(roots []Root, query string, filter Filter) ([]Meeting, error) {
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
	var out []Meeting
	scores := map[string]int{}
	for _, meeting := range applyFilter(all, filter) {
		data, err := os.ReadFile(meeting.Path)
		if err != nil {
			return nil, err
		}
		lowerContent := strings.ToLower(string(data))
		if !record.MatchesAllTerms(lowerContent, terms) {
			continue
		}
		meeting.Snippet = record.SnippetBodyFirst(data, terms)
		scores[meeting.Path] = record.MatchScore(lowerContent, terms)
		out = append(out, meeting)
	}
	sortSearchMeetings(out, scores)
	return out, nil
}

// Get resolves a meeting by id, filename stem, or path and returns its content.
func Get(roots []Root, idOrPath string) (Meeting, string, error) {
	idOrPath = strings.TrimSpace(idOrPath)
	if idOrPath == "" {
		return Meeting{}, "", fmt.Errorf("meeting id or path is required")
	}
	if info, err := os.Stat(idOrPath); err == nil && !info.IsDir() {
		meeting, content, err := parseMeeting(Root{}, idOrPath)
		return meeting, content, err
	}
	all, err := scan(roots)
	if err != nil {
		return Meeting{}, "", err
	}
	var matches []Meeting
	for _, meeting := range all {
		stem := strings.TrimSuffix(filepath.Base(meeting.Path), filepath.Ext(meeting.Path))
		if meeting.ID == idOrPath || stem == idOrPath {
			matches = append(matches, meeting)
		}
	}
	if len(matches) == 0 {
		return Meeting{}, "", fmt.Errorf("meeting %q not found", idOrPath)
	}
	if len(matches) > 1 {
		return Meeting{}, "", fmt.Errorf("meeting %q is ambiguous; pass a path", idOrPath)
	}
	data, err := os.ReadFile(matches[0].Path)
	if err != nil {
		return Meeting{}, "", err
	}
	return matches[0], string(data), nil
}

// Add creates a markdown meeting scaffold in root/meetings.
func Add(root Root, slug string, opts AddOptions) (Meeting, string, error) {
	slug = record.CleanSlug(slug)
	slugDate, slug := record.SplitDatePrefix(slug)
	if slug == "" {
		return Meeting{}, "", fmt.Errorf("meeting slug is required")
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
		status = "draft"
	}
	id := date + "-" + slug
	path := filepath.Join(root.Path, "meetings", id+".md")
	body := scaffold(id, date, title, opts.Customer, opts.Attendees, opts.Partners, opts.Product, opts.SourceID, status)
	meeting := Meeting{
		Manifest:  root.Manifest,
		Workspace: root.Workspace,
		ID:        id,
		Path:      path,
		Date:      date,
		Title:     title,
		Customer:  opts.Customer,
		Attendees: append([]string(nil), opts.Attendees...),
		Partners:  append([]string(nil), opts.Partners...),
		Product:   opts.Product,
		SourceID:  opts.SourceID,
		Status:    status,
	}
	if opts.DryRun {
		return meeting, body, nil
	}
	if _, err := os.Stat(path); err == nil {
		return Meeting{}, "", fmt.Errorf("meeting already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return Meeting{}, "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Meeting{}, "", err
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return Meeting{}, "", err
	}
	return meeting, body, nil
}

func scan(roots []Root) ([]Meeting, error) {
	return record.Scan(roots, "meetings", func(root Root, path string) (Meeting, error) {
		meeting, _, err := parseMeeting(root, path)
		return meeting, err
	})
}

func parseMeeting(root Root, path string) (Meeting, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Meeting{}, "", err
	}
	frontmatter, _ := record.SplitFrontmatter(data)
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	meeting := Meeting{
		Manifest:  root.Manifest,
		Workspace: root.Workspace,
		ID:        record.First(record.FirstValue(frontmatter, "id"), stem),
		Path:      path,
		Date:      record.First(record.FirstValue(frontmatter, "date"), record.DateFromStem(stem)),
		Title:     record.First(record.FirstValue(frontmatter, "title"), record.TitleFromSlug(stem)),
		Customer:  record.FirstValue(frontmatter, "customer"),
		Attendees: record.Values(frontmatter, "attendees"),
		Partners:  record.FirstValues(frontmatter, "partners", "partner"),
		Product:   record.FirstValue(frontmatter, "product"),
		SourceID:  record.First(record.FirstValue(frontmatter, "source_id"), record.FirstValue(frontmatter, "source_ref"), record.FirstValue(frontmatter, "spark_meeting_id")),
		Status:    record.FirstValue(frontmatter, "status"),
	}
	return meeting, string(data), nil
}

func applyFilter(meetings []Meeting, filter Filter) []Meeting {
	var out []Meeting
	for _, meeting := range meetings {
		if filter.Since != "" && meeting.Date < filter.Since {
			continue
		}
		if !record.MatchesFilterValue(meeting.Customer, filter.Customer, filter.CustomerValues) {
			continue
		}
		if filter.Partner != "" && !record.ContainsValue(meeting.Partners, filter.Partner) {
			continue
		}
		if filter.Product != "" && meeting.Product != filter.Product {
			continue
		}
		out = append(out, meeting)
	}
	return out
}

func sortMeetings(meetings []Meeting) {
	sort.Slice(meetings, func(i, j int) bool {
		if meetings[i].Date != meetings[j].Date {
			return meetings[i].Date > meetings[j].Date
		}
		return meetings[i].ID < meetings[j].ID
	})
}

func sortSearchMeetings(meetings []Meeting, scores map[string]int) {
	sort.Slice(meetings, func(i, j int) bool {
		if scores[meetings[i].Path] != scores[meetings[j].Path] {
			return scores[meetings[i].Path] > scores[meetings[j].Path]
		}
		if meetings[i].Date != meetings[j].Date {
			return meetings[i].Date > meetings[j].Date
		}
		return meetings[i].ID < meetings[j].ID
	})
}

func scaffold(id, date, title, customer string, attendees, partners []string, product, sourceID, status string) string {
	return fmt.Sprintf(`---
id: %s
date: %s
title: "%s"
%s
customer: %s
%s
product: %s
%s
source: meeting
status: %s
---

# %s

## Notes

## Promises

## Follow-ups
`, id, date, record.EscapeValue(title), record.YAMLList("attendees", attendees), customer, record.YAMLList("partners", partners), product, record.OptionalScalar("source_id", sourceID), status, title)
}
