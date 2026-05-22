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
	terms := parseSearchTerms(query)
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
		if !matchesAllTerms(lowerContent, terms) {
			continue
		}
		meeting.Snippet = snippetBodyFirst(data, terms)
		scores[meeting.Path] = matchScore(lowerContent, terms)
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
		ID:        first(firstValue(frontmatter, "id"), stem),
		Path:      path,
		Date:      first(firstValue(frontmatter, "date"), dateFromStem(stem)),
		Title:     first(firstValue(frontmatter, "title"), titleFromSlug(stem)),
		Customer:  firstValue(frontmatter, "customer"),
		Attendees: values(frontmatter, "attendees"),
		Partners:  firstValues(frontmatter, "partners", "partner"),
		Product:   firstValue(frontmatter, "product"),
		SourceID:  first(firstValue(frontmatter, "source_id"), firstValue(frontmatter, "source_ref"), firstValue(frontmatter, "spark_meeting_id")),
		Status:    firstValue(frontmatter, "status"),
	}
	return meeting, string(data), nil
}

func splitFrontmatter(data []byte) (map[string][]string, []byte) {
	out := map[string][]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return out, data
	}
	var bodyStart int
	offset := len(scanner.Text()) + 1
	currentKey := ""
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
			trimmed := strings.TrimSpace(line)
			if currentKey != "" && strings.HasPrefix(trimmed, "- ") {
				out[currentKey] = append(out[currentKey], cleanValue(trimmed[2:]))
			}
			continue
		}
		currentKey = ""
		if i := strings.Index(line, ":"); i > 0 {
			key := strings.TrimSpace(line[:i])
			value := strings.TrimSpace(line[i+1:])
			if value == "" {
				out[key] = nil
				currentKey = key
				continue
			}
			out[key] = parseFrontmatterValue(value)
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
		if !matchesFilterValue(meeting.Customer, filter.Customer, filter.CustomerValues) {
			continue
		}
		if filter.Partner != "" && !containsValue(meeting.Partners, filter.Partner) {
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

func snippet(content string, terms []searchTerm) string {
	for _, line := range strings.Split(content, "\n") {
		if matchesAllTerms(strings.ToLower(line), terms) {
			return strings.TrimSpace(line)
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if matchesAnyTerm(strings.ToLower(line), terms) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func snippetBodyFirst(data []byte, terms []searchTerm) string {
	_, body := splitFrontmatter(data)
	if found := snippet(string(body), terms); found != "" {
		return found
	}
	return snippet(string(data), terms)
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
`, id, date, escapeTitle(title), yamlList("attendees", attendees), customer, yamlList("partners", partners), product, optionalScalar("source_id", sourceID), status, title)
}

func cleanValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	return value
}

func parseFrontmatterValue(value string) []string {
	value = strings.TrimSpace(value)
	if value == "[]" {
		return nil
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "["), "]"))
		if inner == "" {
			return nil
		}
		var out []string
		for _, part := range strings.Split(inner, ",") {
			if cleaned := cleanValue(part); cleaned != "" {
				out = append(out, cleaned)
			}
		}
		return out
	}
	return []string{cleanValue(value)}
}

type searchTerm struct {
	value string
}

func parseSearchTerms(query string) []searchTerm {
	var terms []searchTerm
	var current strings.Builder
	inQuote := false
	flush := func() {
		value := strings.ToLower(strings.TrimSpace(current.String()))
		current.Reset()
		if value != "" {
			terms = append(terms, searchTerm{value: value})
		}
	}
	for _, r := range query {
		switch {
		case r == '"':
			flush()
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t' || r == '\n' || r == '\r'):
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return terms
}

func matchesAllTerms(content string, terms []searchTerm) bool {
	for _, term := range terms {
		if !strings.Contains(content, term.value) {
			return false
		}
	}
	return true
}

func matchesAnyTerm(content string, terms []searchTerm) bool {
	for _, term := range terms {
		if strings.Contains(content, term.value) {
			return true
		}
	}
	return false
}

func matchScore(content string, terms []searchTerm) int {
	score := 0
	for _, term := range terms {
		score += strings.Count(content, term.value)
	}
	return score
}

func matchesFilterValue(value, exact string, allowed []string) bool {
	if exact == "" && len(allowed) == 0 {
		return true
	}
	if exact != "" && value == exact {
		return true
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func firstValue(frontmatter map[string][]string, key string) string {
	values := frontmatter[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func values(frontmatter map[string][]string, key string) []string {
	if len(frontmatter[key]) == 0 {
		return nil
	}
	return append([]string(nil), frontmatter[key]...)
}

func firstValues(frontmatter map[string][]string, keys ...string) []string {
	for _, key := range keys {
		if found := values(frontmatter, key); len(found) != 0 {
			return found
		}
	}
	return nil
}

func yamlList(key string, values []string) string {
	if len(values) == 0 {
		return key + ": []"
	}
	var out strings.Builder
	out.WriteString(key)
	out.WriteString(":\n")
	for _, value := range values {
		out.WriteString("  - \"")
		out.WriteString(escapeTitle(value))
		out.WriteString("\"\n")
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func optionalScalar(key, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s", key, value)
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
