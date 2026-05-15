// Package meetings implements stdlib markdown meeting commands.
package meetings

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Root is one workspace root that may contain meetings/.
type Root struct {
	Manifest  string
	Workspace string
	Path      string
}

// Filter limits meeting results.
type Filter struct {
	Since    string
	Customer string
	Product  string
}

// Meeting is a parsed meeting note.
type Meeting struct {
	Manifest  string `json:"manifest"`
	Workspace string `json:"workspace"`
	ID        string `json:"id"`
	Path      string `json:"path"`
	Date      string `json:"date,omitempty"`
	Title     string `json:"title,omitempty"`
	Customer  string `json:"customer,omitempty"`
	Product   string `json:"product,omitempty"`
	Status    string `json:"status,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
}

// AddOptions controls meeting scaffold creation.
type AddOptions struct {
	Date     string
	Title    string
	Customer string
	Product  string
	Status   string
	DryRun   bool
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

// Search returns meeting notes whose markdown contains query.
func Search(roots []Root, query string, filter Filter) ([]Meeting, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("search text is required")
	}
	needle := strings.ToLower(query)
	all, err := scan(roots)
	if err != nil {
		return nil, err
	}
	var out []Meeting
	for _, meeting := range applyFilter(all, filter) {
		data, err := os.ReadFile(meeting.Path)
		if err != nil {
			return nil, err
		}
		if !strings.Contains(strings.ToLower(string(data)), needle) {
			continue
		}
		meeting.Snippet = snippetBodyFirst(data, needle)
		out = append(out, meeting)
	}
	sortMeetings(out)
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
	slug = cleanSlug(slug)
	if slug == "" {
		return Meeting{}, "", fmt.Errorf("meeting slug is required")
	}
	date := opts.Date
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	title := opts.Title
	if title == "" {
		title = titleFromSlug(slug)
	}
	status := opts.Status
	if status == "" {
		status = "draft"
	}
	id := date + "-" + slug
	path := filepath.Join(root.Path, "meetings", id+".md")
	body := scaffold(id, date, title, opts.Customer, opts.Product, status)
	meeting := Meeting{
		Manifest:  root.Manifest,
		Workspace: root.Workspace,
		ID:        id,
		Path:      path,
		Date:      date,
		Title:     title,
		Customer:  opts.Customer,
		Product:   opts.Product,
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
	var out []Meeting
	for _, root := range roots {
		dir := filepath.Join(root.Path, "meetings")
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			meeting, _, err := parseMeeting(root, filepath.Join(dir, entry.Name()))
			if err != nil {
				return nil, err
			}
			out = append(out, meeting)
		}
	}
	return out, nil
}

func parseMeeting(root Root, path string) (Meeting, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Meeting{}, "", err
	}
	frontmatter, _ := splitFrontmatter(data)
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	meeting := Meeting{
		Manifest:  root.Manifest,
		Workspace: root.Workspace,
		ID:        first(frontmatter["id"], stem),
		Path:      path,
		Date:      first(frontmatter["date"], dateFromStem(stem)),
		Title:     first(frontmatter["title"], titleFromSlug(stem)),
		Customer:  frontmatter["customer"],
		Product:   frontmatter["product"],
		Status:    frontmatter["status"],
	}
	return meeting, string(data), nil
}

func splitFrontmatter(data []byte) (map[string]string, []byte) {
	out := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return out, data
	}
	var bodyStart int
	offset := len(scanner.Text()) + 1
	for scanner.Scan() {
		line := scanner.Text()
		bodyStart = offset + len(line) + 1
		offset = bodyStart
		if strings.TrimSpace(line) == "---" {
			if bodyStart > len(data) {
				bodyStart = len(data)
			}
			return out, data[bodyStart:]
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if i := strings.Index(line, ":"); i > 0 {
			key := strings.TrimSpace(line[:i])
			value := cleanValue(line[i+1:])
			out[key] = value
		}
	}
	return out, data
}

func applyFilter(meetings []Meeting, filter Filter) []Meeting {
	var out []Meeting
	for _, meeting := range meetings {
		if filter.Since != "" && meeting.Date < filter.Since {
			continue
		}
		if filter.Customer != "" && meeting.Customer != filter.Customer {
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

func snippet(content, lowerNeedle string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(strings.ToLower(line), lowerNeedle) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func snippetBodyFirst(data []byte, lowerNeedle string) string {
	_, body := splitFrontmatter(data)
	if found := snippet(string(body), lowerNeedle); found != "" {
		return found
	}
	return snippet(string(data), lowerNeedle)
}

func scaffold(id, date, title, customer, product, status string) string {
	return fmt.Sprintf(`---
id: %s
date: %s
title: "%s"
attendees: []
customer: %s
product: %s
source: meeting
status: %s
---

# %s

## Notes

## Promises

## Follow-ups
`, id, date, escapeTitle(title), customer, product, status, title)
}

func cleanValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	return value
}

func cleanSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if r == '-' || r == ' ' {
			if !lastDash && out.Len() > 0 {
				out.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(out.String(), "-")
}

func dateFromStem(stem string) string {
	if len(stem) >= len("2006-01-02") {
		candidate := stem[:len("2006-01-02")]
		if _, err := time.Parse("2006-01-02", candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func titleFromSlug(slug string) string {
	if len(slug) > len("2006-01-02-") && dateFromStem(slug) != "" {
		slug = slug[len("2006-01-02-"):]
	}
	parts := strings.Fields(strings.ReplaceAll(slug, "-", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func escapeTitle(title string) string {
	return strings.ReplaceAll(title, `"`, `\"`)
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
