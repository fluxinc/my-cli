package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/workspace"
)

func (a app) runWorkspace(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing workspace subcommand")
	}

	switch args[0] {
	case "list":
		return a.runWorkspaceList(args[1:])
	case "sync":
		return a.runWorkspaceSync(args[1:])
	case "-h", "--help", "help":
		a.printWorkspaceUsage()
		return nil
	default:
		return fmt.Errorf("unknown workspace subcommand %q", args[0])
	}
}

func (a app) printWorkspaceUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my workspaces list [--manifest NAME] [--home DIR] [--json]
  my workspaces sync <workspace...> | --all [--manifest NAME] [--home DIR] [--print] [--json]

Workspace data comes from synced organization manifests. Use manifest:workspace
to disambiguate duplicate workspace IDs across manifests.`)
}

func (a app) runWorkspaceList(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("my workspaces list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("workspace list does not accept positional arguments")
	}
	entries, err := workspace.List(home, manifestName)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, entries)
	}
	for _, entry := range entries {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", entry.Manifest, entry.ID, entry.GitURL, entry.LocalPath)
	}
	return nil
}

func (a app) runWorkspaceSync(args []string) error {
	var home string
	var manifestName string
	var all bool
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("my workspaces sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&all, "all", false, "sync every selected workspace")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	results, err := workspace.Sync(home, manifestName, rest, all, printOnly, nil)
	if err != nil {
		return err
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
			return err
		}
	} else {
		a.printWorkspaceResults(results)
	}
	if workspaceResultsFailed(results) {
		return fmt.Errorf("one or more workspace syncs failed")
	}
	return nil
}

func (a app) printWorkspaceResults(results []workspace.SyncResult) {
	for _, r := range results {
		line := fmt.Sprintf("%s\t%s\t%s\t%s", r.Manifest, r.Workspace, r.Status, r.LocalPath)
		if r.Message != "" {
			line += "\t" + r.Message
		}
		if r.Error != "" {
			line += "\t" + r.Error
		}
		fmt.Fprintln(a.stdout, line)
	}
}

func recordMountResults(root string, results []workspace.SyncResult) error {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		state = umbrella.State{SchemaVersion: umbrella.SchemaVersion}
	} else if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, result := range results {
		status := result.Status
		lastSync := ""
		lastError := result.Error
		if result.Status == "synced" {
			lastSync = now
			lastError = ""
		} else if result.Status == "inaccessible" || result.Status == "failed" && strings.Contains(result.Error, "gh auth login") {
			status = "inaccessible"
		}
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        result.Workspace,
			Kind:      result.Kind,
			SourceRef: result.SourceRef,
			Status:    status,
			LastSync:  lastSync,
			LastError: lastError,
		})
	}
	return umbrella.SaveState(root, state)
}

func recordMountResultsByRoot(results []workspace.SyncResult) error {
	byRoot := map[string][]workspace.SyncResult{}
	for _, result := range results {
		if result.UmbrellaRoot == "" {
			continue
		}
		byRoot[result.UmbrellaRoot] = append(byRoot[result.UmbrellaRoot], result)
	}
	for root, rootResults := range byRoot {
		if err := recordMountResults(root, rootResults); err != nil {
			return err
		}
	}
	return nil
}

func recordRepoResults(root string, ids []string, results []workspace.SyncResult) error {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		state = umbrella.State{SchemaVersion: umbrella.SchemaVersion}
	} else if err != nil {
		return err
	}
	for _, id := range ids {
		state = umbrella.AddSelectedRepo(state, id)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, result := range results {
		status := result.Status
		lastSync := ""
		lastError := result.Error
		if result.Status == "synced" {
			lastSync = now
			lastError = ""
		}
		state = umbrella.UpsertMount(state, umbrella.MountStatus{
			ID:        result.Workspace,
			Kind:      "repo",
			SourceRef: result.SourceRef,
			Status:    status,
			LastSync:  lastSync,
			LastError: lastError,
		})
	}
	return umbrella.SaveState(root, state)
}

func removeMountsFromState(root string, mountIDs []string, repoIDs []string) error {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, id := range mountIDs {
		state = umbrella.RemoveMount(state, id)
	}
	for _, id := range repoIDs {
		state = umbrella.RemoveSelectedRepo(state, id)
		state = umbrella.RemoveMount(state, repoMountID(id))
	}
	return umbrella.SaveState(root, state)
}

func addStateMountEntries(home, manifestName, umbrellaRoot string, entries []workspace.Entry) ([]workspace.Entry, error) {
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		if strings.Contains(err.Error(), "no my umbrella found") {
			return entries, nil
		}
		return nil, err
	}
	ws, err := umbrella.LoadWorkspace(root)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	if manifestName != "" && manifestName != ws.ManifestRef {
		return entries, nil
	}
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	orgByManifest := map[string]string{}
	for _, entry := range entries {
		seen[entry.ID] = true
		if entry.Manifest != "" && entry.Organization != "" {
			orgByManifest[entry.Manifest] = entry.Organization
		}
	}
	organization := orgByManifest[ws.ManifestRef]
	if organization == "" {
		if docs, err := loadRegisteredDocs(home, ws.ManifestRef); err == nil && len(docs) == 1 {
			organization = docs[0].doc.Organization.ID
		}
	}
	for _, mount := range state.Mounts {
		if seen[mount.ID] {
			continue
		}
		entries = append(entries, workspace.Entry{
			Manifest:     ws.ManifestRef,
			Organization: organization,
			ID:           mount.ID,
			Kind:         mount.Kind,
			Mode:         "optional",
			GitURL:       mount.SourceRef,
			LocalPath:    stateMountPath(root, mount),
			UmbrellaRoot: root,
			SourceRef:    mount.SourceRef,
		})
		seen[mount.ID] = true
	}
	return entries, nil
}

func (a app) runMount(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing mount subcommand")
	}
	switch args[0] {
	case "list":
		return a.runMountList(args[1:])
	case "add":
		return a.runMountAdd(args[1:])
	case "sync":
		return a.runMountSync(args[1:])
	case "remove":
		return a.runMountRemove(args[1:])
	case "-h", "--help", "help":
		a.printMountUsage()
		return nil
	default:
		return fmt.Errorf("unknown mount subcommand %q", args[0])
	}
}

func (a app) printMountUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my mounts list [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]
  my mounts add <kind:id|id> [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
  my mounts sync <mount...> | --all [--manifest NAME] [--home DIR] [--umbrella DIR] [--print] [--json]
  my mounts remove <mount...> [--home DIR] [--umbrella DIR] [--print] [--force] [--json]

Mounts are detached content sources inside the local organization umbrella.`)
}

func (a app) runMountList(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var jsonOut bool
	fs := newFlagSet("my mounts list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
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
		return fmt.Errorf("mount list does not accept positional arguments")
	}
	entries, err := workspace.ListMounts(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	entries, err = addStateMountEntries(home, manifestName, umbrellaRoot, entries)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, entries)
	}
	for _, entry := range entries {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\t%s\n", entry.Manifest, entry.ID, entry.Kind, entry.Mode, entry.GitURL, entry.LocalPath)
	}
	return nil
}

func (a app) runMountAdd(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("my mounts add", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my mounts add <kind:id|id>")
	}
	kind, id := splitMountRef(rest[0])
	if kind == "product" {
		return a.maybeJSONError(jsonOut, structuredCommandError{
			code:        "product_mounts_removed",
			message:     "products are business catalog entries, not checkouts; mount product:" + id + " was removed",
			remediation: "use my repos add " + id + " (declared in catalog/repos.json)",
		})
	}
	if kind == "repo" {
		return a.repoAddByID(home, manifestName, umbrellaRoot, id, printOnly, jsonOut)
	}
	entries, err := workspace.ListMounts(home, manifestName, umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	entry, err := selectMountEntry(entries, kind, id)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	results, err := workspace.SyncMounts(home, entry.Manifest, umbrellaRoot, []string{entry.ID}, false, nil, printOnly, nil)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !printOnly {
		if err := recordMountResultsByRoot(results); err != nil {
			return err
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
			return err
		}
	} else {
		a.printWorkspaceResults(results)
	}
	if workspaceResultsFailed(results) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more mount syncs failed"))
	}
	return nil
}

func (a app) repoAddByID(home, manifestName, umbrellaRoot, id string, printOnly bool, jsonOut bool) error {
	if !portableMountID(id) {
		return fmt.Errorf("repo id %q must be lowercase kebab-case", id)
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	repo, ok, err := manifest.FindRepo(home, doc.ref.Name, id)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !ok {
		return a.maybeJSONError(jsonOut, structuredCommandError{
			code:        "unknown_repo",
			message:     fmt.Sprintf("repo %q is not in catalog/repos.json for manifest %q", id, doc.ref.Name),
			remediation: "my repos list --manifest " + doc.ref.Name,
		})
	}
	if err := checkRepoCloneTarget(umbrella.RepoPath(root, id), repo.GitURL); err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if !printOnly {
		if _, _, err := umbrella.Ensure(root, doc.doc.Organization.ID, doc.ref.Name); err != nil {
			return err
		}
	}
	results := []workspace.SyncResult{workspace.SyncEntry(repoEntry(doc, root, repo), printOnly, nil)}
	normalizeRepoResults(results)
	if !printOnly {
		if err := recordRepoResults(root, []string{id}, results); err != nil {
			return err
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
			return err
		}
	} else {
		a.printWorkspaceResults(results)
	}
	if workspaceResultsFailed(results) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more mount syncs failed"))
	}
	return nil
}

func (a app) runMountSync(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var all bool
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("my mounts sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&all, "all", false, "sync every selected mount")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	mountRefs, repoIDs, err := splitMountSyncRefs(rest, all)
	if err != nil {
		return err
	}
	var results []workspace.SyncResult
	if all || len(mountRefs) != 0 {
		results, err = workspace.SyncMounts(home, manifestName, umbrellaRoot, mountRefs, all, nil, printOnly, nil)
		if err != nil {
			return a.maybeJSONError(jsonOut, err)
		}
	} else if len(repoIDs) == 0 {
		return fmt.Errorf("select a mount ID or pass --all")
	}
	repoResults, err := a.syncRepoMounts(home, manifestName, umbrellaRoot, repoIDs, all, printOnly)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	results = append(results, repoResults...)
	if !printOnly {
		if err := recordMountResultsByRoot(results); err != nil {
			return err
		}
	}
	if jsonOut {
		if err := printJSON(a.stdout, results); err != nil {
			return err
		}
	} else {
		a.printWorkspaceResults(results)
	}
	if workspaceResultsFailed(results) {
		return a.maybeJSONError(jsonOut, fmt.Errorf("one or more mount syncs failed"))
	}
	return nil
}

func (a app) syncRepoMounts(home, manifestName, umbrellaRoot string, repoIDs []string, all bool, printOnly bool) ([]workspace.SyncResult, error) {
	if !all && len(repoIDs) == 0 {
		return nil, nil
	}
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		if all && strings.Contains(err.Error(), "no my umbrella found") {
			return nil, nil
		}
		return nil, err
	}
	ws, err := umbrella.LoadWorkspace(root)
	if err != nil {
		return nil, err
	}
	if manifestName != "" && manifestName != ws.ManifestRef {
		return nil, fmt.Errorf("umbrella uses manifest %q, not %q", ws.ManifestRef, manifestName)
	}
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, err
	}
	if all {
		repoIDs = append([]string(nil), state.SelectedRepos...)
	}
	if len(repoIDs) == 0 {
		return nil, nil
	}
	entries := make([]workspace.Entry, 0, len(repoIDs))
	for _, id := range repoIDs {
		entry, err := repoEntryFromState(home, ws.ManifestRef, root, state, id)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	results := workspace.SyncEntries(entries, printOnly, nil)
	normalizeRepoResults(results)
	if !printOnly {
		if err := recordRepoResults(root, repoIDs, results); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// syncSelectedRepos clones or refreshes the umbrella's selected catalog
// repos at setup time, plus any catalog repo marked default that is not yet
// selected.

// syncSelectedRepos clones or refreshes the umbrella's selected catalog
// repos at setup time, plus any catalog repo marked default that is not yet
// selected.
func (a app) syncSelectedRepos(home string, doc registeredDoc, root string, printOnly bool) ([]workspace.SyncResult, error) {
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	catalog, err := manifest.LoadRepoCatalog(home, doc.ref.Name)
	if err != nil {
		return nil, err
	}
	ids := append([]string(nil), state.SelectedRepos...)
	for _, repo := range catalog {
		if repo.Default && !stringInSlice(ids, repo.ID) {
			ids = append(ids, repo.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	entries := make([]workspace.Entry, 0, len(ids))
	for _, id := range ids {
		repo, ok, err := manifest.FindRepo(home, doc.ref.Name, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, structuredCommandError{
				code:        "unknown_repo",
				message:     fmt.Sprintf("repo %q is not in catalog/repos.json for manifest %q", id, doc.ref.Name),
				remediation: "my repos list --manifest " + doc.ref.Name,
			}
		}
		entries = append(entries, repoEntry(doc, root, repo))
	}
	results := workspace.SyncEntries(entries, printOnly, nil)
	normalizeRepoResults(results)
	if !printOnly {
		if err := recordRepoResults(root, ids, results); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func repoEntryFromState(home, manifestName, root string, state umbrella.State, id string) (workspace.Entry, error) {
	mountID := repoMountID(id)
	for _, mount := range state.Mounts {
		if mount.ID == mountID && mount.Kind == "repo" && mount.SourceRef != "" {
			return workspace.Entry{
				Manifest:     manifestName,
				ID:           mountID,
				Kind:         "repo",
				Mode:         "optional",
				GitURL:       mount.SourceRef,
				LocalPath:    umbrella.RepoPath(root, id),
				UmbrellaRoot: root,
				SourceRef:    mount.SourceRef,
			}, nil
		}
	}
	repo, ok, err := manifest.FindRepo(home, manifestName, id)
	if err != nil {
		return workspace.Entry{}, err
	}
	if !ok {
		return workspace.Entry{}, structuredCommandError{
			code:        "unknown_repo",
			message:     fmt.Sprintf("repo %q is not in catalog/repos.json for manifest %q", id, manifestName),
			remediation: "my repos list --manifest " + manifestName,
		}
	}
	return workspace.Entry{
		Manifest:     manifestName,
		ID:           mountID,
		Kind:         "repo",
		Mode:         "optional",
		GitURL:       repo.GitURL,
		LocalPath:    umbrella.RepoPath(root, id),
		UmbrellaRoot: root,
		SourceRef:    repo.GitURL,
	}, nil
}

func (a app) runMountRemove(args []string) error {
	var home string
	var umbrellaRoot string
	var printOnly bool
	var force bool
	var jsonOut bool
	fs := newFlagSet("my mounts remove", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&printOnly, "print", false, "print planned removals without changing files")
	fs.BoolVar(&force, "force", false, "remove mount directories")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: my mounts remove <mount...>")
	}
	if !force && !printOnly {
		return fmt.Errorf("mount remove requires --force or --print")
	}
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return err
	}
	type removeResult struct {
		Mount      string `json:"mount"`
		TargetPath string `json:"target_path"`
		Status     string `json:"status"`
	}
	var results []removeResult
	var removedMountIDs []string
	var removedRepoIDs []string
	for _, ref := range rest {
		kind, id := splitMountRef(ref)
		if kind == "product" {
			return fmt.Errorf("products are business catalog entries, not checkouts; use repo:%s (see my repos list)", id)
		}
		if !portableMountID(id) {
			return fmt.Errorf("mount id %q must be lowercase kebab-case", id)
		}
		target := mountRemoveTarget(root, kind, id)
		result := removeResult{Mount: id, TargetPath: target}
		if printOnly {
			result.Status = "dry-run"
		} else if err := os.RemoveAll(target); err != nil {
			result.Status = "failed"
		} else {
			result.Status = "removed"
			if kind == "repo" {
				removedRepoIDs = append(removedRepoIDs, id)
			} else {
				removedMountIDs = append(removedMountIDs, id)
			}
		}
		results = append(results, result)
	}
	if !printOnly {
		if err := removeMountsFromState(root, removedMountIDs, removedRepoIDs); err != nil {
			return err
		}
	}
	if jsonOut {
		return printJSON(a.stdout, results)
	}
	for _, result := range results {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", result.Mount, result.Status, result.TargetPath)
	}
	return nil
}

func portableMountID(value string) bool {
	if value == "" || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func splitMountRef(ref string) (kind, id string) {
	if i := strings.Index(ref, ":"); i > 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

func splitMountSyncRefs(refs []string, all bool) ([]string, []string, error) {
	if all && len(refs) != 0 {
		return nil, nil, fmt.Errorf("--all cannot be combined with explicit mount IDs")
	}
	var mountRefs []string
	var repoIDs []string
	for _, ref := range refs {
		kind, id := splitMountRef(ref)
		if kind == "product" {
			return nil, nil, fmt.Errorf("products are business catalog entries, not checkouts; use repo:%s (see my repos list)", id)
		}
		if kind == "repo" {
			if !portableMountID(id) {
				return nil, nil, fmt.Errorf("repo id %q must be lowercase kebab-case", id)
			}
			repoIDs = append(repoIDs, id)
			continue
		}
		mountRefs = append(mountRefs, ref)
	}
	return mountRefs, repoIDs, nil
}

// checkRepoCloneTarget guards an idempotent repo add: an existing clone of
// the same remote is adopted, anything else at the path is a structured hold.

// checkRepoCloneTarget guards an idempotent repo add: an existing clone of
// the same remote is adopted, anything else at the path is a structured hold.
func checkRepoCloneTarget(path, gitURL string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return structuredCommandError{
			code:        "repo_path_conflict",
			message:     fmt.Sprintf("repo path %s exists and is not a directory", path),
			remediation: "move the conflicting file aside, then rerun my repos add",
		}
	}
	if !isGitCheckout(path) {
		if entries, readErr := os.ReadDir(path); readErr == nil && len(entries) == 0 {
			return nil
		}
		return structuredCommandError{
			code:        "repo_path_conflict",
			message:     fmt.Sprintf("repo path %s exists but is not a git checkout", path),
			remediation: "move the conflicting directory aside, then rerun my repos add",
		}
	}
	origin, _ := gitCmdOutput(path, "remote", "get-url", "origin")
	if origin != "" && origin != gitURL {
		return structuredCommandError{
			code:        "repo_remote_mismatch",
			message:     fmt.Sprintf("repo path %s tracks %s, not the declared %s", path, origin, gitURL),
			remediation: "reconcile or move the existing checkout, then rerun my repos add",
		}
	}
	return nil
}

func repoMountID(id string) string {
	return "repo:" + id
}

func mountRemoveTarget(root, kind, id string) string {
	if kind == "repo" {
		return umbrella.RepoPath(root, id)
	}
	return filepath.Join(root, id)
}

func stateMountPath(root string, mount umbrella.MountStatus) string {
	if mount.Kind == "repo" && strings.HasPrefix(mount.ID, "repo:") {
		return umbrella.RepoPath(root, strings.TrimPrefix(mount.ID, "repo:"))
	}
	return umbrella.MountPath(root, mount.ID)
}

func repoEntry(doc registeredDoc, root string, repo manifest.Repo) workspace.Entry {
	return workspace.Entry{
		Manifest:     doc.ref.Name,
		Organization: doc.doc.Organization.ID,
		ID:           repoMountID(repo.ID),
		Kind:         "repo",
		Mode:         "optional",
		GitURL:       repo.GitURL,
		LocalPath:    umbrella.RepoPath(root, repo.ID),
		UmbrellaRoot: root,
		SourceRef:    repo.GitURL,
	}
}

func normalizeRepoResults(results []workspace.SyncResult) {
	for i := range results {
		if results[i].Status == "failed" && strings.Contains(results[i].Error, "gh auth login") {
			results[i].Status = "inaccessible"
		}
	}
}

func selectMountEntry(entries []workspace.Entry, kind, id string) (workspace.Entry, error) {
	var matches []workspace.Entry
	for _, entry := range entries {
		if entry.ID != id {
			continue
		}
		if kind != "" && entry.Kind != kind {
			continue
		}
		matches = append(matches, entry)
	}
	if len(matches) == 0 {
		if kind != "" {
			return workspace.Entry{}, fmt.Errorf("mount %q is not declared for kind %q", id, kind)
		}
		return workspace.Entry{}, fmt.Errorf("mount %q is not declared by any selected manifest", id)
	}
	if len(matches) > 1 {
		return workspace.Entry{}, fmt.Errorf("mount %q is ambiguous; pass --manifest", id)
	}
	return matches[0], nil
}

func resolveUmbrellaRoot(home, explicit string) (string, error) {
	if explicit != "" {
		if explicit == "~" {
			resolved, err := resolveHome(home)
			if err != nil {
				return "", err
			}
			return resolved, nil
		}
		resolvedHome, err := resolveHome(home)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(explicit, "~/") {
			return filepath.Join(resolvedHome, explicit[2:]), nil
		}
		return filepath.Abs(explicit)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return root, nil
	}
	return "", fmt.Errorf("no my umbrella found; run my setup or pass --umbrella")
}
