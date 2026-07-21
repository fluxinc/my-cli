package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/domainrecord"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/outbox"
	"github.com/fluxinc/my-cli/internal/record"
	"github.com/fluxinc/my-cli/internal/syncer"
)

type recordDomainContext struct {
	doc          registeredDoc
	domain       manifest.RecordDomain
	root         record.Root
	umbrellaRoot string
	home         string
}

type recordAddResult struct {
	Record      domainrecord.Item `json:"record"`
	Content     string            `json:"content,omitempty"`
	Publication outbox.Event      `json:"publication,omitzero"`
}

func (a app) runRecordDomains(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my record domains", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("record domains does not accept positional arguments")
	}
	name, err := defaultManifestName(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	doc, err := loadSingleRegisteredDoc(opts.home, name)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	domains := append([]manifest.RecordDomain(nil), doc.doc.Governance.RecordDomains...)
	sort.Slice(domains, func(i, j int) bool { return domains[i].ID < domains[j].ID })
	if opts.jsonOut {
		return printJSON(a.stdout, domains)
	}
	for _, domain := range domains {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s/%s\t%s\t%s\n", domain.ID, domain.Title, domain.Mount, domain.Path, domain.Review, domain.Publish)
	}
	return nil
}

func (a app) runRecordAdd(args []string) error {
	var opts meetingCommonOpts
	var date, title, status, actor string
	var sources, related, fields stringListFlag
	var printOnly, noPublish bool
	fs := newFlagSet("my record add", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	fs.StringVar(&date, "date", "", "record date, YYYY-MM-DD")
	fs.StringVar(&title, "title", "", "record title")
	fs.StringVar(&status, "status", "", "record workflow status")
	fs.StringVar(&actor, "actor", "", "human or system actor")
	fs.Var(&sources, "source", "evidence source reference (repeatable)")
	fs.Var(&related, "related", "related record or source reference (repeatable)")
	fs.Var(&fields, "field", "additional scalar key=value frontmatter (repeatable)")
	fs.BoolVar(&printOnly, "print", false, "print scaffold without writing")
	fs.BoolVar(&noPublish, "no-publish", false, "queue locally without an immediate publication attempt")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home": true, "manifest": true, "workspace": true, "umbrella": true,
		"date": true, "title": true, "status": true, "actor": true,
		"source": true, "related": true, "field": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return fmt.Errorf("usage: my record add <domain> <slug>")
	}
	ctx, err := a.recordDomainContext(opts, rest[0])
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	custom, err := parseRecordFields(fields)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	item, content, err := domainrecord.Add(ctx.root, ctx.domain, rest[1], domainrecord.AddOptions{
		Date: date, Title: title, Status: status, Actor: actor,
		Sources: sources, Related: related, Fields: custom, DryRun: printOnly,
	})
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	result := recordAddResult{Record: item}
	if printOnly {
		result.Content = content
	} else {
		if err := markRecordIntentToAdd(ctx.root, item.Path); err != nil {
			return a.maybeJSONError(opts.jsonOut, fmt.Errorf("record was written at %s but Git adoption failed: %w", item.Path, err))
		}
		event, err := queueDomainRecord(ctx, item)
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, fmt.Errorf("record was written at %s but publication queueing failed; run `my record reconcile`: %w", item.Path, err))
		}
		result.Publication = event
		if ctx.domain.Publish == "auto-pr" && !noPublish {
			published, publishErr := a.publishDomainRepo(ctx, event)
			result.Publication = published
			if publishErr != nil {
				fmt.Fprintf(a.stderr, "warning: record is durable locally and remains in the publication outbox: %v\n", publishErr)
			}
		}
	}
	if opts.jsonOut {
		return printJSON(a.stdout, result)
	}
	if printOnly {
		fmt.Fprintf(a.stdout, "# path: %s\n%s", item.Path, content)
		return nil
	}
	fmt.Fprintf(a.stdout, "%s\t%s\n", item.Path, result.Publication.State)
	return nil
}

func (a app) runRecordList(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my record list", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my record list <domain>")
	}
	ctx, err := a.recordDomainContext(opts, rest[0])
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	items, err := domainrecord.List([]record.Root{ctx.root}, ctx.domain)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, items)
	}
	for _, item := range items {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", item.Date, item.ID, item.Status, item.Title)
	}
	return nil
}

func (a app) runRecordGet(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my record get", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return fmt.Errorf("usage: my record get <domain> <id>")
	}
	ctx, err := a.recordDomainContext(opts, rest[0])
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	item, content, err := domainrecord.Get([]record.Root{ctx.root}, ctx.domain, rest[1])
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, struct {
			Record  domainrecord.Item `json:"record"`
			Content string            `json:"content"`
		}{item, content})
	}
	_, err = fmt.Fprint(a.stdout, content)
	return err
}

func (a app) runRecordOutbox(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my record outbox", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("record outbox does not accept positional arguments")
	}
	name, err := defaultManifestName(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	root, err := resolveMyRoot(opts.home, name, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if _, issues := a.reconcileSubmittedOutbox(root); len(issues) != 0 {
		for _, issue := range issues {
			fmt.Fprintf(a.stderr, "warning: outbox merge reconciliation: %v\n", issue)
		}
	}
	events, itemIssues, err := outbox.ListWithIssues(root)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	a.warnOutboxItemIssues(itemIssues)
	if opts.jsonOut {
		return printJSON(a.stdout, events)
	}
	for _, event := range events {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\n", event.State, event.Domain, event.RelativePath, event.PRURL, event.Message)
	}
	return nil
}

func (a app) runRecordFlush(args []string) error {
	var opts meetingCommonOpts
	var includeManual bool
	fs := newFlagSet("my record flush", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	fs.BoolVar(&includeManual, "include-manual", false, "publish domains configured for manual PR submission")
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("record flush does not accept positional arguments")
	}
	name, err := defaultManifestName(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	root, err := resolveMyRoot(opts.home, name, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if _, err := a.reconcileDomainOutbox(opts.home, name, root); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if _, issues := a.reconcileSubmittedOutbox(root); len(issues) != 0 {
		for _, issue := range issues {
			fmt.Fprintf(a.stderr, "warning: outbox merge reconciliation: %v\n", issue)
		}
	}
	events, itemIssues, err := outbox.ListWithIssues(root)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	a.warnOutboxItemIssues(itemIssues)
	doc, err := loadSingleRegisteredDoc(opts.home, name)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	domains := recordDomainMap(doc.doc)
	seenRepo := map[string]bool{}
	var results []outbox.Event
	for _, event := range events {
		if event.State != outbox.StateQueued && event.State != outbox.StateAttemptFailed {
			continue
		}
		domain, ok := domains[event.Domain]
		if !ok || (domain.Publish == "manual-pr" && !includeManual) || seenRepo[event.RepoPath] {
			continue
		}
		seenRepo[event.RepoPath] = true
		ctx, err := a.recordDomainContext(meetingCommonOpts{home: opts.home, manifestName: name, workspaceID: event.Mount, umbrellaRoot: root}, event.Domain)
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		result, publishErr := a.publishDomainRepo(ctx, event)
		results = append(results, result)
		if publishErr != nil {
			fmt.Fprintf(a.stderr, "warning: %s remains pending: %v\n", event.RelativePath, publishErr)
		}
	}
	if opts.jsonOut {
		return printJSON(a.stdout, results)
	}
	for _, result := range results {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", result.State, result.Domain, result.RelativePath)
	}
	return nil
}

func (a app) runRecordReconcile(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my record reconcile", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("record reconcile does not accept positional arguments")
	}
	name, err := defaultManifestName(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	root, err := resolveMyRoot(opts.home, name, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	events, err := a.reconcileDomainOutbox(opts.home, name, root)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	merged, issues := a.reconcileSubmittedOutbox(root)
	events = append(events, merged...)
	for _, issue := range issues {
		fmt.Fprintf(a.stderr, "warning: outbox merge reconciliation: %v\n", issue)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, events)
	}
	for _, event := range events {
		fmt.Fprintf(a.stdout, "queued\t%s\t%s\n", event.Domain, event.RelativePath)
	}
	return nil
}

func (a app) recordDomainContext(opts meetingCommonOpts, domainID string) (recordDomainContext, error) {
	name, err := defaultManifestName(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return recordDomainContext{}, err
	}
	doc, err := loadSingleRegisteredDoc(opts.home, name)
	if err != nil {
		return recordDomainContext{}, err
	}
	domain, ok := recordDomainMap(doc.doc)[domainID]
	if !ok {
		return recordDomainContext{}, fmt.Errorf("record domain %q is not declared by manifest %q", domainID, name)
	}
	mount, ok := mountByID(doc.doc, domain.Mount)
	if !ok {
		return recordDomainContext{}, fmt.Errorf("record domain %q references missing mount %q", domainID, domain.Mount)
	}
	roots, err := contentRoots(opts.home, name, domain.Mount, opts.umbrellaRoot, "record domain "+domainID, []string{mount.Kind})
	if err != nil {
		return recordDomainContext{}, err
	}
	if len(roots) != 1 {
		return recordDomainContext{}, fmt.Errorf("record domain %q requires exactly one mounted root", domainID)
	}
	if !pathsWithinContent([]string{domain.Path}, roots[0].ContentPaths) {
		return recordDomainContext{}, fmt.Errorf("record domain %q path %q is outside mount %q publish paths", domainID, domain.Path, domain.Mount)
	}
	root, err := resolveMyRoot(opts.home, name, opts.umbrellaRoot)
	if err != nil {
		return recordDomainContext{}, err
	}
	return recordDomainContext{doc: doc, domain: domain, root: roots[0], umbrellaRoot: root, home: opts.home}, nil
}

func queueDomainRecord(ctx recordDomainContext, item domainrecord.Item) (outbox.Event, error) {
	data, err := os.ReadFile(item.Path)
	if err != nil {
		return outbox.Event{}, err
	}
	rel, ok := relativePathUnder(ctx.root.Path, item.Path)
	if !ok {
		return outbox.Event{}, fmt.Errorf("record path escaped mount root")
	}
	digest := outbox.ContentDigest(data)
	event := outbox.Event{
		ItemID:       outbox.ItemID(ctx.doc.doc.Organization.ID, ctx.domain.ID, ctx.domain.Mount, rel, digest),
		Organization: ctx.doc.doc.Organization.ID, Manifest: ctx.doc.ref.Name,
		Domain: ctx.domain.ID, Mount: ctx.domain.Mount, RepoPath: ctx.root.Path,
		RelativePath: filepath.ToSlash(rel), ContentSHA256: digest, State: outbox.StateQueued,
	}
	return outbox.Append(ctx.umbrellaRoot, event, time.Now())
}

func (a app) publishDomainRepo(ctx recordDomainContext, item outbox.Event) (outbox.Event, error) {
	if err := verifyOutboxContent(item); err != nil {
		return appendOutboxFailure(ctx.umbrellaRoot, item, err)
	}
	if err := a.requireGovernedLaunchAccess(ctx.home, ctx.doc, ctx.umbrellaRoot); err != nil {
		return appendOutboxFailure(ctx.umbrellaRoot, item, err)
	}
	if err := a.requireGovernedManifestFreshness(ctx.home, ctx.doc, ctx.umbrellaRoot); err != nil {
		return appendOutboxFailure(ctx.umbrellaRoot, item, err)
	}
	refreshed, err := loadSingleRegisteredDoc(ctx.home, ctx.doc.ref.Name)
	if err != nil {
		return appendOutboxFailure(ctx.umbrellaRoot, item, err)
	}
	if err := a.requireGovernedLaunchAccess(ctx.home, refreshed, ctx.umbrellaRoot); err != nil {
		return appendOutboxFailure(ctx.umbrellaRoot, item, err)
	}
	if err := a.requireGovernedPolicyAcceptances(ctx.home, refreshed, ctx.umbrellaRoot); err != nil {
		return appendOutboxFailure(ctx.umbrellaRoot, item, err)
	}
	entries, err := a.collectSyncEntries(ctx.home, ctx.doc.ref.Name, ctx.umbrellaRoot, "content")
	if err != nil {
		return appendOutboxFailure(ctx.umbrellaRoot, item, err)
	}
	var entry syncer.Entry
	for _, candidate := range entries {
		if samePath(candidate.LocalPath, ctx.root.Path) {
			entry = candidate
			break
		}
	}
	if entry.LocalPath == "" {
		return appendOutboxFailure(ctx.umbrellaRoot, item, fmt.Errorf("mounted record repository is absent from sync inventory"))
	}
	publisher := a.pullRequestPublisher(ctx.home)
	if ctx.domain.Publish == "manual-pr" {
		publisher = a.pullRequestPublisherAllowManual(ctx.home)
	}
	report := syncer.Run([]syncer.Entry{entry}, syncer.Options{
		Backend: "builtin", Publish: "pr", Message: "Record " + ctx.domain.Title,
		PRPublisher: publisher,
	})
	if len(report.Results) != 1 || report.Results[0].Status != "pull request opened" {
		message := "publication did not open a pull request"
		if len(report.Results) != 0 {
			message = strings.TrimSpace(report.Results[0].Message + " " + report.Results[0].Error)
		}
		return appendOutboxFailure(ctx.umbrellaRoot, item, fmt.Errorf("%s", message))
	}
	result := report.Results[0]
	if result.PRURL == "" || result.PRHeadSHA == "" || result.PRBase == "" {
		return appendOutboxFailure(ctx.umbrellaRoot, item, fmt.Errorf("pull request publisher did not return structural PR proof"))
	}
	current, itemIssues, err := outbox.ListWithIssues(ctx.umbrellaRoot)
	if err != nil {
		return item, err
	}
	a.warnOutboxItemIssues(itemIssues)
	changed := map[string]bool{}
	for _, path := range append(append([]string(nil), result.Dirty...), result.Changed...) {
		changed[filepath.ToSlash(path)] = true
	}
	publishedPaths := make([]string, 0, len(changed))
	for path := range changed {
		publishedPaths = append(publishedPaths, path)
	}
	sort.Strings(publishedPaths)
	if len(publishedPaths) != 0 {
		fmt.Fprintf(a.stderr, "notice: governed pull request includes: %s\n", strings.Join(publishedPaths, ", "))
	}
	var returned outbox.Event
	for _, pending := range current {
		if !samePath(pending.RepoPath, ctx.root.Path) || (!changed[pending.RelativePath] && pending.ItemID != item.ItemID) || (pending.State != outbox.StateQueued && pending.State != outbox.StateAttemptFailed) {
			continue
		}
		if err := verifyOutboxContent(pending); err != nil {
			pending.State = outbox.StateAttemptFailed
			pending.Message = err.Error()
			if _, appendErr := outbox.Append(ctx.umbrellaRoot, pending, time.Now()); appendErr != nil {
				return item, appendErr
			}
			continue
		}
		pending.State = outbox.StateSubmitted
		pending.PRURL = result.PRURL
		pending.PRHeadSHA = result.PRHeadSHA
		pending.PRBase = result.PRBase
		pending.PublishedPaths = append([]string(nil), publishedPaths...)
		pending.Message = "remote branch and pull request verified; awaiting repository checks and merge"
		appended, appendErr := outbox.Append(ctx.umbrellaRoot, pending, time.Now())
		if appendErr != nil {
			return item, appendErr
		}
		if pending.ItemID == item.ItemID {
			returned = appended
		}
	}
	if returned.ItemID == "" {
		return item, fmt.Errorf("pull request opened but the record outbox item could not be matched")
	}
	return returned, nil
}

func (a app) warnOutboxItemIssues(issues []outbox.ItemIssue) {
	for _, issue := range issues {
		fmt.Fprintf(a.stderr, "warning: outbox item %s is unreadable and stays blocked: %v\n", issue.ItemID, issue.Err)
	}
}

func appendOutboxFailure(root string, item outbox.Event, cause error) (outbox.Event, error) {
	item.State = outbox.StateAttemptFailed
	item.Message = cause.Error()
	appended, err := outbox.Append(root, item, time.Now())
	if err != nil {
		return item, fmt.Errorf("%v; additionally could not append outbox failure: %w", cause, err)
	}
	return appended, cause
}

func (a app) reconcileDomainOutbox(home, manifestName, umbrellaRoot string) ([]outbox.Event, error) {
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return nil, err
	}
	current, itemIssues, err := outbox.ListWithIssues(umbrellaRoot)
	if err != nil {
		return nil, err
	}
	a.warnOutboxItemIssues(itemIssues)
	known := map[string]bool{}
	for _, event := range current {
		known[event.ItemID] = true
	}
	var queued []outbox.Event
	for _, domain := range doc.doc.Governance.RecordDomains {
		ctx, err := a.recordDomainContext(meetingCommonOpts{home: home, manifestName: manifestName, workspaceID: domain.Mount, umbrellaRoot: umbrellaRoot}, domain.ID)
		if err != nil {
			fmt.Fprintf(a.stderr, "warning: record domain %s skipped during reconcile: %v\n", domain.ID, err)
			continue
		}
		items, err := domainrecord.List([]record.Root{ctx.root}, domain)
		if err != nil {
			fmt.Fprintf(a.stderr, "warning: record domain %s skipped during reconcile: %v\n", domain.ID, err)
			continue
		}
		for _, item := range items {
			dirty, err := recordNeedsPublication(ctx.root.Path, item.Path)
			if err != nil {
				fmt.Fprintf(a.stderr, "warning: record domain %s item %s skipped during reconcile: %v\n", domain.ID, item.ID, err)
				continue
			}
			if !dirty {
				continue
			}
			data, err := os.ReadFile(item.Path)
			if err != nil {
				fmt.Fprintf(a.stderr, "warning: record domain %s item %s skipped during reconcile: %v\n", domain.ID, item.ID, err)
				continue
			}
			rel, ok := relativePathUnder(ctx.root.Path, item.Path)
			if !ok {
				fmt.Fprintf(a.stderr, "warning: record domain %s item %s escaped its repository during reconcile\n", domain.ID, item.ID)
				continue
			}
			digest := outbox.ContentDigest(data)
			id := outbox.ItemID(doc.doc.Organization.ID, domain.ID, domain.Mount, rel, digest)
			if known[id] {
				continue
			}
			event := outbox.Event{
				ItemID: id, Organization: doc.doc.Organization.ID, Manifest: doc.ref.Name,
				Domain: domain.ID, Mount: domain.Mount, RepoPath: ctx.root.Path,
				RelativePath: filepath.ToSlash(rel), ContentSHA256: digest, State: outbox.StateQueued,
				Message: "reconciled from unpublished Git working-tree or commit state",
			}
			event, err = outbox.Append(umbrellaRoot, event, time.Now())
			if err != nil {
				fmt.Fprintf(a.stderr, "warning: record domain %s item %s could not be queued during reconcile: %v\n", domain.ID, item.ID, err)
				continue
			}
			queued = append(queued, event)
			known[id] = true
		}
	}
	return queued, nil
}

func recordNeedsPublication(repo, path string) (bool, error) {
	rel, ok := relativePathUnder(repo, path)
	if !ok {
		return false, fmt.Errorf("record path escaped repository")
	}
	status, err := gitPRText(repo, nil, "status", "--porcelain", "--", rel)
	if err != nil {
		return false, fmt.Errorf("inspect record publication state: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return true, nil
	}
	if _, err := gitPRBytes(repo, nil, "ls-files", "--error-unmatch", "--", rel); err != nil {
		return true, nil
	}
	if _, err := gitPRText(repo, nil, "rev-parse", "--verify", "@{upstream}^{commit}"); err != nil {
		return false, fmt.Errorf("record repository has no verified upstream: %w", err)
	}
	out, err := gitPRText(repo, nil, "rev-list", "@{upstream}..HEAD", "--", rel)
	if err != nil {
		return false, fmt.Errorf("determine unpublished record commits: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

func verifyOutboxContent(event outbox.Event) error {
	path := filepath.Join(event.RepoPath, filepath.FromSlash(event.RelativePath))
	rel, ok := relativePathUnder(event.RepoPath, path)
	if !ok || filepath.ToSlash(rel) != filepath.ToSlash(event.RelativePath) {
		return fmt.Errorf("outbox path escaped repository")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read queued record %s: %w", event.RelativePath, err)
	}
	if digest := outbox.ContentDigest(data); digest != event.ContentSHA256 {
		return fmt.Errorf("queued record %s changed after queueing; expected %s, found %s; reconcile before publishing", event.RelativePath, event.ContentSHA256, digest)
	}
	return nil
}

type outboxMergeProof struct {
	State       string `json:"state"`
	HeadRefOID  string `json:"headRefOid"`
	MergeCommit struct {
		OID string `json:"oid"`
	} `json:"mergeCommit"`
}

func (a app) reconcileSubmittedOutbox(umbrellaRoot string) ([]outbox.Event, []error) {
	events, itemIssues, err := outbox.ListWithIssues(umbrellaRoot)
	if err != nil {
		return nil, []error{err}
	}
	var issues []error
	for _, issue := range itemIssues {
		issues = append(issues, fmt.Errorf("outbox item %s is unreadable and stays blocked: %w", issue.ItemID, issue.Err))
	}
	runner := a.publishRunner
	if runner == nil {
		runner = governanceExec
	}
	var merged []outbox.Event
	for _, event := range events {
		if event.State != outbox.StateSubmitted {
			continue
		}
		if event.PRURL == "" || event.PRHeadSHA == "" || event.PRBase == "" {
			issues = append(issues, fmt.Errorf("%s lacks structural PR proof", event.ItemID))
			continue
		}
		out, err := runner("gh", "pr", "view", event.PRURL, "--json", "state,headRefOid,mergeCommit")
		if err != nil {
			issues = append(issues, fmt.Errorf("%s: %w", event.PRURL, err))
			continue
		}
		var proof outboxMergeProof
		if err := json.Unmarshal(out, &proof); err != nil {
			issues = append(issues, fmt.Errorf("%s returned invalid merge proof: %w", event.PRURL, err))
			continue
		}
		if proof.State != "MERGED" {
			continue
		}
		if proof.HeadRefOID != event.PRHeadSHA {
			issues = append(issues, fmt.Errorf("%s merged a different head than the submitted outbox proof", event.PRURL))
			continue
		}
		if !fullGitOID(proof.MergeCommit.OID) {
			issues = append(issues, fmt.Errorf("%s returned invalid merge commit", event.PRURL))
			continue
		}
		remoteRef := "refs/remotes/origin/" + event.PRBase
		if out, err := gitPRBytes(event.RepoPath, nil, "check-ref-format", "refs/heads/"+event.PRBase); err != nil {
			issues = append(issues, fmt.Errorf("invalid trusted base for %s: %s", event.PRURL, commandMessage(out, err)))
			continue
		}
		if out, err := gitPRBytes(event.RepoPath, nil, "fetch", "--quiet", "origin", event.PRBase+":"+remoteRef); err != nil {
			issues = append(issues, fmt.Errorf("fetch trusted base for %s: %s", event.PRURL, commandMessage(out, err)))
			continue
		}
		if out, err := gitPRBytes(event.RepoPath, nil, "merge-base", "--is-ancestor", proof.MergeCommit.OID, remoteRef); err != nil {
			issues = append(issues, fmt.Errorf("merge commit for %s is not reachable from trusted base: %s", event.PRURL, commandMessage(out, err)))
			continue
		}
		blob, err := gitPRBytes(event.RepoPath, nil, "show", proof.MergeCommit.OID+":"+filepath.ToSlash(event.RelativePath))
		if err != nil || outbox.ContentDigest(blob) != event.ContentSHA256 {
			issues = append(issues, fmt.Errorf("merged record digest for %s does not match queued evidence", event.PRURL))
			continue
		}
		event.State = outbox.StateMerged
		event.MergedCommit = proof.MergeCommit.OID
		event.Message = "pull request merged; exact record digest verified at merge commit"
		appended, err := outbox.Append(umbrellaRoot, event, time.Now())
		if err != nil {
			issues = append(issues, err)
			continue
		}
		merged = append(merged, appended)
	}
	return merged, issues
}

func fullGitOID(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func parseRecordFields(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, value := range values {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			return nil, fmt.Errorf("record field must be key=value")
		}
		out[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return out, nil
}

func recordDomainMap(doc manifest.Document) map[string]manifest.RecordDomain {
	out := map[string]manifest.RecordDomain{}
	for _, domain := range doc.Governance.RecordDomains {
		out[domain.ID] = domain
	}
	return out
}

func mountByID(doc manifest.Document, id string) (manifest.Mount, bool) {
	for _, mount := range manifest.EffectiveMounts(doc) {
		if mount.ID == id {
			return mount, true
		}
	}
	return manifest.Mount{}, false
}
