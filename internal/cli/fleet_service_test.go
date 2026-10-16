package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fluxinc/my-cli/internal/recordindex"
)

const fakeRegistryToken = "fake-registry-token-91c2"

// fakeRegistry is a minimal records-http/v1 service: exact resolve, search,
// atomic multi-field PATCH guarded by every revision field, and transitions.
type fakeRegistry struct {
	mu      sync.Mutex
	records map[string]map[string]any
	fields  map[string]string // writable field -> path in the record ("box.serial_number")
	patches int
	feed    []map[string]any // the change feed: {id, op}
	// links: record id + reference -> body; linkPosts counts every link
	// request; staleLinks makes that many link requests find the record
	// changed first.
	links      map[string]string
	linkPosts  int
	linkKeys   []string
	staleLinks int
	// redirectOnRefresh makes the GET after a stale link answer with
	// another record's id.
	redirectOnRefresh string
}

// touch records a change in the feed, as the real service does on writes.
func (f *fakeRegistry) touch(id, op string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feed = append(f.feed, map[string]any{"id": id, "op": op})
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		records: map[string]map[string]any{
			"reg:box:1": {
				"id": "reg:box:1", "revision": 3, "order_revision": 1, "stage": "install", "archived": false,
				"site": "Main campus", "ship_to": "Dock 1",
				"customer":    map[string]any{"name": "Sample Clinic", "workspace_customer_id": "sampleco"},
				"order":       map[string]any{"so_number": "SO-1001", "partner": "integratorco"},
				"box":         map[string]any{"label": "acme-box-1", "serial_number": "SN-1", "device_model": "Unit X"},
				"identifiers": map[string]any{"fl": []any{map[string]any{"value": "400-0001"}}},
				"sources":     []any{map[string]any{"source": "legacy", "source_id": "acme-box-1"}},
				"notes":       []any{map[string]any{"written_at": "2026-10-01T10:00:00Z", "author": "Alex", "body": "Waiting on the site network."}},
			},
			"reg:box:2": {
				"id": "reg:box:2", "revision": 7, "order_revision": 2, "stage": "build", "archived": false,
				"customer": map[string]any{"name": "Other Clinic"},
				"order":    map[string]any{"so_number": "SHARED-1"},
				"box":      map[string]any{"label": "acme-box-2", "serial_number": "SN-2"},
			},
		},
		fields: map[string]string{"serial": "box.serial_number", "model": "box.device_model", "ship_to": "ship_to"},
	}
}

func (f *fakeRegistry) identifiers(rec map[string]any) []string {
	var out []string
	add := func(v any) {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, strings.ToLower(s))
		}
	}
	add(rec["id"])
	for _, part := range []string{"order", "box"} {
		for _, v := range rec[part].(map[string]any) {
			add(v)
		}
	}
	if ids, ok := rec["identifiers"].(map[string]any); ok {
		for _, list := range ids {
			for _, item := range list.([]any) {
				add(item.(map[string]any)["value"])
			}
		}
	}
	if sources, ok := rec["sources"].([]any); ok {
		for _, item := range sources {
			add(item.(map[string]any)["source_id"])
		}
	}
	return out
}

func (f *fakeRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+fakeRegistryToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	reply := func(status int, body any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	switch {
	case r.Method == http.MethodGet && path == "records/resolve":
		q := strings.ToLower(r.URL.Query().Get("identifier"))
		var candidates []any
		for _, id := range sortedRegistryIDs(f.records) {
			for _, ident := range f.identifiers(f.records[id]) {
				if ident == q {
					candidates = append(candidates, map[string]any{"id": id})
					break
				}
			}
		}
		reply(200, map[string]any{"candidates": candidates})
	case r.Method == http.MethodGet && path == "changes":
		start := 0
		fmt.Sscan(r.URL.Query().Get("after"), &start)
		end := start + 2
		if end > len(f.feed) {
			end = len(f.feed)
		}
		reply(200, map[string]any{"changes": f.feed[start:end], "cursor": fmt.Sprint(end), "more": end < len(f.feed)})
	case r.Method == http.MethodGet && path == "records" && r.URL.Query().Has("after"):
		after := r.URL.Query().Get("after")
		var page []any
		next := any(nil)
		for _, id := range sortedRegistryIDs(f.records) {
			if id <= after {
				continue
			}
			if len(page) == 2 {
				next = page[1].(map[string]any)["id"]
				break
			}
			page = append(page, f.records[id])
		}
		reply(200, map[string]any{"records": page, "next_after": next})
	case r.Method == http.MethodGet && path == "records":
		q := strings.ToLower(r.URL.Query().Get("q"))
		var out []any
		for _, id := range sortedRegistryIDs(f.records) {
			blob, _ := json.Marshal(f.records[id])
			if q == "" || strings.Contains(strings.ToLower(string(blob)), q) {
				out = append(out, f.records[id])
			}
		}
		reply(200, map[string]any{"records": out, "page": 1, "next_page": nil})
	case r.Method == http.MethodPost && strings.HasPrefix(path, "records/") && strings.HasSuffix(path, "/notes"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "records/"), "/notes")
		rec, ok := f.records[id]
		if !ok {
			reply(404, map[string]any{"error": "not_found"})
			return
		}
		f.linkPosts++
		f.linkKeys = append(f.linkKeys, r.Header.Get("Idempotency-Key"))
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		ref, _ := json.Marshal(body["reference"])
		key := id + " " + string(ref)
		if f.links == nil {
			f.links = map[string]string{}
		}
		// As the service does: a replay is answered before revisions.
		if existing, ok := f.links[key]; ok {
			if existing != body["body"] {
				reply(409, map[string]any{"error": "reference_conflict"})
				return
			}
			reply(200, map[string]any{"note": map[string]any{"body": existing}})
			return
		}
		if f.staleLinks > 0 {
			f.staleLinks--
			rec["revision"] = toInt(rec["revision"]) + 1
		}
		for _, k := range []string{"revision", "order_revision"} {
			if fmt.Sprint(body[k]) != fmt.Sprint(rec[k]) {
				reply(409, map[string]any{"error": "stale", "record": rec})
				return
			}
		}
		f.links[key] = fmt.Sprint(body["body"])
		rec["revision"] = toInt(rec["revision"]) + 1
		reply(201, map[string]any{"note": map[string]any{"body": body["body"]}})
	case strings.HasPrefix(path, "records/"):
		rest := strings.TrimPrefix(path, "records/")
		id, action, _ := strings.Cut(rest, "/")
		rec, ok := f.records[id]
		if !ok {
			reply(404, map[string]any{"error": "not_found"})
			return
		}
		var body map[string]any
		if r.Method != http.MethodGet {
			json.NewDecoder(r.Body).Decode(&body)
			for _, key := range []string{"revision", "order_revision"} {
				if fmt.Sprint(body[key]) != fmt.Sprint(rec[key]) {
					reply(409, map[string]any{"error": "stale", "message": "Someone else changed this row; token " + fakeRegistryToken, "record": rec})
					return
				}
			}
		}
		switch {
		case r.Method == http.MethodGet && action == "":
			if f.redirectOnRefresh != "" && f.linkPosts > 0 {
				reply(200, f.records[f.redirectOnRefresh])
				return
			}
			reply(200, rec)
		case r.Method == http.MethodPatch && action == "":
			changes, _ := body["changes"].(map[string]any)
			if so, ok := changes["so"]; ok {
				confirmed := fmt.Sprint(body["confirmed_ids"])
				if confirmed != "[reg:box:1 reg:box:9]" {
					reply(409, map[string]any{"error": "confirmation_required", "message": "SO is shared by 2 records",
						"affected": []string{"reg:box:1", "reg:box:9"}, "review_url": "https://registry.example.test/?q=SO-1001"})
					return
				}
				rec["order"].(map[string]any)["so_number"] = so
				delete(changes, "so")
			}
			for field := range changes {
				if _, ok := f.fields[field]; !ok {
					reply(422, map[string]any{"error": "not_editable", "field": field})
					return
				}
			}
			for field, value := range changes {
				target := f.fields[field]
				if part, key, nested := strings.Cut(target, "."); nested {
					rec[part].(map[string]any)[key] = value
				} else {
					rec[target] = value
				}
			}
			rec["revision"] = toInt(rec["revision"]) + 1
			f.patches++
			reply(200, rec)
		case r.Method == http.MethodPost && action == "transitions":
			if body["operation"] != "advance" {
				reply(422, map[string]any{"error": "refused", "messages": []string{"Unknown action."}})
				return
			}
			rec["stage"] = "live"
			rec["revision"] = toInt(rec["revision"]) + 1
			reply(200, rec)
		default:
			reply(404, map[string]any{"error": "not_found"})
		}
	default:
		reply(404, map[string]any{"error": "not_found"})
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

func sortedRegistryIDs(m map[string]map[string]any) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids
}

// setupServiceFleet registers a manifest that binds fleet to the fake
// registry and to a markdown mount holding a pointer, a record of another
// kind and a support record.
func setupServiceFleet(t *testing.T, serverURL string) (string, app, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv("ACME_REGISTRY_TOKEN", fakeRegistryToken)
	home := t.TempDir()
	manifestCache := filepath.Join(home, ".local", "share", "my-cli", "manifests", "acme")
	workspaceRoot := filepath.Join(home, ".my-cli", "workspaces", "handbook")
	if err := os.MkdirAll(manifestCache, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, filepath.Join(manifestCache, "manifest.json"), fmt.Sprintf(`{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme Example" },
  "workspaces": [
    { "id": "handbook", "git_url": "https://github.com/acme/acme-handbook.git", "local_path": "~/.my-cli/workspaces/handbook" }
  ],
  "services": [
    { "id": "registry", "kind": "http", "purpose": "Instance registry", "auth_ref": "env://ACME_REGISTRY_TOKEN",
      "connection": { "type": "http", "url": %q } }
  ],
  "data_bindings": {
    "fleet": { "surfaces": ["service:registry", "mount:handbook"], "field_aliases": { "device": "model" } }
  }
}`, serverURL+"/api/v1"))
	writeCLITestFile(t, filepath.Join(workspaceRoot, "fleet", "acme-box-1.md"), `---
id: acme-box-1
status: live
serial: SN-OLD
identifiers: ["SO-1001"]
managed_by: service:registry
managed_id: reg:box:1
source: fleet
---

# acme-box-1

Moved to the registry. These values are history, not current.
`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "fleet", "printer-7.md"), `---
id: printer-7
status: live
device: Document Unit
identifiers: ["SHARED-1"]
source: fleet
---

# printer-7

A different product, still kept in markdown.
`)
	writeCLITestFile(t, filepath.Join(workspaceRoot, "support", "2026-10-02-network.md"), `---
id: 2026-10-02-network
date: 2026-10-02
title: "Site network"
identifiers: [400-0001]
status: open
source: support
---

Waiting on the site network.
`)
	var stdout, stderr bytes.Buffer
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "manifests", "add", "acme", "https://github.com/acme/acme-ai-manifest.git", "--home", home}); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	return home, a, &stdout, &stderr
}

func TestServiceBackedFleetReadsLiveAndFailsClosed(t *testing.T) {
	registry := newFakeRegistry()
	server := httptest.NewServer(registry)
	defer server.Close()
	home, a, stdout, _ := setupServiceFleet(t, server.URL)

	// An identifier resolves in the service; current values, notes and
	// related support (by an identifier only the service record carries).
	if err := a.run([]string{"my", "fleet", "get", "SO-1001", "--home", home, "--manifest", "acme"}); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"# reg:box:1", "surface: service:registry", "serial: SN-1", "status: install", "Waiting on the site network.", "2026-10-02-network"} {
		if !strings.Contains(out, want) {
			t.Fatalf("get output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SN-OLD") {
		t.Fatalf("get showed the pointer's stale values:\n%s", out)
	}

	// The pointer's own id follows managed_id to the service.
	stdout.Reset()
	if err := a.run([]string{"my", "fleet", "get", "acme-box-1", "--home", home, "--manifest", "acme", "--json"}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Record struct {
			ID      string `json:"id"`
			Surface string `json:"surface"`
		} `json:"record"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Record.ID != "reg:box:1" || got.Record.Surface != "service:registry" {
		t.Fatalf("pointer get = %+v, %v\n%s", got, err, stdout.String())
	}

	// list merges both surfaces and hides the pointer.
	stdout.Reset()
	if err := a.run([]string{"my", "fleet", "list", "--home", home, "--manifest", "acme"}); err != nil {
		t.Fatal(err)
	}
	out = stdout.String()
	if !strings.Contains(out, "reg:box:1") || !strings.Contains(out, "reg:box:2") || !strings.Contains(out, "printer-7") || strings.Contains(out, "acme-box-1\t") {
		t.Fatalf("list output:\n%s", out)
	}

	// One identifier naming a service record and a markdown record is
	// ambiguous: listed, never resolved to either.
	stdout.Reset()
	err := a.run([]string{"my", "fleet", "get", "SHARED-1", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "reg:box:2") || !strings.Contains(err.Error(), "printer-7") {
		t.Fatalf("ambiguous get err = %v", err)
	}

	// The markdown record of another kind still reads as before.
	stdout.Reset()
	if err := a.run([]string{"my", "fleet", "get", "printer-7", "--home", home, "--manifest", "acme"}); err != nil || !strings.Contains(stdout.String(), "A different product") {
		t.Fatalf("local get = %v\n%s", err, stdout.String())
	}

	// A service that cannot answer fails the command; nothing local stands in.
	server.Close()
	for _, args := range [][]string{
		{"my", "fleet", "get", "acme-box-1", "--home", home, "--manifest", "acme"},
		{"my", "fleet", "get", "printer-7", "--home", home, "--manifest", "acme"},
		{"my", "fleet", "list", "--home", home, "--manifest", "acme"},
	} {
		stdout.Reset()
		err := a.run(args)
		if err == nil || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("%v with the service down: err = %v, out = %s", args, err, stdout.String())
		}
	}
}

func TestServiceBackedFleetWritesAtomicallyAndThroughTransitions(t *testing.T) {
	registry := newFakeRegistry()
	server := httptest.NewServer(registry)
	defer server.Close()
	home, a, stdout, stderr := setupServiceFleet(t, server.URL)

	// Several fields, one request, revisions echoed; the alias maps device.
	if err := a.run([]string{"my", "fleet", "set", "SO-1001", "serial=SN-9", "device=Unit Y", "--home", home, "--manifest", "acme"}); err != nil {
		t.Fatal(err)
	}
	box := registry.records["reg:box:1"]["box"].(map[string]any)
	if box["serial_number"] != "SN-9" || box["device_model"] != "Unit Y" || registry.patches != 1 {
		t.Fatalf("after set: box = %v, patches = %d", box, registry.patches)
	}
	if strings.Contains(stdout.String(), "my sync") {
		t.Fatalf("service write suggested a sync:\n%s", stdout.String())
	}

	// An unknown field is refused by the service and nothing is saved.
	err := a.run([]string{"my", "fleet", "set", "SO-1001", "serial=SN-10", "colour=blue", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "colour") || box["serial_number"] != "SN-9" {
		t.Fatalf("unknown field: err = %v, serial = %v", err, box["serial_number"])
	}

	// A shared field reports confirmation, not staleness.
	err = a.run([]string{"my", "fleet", "set", "SO-1001", "so=SO-2000", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "confirmation required") || strings.Contains(err.Error(), "someone changed") ||
		!strings.Contains(err.Error(), "reg:box:1, reg:box:9") || !strings.Contains(err.Error(), "https://registry.example.test/?q=SO-1001") {
		t.Fatalf("shared field err = %v", err)
	}

	// A wrong confirmation is refused again; the listed ids apply it.
	err = a.run([]string{"my", "fleet", "set", "SO-1001", "so=SO-2000", "--confirm", "reg:box:1", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "confirmation required") || !strings.Contains(err.Error(), "--confirm reg:box:1 --confirm reg:box:9") {
		t.Fatalf("partial confirm err = %v", err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "fleet", "set", "SO-1001", "so=SO-2000", "--confirm", "reg:box:1", "--confirm", "reg:box:9", "--home", home, "--manifest", "acme"}); err != nil {
		t.Fatal(err)
	}
	if got := registry.records["reg:box:1"]["order"].(map[string]any)["so_number"]; got != "SO-2000" {
		t.Fatalf("confirmed shared change: so = %v", got)
	}
	err = a.run([]string{"my", "fleet", "set", "printer-7", "status=x", "--confirm", "reg:box:1", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "markdown record") {
		t.Fatalf("confirm on markdown err = %v", err)
	}

	// Workflow state changes only through transition.
	err = a.run([]string{"my", "fleet", "set", "SO-1001", "status=live", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "my fleet transition") {
		t.Fatalf("status set err = %v", err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "fleet", "transition", "SO-1001", "advance", "occurred_on=2026-10-12", "--home", home, "--manifest", "acme"}); err != nil {
		t.Fatal(err)
	}
	if registry.records["reg:box:1"]["stage"] != "live" || !strings.Contains(stdout.String(), "-> live") {
		t.Fatalf("transition: stage = %v, out = %s", registry.records["reg:box:1"]["stage"], stdout.String())
	}

	// A stale revision names the conflict and never echoes the credential:
	// someone else saves between set's read and its write.
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			registry.mu.Lock()
			registry.records["reg:box:2"]["revision"] = 8
			registry.mu.Unlock()
		}
		registry.ServeHTTP(w, r)
	})
	err = a.run([]string{"my", "fleet", "set", "reg:box:2", "serial=SN-3", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "someone changed this record first") || strings.Contains(err.Error(), fakeRegistryToken) {
		t.Fatalf("stale set err = %v", err)
	}
	if strings.Contains(stdout.String()+stderr.String(), fakeRegistryToken) {
		t.Fatal("the credential reached the output")
	}

	// Markdown stays writable for records of another kind; add needs a target.
	stdout.Reset()
	if err := a.run([]string{"my", "fleet", "set", "printer-7", "status=retired", "--home", home, "--manifest", "acme"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "my sync --push") {
		t.Fatalf("markdown set output:\n%s", stdout.String())
	}
	err = a.run([]string{"my", "fleet", "add", "printer-8", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("add without a target err = %v", err)
	}
	stdout.Reset()
	if err := a.run([]string{"my", "fleet", "add", "printer-8", "--workspace", "handbook", "--home", home, "--manifest", "acme"}); err != nil {
		t.Fatal(err)
	}
}

// Affected ids are opaque: --confirm sends each exactly (commas, spaces and
// quotes included), and the printed rerun quotes each one for the shell.
func TestFleetConfirmationRoundTripsOpaqueAffectedIDs(t *testing.T) {
	registry := newFakeRegistry()
	affected := []string{"reg:box:1", "reg:box:9,part", " box 9's ", "a$(b)"}
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			registry.ServeHTTP(w, r)
			return
		}
		var body struct {
			Confirmed []string `json:"confirmed_ids"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		sent = body.Confirmed
		w.Header().Set("Content-Type", "application/json")
		if strings.Join(sent, "\x00") != strings.Join(affected, "\x00") {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": "confirmation_required", "affected": affected})
			return
		}
		json.NewEncoder(w).Encode(registry.records["reg:box:1"])
	}))
	defer server.Close()
	home, a, _, _ := setupServiceFleet(t, server.URL)

	err := a.run([]string{"my", "fleet", "set", "reg:box:1", "so=SO-2000", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), `--confirm reg:box:1 --confirm 'reg:box:9,part' --confirm ' box 9'\''s ' --confirm 'a$(b)'`) {
		t.Fatalf("rerun hint = %v", err)
	}
	args := []string{"my", "fleet", "set", "reg:box:1", "so=SO-2000", "--home", home, "--manifest", "acme"}
	for _, id := range affected {
		args = append(args, "--confirm", id)
	}
	if err := a.run(args); err != nil || strings.Join(sent, "\x00") != strings.Join(affected, "\x00") {
		t.Fatalf("sent %q, want %q exactly (err %v)", sent, affected, err)
	}
}

// textQMD is a stand-in semantic engine: a document matches when it contains
// every word of the query. It reads only the generation's documents.
type textQMD struct{}

func (textQMD) Build(context.Context, string) error  { return nil }
func (textQMD) Update(context.Context, string) error { return nil }
func (textQMD) Query(_ context.Context, genDir, text string, limit int) ([]recordindex.QueryResult, error) {
	entries, _ := os.ReadDir(filepath.Join(genDir, "docs"))
	var out []recordindex.QueryResult
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(genDir, "docs", e.Name()))
		matches := true
		for _, word := range strings.Fields(strings.ToLower(text)) {
			if !strings.Contains(strings.ToLower(string(data)), word) {
				matches = false
			}
		}
		if matches {
			out = append(out, recordindex.QueryResult{File: e.Name(), Score: 0.8})
		}
	}
	return out, nil
}

func TestServiceBackedFleetSemanticSearchShowsOnlyCurrentValues(t *testing.T) {
	registry := newFakeRegistry()
	for _, id := range sortedRegistryIDs(registry.records) {
		registry.touch(id, "upsert")
	}
	server := httptest.NewServer(registry)
	defer server.Close()
	previous := fleetQMD
	fleetQMD = func() recordindex.QMD { return textQMD{} }
	defer func() { fleetQMD = previous }()
	home, a, stdout, stderr := setupServiceFleet(t, server.URL)
	run := func(args ...string) error {
		stdout.Reset()
		stderr.Reset()
		return a.run(append(args, "--home", home, "--manifest", "acme"))
	}

	// No index yet: the service's search only, and it says so.
	if err := run("my", "fleet", "search", "site network"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "no semantic index yet") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	if err := run("my", "fleet", "index"); err != nil || !strings.Contains(stdout.String(), "built: 2 added") {
		t.Fatalf("index = %v: %s", err, stdout.String())
	}

	// The note "Waiting on the site network." is found by meaning (the
	// service's own search here is a plain substring match on the JSON, so
	// use words in another order). The hit shows the record as it is now:
	// its serial changed after indexing, without a feed entry.
	registry.mu.Lock()
	registry.records["reg:box:1"]["box"].(map[string]any)["serial_number"] = "SN-NOW"
	registry.mu.Unlock()
	if err := run("my", "fleet", "search", "network site", "--json"); err != nil {
		t.Fatal(err)
	}
	var found []struct {
		ID      string `json:"id"`
		Serial  string `json:"serial"`
		Match   string `json:"match"`
		Snippet string `json:"snippet"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &found); err != nil {
		t.Fatalf("%v: %s", err, stdout.String())
	}
	if len(found) != 1 || found[0].ID != "reg:box:1" || found[0].Match != "semantic" || found[0].Serial != "SN-NOW" ||
		strings.Contains(found[0].Snippet, "network") {
		t.Fatalf("semantic hits = %+v", found)
	}

	// A record deleted in the service drops out even though the index still
	// has it (no feed entry yet).
	registry.mu.Lock()
	delete(registry.records, "reg:box:1")
	registry.mu.Unlock()
	if err := run("my", "fleet", "search", "network site", "--json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "reg:box:1") {
		t.Fatalf("a deleted record was shown: %s", stdout.String())
	}

	// A service that can't answer fails the search; the index never stands in.
	server.Close()
	if err := run("my", "fleet", "search", "network site"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("down service err = %v", err)
	}
}
