package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fluxinc/our-ai/internal/record"
	"github.com/fluxinc/our-ai/internal/umbrella"
	"github.com/fluxinc/our-ai/internal/worksession"
	"github.com/fluxinc/our-ai/internal/workspace"
)

func contentRoots(home, manifestName, workspaceID, umbrellaRoot, noun string, kinds []string) ([]record.Root, error) {
	if umbrellaRoot != "" {
		root, err := resolveUmbrellaRoot(home, umbrellaRoot)
		if err != nil {
			return nil, err
		}
		if roots, ok, err := currentSessionContentRoots(root, workspaceID, noun, kinds); ok || err != nil {
			return roots, err
		}
		return umbrellaContentRootsForRoot(root, workspaceID, noun, kinds)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		if roots, ok, err := currentSessionContentRoots(root, workspaceID, noun, kinds); ok || err != nil {
			return roots, err
		}
		return umbrellaContentRootsForRoot(root, workspaceID, noun, kinds)
	}
	if roots, ok, err := configuredUmbrellaContentRoots(home, manifestName, workspaceID, noun, kinds); ok || err != nil {
		return roots, err
	}
	if manifestName == "" {
		return nil, noUmbrellaError("no our umbrella found; run our setup or pass --umbrella", "run our setup or pass --umbrella <path>")
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
			Manifest:  entry.Manifest,
			Workspace: entry.ID,
			Path:      entry.LocalPath,
		})
	}
	if len(roots) == 0 {
		if workspaceID != "" {
			return nil, fmt.Errorf("workspace %q is not declared by any selected manifest", workspaceID)
		}
		return nil, fmt.Errorf("no workspaces declared by selected manifests")
	}
	return roots, nil
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
		roots, err := umbrellaContentRoots(home, workspaceID, candidates[0].root, noun, kinds)
		return roots, true, err
	}
	if len(candidates) > 1 {
		return nil, true, fmt.Errorf("multiple our umbrellas configured; pass --manifest or --umbrella")
	}
	if manifestName == "" && len(configured) == 1 {
		return nil, true, noUmbrellaError(
			fmt.Sprintf("no our umbrella found; configured umbrella is %s", configured[0].root),
			fmt.Sprintf("run our setup --manifest %s or pass --umbrella %s", configured[0].ref, configured[0].root),
		)
	}
	return nil, false, nil
}

func umbrellaContentRoots(home, workspaceID, umbrellaRoot, noun string, kinds []string) ([]record.Root, error) {
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return nil, err
	}
	return umbrellaContentRootsForRoot(root, workspaceID, noun, kinds)
}

func umbrellaContentRootsForRoot(root, workspaceID, noun string, kinds []string) ([]record.Root, error) {
	state, err := umbrella.LoadState(root)
	if err != nil {
		return nil, fmt.Errorf("read umbrella state: %w", err)
	}
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
		roots = append(roots, record.Root{
			Manifest:  mount.SourceRef,
			Workspace: mount.ID,
			Path:      umbrella.MountPath(root, mount.ID),
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
			Manifest:  sourceRefs[mount.ID],
			Workspace: mount.ID,
			Path:      mount.WorktreePath,
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

func currentActiveSession(root string) (worksession.Session, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return worksession.Session{}, false, err
	}
	return activeSessionForPath(root, cwd)
}

func activeSessionForPath(root, path string) (worksession.Session, bool, error) {
	workRoot := filepath.Join(root, worksession.WorkDirName)
	if !pathWithinRoot(path, workRoot) {
		return worksession.Session{}, false, nil
	}
	sessions, err := worksession.List(root)
	if err != nil {
		return worksession.Session{}, true, err
	}
	for _, session := range sessions {
		if session.Status == worksession.StatusActive && pathWithinRoot(path, session.Path) {
			return session, true, nil
		}
	}
	return worksession.Session{}, true, fmt.Errorf("current directory is under %s but no active work session matched; run our work status or cd %s", workRoot, root)
}
