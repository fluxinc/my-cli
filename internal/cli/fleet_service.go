package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/fleet"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/recordindex"
	"github.com/fluxinc/my-cli/internal/recordsvc"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

// serviceBackedDataType reports the data types whose commands can read and
// write a bound service (docs/plans/2026-10-12-service-backed-fleet.md).
func serviceBackedDataType(dataType string) bool { return dataType == "fleet" }

// fleetAuthRunner runs the credential helper (op); tests replace it.
var fleetAuthRunner recordsvc.CommandRunner

const fleetServiceTimeout = 60 * time.Second

// fleetService is the service a fleet binding names, if any.
type fleetService struct {
	manifest string
	service  manifest.Service
	aliases  map[string]string
	client   *recordsvc.Client
}

func (s *fleetService) surface() string { return "service:" + s.service.ID }

// boundFleetService finds the service the selected manifest binds fleet to.
// It returns nil when fleet is not service-backed.
func boundFleetService(home, manifestName, umbrellaRoot string) (*fleetService, error) {
	docs, err := loadRegisteredDocs(home, bindingManifestName(home, manifestName, umbrellaRoot))
	if err != nil {
		return nil, err
	}
	var found []*fleetService
	for _, doc := range docs {
		binding, ok := doc.doc.DataBindings["fleet"]
		if !ok {
			continue
		}
		for _, ref := range binding.SurfaceRefs() {
			kind, id, ok := manifest.ParseSurfaceRef(ref)
			if !ok || kind != "service" {
				continue
			}
			for _, service := range doc.doc.Services {
				if service.ID == id {
					found = append(found, &fleetService{manifest: doc.ref.Name, service: service, aliases: binding.FieldAliases})
				}
			}
		}
	}
	switch len(found) {
	case 0:
		return nil, nil
	case 1:
		return found[0], nil
	default:
		var names []string
		for _, f := range found {
			names = append(names, f.manifest+":"+f.service.ID)
		}
		return nil, fmt.Errorf("fleet is bound to several services (%s); pass --manifest", strings.Join(names, ", "))
	}
}

// bindingManifestName picks the manifest whose bindings apply, the same way
// contentRoots does: an explicit --manifest, else the umbrella's manifest.
func bindingManifestName(home, manifestName, umbrellaRoot string) string {
	if manifestName != "" {
		return manifestName
	}
	root := ""
	if umbrellaRoot != "" {
		if resolved, err := resolveUmbrellaRoot(home, umbrellaRoot); err == nil {
			root = resolved
		}
	} else if found, ok := umbrella.FindRoot("."); ok {
		root = found
	}
	if root != "" {
		if ws, err := umbrella.LoadWorkspace(root); err == nil {
			return ws.ManifestRef
		}
	}
	return ""
}

// connect resolves the credential and builds the client on first use.
func (s *fleetService) connect() (*recordsvc.Client, error) {
	if s.client != nil {
		return s.client, nil
	}
	if s.service.Kind != "http" || s.service.Connection.URL == "" {
		return nil, fmt.Errorf("fleet is bound to service %s, which needs kind http and connection.url", s.service.ID)
	}
	token, err := recordsvc.ResolveAuth(s.service.AuthRef, fleetAuthRunner)
	if err != nil {
		return nil, fmt.Errorf("service %s: %w", s.service.ID, err)
	}
	client, err := recordsvc.NewClient(s.service.ID, s.service.Connection.URL, token)
	if err != nil {
		return nil, err
	}
	s.client = client
	return client, nil
}

func serviceContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), fleetServiceTimeout)
}

// fleetMatch is one record a reference names, on any surface.
type fleetMatch struct {
	local  *fleet.Record    // an unmanaged markdown record
	remote recordsvc.Record // a full service record
}

func (m fleetMatch) record(svc *fleetService) fleet.Record {
	if m.local != nil {
		return *m.local
	}
	return recordsvc.ToFleet(svc.service.ID, m.remote)
}

// resolveFleet finds every record key names. With a bound service, the key
// is resolved there (never guessed from its shape); markdown pointers follow
// the service, and a service that cannot answer fails the lookup rather than
// leaving only the local half.
func resolveFleet(roots []fleet.Root, svc *fleetService, key string) ([]fleetMatch, error) {
	var local []fleet.Record
	if len(roots) != 0 {
		found, err := fleet.Matches(roots, key)
		if err != nil {
			return nil, err
		}
		local = found
	}
	if svc == nil {
		var out []fleetMatch
		for i := range local {
			out = append(out, fleetMatch{local: &local[i]})
		}
		return out, nil
	}
	client, err := svc.connect()
	if err != nil {
		return nil, err
	}
	ctx, cancel := serviceContext()
	defer cancel()

	var out []fleetMatch
	seen := map[string]bool{}
	addRemote := func(id string) error {
		if seen[id] {
			return nil
		}
		rec, err := client.Get(ctx, id)
		if err != nil {
			return err
		}
		seen[id] = true
		out = append(out, fleetMatch{remote: rec})
		return nil
	}
	candidates, err := client.Resolve(ctx, key)
	if err != nil {
		return nil, err
	}
	for _, c := range candidates {
		if err := addRemote(c.ID()); err != nil {
			return nil, err
		}
	}
	for i := range local {
		rec := local[i]
		if !rec.Managed() {
			out = append(out, fleetMatch{local: &local[i]})
			continue
		}
		if rec.ManagedBy != svc.surface() {
			return nil, fmt.Errorf("fleet record %s is managed by %s, which is not bound here", rec.ID, rec.ManagedBy)
		}
		ids, err := pointerTargets(ctx, client, rec)
		if err != nil {
			return nil, fmt.Errorf("fleet record %s moved to %s: %w", rec.ID, rec.ManagedBy, err)
		}
		for _, id := range ids {
			if err := addRemote(id); err != nil {
				return nil, fmt.Errorf("fleet record %s moved to %s: %w", rec.ID, rec.ManagedBy, err)
			}
		}
	}
	return out, nil
}

// pointerTargets finds the service record a markdown pointer stands for: its
// managed_id, else its own id resolved there (the service keeps old ids).
func pointerTargets(ctx context.Context, client *recordsvc.Client, rec fleet.Record) ([]string, error) {
	if rec.ManagedID != "" {
		return []string{rec.ManagedID}, nil
	}
	candidates, err := client.Resolve(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("the service has no record for %s; add managed_id to the pointer", rec.ID)
	}
	var ids []string
	for _, c := range candidates {
		ids = append(ids, c.ID())
	}
	return ids, nil
}

// resolveOneFleet is resolveFleet that insists on exactly one record.
func resolveOneFleet(roots []fleet.Root, svc *fleetService, key string) (fleetMatch, error) {
	matches, err := resolveFleet(roots, svc, key)
	if err != nil {
		return fleetMatch{}, err
	}
	switch len(matches) {
	case 0:
		return fleetMatch{}, fmt.Errorf("fleet record %q not found", key)
	case 1:
		return matches[0], nil
	}
	lines := []string{fmt.Sprintf("fleet record %q is ambiguous; it names %d records:", key, len(matches))}
	for _, m := range matches {
		rec := m.record(svc)
		where := rec.Surface
		if rec.Path != "" {
			where += " " + rec.Path
		}
		lines = append(lines, fmt.Sprintf("  %s\t%s\t%s\t%s", rec.ID, rec.Status, rec.Customer, where))
	}
	lines = append(lines, "pass one of these ids")
	return fleetMatch{}, errors.New(strings.Join(lines, "\n"))
}

// serviceFleetRecords lists or searches the service, projected and filtered.
func serviceFleetRecords(svc *fleetService, query string, filter fleet.Filter) ([]fleet.Record, error) {
	client, err := svc.connect()
	if err != nil {
		return nil, err
	}
	ctx, cancel := serviceContext()
	defer cancel()
	found, err := client.Search(ctx, query, false)
	if err != nil {
		return nil, err
	}
	out := make([]fleet.Record, 0, len(found))
	for _, rec := range found {
		out = append(out, recordsvc.ToFleet(svc.service.ID, rec))
	}
	return fleet.FilterRecords(out, filter), nil
}

// withoutPointers drops markdown records that moved to a service.
func withoutPointers(records []fleet.Record) []fleet.Record {
	out := records[:0:0]
	for _, rec := range records {
		if !rec.Managed() {
			out = append(out, rec)
		}
	}
	return out
}

// serviceChanges maps set keys through the binding's field_aliases. Other
// keys pass through as the service's own field names; the service refuses
// unknown ones and, being atomic, then saves nothing.
func (s *fleetService) serviceChanges(updates map[string]string) (map[string]string, error) {
	changes := map[string]string{}
	for key, value := range updates {
		if key == "status" || key == "stage" {
			return nil, fmt.Errorf("%s is a workflow state in %s; use my fleet transition <id> <operation> instead of set", key, s.service.ID)
		}
		if alias, ok := s.aliases[key]; ok {
			key = alias
		}
		if _, dup := changes[key]; dup {
			return nil, fmt.Errorf("%s is set twice (directly and through an alias)", key)
		}
		changes[key] = value
	}
	return changes, nil
}

func sortedUpdateKeys(updates map[string]string) []string {
	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// describeServiceError adds the next step to a service refusal.
func describeServiceError(svc *fleetService, err error) error {
	var apiErr *recordsvc.Error
	if svc == nil || !errors.As(err, &apiErr) {
		return err
	}
	switch {
	case apiErr.Code == "confirmation_required":
		msg := fmt.Sprintf("%v\nconfirmation required: this change also applies to other records; nothing was saved", apiErr)
		if len(apiErr.Affected) != 0 {
			msg += "\naffected: " + strings.Join(apiErr.Affected, ", ")
		}
		if apiErr.ReviewURL != "" {
			msg += "\nreview them in " + svc.service.ID + ": " + apiErr.ReviewURL
		}
		if len(apiErr.Affected) != 0 {
			var flags []string
			for _, id := range apiErr.Affected {
				flags = append(flags, "--confirm "+shellQuote(id))
			}
			msg += "\nto apply it to all of them, run the same set again with " + strings.Join(flags, " ")
		} else {
			msg += "\nmake the change in " + svc.service.ID + "'s own UI"
		}
		return errors.New(msg)
	case apiErr.Stale():
		msg := fmt.Sprintf("%v\nsomeone changed this record first; nothing was saved", apiErr)
		if apiErr.Record != nil {
			rec := recordsvc.ToFleet(svc.service.ID, apiErr.Record)
			msg += fmt.Sprintf("\ncurrent: status=%s serial=%s ship_to=%q", rec.Status, rec.Serial, rec.ShipTo)
		}
		return errors.New(msg + "\nread it again with my fleet get and retry")
	case apiErr.Unavailable():
		return fmt.Errorf("%v\nfleet records in %s cannot be read or changed until it answers; no local copy is used", apiErr, svc.service.ID)
	}
	return apiErr
}

// renderServiceFleetRecord prints a service record the way a markdown fleet
// record reads: the registry fields, then notes and field provenance.
func renderServiceFleetRecord(rec fleet.Record, raw recordsvc.Record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", rec.ID)
	for _, kv := range [][2]string{
		{"surface", rec.Surface}, {"status", rec.Status}, {"customer", rec.Customer}, {"partner", rec.Partner},
		{"device", rec.Device}, {"serial", rec.Serial}, {"config_repo", rec.ConfigRepo}, {"config_branch", rec.ConfigBranch},
		{"deployed_site", rec.DeployedSite}, {"ship_to", rec.ShipTo}, {"install_date", rec.InstallDate}, {"url", rec.URL},
	} {
		if kv[1] != "" {
			fmt.Fprintf(&b, "%s: %s\n", kv[0], kv[1])
		}
	}
	if rec.Archived {
		b.WriteString("archived: true\n")
	}
	if len(rec.Identifiers) != 0 {
		fmt.Fprintf(&b, "identifiers: %s\n", strings.Join(rec.Identifiers, ", "))
	}
	if len(rec.Contacts) != 0 {
		fmt.Fprintf(&b, "contacts: %s\n", strings.Join(rec.Contacts, "; "))
	}
	if notes, _ := raw["notes"].([]any); len(notes) != 0 {
		b.WriteString("\n## Notes\n\n")
		for _, item := range notes {
			note, _ := item.(map[string]any)
			fmt.Fprintf(&b, "- %v %v: %v\n", note["written_at"], note["author"], note["body"])
		}
	}
	if provenance, _ := raw["provenance"].(map[string]any); len(provenance) != 0 {
		b.WriteString("\n## Last written by\n\n")
		keys := make([]string, 0, len(provenance))
		for key := range provenance {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "- %s: %v\n", key, provenance[key])
		}
	}
	return b.String()
}

// setServiceFleet writes every update in one request against the revisions
// the record was read at. There is nothing local to publish afterwards.
func (a app) setServiceFleet(svc *fleetService, current recordsvc.Record, updates map[string]string, confirmed []string, jsonOut bool) error {
	changes, err := svc.serviceChanges(updates)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	client, err := svc.connect()
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	ctx, cancel := serviceContext()
	defer cancel()
	updated, err := client.Patch(ctx, current.ID(), changes, current.Revisions(), confirmed)
	if err != nil {
		return a.maybeJSONError(jsonOut, describeServiceError(svc, err))
	}
	rec := recordsvc.ToFleet(svc.service.ID, updated)
	if jsonOut {
		return printJSON(a.stdout, struct {
			Record        fleet.Record      `json:"record"`
			Changes       map[string]string `json:"changes"`
			ServiceRecord map[string]any    `json:"service_record"`
		}{Record: rec, Changes: changes, ServiceRecord: updated})
	}
	fmt.Fprintf(a.stdout, "updated %s in %s\n", rec.ID, svc.service.ID)
	for _, key := range sortedUpdateKeys(changes) {
		fmt.Fprintf(a.stdout, "  %s: %q\n", key, changes[key])
	}
	return nil
}

func (a app) runFleetTransition(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my fleet transition", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) < 2 {
		return fmt.Errorf("usage: my fleet transition <id|identifier> <operation> [KEY=VALUE...]")
	}
	params := map[string]string{}
	for _, pair := range rest[2:] {
		key, value, found := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" {
			return fmt.Errorf("fleet transition arguments must be KEY=VALUE, got %q", pair)
		}
		if key == "operation" || key == "revision" || strings.HasSuffix(key, "_revision") {
			return fmt.Errorf("%s is set by my fleet transition itself", key)
		}
		params[key] = strings.TrimSpace(value)
	}
	roots, err := fleetRoots(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	svc, err := boundFleetService(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if svc == nil {
		return a.maybeJSONError(opts.jsonOut, fmt.Errorf("fleet is not bound to a service; markdown records change status with my fleet set <id> status=<value>"))
	}
	match, err := resolveOneFleet(roots, svc, rest[0])
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, describeServiceError(svc, err))
	}
	if match.local != nil {
		return a.maybeJSONError(opts.jsonOut, fmt.Errorf("%s is a markdown record; change its status with my fleet set %s status=<value>", match.local.ID, match.local.ID))
	}
	client, err := svc.connect()
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	ctx, cancel := serviceContext()
	defer cancel()
	updated, err := client.Transition(ctx, match.remote.ID(), rest[1], params, match.remote.Revisions())
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, describeServiceError(svc, err))
	}
	rec := recordsvc.ToFleet(svc.service.ID, updated)
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record        fleet.Record   `json:"record"`
			ServiceRecord map[string]any `json:"service_record"`
		}{Record: rec, ServiceRecord: updated})
	}
	fmt.Fprintf(a.stdout, "%s: %s -> %s in %s\n", rec.ID, rest[1], rec.Status, svc.service.ID)
	return nil
}

// rawListFlag collects a repeated flag's values exactly as given: ids are
// opaque, so nothing is trimmed or split.
type rawListFlag []string

func (f *rawListFlag) String() string { return strings.Join(*f, " ") }

func (f *rawListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

// semanticSyncPages bounds the catch-up a search does before querying; a
// longer backlog means my fleet index, not a slow search.
const semanticSyncPages = 5

// fleetQMD is the semantic engine; tests replace it.
var fleetQMD = func() recordindex.QMD {
	home, _ := os.UserHomeDir()
	return recordindex.CommandQMD{ModelCache: filepath.Join(home, ".cache", "qmd", "models")}
}

func openFleetIndex(home string, svc *fleetService) (*recordindex.Index, *recordsvc.Client, error) {
	client, err := svc.connect()
	if err != nil {
		return nil, nil, err
	}
	resolved, err := resolveHome(home)
	if err != nil {
		return nil, nil, err
	}
	root := filepath.Join(resolved, ".local", "state", "my-cli", "records")
	source := recordindex.Source{Manifest: svc.manifest, ServiceID: svc.service.ID, BaseURL: svc.service.Connection.URL}
	render := func(rec recordsvc.Record) string {
		return renderServiceFleetRecord(recordsvc.ToFleet(svc.service.ID, rec), rec)
	}
	ix, err := recordindex.Open(root, source, client, fleetQMD(), render)
	return ix, client, err
}

// semanticFleetMatches adds records the local index finds by meaning. Each
// is re-read from the service (a gone or archived one is dropped) and shown
// with current values only. When the index can't answer for a reason other
// than the service, the search says so and stays with the service's results;
// a service failure fails the command.
func (a app) semanticFleetMatches(home string, svc *fleetService, query string, filter fleet.Filter, already []fleet.Record) ([]fleet.Record, error) {
	ix, client, err := openFleetIndex(home, svc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := serviceContext()
	defer cancel()
	hits, res, err := ix.Search(ctx, query, semanticSyncPages, 20)
	var apiErr *recordsvc.Error
	switch {
	case errors.As(err, &apiErr):
		return nil, err
	case errors.Is(err, recordindex.ErrNotBuilt):
		fmt.Fprintln(a.stderr, "note: no semantic index yet; showing the service's search only (run my fleet index to build it)")
		return nil, nil
	case err != nil:
		fmt.Fprintf(a.stderr, "note: semantic search unavailable (%v); showing the service's search only\n", err)
		return nil, nil
	case res.Backlog:
		fmt.Fprintln(a.stderr, "note: the semantic index is behind the service; showing the service's search only (run my fleet index)")
		return nil, nil
	}
	seen := map[string]bool{}
	for _, rec := range already {
		seen[rec.ID] = true
	}
	var out []fleet.Record
	for _, hit := range hits {
		if seen[hit.ID] {
			continue
		}
		rec, err := client.Get(ctx, hit.ID)
		if errors.As(err, &apiErr) && apiErr.NotFound() {
			continue
		}
		if err != nil {
			return nil, err
		}
		if rec["archived"] == true {
			continue
		}
		projected := recordsvc.ToFleet(svc.service.ID, rec)
		projected.Match = "semantic"
		projected.Score = hit.Score
		projected.Snippet = fmt.Sprintf("semantic match %.2f", hit.Score)
		if len(fleet.FilterRecords([]fleet.Record{projected}, filter)) == 0 {
			continue
		}
		seen[hit.ID] = true
		out = append(out, projected)
	}
	return out, nil
}

func (a app) runFleetIndex(args []string) error {
	var opts meetingCommonOpts
	rebuild := false
	fs := newFlagSet("my fleet index", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	fs.BoolVar(&rebuild, "rebuild", false, "rebuild the index from scratch")
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my fleet index [--rebuild]")
	}
	svc, err := boundFleetService(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if svc == nil {
		return a.maybeJSONError(opts.jsonOut, fmt.Errorf("fleet is not bound to a service; markdown fleet search reads the mounted files directly (and uses qmd when it is installed)"))
	}
	ix, _, err := openFleetIndex(opts.home, svc)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := ix.Sync(ctx, 0, rebuild)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, describeServiceError(svc, err))
	}
	if opts.jsonOut {
		return printJSON(a.stdout, res)
	}
	verb := "updated"
	if res.Rebuilt {
		verb = "built"
	}
	fmt.Fprintf(a.stdout, "semantic index for %s %s: %d added, %d updated, %d removed\n", svc.service.ID, verb, res.Added, res.Updated, res.Removed)
	return nil
}
