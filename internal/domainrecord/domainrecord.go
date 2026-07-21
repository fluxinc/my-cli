// Package domainrecord implements generic manifest-routed Markdown records.
// Domain policy and repository authority stay in the manifest/Git host; this
// package only creates and reads canonical additive record files.
package domainrecord

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/record"
	"github.com/fluxinc/my-cli/internal/safefs"
)

type Item struct {
	Manifest  string            `json:"manifest"`
	Workspace string            `json:"workspace"`
	Domain    string            `json:"domain"`
	ID        string            `json:"id"`
	Date      string            `json:"date"`
	Title     string            `json:"title"`
	Status    string            `json:"status,omitempty"`
	Actor     string            `json:"actor,omitempty"`
	Sources   []string          `json:"sources,omitempty"`
	Related   []string          `json:"related,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	Path      string            `json:"path"`
}

type AddOptions struct {
	Date    string
	Title   string
	Status  string
	Actor   string
	Sources []string
	Related []string
	Fields  map[string]string
	Now     time.Time
	DryRun  bool
}

var reservedFields = map[string]bool{
	"schema_version": true, "id": true, "domain": true, "date": true,
	"title": true, "status": true, "actor": true, "sources": true,
	"related": true, "created_at": true,
}

func Add(root record.Root, domain manifest.RecordDomain, slug string, opts AddOptions) (Item, string, error) {
	slug = record.CleanSlug(slug)
	if slug == "" {
		return Item{}, "", fmt.Errorf("record slug is required")
	}
	date := strings.TrimSpace(opts.Date)
	if date == "" {
		date, slug = record.SplitDatePrefix(slug)
	}
	if date == "" {
		now := opts.Now
		if now.IsZero() {
			now = time.Now()
		}
		date = now.Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return Item{}, "", fmt.Errorf("record date must be YYYY-MM-DD")
	}
	if strings.HasPrefix(slug, date+"-") {
		slug = strings.TrimPrefix(slug, date+"-")
	}
	id := date + "-" + slug
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = record.TitleFromSlug(slug)
	}
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "draft"
	}
	created := opts.Now
	if created.IsZero() {
		created = time.Now()
	}
	created = created.UTC()
	fields := map[string]string{}
	for key, value := range opts.Fields {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || reservedFields[key] || !portableFieldName(key) {
			return Item{}, "", fmt.Errorf("custom field %q is reserved or invalid", key)
		}
		if value != "" {
			fields[key] = value
		}
	}
	item := Item{
		Manifest: root.Manifest, Workspace: root.Workspace, Domain: domain.ID,
		ID: id, Date: date, Title: title, Status: status,
		Actor: strings.TrimSpace(opts.Actor), Sources: compact(opts.Sources),
		Related: compact(opts.Related), Fields: fields,
		Path: filepath.Join(root.Path, filepath.FromSlash(domain.Path), id+".md"),
	}
	content := scaffold(item, created)
	if opts.DryRun {
		return item, content, nil
	}
	if err := writeNewDurable(item.Path, []byte(content)); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Item{}, "", fmt.Errorf("record %s already exists", item.Path)
		}
		return Item{}, "", err
	}
	return item, content, nil
}

func List(roots []record.Root, domain manifest.RecordDomain) ([]Item, error) {
	items, err := record.Scan(roots, domain.Path, func(root record.Root, path string) (Item, error) {
		return parse(root, domain, path)
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Date != items[j].Date {
			return items[i].Date > items[j].Date
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func Get(roots []record.Root, domain manifest.RecordDomain, id string) (Item, string, error) {
	id = strings.TrimSuffix(filepath.Base(strings.TrimSpace(id)), ".md")
	if id == "" || id == "." {
		return Item{}, "", fmt.Errorf("record id is required")
	}
	var found []Item
	for _, root := range roots {
		path := filepath.Join(root.Path, filepath.FromSlash(domain.Path), id+".md")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return Item{}, "", err
		}
		item, err := parse(root, domain, path)
		if err != nil {
			return Item{}, "", err
		}
		found = append(found, item)
	}
	if len(found) == 0 {
		return Item{}, "", fmt.Errorf("record %q not found in domain %q", id, domain.ID)
	}
	if len(found) > 1 {
		return Item{}, "", fmt.Errorf("record %q exists in multiple roots; pass --manifest and --workspace", id)
	}
	data, err := os.ReadFile(found[0].Path)
	return found[0], string(data), err
}

func parse(root record.Root, domain manifest.RecordDomain, path string) (Item, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Item{}, err
	}
	fm, _ := record.SplitFrontmatter(data)
	item := Item{
		Manifest: root.Manifest, Workspace: root.Workspace, Domain: record.FirstValue(fm, "domain"),
		ID: record.FirstValue(fm, "id"), Date: record.FirstValue(fm, "date"),
		Title: record.FirstValue(fm, "title"), Status: record.FirstValue(fm, "status"),
		Actor: record.FirstValue(fm, "actor"), Sources: record.Values(fm, "sources"),
		Related: record.Values(fm, "related"), Fields: record.ScalarFields(fm), Path: path,
	}
	if item.ID == "" {
		item.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if item.Domain != domain.ID {
		return Item{}, fmt.Errorf("record %s declares domain %q, expected %q", path, item.Domain, domain.ID)
	}
	for key := range reservedFields {
		delete(item.Fields, key)
	}
	return item, nil
}

func scaffold(item Item, created time.Time) string {
	var out bytes.Buffer
	fmt.Fprintln(&out, "---")
	fmt.Fprintln(&out, "schema_version: 1")
	fmt.Fprintln(&out, record.YAMLScalar("id", item.ID))
	fmt.Fprintln(&out, record.YAMLScalar("domain", item.Domain))
	fmt.Fprintln(&out, record.YAMLScalar("date", item.Date))
	fmt.Fprintln(&out, record.YAMLScalar("title", item.Title))
	fmt.Fprintln(&out, record.YAMLScalar("status", item.Status))
	if item.Actor != "" {
		fmt.Fprintln(&out, record.YAMLScalar("actor", item.Actor))
	}
	fmt.Fprintln(&out, record.YAMLList("sources", item.Sources))
	fmt.Fprintln(&out, record.YAMLList("related", item.Related))
	keys := make([]string, 0, len(item.Fields))
	for key := range item.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintln(&out, record.YAMLScalar(key, item.Fields[key]))
	}
	fmt.Fprintln(&out, record.YAMLScalar("created_at", created.Format(time.RFC3339Nano)))
	fmt.Fprintln(&out, "---")
	fmt.Fprintf(&out, "\n# %s\n\n## Summary\n\n## Evidence\n\n## Assessment\n\n## Follow-up\n", item.Title)
	return out.String()
}

func writeNewDurable(path string, data []byte) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	remove := true
	defer func() {
		_ = f.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := safefs.SyncDirectory(parent); err != nil {
		return err
	}
	remove = false
	return nil
}

func compact(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			out = append(out, value)
			seen[value] = true
		}
	}
	return out
}

func portableFieldName(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return value != ""
}
