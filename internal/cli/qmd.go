package cli

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
)

type qmdSearchResult struct {
	File    string  `json:"file"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

func runQMDContentSearch[T any](items []T, query, dirToken string, path func(T) string, withSnippet func(T, string) T) ([]T, bool) {
	qmd, err := exec.LookPath("qmd")
	if err != nil {
		return nil, false
	}
	index := map[string]T{}
	for _, item := range items {
		for _, key := range qmdContentKeys(path(item)) {
			if _, exists := index[key]; !exists {
				index[key] = item
			}
		}
	}
	if len(index) == 0 {
		return nil, false
	}
	cmd := exec.Command(qmd, "search", query, "--json", "-n", "100")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || strings.HasPrefix(trimmed, "No results found") {
		return nil, true
	}
	var results []qmdSearchResult
	if err := json.Unmarshal([]byte(trimmed), &results); err != nil {
		return nil, false
	}
	var found []T
	seen := map[string]bool{}
	for _, result := range results {
		item, ok := matchQMDContent(index, result.File, dirToken)
		if !ok || seen[path(item)] {
			continue
		}
		if snippet := cleanQMDSnippet(result.Snippet); snippet != "" {
			item = withSnippet(item, snippet)
		}
		found = append(found, item)
		seen[path(item)] = true
	}
	return found, true
}

func qmdContentKeys(path string) []string {
	var keys []string
	add := func(value string) {
		value = strings.ToLower(filepath.ToSlash(value))
		if value != "" {
			keys = append(keys, value)
		}
	}
	add(path)
	if abs, err := filepath.Abs(path); err == nil {
		add(abs)
	}
	root := filepath.Dir(filepath.Dir(path))
	if rel, err := filepath.Rel(root, path); err == nil {
		add(rel)
	}
	if rel, err := filepath.Rel(filepath.Dir(root), path); err == nil {
		add(rel)
	}
	return keys
}

func matchQMDContent[T any](index map[string]T, file, dirToken string) (T, bool) {
	for _, key := range qmdContentResultKeys(file, dirToken) {
		if item, ok := index[key]; ok {
			return item, true
		}
	}
	var zero T
	return zero, false
}

func qmdContentResultKeys(file, dirToken string) []string {
	file = strings.TrimSpace(file)
	var keys []string
	add := func(value string) {
		value = strings.ToLower(filepath.ToSlash(value))
		if value != "" {
			keys = append(keys, value)
		}
	}
	add(file)
	withoutScheme := strings.TrimPrefix(file, "qmd://")
	add(withoutScheme)
	if i := strings.Index(withoutScheme, "/"); i >= 0 {
		rel := withoutScheme[i+1:]
		add(rel)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		for i, part := range parts {
			if part != dirToken {
				continue
			}
			add(strings.Join(parts[i:], "/"))
			if i > 0 {
				add(strings.Join(parts[i-1:], "/"))
			}
		}
	}
	return keys
}

func cleanQMDSnippet(snippet string) string {
	for _, line := range strings.Split(snippet, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "@@") {
			continue
		}
		return line
	}
	return ""
}

func mergeContentResults[T any](primary, fallback []T, path func(T) string) []T {
	out := append([]T(nil), primary...)
	seen := map[string]bool{}
	for _, item := range primary {
		seen[path(item)] = true
	}
	for _, item := range fallback {
		if !seen[path(item)] {
			out = append(out, item)
			seen[path(item)] = true
		}
	}
	return out
}
