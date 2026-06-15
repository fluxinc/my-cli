package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fluxinc/our-ai/internal/guidance"
	"github.com/fluxinc/our-ai/internal/harness"
	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/selfskill"
	"github.com/fluxinc/our-ai/internal/selfupdate"
	"github.com/fluxinc/our-ai/internal/skills"
	"github.com/fluxinc/our-ai/internal/syncer"
	"github.com/fluxinc/our-ai/internal/umbrella"
)

func (a app) runDoctor(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var noFetch bool
	var fix bool
	var jsonOut bool
	fs := newFlagSet("our doctor", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&noFetch, "no-fetch", false, "use local tracking refs without fetching remotes")
	fs.BoolVar(&fix, "fix", false, "fast-forward safe stale checkouts and reconcile derived artifacts")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("doctor does not accept positional arguments")
	}
	if fix && noFetch {
		return fmt.Errorf("--fix requires fetched freshness; omit --no-fetch")
	}
	report := a.buildDoctorReport(home, manifestName, umbrellaRoot, doctorOptions{NoFetch: noFetch, Fix: fix})
	if jsonOut {
		if err := printJSON(a.stdout, report); err != nil {
			return err
		}
	} else {
		a.printDoctorReport(report)
	}
	if fix && doctorFixFailed(report.Fixes) {
		return fmt.Errorf("one or more doctor fixes failed")
	}
	return nil
}

type doctorOptions struct {
	NoFetch bool
	Fix     bool
}

type doctorReport struct {
	Version    []doctorItem `json:"version,omitempty"`
	Legacy     []doctorItem `json:"legacy,omitempty"`
	Umbrella   []doctorItem `json:"umbrella,omitempty"`
	Manifests  []doctorItem `json:"manifests"`
	Freshness  []doctorItem `json:"freshness,omitempty"`
	Derived    []doctorItem `json:"derived,omitempty"`
	Fixes      []doctorItem `json:"fixes,omitempty"`
	LastSync   []doctorItem `json:"last_sync,omitempty"`
	Sessions   []doctorItem `json:"sessions,omitempty"`
	Workspaces []doctorItem `json:"workspaces"`
	Tools      []doctorItem `json:"tools"`
	Services   []doctorItem `json:"services,omitempty"`
}

type doctorItem struct {
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message,omitempty"`
	WouldFix string   `json:"would_fix,omitempty"`
	Details  []string `json:"details,omitempty"`
}

func (a app) buildDoctorReport(home, manifestName, umbrellaRoot string, opts doctorOptions) doctorReport {
	var report doctorReport
	report.Version = append(report.Version, a.doctorVersion(home))
	var root string
	if umbrellaRoot != "" {
		resolved, err := resolveUmbrellaRoot(home, umbrellaRoot)
		if err != nil {
			report.Umbrella = append(report.Umbrella, doctorItem{Name: umbrellaRoot, Status: "error", Message: err.Error()})
		} else {
			root = resolved
			report.Umbrella = append(report.Umbrella, doctorUmbrella(home, root)...)
		}
	} else if found, ok := umbrella.FindRoot("."); ok {
		root = found
		report.Umbrella = append(report.Umbrella, doctorUmbrella(home, root)...)
	}
	report.Legacy = append(report.Legacy, doctorLegacy(home, root)...)
	refs, err := manifestRefs(home, manifestName)
	if err != nil {
		report.Manifests = append(report.Manifests, doctorItem{Name: manifestName, Status: "error", Message: err.Error()})
		return report
	}
	for _, ref := range refs {
		result := manifest.ValidateFile(ref.LocalPath)
		item := doctorItem{Name: ref.Name, Path: result.Path}
		switch {
		case len(result.Errors) != 0:
			item.Status = "error"
			item.Details = append(item.Details, result.Errors...)
		case len(result.Warnings) != 0:
			item.Status = "warning"
			item.Details = append(item.Details, result.Warnings...)
		default:
			item.Status = "ok"
		}
		report.Manifests = append(report.Manifests, item)
		if len(result.Errors) != 0 {
			continue
		}
		doc, _, err := manifest.LoadDocument(ref.LocalPath)
		if err != nil {
			continue
		}
		report.Manifests = append(report.Manifests, doctorLocalMountURLs(ref, doc)...)
		report.Workspaces = append(report.Workspaces, doctorWorkspaces(home, ref.Name, doc.Workspaces)...)
		report.Tools = append(report.Tools, doctorTools(ref.Name, doc.Tools)...)
		report.Services = append(report.Services, doctorServices(ref.Name, ref.LocalPath, doc.Services)...)
	}
	report.Freshness = append(report.Freshness, a.doctorFreshness(home, manifestName, umbrellaRoot, !opts.NoFetch, root)...)
	report.Derived = append(report.Derived, a.doctorDerived(home, manifestName, root)...)
	if root != "" {
		for i := range report.Derived {
			item := &report.Derived[i]
			if item.Status == "ok" || item.Status == "error" {
				continue
			}
			if item.Name == "selfskill" {
				item.WouldFix = "reinstall the our self-skill"
			} else if item.WouldFix == "" {
				item.WouldFix = "reconcile derived guidance and skills"
			}
		}
	}
	if opts.Fix {
		clearDoctorWouldFix(report.Freshness)
		clearDoctorWouldFix(report.Derived)
		report.Fixes = append(report.Fixes, a.doctorFix(home, manifestName, umbrellaRoot, root, report.Derived)...)
	}
	if root != "" {
		report.LastSync = append(report.LastSync, doctorLastSync(root))
		report.Sessions = append(report.Sessions, doctorSessions(root)...)
	}
	return report
}

func doctorLocalMountURLs(ref manifest.Ref, doc manifest.Document) []doctorItem {
	var items []doctorItem
	for _, mount := range manifest.EffectiveMounts(doc) {
		if !localMountGitURL(mount.GitURL) {
			continue
		}
		items = append(items, doctorItem{
			Name:    ref.Name + ":mount:" + mount.ID,
			Status:  "local-only",
			Path:    mount.GitURL,
			Message: "mount git_url is local-only; run our publish --manifest " + ref.Name,
			Details: []string{
				"manifest=" + ref.LocalPath,
			},
		})
	}
	return items
}

func (a app) doctorVersion(home string) doctorItem {
	current := a.currentOurVersion()
	item := doctorItem{Name: "our", Status: "ok", Message: "current=v" + strings.TrimPrefix(current, "v")}
	notice, err := selfupdate.CheckNotice(context.Background(), selfupdate.NoticeOptions{
		CurrentVersion: current,
		Home:           home,
		Source:         a.updateSource,
		TTL:            selfupdate.UpdateCheckTTLFromEnv(),
		Now:            a.updateNow,
	})
	if err != nil {
		item.Status = "unknown"
		item.Message = item.Message + " update_check=unavailable"
		item.Details = append(item.Details, err.Error())
		return item
	}
	item.Message = fmt.Sprintf("current=v%s latest=v%s", notice.CurrentVersion, notice.LatestVersion)
	if notice.UpdateAvailable {
		item.Status = "stale"
		item.Message += " run our update"
	}
	return item
}

func doctorLegacy(home, root string) []doctorItem {
	homeDir, err := resolveHome(home)
	if err != nil {
		return []doctorItem{{Name: "legacy", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	if root != "" {
		legacyUmbrella := filepath.Join(root, ".flux")
		if info, err := os.Stat(legacyUmbrella); err == nil && info.IsDir() {
			items = append(items, doctorItem{
				Name:    ".flux",
				Status:  "warning",
				Path:    legacyUmbrella,
				Message: "legacy Flux workspace marker found; migrate state to .our or re-run our setup in the intended umbrella",
			})
		}
	}
	legacyShare := filepath.Join(homeDir, ".local", "share", "flux")
	if info, err := os.Stat(legacyShare); err == nil && info.IsDir() {
		items = append(items, doctorItem{
			Name:    "flux data",
			Status:  "warning",
			Path:    legacyShare,
			Message: "legacy Flux data directory found; Our AI uses ~/.local/share/our",
		})
	}
	legacyManifestRegistry := filepath.Join(homeDir, ".config", "flux", "manifests.json")
	if info, err := os.Stat(legacyManifestRegistry); err == nil && !info.IsDir() {
		items = append(items, doctorItem{
			Name:    "flux manifest registry",
			Status:  "warning",
			Path:    legacyManifestRegistry,
			Message: "legacy Flux manifest registry found; Our AI uses ~/.config/our/manifests.json",
		})
	}
	var legacyEnv []string
	for _, pair := range os.Environ() {
		key, _, _ := strings.Cut(pair, "=")
		if strings.HasPrefix(key, "FLUX_") {
			legacyEnv = append(legacyEnv, key)
		}
	}
	if len(legacyEnv) != 0 {
		sort.Strings(legacyEnv)
		items = append(items, doctorItem{
			Name:    "FLUX_* env",
			Status:  "warning",
			Message: "legacy Flux environment variables are set; rename them to OUR_*",
			Details: legacyEnv,
		})
	}
	if path, err := exec.LookPath("flux"); err == nil {
		items = append(items, doctorItem{
			Name:    "flux binary",
			Status:  "warning",
			Path:    path,
			Message: "legacy flux binary is still on PATH; remove it or replace workflows with our",
		})
	}
	for _, h := range []harness.Harness{harness.ClaudeCode, harness.Codex, harness.OpenCode} {
		target := h.SkillTargetPath(homeDir, "flux")
		if _, err := os.Lstat(target); err == nil {
			items = append(items, doctorItem{
				Name:    string(h) + ":flux skill",
				Status:  "warning",
				Path:    target,
				Message: "legacy flux self-skill is installed; run our skills self install " + string(h),
			})
		}
	}
	return items
}

func (a app) doctorFreshness(home, manifestName, umbrellaRoot string, fetch bool, root string) []doctorItem {
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, "local")
	if err != nil {
		return []doctorItem{{Name: "git", Status: "error", Message: err.Error()}}
	}
	if len(entries) == 0 {
		return nil
	}
	refreshes := doctorMountRefreshes(root)
	results := syncer.Inspect(entries, syncer.InspectOptions{Fetch: fetch})
	items := make([]doctorItem, 0, len(results))
	for _, result := range results {
		items = append(items, doctorFreshnessItem(result, fetch, refreshes))
	}
	return items
}

func doctorMountRefreshes(root string) map[string]string {
	if root == "" {
		return nil
	}
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, mount := range state.Mounts {
		if mount.LastSync == "" {
			continue
		}
		out["content\x00"+mount.ID] = mount.LastSync
		out["product\x00"+mount.ID] = mount.LastSync
	}
	return out
}

func doctorFreshnessItem(result syncer.Result, fetch bool, refreshes map[string]string) doctorItem {
	item := doctorItem{
		Name: doctorFreshnessName(result),
		Path: result.LocalPath,
	}
	switch result.Status {
	case "already landed":
		item.Status = "ok"
	case "pending":
		item.Status = doctorPendingFreshnessStatus(result)
	case "held back":
		if strings.Contains(result.Message, "not cloned") {
			item.Status = "missing"
		} else {
			item.Status = "held back"
		}
	case "unknown":
		item.Status = "unknown"
	case "failed":
		item.Status = "error"
	default:
		item.Status = result.Status
	}
	item.Message = doctorFreshnessMessage(result, fetch)
	if result.Branch != "" {
		item.Details = append(item.Details, "branch="+result.Branch)
	}
	if result.Head != "" {
		item.Details = append(item.Details, "head="+result.Head)
	}
	if lastRefresh := refreshes[result.Role+"\x00"+result.ID]; lastRefresh != "" {
		item.Details = append(item.Details, "last_refresh="+lastRefresh)
	}
	if len(result.Dirty) != 0 {
		item.Details = append(item.Details, "dirty="+strings.Join(result.Dirty, ","))
	}
	if len(result.Changed) != 0 {
		item.Details = append(item.Details, "changed="+strings.Join(result.Changed, ","))
	}
	if result.FetchError != "" {
		item.Details = append(item.Details, "fetch_error="+result.FetchError)
	}
	if result.Error != "" {
		item.Details = append(item.Details, result.Error)
	}
	if item.Status == "stale" && !result.BehindUnknown && len(result.Dirty) == 0 &&
		result.Ahead == 0 && result.Behind > 0 &&
		(result.Role == "manifest" || result.Role == "content") {
		item.WouldFix = "fast-forward"
	}
	return item
}

// clearDoctorWouldFix drops dry-run plans once --fix is actually applying them.

// clearDoctorWouldFix drops dry-run plans once --fix is actually applying them.
func clearDoctorWouldFix(items []doctorItem) {
	for i := range items {
		items[i].WouldFix = ""
	}
}

func doctorFreshnessName(result syncer.Result) string {
	name := result.Role + ":" + result.ID
	if result.Manifest != "" && result.Role != "manifest" {
		return result.Manifest + ":" + name
	}
	return name
}

func doctorPendingFreshnessStatus(result syncer.Result) string {
	if result.BehindUnknown {
		return "unknown"
	}
	if len(result.Dirty) != 0 {
		return "dirty"
	}
	if result.Ahead != 0 && result.Behind != 0 {
		return "diverged"
	}
	if result.Ahead != 0 {
		return "ahead"
	}
	if result.Behind != 0 {
		return "stale"
	}
	return "warning"
}

func doctorFreshnessMessage(result syncer.Result, fetch bool) string {
	if result.Status == "held back" || result.Status == "failed" {
		if result.Message != "" {
			return result.Message
		}
		return result.Error
	}
	if result.Status == "already landed" {
		if fetch {
			return "up to date"
		}
		return "up to date (as of last fetch)"
	}
	parts := []string{}
	if result.BehindUnknown {
		parts = append(parts, "behind=unknown (remote unreachable)")
	} else {
		parts = append(parts, fmt.Sprintf("behind=%d", result.Behind))
	}
	parts = append(parts, fmt.Sprintf("ahead=%d", result.Ahead))
	if len(result.Dirty) != 0 {
		parts = append(parts, fmt.Sprintf("dirty=%d", len(result.Dirty)))
	}
	if !fetch {
		parts = append(parts, "as of last fetch")
	}
	if result.Message != "" {
		parts = append(parts, result.Message)
	}
	return strings.Join(parts, " ")
}

func (a app) doctorFix(home, manifestName, umbrellaRoot, root string, derived []doctorItem) []doctorItem {
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, "local")
	if err != nil {
		return []doctorItem{{Name: "git", Status: "error", Message: err.Error()}}
	}
	entryByPath := map[string]syncer.Entry{}
	for _, entry := range entries {
		entryByPath[entry.LocalPath] = entry
	}
	results := syncer.Inspect(entries, syncer.InspectOptions{Fetch: true})
	var items []doctorItem
	manifestFixed := false
	for _, result := range results {
		entry, ok := entryByPath[result.LocalPath]
		if !ok {
			continue
		}
		item, fixedManifest, include := doctorFixFreshnessItem(entry, result)
		if !include {
			continue
		}
		if fixedManifest {
			manifestFixed = true
		}
		items = append(items, item)
	}
	if manifestFixed || doctorDerivedHasDrift(derived) {
		items = append(items, a.doctorFixDerived(home, manifestName, root)...)
	}
	items = append(items, a.doctorFixSelfSkill(home)...)
	return items
}

func (a app) doctorFixSelfSkill(home string) []doctorItem {
	rows, err := selfskill.Inspect(harness.All(), selfskill.Options{Home: home})
	if err != nil {
		return []doctorItem{{Name: "selfskill", Status: "error", Message: err.Error()}}
	}
	var hs []harness.Harness
	for _, row := range rows {
		if row.Status == "absent" || row.Status == "stale" {
			hs = append(hs, row.Harness)
		}
	}
	if len(hs) == 0 {
		return nil
	}
	results, err := selfskill.Install(hs, selfskill.Options{Home: home, Link: true, SkipMissing: true})
	if err != nil {
		return []doctorItem{{Name: "selfskill", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	for _, result := range results {
		item := doctorItem{
			Name:    "selfskill:" + string(result.Harness),
			Status:  doctorFixStatusFromSkill(result.Status),
			Path:    result.TargetPath,
			Message: result.Message,
		}
		if item.Message == "" {
			item.Message = "reinstalled our self-skill"
		}
		if result.Err != nil {
			item.Details = append(item.Details, result.Err.Error())
		}
		items = append(items, item)
	}
	return items
}

func doctorFixFreshnessItem(entry syncer.Entry, result syncer.Result) (doctorItem, bool, bool) {
	item := doctorItem{Name: doctorFreshnessName(result), Path: result.LocalPath}
	if result.BehindUnknown || result.Status == "unknown" {
		item.Status = "skipped"
		item.Message = "remote freshness unknown"
		return item, false, true
	}
	if result.Status == "failed" {
		item.Status = "error"
		item.Message = result.Error
		return item, false, true
	}
	if result.Role == "repo" && result.Behind > 0 {
		item.Status = "skipped"
		item.Message = "repo checkouts are never fixed by doctor"
		return item, false, true
	}
	if len(result.Dirty) != 0 {
		item.Status = "skipped"
		item.Message = "dirty checkout; commit or stash before fixing"
		item.Details = append(item.Details, "dirty="+strings.Join(result.Dirty, ","))
		return item, false, true
	}
	if result.Ahead != 0 && result.Behind != 0 {
		item.Status = "skipped"
		item.Message = "diverged checkout; reconcile manually"
		return item, false, true
	}
	if result.Behind == 0 {
		return doctorItem{}, false, false
	}
	if result.Role != "manifest" && result.Role != "content" {
		item.Status = "skipped"
		item.Message = "only manifest and read-mostly content checkouts are fixed"
		return item, false, true
	}
	fixed := syncer.FastForward(entry, syncer.FastForwardOptions{})
	item.Message = fixed.Message
	switch fixed.Status {
	case "pulled":
		item.Status = "fixed"
		item.Message = "pulled --ff-only"
		if fixed.Head != "" {
			item.Details = append(item.Details, "head="+fixed.Head)
		}
		return item, result.Role == "manifest", true
	case "already landed":
		item.Status = "skipped"
		item.Message = "already up to date"
	case "failed":
		item.Status = "error"
		item.Message = fixed.Error
	default:
		item.Status = "skipped"
		if item.Message == "" {
			item.Message = fixed.Status
		}
	}
	return item, false, true
}

func doctorDerivedHasDrift(items []doctorItem) bool {
	for _, item := range items {
		if item.Status != "ok" {
			return true
		}
	}
	return false
}

func (a app) doctorFixDerived(home, manifestName, root string) []doctorItem {
	if root == "" {
		return []doctorItem{{Name: "derived", Status: "skipped", Message: "no our umbrella found; run our setup or pass --umbrella"}}
	}
	report, err := a.reconcileDerived(home, manifestName, root)
	if err != nil {
		return []doctorItem{{Name: "derived", Status: "error", Message: err.Error()}}
	}
	items := doctorDerivedFixItems(report)
	items = append(items, a.doctorFixLegacyGlobalOrgSkills(home, manifestName)...)
	return items
}

func doctorDerivedFixItems(report derivedReconcileReport) []doctorItem {
	items := []doctorItem{{
		Name:    "guidance",
		Status:  doctorFixStatusFromGuidance(report.Guidance.Status),
		Path:    report.Guidance.TargetPath,
		Message: report.Guidance.Message,
	}}
	if report.Guidance.ClaudePath != "" {
		items[0].Details = append(items[0].Details, "claude_path="+report.Guidance.ClaudePath)
	}
	for _, result := range report.Skills {
		item := doctorItem{
			Name:    "skill:" + string(result.Harness) + ":" + result.Skill,
			Status:  doctorFixStatusFromSkill(result.Status),
			Path:    result.TargetPath,
			Message: result.Message,
		}
		if result.CanonicalID != "" {
			item.Details = append(item.Details, "canonical_id="+result.CanonicalID)
		}
		if result.Err != nil {
			item.Details = append(item.Details, result.Err.Error())
		}
		items = append(items, item)
	}
	return items
}

func doctorFixStatusFromGuidance(status string) string {
	switch status {
	case "installed", "updated":
		return "fixed"
	case "blocked":
		return "error"
	default:
		return status
	}
}

func doctorFixStatusFromSkill(status string) string {
	switch status {
	case skills.StatusInstalled, skills.StatusUpdated, skills.StatusRemoved:
		return "fixed"
	case skills.StatusFailed, skills.StatusBlocked:
		return "error"
	case skills.StatusSkipped, skills.StatusDryRun, skills.StatusNotInstalled:
		return "skipped"
	default:
		return status
	}
}

func doctorFixFailed(items []doctorItem) bool {
	for _, item := range items {
		if item.Status == "error" {
			return true
		}
	}
	return false
}

func (a app) doctorDerived(home, manifestName, root string) []doctorItem {
	var items []doctorItem
	items = append(items, a.doctorGlobalOrgSkills(home, manifestName)...)
	items = append(items, a.doctorSelfSkill(home)...)
	if root != "" {
		items = append(items, doctorDerivedGuidance(home, root, manifestName))
	}
	if len(items) == 0 {
		items = append(items, doctorItem{Name: "derived", Status: "ok", Message: "no derived drift detected"})
	}
	return items
}

func (a app) doctorGlobalOrgSkills(home, manifestName string) []doctorItem {
	opts := skillsCommandOpts{home: home, manifestName: manifestName, quietSource: true, allowMissingToolSkills: true}
	found, leftovers, err := a.scanLegacyGlobalOrgSkills(opts, harness.All())
	if err != nil {
		return []doctorItem{{Name: "skills", Status: "error", Message: err.Error()}}
	}
	if len(found) == 0 {
		return nil
	}
	var items []doctorItem
	for _, leftover := range leftovers {
		existing := leftover.Installed
		item := doctorItem{
			Name:     "skill:" + string(existing.Harness) + ":" + existing.Skill,
			Status:   "legacy-global",
			Path:     existing.TargetPath,
			Message:  "user-global org skill is legacy; org skills are now launch-scoped",
			WouldFix: "remove legacy user-global org skill",
		}
		if existing.CanonicalID != "" {
			item.Details = append(item.Details, "canonical_id="+existing.CanonicalID)
		}
		if existing.LinkTarget != "" {
			item.Details = append(item.Details, "link_target="+existing.LinkTarget)
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		items = append(items, doctorItem{Name: "skills", Status: "ok", Message: "org skills are launch-scoped; no legacy user-global org skills detected"})
	}
	return items
}

func (a app) doctorFixLegacyGlobalOrgSkills(home, manifestName string) []doctorItem {
	opts := skillsCommandOpts{home: home, manifestName: manifestName, quietSource: true, allowMissingToolSkills: true}
	results, err := a.removeLegacyGlobalOrgSkills(opts, harness.All())
	if err != nil {
		return []doctorItem{{Name: "skills", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	for _, result := range results {
		item := doctorItem{
			Name:    "skill:" + string(result.Harness) + ":" + result.Skill,
			Status:  doctorFixStatusFromSkill(result.Status),
			Path:    result.TargetPath,
			Message: result.Message,
		}
		if result.CanonicalID != "" {
			item.Details = append(item.Details, "canonical_id="+result.CanonicalID)
		}
		if result.Err != nil {
			item.Details = append(item.Details, result.Err.Error())
		}
		items = append(items, item)
	}
	return items
}

func (a app) doctorSelfSkill(home string) []doctorItem {
	rows, err := selfskill.Inspect(harness.All(), selfskill.Options{Home: home})
	if err != nil {
		return []doctorItem{{Name: "selfskill", Status: "error", Message: err.Error()}}
	}
	var items []doctorItem
	for _, row := range rows {
		if row.Status == "installed" || row.Status == "missing-harness" {
			continue
		}
		item := doctorItem{
			Name:   "selfskill:" + string(row.Harness),
			Status: row.Status,
			Path:   row.TargetPath,
		}
		if row.Message != "" {
			item.Message = row.Message
		} else {
			item.Message = row.Remedy
		}
		if row.CanonicalID != "" {
			item.Details = append(item.Details, "canonical_id="+row.CanonicalID)
		}
		if row.LinkTarget != "" {
			item.Details = append(item.Details, "link_target="+row.LinkTarget)
		}
		if row.Remedy != "" && row.Remedy != item.Message {
			item.Details = append(item.Details, "remedy="+row.Remedy)
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		items = append(items, doctorItem{Name: "selfskill", Status: "ok", Message: "our self-skill installed for present harnesses"})
	}
	return items
}

func doctorSkillHarnessPresent(row skillStatusRow) bool {
	if row.TargetPath == "" {
		return false
	}
	info, err := os.Stat(filepath.Dir(row.TargetPath))
	return err == nil && info.IsDir()
}

func doctorSkillStatusOK(status string) bool {
	return status == "installed"
}

func doctorDerivedGuidance(home, root, manifestName string) doctorItem {
	if manifestName == "" {
		if ws, err := umbrella.LoadWorkspace(root); err == nil {
			manifestName = ws.ManifestRef
		}
	}
	item := doctorGuidance(home, root, manifestName)
	item.Name = "guidance"
	if item.Status == "ok" && item.Message == "" {
		item.Message = "workspace guidance matches current manifest"
	}
	return item
}

func doctorLastSync(root string) doctorItem {
	path := filepath.Join(root, umbrella.DirName, lastSyncFile)
	audit, ok, err := loadLastSyncAudit(root)
	if err != nil {
		return doctorItem{Name: "last publish", Status: "error", Path: path, Message: err.Error()}
	}
	if !ok {
		return doctorItem{Name: "last publish", Status: "missing", Path: path, Message: "run our sync to record an audit"}
	}
	item := doctorItem{
		Name:    "last publish",
		Status:  "ok",
		Path:    path,
		Message: lastSyncSummary(audit),
	}
	if syncReportFailed(audit.Report) {
		item.Status = "warning"
	}
	for _, result := range audit.Report.Results {
		item.Details = append(item.Details, lastSyncResultDetail(result))
	}
	return item
}

func lastSyncSummary(audit lastSyncAudit) string {
	parts := []string{"saved_at=" + audit.SavedAt}
	if audit.Report.Publish != "" {
		parts = append(parts, "publish="+audit.Report.Publish)
	}
	if audit.Report.Backend != "" {
		parts = append(parts, "backend="+audit.Report.Backend)
	}
	for _, part := range syncStatusCounts(audit.Report.Results) {
		parts = append(parts, part)
	}
	return strings.Join(parts, " ")
}

func syncStatusCounts(results []syncer.Result) []string {
	order := []string{"pushed", "pulled", "held back", "dry-run", "already landed", "failed"}
	counts := map[string]int{}
	for _, result := range results {
		counts[result.Status]++
	}
	var out []string
	for _, status := range order {
		if counts[status] == 0 {
			continue
		}
		out = append(out, strings.ReplaceAll(status, " ", "_")+"="+strconv.Itoa(counts[status]))
		delete(counts, status)
	}
	var rest []string
	for status := range counts {
		rest = append(rest, status)
	}
	sort.Strings(rest)
	for _, status := range rest {
		out = append(out, strings.ReplaceAll(status, " ", "_")+"="+strconv.Itoa(counts[status]))
	}
	return out
}

func lastSyncResultDetail(result syncer.Result) string {
	parts := []string{result.Role + ":" + result.ID, "status=" + strings.ReplaceAll(result.Status, " ", "_")}
	if result.Manifest != "" {
		parts = append(parts, "manifest="+result.Manifest)
	}
	if result.GitURL != "" {
		parts = append(parts, "remote="+result.GitURL)
	}
	if result.Branch != "" {
		parts = append(parts, "branch="+result.Branch)
	}
	if result.Head != "" {
		parts = append(parts, "head="+result.Head)
	}
	if result.Direction != "" {
		parts = append(parts, "direction="+result.Direction)
	}
	return strings.Join(parts, " ")
}

func doctorWorkspaces(home, manifestName string, declared []manifest.Workspace) []doctorItem {
	homeDir, err := resolveHome(home)
	if err != nil {
		return []doctorItem{{Name: manifestName, Status: "error", Message: err.Error()}}
	}
	out := make([]doctorItem, 0, len(declared))
	for _, w := range declared {
		path := expandUserPath(homeDir, w.LocalPath)
		item := doctorItem{Name: manifestName + ":" + w.ID, Path: path}
		if path == "" {
			item.Status = "error"
			item.Message = "local_path is required"
			out = append(out, item)
			continue
		}
		if info, err := os.Stat(path); err != nil {
			item.Status = "missing"
			item.Message = "run our workspaces sync " + w.ID + " --manifest " + manifestName
		} else if !info.IsDir() {
			item.Status = "error"
			item.Message = "target exists and is not a directory"
		} else if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
			item.Status = "warning"
			item.Message = "target exists but is not a git repository"
		} else {
			item.Status = "ok"
		}
		out = append(out, item)
	}
	return out
}

func doctorTools(manifestName string, tools []manifest.Tool) []doctorItem {
	out := make([]doctorItem, 0, len(tools))
	for _, tool := range tools {
		item := doctorItem{Name: manifestName + ":" + tool.ID}
		if path, err := exec.LookPath(tool.ID); err == nil {
			item.Status = "ok"
			item.Path = path
		} else {
			item.Status = "missing"
			item.Message = tool.Purpose
			item.Details = append(item.Details, tool.Install.Commands...)
			if tool.Install.DocsURL != "" {
				item.Details = append(item.Details, tool.Install.DocsURL)
			}
		}
		out = append(out, item)
	}
	return out
}

func doctorServices(manifestName, manifestRoot string, services []manifest.Service) []doctorItem {
	out := make([]doctorItem, 0, len(services))
	for _, service := range services {
		item := doctorItem{Name: manifestName + ":" + service.ID, Status: "ok"}
		warn := func(format string, args ...any) {
			if item.Status == "ok" {
				item.Status = "warning"
			}
			item.Details = append(item.Details, fmt.Sprintf(format, args...))
		}
		fail := func(format string, args ...any) {
			item.Status = "error"
			item.Details = append(item.Details, fmt.Sprintf(format, args...))
		}

		if service.Kind == "mcp" && service.Connection.IsZero() {
			switch {
			case service.DescribeRef == "":
				fail("no connection data; add an inline connection or a checked-in descriptor")
			case strings.HasPrefix(service.DescribeRef, "http://"), strings.HasPrefix(service.DescribeRef, "https://"):
				warn("describe_ref %s is a URL; not materializable offline — add an inline connection or a checked-in descriptor", service.DescribeRef)
			default:
				path := filepath.Join(manifestRoot, filepath.FromSlash(service.DescribeRef))
				if _, err := os.Stat(path); err != nil {
					fail("descriptor %s missing at %s; add the file or an inline connection", service.DescribeRef, path)
				}
			}
		}

		for _, name := range serviceEnvVars(service) {
			if _, ok := os.LookupEnv(name); !ok {
				warn("environment variable %s is not set", name)
			}
		}
		if strings.HasPrefix(service.AuthRef, "op://") {
			if _, err := exec.LookPath("op"); err != nil {
				warn("auth_ref %s needs the op CLI, which is not on PATH", service.AuthRef)
			}
		}

		if item.Message == "" && len(item.Details) != 0 {
			item.Message = item.Details[0]
			item.Details = item.Details[1:]
		}
		out = append(out, item)
	}
	return out
}

// serviceEnvVars collects environment variable names a service expects
// locally: an env:// auth reference plus ${VAR} placeholders in inline
// connection env values.

// serviceEnvVars collects environment variable names a service expects
// locally: an env:// auth reference plus ${VAR} placeholders in inline
// connection env values.
func serviceEnvVars(service manifest.Service) []string {
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if name, ok := strings.CutPrefix(service.AuthRef, "env://"); ok {
		add(name)
	}
	for _, value := range service.Connection.Env {
		rest := value
		for {
			_, after, found := strings.Cut(rest, "${")
			if !found {
				break
			}
			name, after, found := strings.Cut(after, "}")
			if !found {
				break
			}
			add(name)
			rest = after
		}
	}
	return names
}

func (a app) printDoctorReport(report doctorReport) {
	fixable := 0
	printItems := func(kind string, items []doctorItem) {
		for _, item := range items {
			line := fmt.Sprintf("%s\t%s\t%s", kind, item.Name, item.Status)
			if item.Path != "" {
				line += "\t" + item.Path
			}
			if item.Message != "" {
				line += "\t" + item.Message
			}
			if item.WouldFix != "" {
				fixable++
				line += "\twould " + item.WouldFix
			}
			fmt.Fprintln(a.stdout, line)
			for _, detail := range item.Details {
				fmt.Fprintf(a.stdout, "%s\t%s\tdetail\t%s\n", kind, item.Name, detail)
			}
		}
	}
	printItems("manifest", report.Manifests)
	printItems("version", report.Version)
	printItems("legacy", report.Legacy)
	printItems("umbrella", report.Umbrella)
	printItems("freshness", report.Freshness)
	printItems("derived", report.Derived)
	printItems("fix", report.Fixes)
	printItems("last-sync", report.LastSync)
	printItems("session", report.Sessions)
	printItems("workspace", report.Workspaces)
	printItems("tool", report.Tools)
	printItems("service", report.Services)
	if fixable > 0 {
		fmt.Fprintf(a.stdout, "fixable\t%d\trun `our doctor --fix` to apply\n", fixable)
	}
}

func doctorUmbrella(home, root string) []doctorItem {
	ws, err := umbrella.LoadWorkspace(root)
	if err != nil {
		return []doctorItem{{Name: root, Status: "error", Path: root, Message: err.Error()}}
	}
	items := []doctorItem{{
		Name:    ws.Organization,
		Status:  "ok",
		Path:    root,
		Message: "manifest " + ws.ManifestRef,
	}}
	items = append(items, doctorGuidance(home, root, ws.ManifestRef))
	state, err := umbrella.LoadState(root)
	if err != nil {
		items = append(items, doctorItem{Name: "state", Status: "error", Path: filepath.Join(root, umbrella.DirName, umbrella.StateFile), Message: err.Error()})
		return items
	}
	for _, mount := range state.Mounts {
		item := doctorItem{
			Name:    mount.ID,
			Status:  mount.Status,
			Path:    stateMountPath(root, mount),
			Message: mount.Kind,
		}
		if mount.LastError != "" {
			item.Details = append(item.Details, mount.LastError)
		}
		items = append(items, item)
	}
	return items
}

func doctorGuidance(home, root, manifestName string) doctorItem {
	item := doctorItem{Name: "guidance"}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		item.Status = "error"
		item.Message = err.Error()
		return item
	}
	opts := guidance.Options{}
	if state, err := umbrella.LoadState(root); err == nil {
		role, err := roleByID(doc.doc, state.SelectedRole)
		if err != nil {
			item.Status = "error"
			item.Message = err.Error()
			return item
		}
		opts.RoleGuidancePaths = role.GuidancePaths
	} else if !errors.Is(err, os.ErrNotExist) {
		item.Status = "error"
		item.Message = err.Error()
		return item
	}
	result, err := guidance.CheckWithOptions(root, doc.ref.LocalPath, doc.doc, opts)
	if err != nil {
		item.Status = "error"
		item.Path = result.AgentsPath
		item.Message = err.Error()
		return item
	}
	item.Status = result.Status
	item.Path = result.AgentsPath
	item.Message = result.Message
	if result.ClaudePath != "" {
		item.Details = append(item.Details, "claude_path="+result.ClaudePath)
	}
	return item
}
