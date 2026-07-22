package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/workspace"
)

func (a app) runSync(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var backend string
	var publish string
	var scope string
	var message string
	var printOnly bool
	var push bool
	var noDerived bool
	var verbose bool
	var jsonOut bool
	fs := newFlagSet("my sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.StringVar(&backend, "backend", "auto", "sync backend: auto, gnit, or builtin")
	fs.StringVar(&publish, "publish", "never", "explicit publish mode: auto, never, direct, or pr")
	fs.StringVar(&scope, "scope", "all", "sync scope: all, local, content, manifest, or repos")
	fs.StringVar(&message, "message", "", "commit message for newly committed content")
	fs.BoolVar(&printOnly, "print", false, "print planned actions without changing files or remotes")
	fs.BoolVar(&push, "push", false, "publish eligible local changes using the manifest policy or auto policy")
	fs.BoolVar(&noDerived, "no-derived", false, "skip derived skill/guidance reconciliation after manifest changes")
	fs.BoolVar(&verbose, "verbose", false, "show full human-readable sync detail")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.Usage = func() {
		a.printSyncUsage()
		fs.PrintDefaults()
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
		"backend":  true,
		"publish":  true,
		"scope":    true,
		"message":  true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("sync does not accept positional arguments")
	}
	if !validSyncBackend(backend) {
		return fmt.Errorf("--backend must be one of auto, gnit, or builtin")
	}
	if !validSyncPublish(publish) {
		return fmt.Errorf("--publish must be one of auto, never, direct, or pr")
	}
	if !validSyncScope(scope) {
		return fmt.Errorf("--scope must be one of all, local, content, manifest, or repos")
	}
	publishExplicit := flagWasSet(fs, "publish")
	if push && publishExplicit {
		return fmt.Errorf("--push and --publish cannot be combined")
	}
	manifestName, err = defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if doc, loadErr := loadSingleRegisteredDoc(home, manifestName); loadErr != nil {
		return a.maybeJSONError(jsonOut, loadErr)
	} else if root, rootErr := resolveMyRoot(home, manifestName, umbrellaRoot); rootErr != nil {
		return a.maybeJSONError(jsonOut, rootErr)
	} else if accessErr := a.requireGovernedLaunchAccess(home, doc, root); accessErr != nil {
		return a.maybeJSONError(jsonOut, accessErr)
	} else if freshnessErr := a.requireGovernedManifestFreshness(home, doc, root); freshnessErr != nil {
		return a.maybeJSONError(jsonOut, freshnessErr)
	} else if refreshed, refreshErr := loadSingleRegisteredDoc(home, manifestName); refreshErr != nil {
		return a.maybeJSONError(jsonOut, refreshErr)
	} else if accessErr := a.requireGovernedLaunchAccess(home, refreshed, root); accessErr != nil {
		return a.maybeJSONError(jsonOut, accessErr)
	} else if policyErr := a.requireGovernedPolicyAcceptances(home, refreshed, root); policyErr != nil {
		return a.maybeJSONError(jsonOut, policyErr)
	}
	if push {
		publish, err = a.syncPushPublish(home, manifestName)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	} else if !publishExplicit {
		publish = "never"
	}
	if governedDoc, loadErr := loadSingleRegisteredDoc(home, manifestName); loadErr == nil && manifest.GovernanceConfigured(governedDoc.doc.Governance) && publish != "never" {
		if publish == "direct" {
			return a.maybeJSONError(jsonOut, fmt.Errorf("governed organizations refuse direct publish; use `my sync --publish pr --print` so required checks and approvals remain authoritative"))
		}
		publish = "pr"
	}
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, scope)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	var localMountBlocks []syncer.Result
	if publish != "never" && syncScopeAllowsDerived(scope) {
		localMountBlocks, err = a.localMountSyncBlocks(home, manifestName)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
		entries = withoutBlockedManifestEntries(entries, localMountBlocks)
	}
	gnitRoot := ""
	var sessionHolds []syncer.SessionHold
	if root, err := resolveMyRoot(home, manifestName, umbrellaRoot); err == nil {
		gnitRoot = findGnitWorkspaceRoot(root)
		sessionHolds, err = collectSessionHolds(root)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	}
	effectiveBackend := backend
	backendMessage := ""
	if effectiveBackend == "auto" {
		if publish == "pr" || gnitRoot == "" {
			effectiveBackend = "builtin"
			if publish == "pr" {
				backendMessage = "PR mode is handled by My AI/gh; Gnit remains the publish substrate after PR support lands"
			}
		}
	}
	var visibility syncer.VisibilityFunc
	if publish == "auto" {
		visibility = a.githubRepoVisibility
	}
	report := syncer.Run(entries, syncer.Options{
		Backend:      effectiveBackend,
		GnitRoot:     gnitRoot,
		Publish:      publish,
		DryRun:       printOnly,
		Message:      message,
		Visibility:   visibility,
		SessionHolds: sessionHolds,
		PRPublisher:  a.pullRequestPublisher(home),
	})
	if backendMessage != "" && report.BackendMessage == "" {
		report.BackendMessage = backendMessage
	}
	report.Results = append(report.Results, localMountBlocks...)
	var derived *derivedReconcileReport
	if !printOnly && !noDerived && syncScopeAllowsDerived(scope) {
		if changedManifest, ok := changedManifestForDerived(report); ok {
			if root, hasRoot, err := existingUmbrellaRoot(home, changedManifest, umbrellaRoot); err != nil {
				return a.maybeJSONError(jsonOut, err)
			} else if hasRoot {
				derivedReport, err := a.reconcileDerived(home, changedManifest, root)
				if err != nil {
					return a.maybeJSONError(jsonOut, err)
				}
				derived = &derivedReport
			}
		}
	}
	if !printOnly {
		if err := a.saveLastSyncReport(home, manifestName, umbrellaRoot, report); err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, syncCommandReport{Report: report, Derived: derived}); err != nil {
			return err
		}
	} else {
		a.printSyncReport(report, verbose, syncNextCommands{
			Apply:  syncApplyCommand(push, publishExplicit, publish, message),
			Review: syncReviewCommand(message),
		})
		if derived != nil {
			a.printDerivedReconcileReport(*derived, verbose)
		}
	}
	if syncReportFailed(report) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more sync operations failed"))
	}
	if derivedReportFailed(derived) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more derived reconciliation operations failed"))
	}
	return nil
}

func (a app) localMountSyncBlocks(home, manifestName string) ([]syncer.Result, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var out []syncer.Result
	for _, doc := range docs {
		localMounts := localMountGitURLs(doc.doc)
		if len(localMounts) == 0 {
			continue
		}
		out = append(out, syncer.Result{
			Manifest:    doc.ref.Name,
			ID:          doc.ref.Name,
			Role:        "manifest",
			Kind:        "manifest",
			GitURL:      doc.ref.GitURL,
			LocalPath:   doc.ref.LocalPath,
			Status:      "held back",
			Message:     "manifest has local mount URL(s): " + strings.Join(localMounts, ", ") + "; run my publish --manifest " + doc.ref.Name,
			ReasonCode:  "local_mount_urls",
			NextCommand: "my publish --manifest " + shellQuote(doc.ref.Name),
		})
	}
	return out, nil
}

func withoutBlockedManifestEntries(entries []syncer.Entry, blocks []syncer.Result) []syncer.Entry {
	if len(blocks) == 0 {
		return entries
	}
	blocked := map[string]bool{}
	for _, block := range blocks {
		name := block.Manifest
		if name == "" {
			name = block.ID
		}
		blocked[name] = true
	}
	out := entries[:0]
	for _, entry := range entries {
		name := entry.Manifest
		if name == "" {
			name = entry.ID
		}
		if entry.Role == "manifest" && blocked[name] {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func localMountGitURLs(doc manifest.Document) []string {
	var out []string
	for _, mount := range manifest.EffectiveMounts(doc) {
		if localMountGitURL(mount.GitURL) {
			out = append(out, mount.ID+"="+mount.GitURL)
		}
	}
	sort.Strings(out)
	return out
}

type syncCommandReport struct {
	syncer.Report
	Derived *derivedReconcileReport `json:"derived,omitempty"`
}

func (a app) printSyncUsage() {
	fmt.Fprintln(a.stderr, `Usage of my sync:
  my sync [--backend auto|gnit|builtin] [--push|--publish auto|never|direct|pr] [--scope all|local|content|manifest|repos] [--manifest NAME] [--home DIR] [--umbrella DIR] [--message TEXT] [--no-derived] [--print] [--verbose] [--json]

Synchronizes registered My AI repositories in both directions. The default
backend chooses the correct guarded publication path for each checkout;
workspace coordination remains internal machinery. Bare my sync
pulls/reconciles only and never publishes local changes. Use --push to publish
eligible changes with the manifest policy (or auto policy when none is set), or
--publish to choose an explicit publish mode. Direct mode can push existing
commits; for reviewed dirty manifest control-plane files, prefer
my publish --manifest NAME or my sync --publish direct --scope manifest.
Unrelated dirty non-content files are still held for explicit review.
Non-print runs write .my-cli/last-sync.json when an umbrella is present. When a
manifest checkout changes, sync also reconciles generated guidance and manifest
skills unless --no-derived is passed.`)
}

func syncScopeAllowsDerived(scope string) bool {
	switch scope {
	case "all", "local", "manifest":
		return true
	default:
		return false
	}
}

func changedManifestForDerived(report syncer.Report) (string, bool) {
	seen := map[string]bool{}
	var names []string
	for _, result := range report.Results {
		if result.Role != "manifest" {
			continue
		}
		if !syncManifestResultChanged(result) {
			continue
		}
		name := result.Manifest
		if name == "" {
			name = result.ID
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) != 1 {
		return "", false
	}
	return names[0], true
}

func syncManifestResultChanged(result syncer.Result) bool {
	if result.Status == "pulled" || result.Status == "pushed" {
		return true
	}
	return len(result.Changed) != 0 && result.Status != "dry-run"
}

func (a app) syncPushPublish(home, manifestName string) (string, error) {
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return "auto", nil
	}
	if manifest.GovernanceConfigured(doc.doc.Governance) {
		return "pr", nil
	}
	if doc.doc.Sync.PublishPolicy == "" {
		return "auto", nil
	}
	return doc.doc.Sync.PublishPolicy, nil
}

func validSyncBackend(value string) bool {
	switch value {
	case "auto", "gnit", "builtin":
		return true
	default:
		return false
	}
}

func validSyncPublish(value string) bool {
	switch value {
	case "auto", "never", "direct", "pr":
		return true
	default:
		return false
	}
}

func validSyncScope(value string) bool {
	switch value {
	case "all", "local", "content", "manifest", "repos":
		return true
	default:
		return false
	}
}

func (a app) collectSyncEntries(home, manifestName, umbrellaRoot, scope string) ([]syncer.Entry, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var entries []syncer.Entry
	for _, doc := range docs {
		manifestWanted := scope == "all" || scope == "local" || scope == "manifest"
		contentWanted := scope == "all" || scope == "local" || scope == "content"
		if manifestWanted {
			entries = append(entries, syncer.Entry{
				Manifest:     doc.ref.Name,
				ID:           doc.ref.Name,
				Role:         "manifest",
				Kind:         "manifest",
				GitURL:       doc.ref.GitURL,
				LocalPath:    doc.ref.LocalPath,
				ContentPaths: manifestControlPaths(),
			})
		}
		if contentWanted {
			mounts, err := workspace.ListMounts(home, doc.ref.Name, umbrellaRoot)
			if err != nil {
				return nil, err
			}
			for _, mount := range mounts {
				entries = append(entries, syncer.Entry{
					Manifest:     mount.Manifest,
					ID:           mount.ID,
					Role:         "content",
					Kind:         mount.Kind,
					GitURL:       mount.GitURL,
					LocalPath:    mount.LocalPath,
					ContentPaths: syncContentPaths(mount),
				})
			}
		}
		if scope == "all" || scope == "local" || scope == "repos" {
			productEntries, err := a.collectSyncRepoEntries(home, doc, umbrellaRoot)
			if err != nil {
				return nil, err
			}
			entries = append(entries, productEntries...)
		}
	}
	entries = dedupeSyncEntries(entries)
	if scope == "local" {
		entries = existingSyncEntries(entries)
	}
	return entries, nil
}

func (a app) collectSyncRepoEntries(home string, doc registeredDoc, umbrellaRoot string) ([]syncer.Entry, error) {
	root, err := umbrella.ResolveRoot(home, "", umbrellaRoot, doc.doc)
	if err != nil {
		return nil, err
	}
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []syncer.Entry
	for _, id := range state.SelectedRepos {
		repo, ok, err := manifest.FindRepo(home, doc.ref.Name, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		entry := repoEntry(doc, root, repo)
		entries = append(entries, syncer.Entry{
			Manifest:  entry.Manifest,
			ID:        entry.ID,
			Role:      "repo",
			Kind:      entry.Kind,
			GitURL:    entry.GitURL,
			LocalPath: entry.LocalPath,
		})
	}
	return entries, nil
}

func syncContentPaths(entry workspace.Entry) []string {
	return mountContentPaths(entry.Kind, entry.IncludePaths)
}

func manifestControlPaths() []string {
	return []string{"manifest.json", "catalog", "skills", "guidance", "agent-guidance"}
}

func mountContentPaths(kind string, includePaths []string) []string {
	if len(includePaths) != 0 {
		return append([]string(nil), includePaths...)
	}
	switch kind {
	case "handbook":
		return []string{"customers", "meetings", "support", "fleet", "decisions", "projects", "policy", "people"}
	case "customers":
		return []string{"customers"}
	case "meetings":
		return []string{"meetings"}
	case "support":
		return []string{"support"}
	case "fleet":
		return []string{"fleet"}
	case "policy":
		return []string{"policy"}
	case "docs":
		return []string{"docs"}
	default:
		return nil
	}
}

func dedupeSyncEntries(entries []syncer.Entry) []syncer.Entry {
	seen := map[string]bool{}
	var out []syncer.Entry
	for _, entry := range entries {
		key := entry.Role + "\x00" + entry.LocalPath
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entry)
	}
	return out
}

func existingSyncEntries(entries []syncer.Entry) []syncer.Entry {
	var out []syncer.Entry
	for _, entry := range entries {
		if _, err := os.Stat(entry.LocalPath); err == nil {
			out = append(out, entry)
		}
	}
	return out
}

func (a app) githubRepoVisibility(gitURL string) (string, error) {
	slug, ok := githubRepoSlug(gitURL)
	if !ok {
		return "", fmt.Errorf("not a GitHub repository")
	}
	cmd := exec.Command("gh", "repo", "view", slug, "--json", "visibility", "--jq", ".visibility")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func githubRepoSlug(gitURL string) (string, bool) {
	value := strings.TrimSpace(gitURL)
	value = strings.TrimSuffix(value, ".git")
	switch {
	case strings.HasPrefix(value, "https://github.com/"):
		return strings.TrimPrefix(value, "https://github.com/"), true
	case strings.HasPrefix(value, "git@github.com:"):
		return strings.TrimPrefix(value, "git@github.com:"), true
	case strings.HasPrefix(value, "ssh://git@github.com/"):
		return strings.TrimPrefix(value, "ssh://git@github.com/"), true
	default:
		return "", false
	}
}

type syncNextCommands struct {
	Apply  string
	Review string
}

func (a app) printSyncReport(report syncer.Report, verbose bool, next syncNextCommands) {
	if verbose && report.Backend != "" {
		line := "# backend: " + report.Backend
		if report.GnitRoot != "" {
			line += "\tgnit_root=" + report.GnitRoot
		}
		if report.BackendMessage != "" {
			line += "\t" + report.BackendMessage
		}
		fmt.Fprintln(a.stdout, line)
	}
	visible := 0
	for _, result := range report.Results {
		if !syncResultVisible(result, verbose) {
			continue
		}
		visible++
		line := fmt.Sprintf("%s\t%s\t%s\t%s\t%s", result.Manifest, result.ID, result.Role, result.Status, result.LocalPath)
		if result.Ahead != 0 || result.Behind != 0 {
			line += fmt.Sprintf("\tahead=%d behind=%d", result.Ahead, result.Behind)
		}
		if len(result.Dirty) != 0 {
			line += "\tdirty=" + strings.Join(result.Dirty, ",")
		}
		if len(result.Changed) != 0 {
			line += "\tchanged=" + strings.Join(result.Changed, ",")
		}
		if result.Message != "" {
			line += "\t" + strings.ReplaceAll(result.Message, "\n", " ")
		}
		if result.NextCommand != "" {
			line += "\tnext=" + result.NextCommand
		}
		if result.Error != "" {
			line += "\t" + result.Error
		}
		fmt.Fprintln(a.stdout, line)
	}
	if visible == 0 {
		fmt.Fprintln(a.stdout, "up to date")
	}
	if label, command := syncNextStep(report, next); command != "" {
		fmt.Fprintf(a.stdout, "next\t%s\t%s\n", label, command)
	}
}

func syncResultVisible(result syncer.Result, verbose bool) bool {
	if verbose {
		return true
	}
	return result.Status != "already landed"
}

func syncNextStep(report syncer.Report, next syncNextCommands) (string, string) {
	if report.Publish != "never" && report.DryRun && syncReportHasOutboundDryRun(report) {
		return "apply", next.Apply
	}
	if report.Publish == "never" && syncReportHasPublishDisabledHold(report) {
		return "review", next.Review
	}
	return "", ""
}

func syncApplyCommand(push, publishExplicit bool, publish, message string) string {
	if push {
		return appendSyncMessageFlag("my sync --push", message)
	}
	if publishExplicit && publish != "never" {
		return appendSyncMessageFlag("my sync --publish "+publish, message)
	}
	return appendSyncMessageFlag("my sync --push", message)
}

func syncReviewCommand(message string) string {
	return appendSyncMessageFlag("my sync --push --print", message)
}

func appendSyncMessageFlag(command, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return command
	}
	return command + " --message " + shellQuote(message)
}

func syncReportHasOutboundDryRun(report syncer.Report) bool {
	for _, result := range report.Results {
		if result.Status == "dry-run" && result.Direction == "outbound" {
			return true
		}
	}
	return false
}

func syncReportHasPublishDisabledHold(report syncer.Report) bool {
	for _, result := range report.Results {
		if result.Status == "held back" && strings.Contains(result.Message, "publish disabled") {
			return true
		}
	}
	return false
}

func syncReportFailed(report syncer.Report) bool {
	for _, result := range report.Results {
		if result.Status == "failed" {
			return true
		}
	}
	return false
}

const lastSyncFile = "last-sync.json"

type lastSyncAudit struct {
	SchemaVersion int           `json:"schema_version"`
	SavedAt       string        `json:"saved_at"`
	Report        syncer.Report `json:"report"`
}

func (a app) saveLastSyncReport(home, manifestName, umbrellaRoot string, report syncer.Report) error {
	root, ok, err := existingUmbrellaRoot(home, manifestName, umbrellaRoot)
	if err != nil || !ok {
		return err
	}
	audit := lastSyncAudit{
		SchemaVersion: 1,
		SavedAt:       time.Now().UTC().Format(time.RFC3339),
		Report:        report,
	}
	data, err := json.MarshalIndent(audit, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(root, umbrella.DirName, lastSyncFile), data, 0o644)
}

func loadLastSyncAudit(root string) (lastSyncAudit, bool, error) {
	path := filepath.Join(root, umbrella.DirName, lastSyncFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return lastSyncAudit{}, false, nil
	}
	if err != nil {
		return lastSyncAudit{}, false, err
	}
	var audit lastSyncAudit
	if err := json.Unmarshal(data, &audit); err != nil {
		return lastSyncAudit{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	return audit, true, nil
}
