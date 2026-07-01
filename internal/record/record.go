// Package record is the shared markdown-record engine behind the meetings,
// support, and fleet content nouns. It owns the pieces every noun needs the
// same way — roots, directory scans, frontmatter parsing and rewriting,
// token-AND search, YAML scaffold emission, and slug/date helpers — while
// each noun keeps its own typed record, filter semantics, and scaffold
// template.
package record

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

// Root is one workspace root that may contain a content directory.
type Root struct {
	Manifest     string
	Workspace    string
	Path         string
	ContentPaths []string
}

// Scan parses every markdown file under dir in each root with parse.
func Scan[T any](roots []Root, dir string, parse func(Root, string) (T, error)) ([]T, error) {
	var out []T
	for _, root := range roots {
		full := filepath.Join(root.Path, dir)
		entries, err := os.ReadDir(full)
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
			item, err := parse(root, filepath.Join(full, entry.Name()))
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// SplitFrontmatter parses a leading YAML frontmatter block into a key to
// values map and returns the remaining body bytes.
func SplitFrontmatter(data []byte) (map[string][]string, []byte) {
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
				out[currentKey] = append(out[currentKey], CleanValue(trimmed[2:]))
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

// FieldChange records one frontmatter field update applied by SetScalars.
type FieldChange struct {
	Key string `json:"key"`
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// SetScalars rewrites top-level scalar frontmatter keys in the file at path,
// leaving every other line — unknown keys, nested blocks, and the body —
// untouched. Missing keys are appended before the closing delimiter. Keys
// holding block lists or nested maps are rejected, as is "id" (the record's
// stable key). Inline comments on a replaced line are not preserved. The file
// is rewritten only when at least one value actually changes; the returned
// changes are sorted by key.
func SetScalars(path string, updates map[string]string) ([]FieldChange, error) {
	if _, ok := updates["id"]; ok {
		return nil, fmt.Errorf("field \"id\" is the record's stable key; rename the file instead")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)
	crlf := strings.Contains(content, "\r\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("%s has no frontmatter block", path)
	}
	emitLine := func(key, value string) string {
		line := yamlScalarLine(key, value)
		if crlf {
			line += "\r"
		}
		return line
	}
	closeIdx := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return nil, fmt.Errorf("%s has an unterminated frontmatter block", path)
	}
	keyLine := map[string]int{}
	for i := 1; i < closeIdx; i++ {
		line := lines[i]
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		if j := strings.Index(line, ":"); j > 0 {
			key := strings.TrimSpace(line[:j])
			if _, exists := keyLine[key]; exists {
				return nil, fmt.Errorf("%s has duplicate frontmatter key %q; fix the file before updating it", path, key)
			}
			keyLine[key] = i
		}
	}
	keys := make([]string, 0, len(updates))
	for key := range updates {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("field name is required")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var changes []FieldChange
	var appended []string
	for _, key := range keys {
		value := strings.TrimSpace(updates[key])
		i, exists := keyLine[key]
		if !exists {
			if value == "" {
				continue
			}
			appended = append(appended, emitLine(key, value))
			changes = append(changes, FieldChange{Key: key, New: value})
			continue
		}
		line := lines[i]
		rest := strings.TrimSpace(line[strings.Index(line, ":")+1:])
		if strings.HasPrefix(rest, "[") || (rest == "" && i+1 < closeIdx &&
			(strings.HasPrefix(lines[i+1], " ") || strings.HasPrefix(lines[i+1], "\t"))) {
			return nil, fmt.Errorf("field %q holds a list or nested block; edit the file directly", key)
		}
		old := CleanValue(strings.TrimSpace(stripInlineComment(rest)))
		if old == value {
			continue
		}
		lines[i] = emitLine(key, value)
		changes = append(changes, FieldChange{Key: key, Old: old, New: value})
	}
	if len(changes) == 0 {
		return nil, nil
	}
	if len(appended) != 0 {
		lines = append(lines[:closeIdx], append(append([]string{}, appended...), lines[closeIdx:]...)...)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return nil, err
	}
	return changes, nil
}

func yamlScalarLine(key, value string) string {
	if value == "" {
		return key + ":"
	}
	if scalarNeedsQuoting(value) {
		return key + `: "` + EscapeValue(value) + `"`
	}
	return key + ": " + value
}

// scalarNeedsQuoting reports whether a plain scalar would be misread on
// round-trip — a hash comment, a leading YAML indicator, or a map-like
// colon — and therefore must be emitted quoted.
func scalarNeedsQuoting(value string) bool {
	switch value[0] {
	case '"', '\'', '[', '{', '#', '&', '*', '>', '|':
		return true
	}
	return strings.Contains(value, " #") || strings.Contains(value, ": ")
}

func commentSuffix(value string) string {
	if i := strings.Index(value, " #"); i >= 0 {
		return value[i:]
	}
	return ""
}

// stripInlineComment drops a trailing YAML comment from an unquoted value.
func stripInlineComment(value string) string {
	if strings.HasPrefix(value, `"`) || strings.HasPrefix(value, "'") {
		return value
	}
	return strings.TrimSuffix(value, commentSuffix(value))
}

// CleanValue trims whitespace and surrounding quotes from a YAML value.
func CleanValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'")
	return value
}

func parseFrontmatterValue(value string) []string {
	value = strings.TrimSpace(stripInlineComment(strings.TrimSpace(value)))
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
			if cleaned := CleanValue(part); cleaned != "" {
				out = append(out, cleaned)
			}
		}
		return out
	}
	return []string{CleanValue(value)}
}

// FirstValue returns the first frontmatter value for key.
func FirstValue(frontmatter map[string][]string, key string) string {
	values := frontmatter[key]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Values returns a copy of the frontmatter values for key.
func Values(frontmatter map[string][]string, key string) []string {
	if len(frontmatter[key]) == 0 {
		return nil
	}
	return append([]string(nil), frontmatter[key]...)
}

// FirstValues returns the values for the first key that has any.
func FirstValues(frontmatter map[string][]string, keys ...string) []string {
	for _, key := range keys {
		if found := Values(frontmatter, key); len(found) != 0 {
			return found
		}
	}
	return nil
}

// ScalarFields flattens frontmatter to the first value of every key, for
// generic key=value filtering.
func ScalarFields(frontmatter map[string][]string) map[string]string {
	out := map[string]string{}
	for key, values := range frontmatter {
		if len(values) != 0 {
			out[key] = values[0]
		}
	}
	return out
}

// BoolValue reports whether a frontmatter value is truthy.
func BoolValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes", "1":
		return true
	default:
		return false
	}
}

// Term is one lowercase search token.
type Term struct {
	Value string
}

// ParseTerms splits a query into lowercase tokens, honoring double quotes.
func ParseTerms(query string) []Term {
	var terms []Term
	var current strings.Builder
	inQuote := false
	flush := func() {
		value := strings.ToLower(strings.TrimSpace(current.String()))
		current.Reset()
		if value != "" {
			terms = append(terms, Term{Value: value})
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

// MatchesAllTerms reports whether content contains every term.
func MatchesAllTerms(content string, terms []Term) bool {
	for _, term := range terms {
		if !strings.Contains(content, term.Value) {
			return false
		}
	}
	return true
}

// MatchesAnyTerm reports whether content contains at least one term.
func MatchesAnyTerm(content string, terms []Term) bool {
	for _, term := range terms {
		if strings.Contains(content, term.Value) {
			return true
		}
	}
	return false
}

// MatchScore counts term occurrences in content.
func MatchScore(content string, terms []Term) int {
	score := 0
	for _, term := range terms {
		score += strings.Count(content, term.Value)
	}
	return score
}

// Snippet returns the first line matching all terms, then any term.
func Snippet(content string, terms []Term) string {
	for _, line := range strings.Split(content, "\n") {
		if MatchesAllTerms(strings.ToLower(line), terms) {
			return strings.TrimSpace(line)
		}
	}
	for _, line := range strings.Split(content, "\n") {
		if MatchesAnyTerm(strings.ToLower(line), terms) {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// SnippetBodyFirst prefers a snippet from the body over the frontmatter.
func SnippetBodyFirst(data []byte, terms []Term) string {
	_, body := SplitFrontmatter(data)
	if found := Snippet(string(body), terms); found != "" {
		return found
	}
	return Snippet(string(data), terms)
}

// MatchesFilterValue reports whether value matches an exact filter or any
// allowed alias expansion.
func MatchesFilterValue(value, exact string, allowed []string) bool {
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

// ContainsValue reports whether values contains want exactly.
func ContainsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// YAMLScalar emits a scalar frontmatter line, with no trailing space when
// the value is empty.
func YAMLScalar(key, value string) string {
	return yamlScalarLine(key, value)
}

// YAMLList emits a block list frontmatter entry, or an empty inline list.
func YAMLList(key string, values []string) string {
	if len(values) == 0 {
		return key + ": []"
	}
	var out strings.Builder
	out.WriteString(key)
	out.WriteString(":\n")
	for _, value := range values {
		out.WriteString("  - \"")
		out.WriteString(EscapeValue(value))
		out.WriteString("\"\n")
	}
	return strings.TrimSuffix(out.String(), "\n")
}

// OptionalScalar emits a scalar line, or nothing when the value is empty.
func OptionalScalar(key, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return fmt.Sprintf("%s: %s", key, value)
}

// EscapeValue escapes double quotes for quoted YAML emission.
func EscapeValue(value string) string {
	return strings.ReplaceAll(value, `"`, `\"`)
}

// CleanSlug lowercases and kebab-cases a record slug.
func CleanSlug(value string) string {
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

// DateFromStem extracts a leading YYYY-MM-DD date from a filename stem.
func DateFromStem(stem string) string {
	if len(stem) >= len("2006-01-02") {
		candidate := stem[:len("2006-01-02")]
		if _, err := time.Parse("2006-01-02", candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// SplitDatePrefix splits a YYYY-MM-DD- prefix off a slug when present.
func SplitDatePrefix(slug string) (string, string) {
	if len(slug) <= len("2006-01-02-") || slug[len("2006-01-02")] != '-' {
		return "", slug
	}
	date := DateFromStem(slug)
	if date == "" {
		return "", slug
	}
	return date, slug[len("2006-01-02-"):]
}

// TitleFromSlug derives a title from a (possibly date-prefixed) slug.
func TitleFromSlug(slug string) string {
	if _, rest := SplitDatePrefix(slug); rest != slug {
		slug = rest
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

// First returns the first non-empty value.
func First(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
