package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/record"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/worksession"
	"github.com/fluxinc/my-cli/internal/workspace"
)

func contentRoots(home, manifestName, workspaceID, umbrellaRoot, noun string, kinds []string) ([]record.Root, error) {
	filterRoots := func(bindingManifest string, roots []record.Root) ([]record.Root, error) {
		return applyDataBinding(home, bindingManifest, noun, roots)
	}
	if umbrellaRoot != "" {
		root, err := resolveUmbrellaRoot(home, umbrellaRoot)
		if err != nil {
			return nil, err
		}
		bindingManifest := manifestName
		if bindingManifest == "" {
			if ws, err := umbrella.LoadWorkspace(root); err == nil {
				bindingManifest = ws.ManifestRef
			} else if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		if roots, ok, err := currentSessionContentRoots(root, workspaceID, noun, kinds); ok || err != nil {
			if err != nil {
				return nil, err
			}
			return filterRoots(bindingManifest, roots)
		}
		roots, err := umbrellaContentRootsForRoot(home, manifestName, root, workspaceID, noun, kinds)
		if err != nil {
			return nil, err
		}
		return filterRoots(bindingManifest, roots)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		if roots, ok, err := currentSessionContentRoots(root, workspaceID, noun, kinds); ok || err != nil {
			if err != nil {
				return nil, err
			}
			return filterRoots(manifestName, roots)
		}
		roots, err := umbrellaContentRootsForRoot(home, manifestName, root, workspaceID, noun, kinds)
		if err != nil {
			return nil, err
		}
		return filterRoots(manifestName, roots)
	}
	if roots, ok, err := configuredUmbrellaContentRoots(home, manifestName, workspaceID, noun, kinds); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return filterRoots(manifestName, roots)
	}
	if manifestName == "" {
		return nil, noUmbrellaError("no my umbrella found; run my setup or pass --umbrella", "run my setup or pass --umbrella <path>")
	}
	entries, err := workspace.List(home, manifestName)
	if err != nil {
		return nil, err
	}
	var roots []record.Root
	for _, entry := range entries {
		if workspaceID != "" && entry.ID != workspaceID {
			continue
		}
		roots = append(roots, record.Root{
			Manifest:     entry.Manifest,
			Workspace:    entry.ID,
			Path:         entry.LocalPath,
			ContentPaths: mountContentPaths(entry.Kind, entry.IncludePaths),
		})
	}
	if len(roots) == 0 {
		if workspaceID != "" {
			return nil, fmt.Errorf("workspace %q is not declared by any selected manifest", workspaceID)
		}
		return nil, fmt.Errorf("no workspaces declared by selected manifests")
	}
	return filterRoots(manifestName, roots)
}

func applyDataBinding(home, manifestName, noun string, roots []record.Root) ([]record.Root, error) {
	dataType := dataTypeForContentNoun(noun)
	if !manifest.ValidDataType(dataType) {
		return roots, nil
	}
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return roots, nil
	}
	mountIDs := map[string]bool{}
	var serviceRefs []string
	for _, doc := range docs {
		binding, ok := doc.doc.DataBindings[dataType]
		if !ok {
			continue
		}
		kind, id, ok := manifest.ParseSurfaceRef(binding.Surface)
		if !ok {
			// loadRegisteredDocs validates manifests before returning them, so this
			// path only protects against tests or future callers bypassing validation.
			return nil, fmt.Errorf("data binding %s has invalid surface %q", dataType, binding.Surface)
		}
		switch kind {
		case "mount":
			mountIDs[id] = true
		case "service":
			serviceRefs = append(serviceRefs, doc.ref.Name+":"+id)
		}
	}
	if len(serviceRefs) != 0 {
		sort.Strings(serviceRefs)
		return nil, fmt.Errorf("%s is bound to service surface(s) %s; service-backed data domains are not implemented yet", dataType, strings.Join(serviceRefs, ", "))
	}
	if len(mountIDs) == 0 {
		return roots, nil
	}
	filtered := make([]record.Root, 0, len(roots))
	for _, root := range roots {
		if mountIDs[root.Workspace] {
			filtered = append(filtered, root)
		}
	}
	if len(filtered) == 0 {
		ids := make([]string, 0, len(mountIDs))
		for id := range mountIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("%s is bound to mount(s) %s, but no matching synced mount roots are available", dataType, strings.Join(ids, ", "))
	}
	return filtered, nil
}

func dataTypeForContentNoun(noun string) string {
	switch noun {
	case "customer":
		return "customers"
	case "meeting":
		return "meetings"
	default:
		return noun
	}
}

func configuredUmbrellaContentRoots(home, manifestName, workspaceID, noun string, kinds []string) ([]record.Root, bool, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil || len(docs) == 0 {
		return nil, false, nil
	}
	type candidate struct {
		root string
		ref  string
	}
	var candidates []candidate
	var configured []candidate
	for _, doc := range docs {
		root, err := umbrella.ResolveRoot(home, "", "", doc.doc)
		if err != nil {
			return nil, true, err
		}
		configured = append(configured, candidate{root: root, ref: doc.ref.Name})
		if _, err := umbrella.LoadWorkspace(root); err == nil {
			candidates = append(candidates, candidate{root: root, ref: doc.ref.Name})
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, true, err
		}
	}
	if len(candidates) == 1 {
		roots, err := umbrellaContentRoots(home, candidates[0].ref, workspaceID, candidates[0].root, noun, kinds)
		return roots, true, err
	}
	if len(candidates) > 1 {
		return nil, true, fmt.Errorf("multiple my umbrellas configured; pass --manifest or --umbrella")
	}
	if manifestName == "" && len(configured) == 1 {
		return nil, true, noUmbrellaError(
			fmt.Sprintf("no my umbrella found; configured umbrella is %s", configured[0].root),
			fmt.Sprintf("run my setup --manifest %s or pass --umbrella %s", configured[0].ref, configured[0].root),
		)
	}
	return nil, false, nil
}

func umbrellaContentRoots(home, manifestName, workspaceID, umbrellaRoot, noun string, kinds []string) ([]record.Root, error) {
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return nil, err
	}
	return umbrellaContentRootsForRoot(home, manifestName, root, workspaceID, noun, kinds)
}

func umbrellaContentRootsForRoot(home, manifestName, root, workspaceID, noun string, kinds []string) ([]record.Root, error) {
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, fmt.Errorf("read umbrella state: %w", err)
	}
	manifestMounts := manifestMountEntriesForRoot(home, manifestName, root)
	var roots []record.Root
	for _, mount := range state.Mounts {
		if workspaceID != "" && mount.ID != workspaceID {
			continue
		}
		// local-only mounts (no origin yet, pre-publish) are present and
		// writable; recording must work before the org is published.
		if mount.Status != "synced" && mount.Status != "local-only" {
			continue
		}
		if !record.ContainsValue(kinds, mount.Kind) {
			continue
		}
		contentPaths := mountContentPaths(mount.Kind, nil)
		if entry, ok := manifestMounts[mount.ID]; ok {
			contentPaths = syncContentPaths(entry)
		}
		roots = append(roots, record.Root{
			Manifest:     mount.SourceRef,
			Workspace:    mount.ID,
			Path:         umbrella.MountPath(root, mount.ID),
			ContentPaths: contentPaths,
		})
	}
	if len(roots) == 0 {
		if workspaceID != "" {
			return nil, fmt.Errorf("workspace %q is not mounted in umbrella %s", workspaceID, root)
		}
		return nil, fmt.Errorf("no %s mounts synced in umbrella %s", noun, root)
	}
	return roots, nil
}

func manifestMountEntriesForRoot(home, manifestName, root string) map[string]workspace.Entry {
	out := map[string]workspace.Entry{}
	if manifestName == "" {
		if ws, err := umbrella.LoadWorkspace(root); err == nil {
			manifestName = ws.ManifestRef
		}
	}
	if manifestName == "" {
		return out
	}
	mounts, err := workspace.ListMounts(home, manifestName, root)
	if err != nil {
		return out
	}
	for _, mount := range mounts {
		if mount.UmbrellaRoot == root {
			out[mount.ID] = mount
		}
	}
	return out
}

func currentSessionContentRoots(root, workspaceID, noun string, kinds []string) ([]record.Root, bool, error) {
	session, ok, err := currentActiveSession(root)
	if err != nil || !ok {
		return nil, ok, err
	}
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, true, fmt.Errorf("read umbrella state: %w", err)
	}
	sourceRefs := map[string]string{}
	for _, mount := range state.Mounts {
		sourceRefs[mount.ID] = mount.SourceRef
	}
	var roots []record.Root
	for _, mount := range session.Mounts {
		if workspaceID != "" && mount.ID != workspaceID {
			continue
		}
		if !record.ContainsValue(kinds, mount.Kind) {
			continue
		}
		roots = append(roots, record.Root{
			Manifest:     sourceRefs[mount.ID],
			Workspace:    mount.ID,
			Path:         mount.WorktreePath,
			ContentPaths: sessionContentPaths(mount),
		})
	}
	if len(roots) == 0 {
		if workspaceID != "" {
			return nil, true, fmt.Errorf("workspace %q is not mounted in active session %s", workspaceID, session.ID)
		}
		return nil, true, fmt.Errorf("no %s mounts are available in active session %s", noun, session.ID)
	}
	return roots, true, nil
}

func sessionContentPaths(mount worksession.Mount) []string {
	if len(mount.ContentPaths) != 0 {
		return append([]string(nil), mount.ContentPaths...)
	}
	return mountContentPaths(mount.Kind, nil)
}

func currentActiveSession(root string) (worksession.Session, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return worksession.Session{}, false, err
	}
	return activeSessionForPath(root, cwd)
}

func activeSessionForPath(root, path string) (worksession.Session, bool, error) {
	if !pathWithinRoot(path, root) {
		return worksession.Session{}, false, nil
	}
	sessions, err := worksession.List(root)
	if err != nil {
		return worksession.Session{}, false, err
	}
	for _, session := range sessions {
		if !pathWithinRoot(path, session.Path) {
			continue
		}
		if session.Status == worksession.StatusActive {
			return session, true, nil
		}
		status := session.Status
		if status == "" {
			status = "inactive"
		}
		return worksession.Session{}, true, fmt.Errorf("current directory is under %s session %s; cd %s or run my session status --all", status, session.ID, root)
	}
	sessionRoot := filepath.Join(root, worksession.WorkDirName)
	legacyRoot := filepath.Join(root, worksession.LegacyWorkDirName)
	if pathWithinRoot(path, sessionRoot) || pathWithinRoot(path, legacyRoot) {
		if id := sessionDirectoryID(root, path); id != "" {
			return worksession.Session{}, true, fmt.Errorf("current directory is under unregistered session directory %s; cd %s or run my doctor", id, root)
		}
		return worksession.Session{}, true, fmt.Errorf("current directory is under a session directory but no active session matched; cd %s or run my session status --all", root)
	}
	return worksession.Session{}, false, nil
}

func sessionDirectoryID(root, path string) string {
	for _, name := range []string{worksession.WorkDirName, worksession.LegacyWorkDirName} {
		base := filepath.Join(root, name)
		if !pathWithinRoot(path, base) {
			continue
		}
		absBase, err := filepath.Abs(base)
		if err != nil {
			return ""
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return ""
		}
		rel, err := filepath.Rel(absBase, absPath)
		if err != nil || rel == "." {
			return ""
		}
		id := strings.Split(filepath.ToSlash(rel), "/")[0]
		if id == "." || id == ".." {
			return ""
		}
		return id
	}
	return ""
}
