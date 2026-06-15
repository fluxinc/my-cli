package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxinc/our-ai/internal/bundle"
	"github.com/fluxinc/our-ai/internal/harness"
	"github.com/fluxinc/our-ai/internal/launchprofile"
	"github.com/fluxinc/our-ai/internal/skills"
	"github.com/fluxinc/our-ai/internal/umbrella"
)

func launchSelectorFromOpts(opts launchCommandOpts) (launchprofile.Selector, error) {
	if opts.skillsSelector != "" && opts.profileID != "" {
		return launchprofile.Selector{}, fmt.Errorf("--skills and --profile are mutually exclusive")
	}
	if opts.profileID != "" {
		if strings.TrimSpace(opts.profileID) == "" {
			return launchprofile.Selector{}, fmt.Errorf("--profile requires a profile id")
		}
		return launchprofile.Selector{Kind: launchprofile.SelectorProfile, ProfileID: opts.profileID}, nil
	}
	if opts.skillsSelector == "" {
		return launchprofile.Selector{}, nil
	}
	value := strings.TrimSpace(opts.skillsSelector)
	switch value {
	case "":
		return launchprofile.Selector{}, fmt.Errorf("--skills requires all, none, or a comma-separated skill id list")
	case "all":
		return launchprofile.Selector{Kind: launchprofile.SelectorAll}, nil
	case "none":
		return launchprofile.Selector{Kind: launchprofile.SelectorNone}, nil
	}
	parts := strings.Split(value, ",")
	refs := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return launchprofile.Selector{}, fmt.Errorf("--skills contains an empty skill id")
		}
		refs = append(refs, part)
	}
	return launchprofile.Selector{Kind: launchprofile.SelectorExplicit, SkillRefs: uniqueStrings(refs)}, nil
}

func (a app) ensureLaunchOrgSkills(h harness.Harness, opts launchCommandOpts, doc registeredDoc, root, targetDir string, selector launchprofile.Selector) error {
	if !h.SupportsLaunchRootSkills() {
		if selector.Kind != "" {
			return fmt.Errorf("harness %q does not support launch-scoped skill profiles yet; omit --skills/--profile or use claude-code, codex, or antigravity", h)
		}
		return a.ensureCompatibilityGlobalOrgSkills(h, opts, doc)
	}

	if opts.repoID != "" {
		if selector.Kind != "" && selector.Kind != launchprofile.SelectorNone {
			return fmt.Errorf("repo-scoped skill profiles are not supported yet; omit --repo or omit --skills/--profile")
		}
		fmt.Fprintln(a.stderr, "notice: repo-scoped org skills are deferred; no org skills materialized for --repo")
		return nil
	}

	ctx, err := launchProfileContext(root, targetDir, doc.ref.Name, doc.doc.Organization.ID)
	if err != nil {
		return err
	}
	profile, err := launchprofile.Compose(doc.doc, ctx, selector)
	if err != nil {
		return err
	}
	ids := profile.SkillIDs()
	selected, err := a.launchProfileSkills(opts.home, doc, ids)
	if err != nil {
		return err
	}
	targets := []string{filepath.Join(targetDir, ".agents", "skills")}
	if mirror := h.MirrorSkillDir(targetDir); mirror != "" {
		targets = append(targets, mirror)
	}
	for _, dir := range targets {
		if err := materializeLaunchSkillDir(dir, selected); err != nil {
			return err
		}
	}
	return nil
}

func (a app) ensureCompatibilityGlobalOrgSkills(h harness.Harness, opts launchCommandOpts, doc registeredDoc) error {
	local := skillsCommandOpts{
		home:                   opts.home,
		manifestName:           doc.ref.Name,
		quietSource:            true,
		allowMissingToolSkills: true,
	}
	_, err := a.collectSkillSyncResultsWithScope(local, []harness.Harness{h}, false, compatibilityGlobalSkillScope)
	return err
}

func launchProfileContext(root, targetDir, manifestName, organizationID string) (launchprofile.Context, error) {
	ctx := launchprofile.Context{Target: launchprofile.TargetUmbrella}
	if !samePath(root, targetDir) {
		ctx.Target = launchprofile.TargetSession
		session, ok, err := activeSessionForPath(root, targetDir)
		if err != nil {
			return ctx, err
		}
		if ok {
			for _, mount := range session.Mounts {
				ctx.Mounts = append(ctx.Mounts, mount.ID)
			}
		}
	}
	state, err := umbrella.LoadState(root)
	if err == nil {
		ctx.SelectedRole = state.SelectedRole
		return ctx, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return ctx, nil
	}
	if workspace, wsErr := umbrella.LoadWorkspace(root); wsErr == nil && workspace.ManifestRef == manifestName && workspace.Organization == organizationID {
		return ctx, nil
	}
	return ctx, err
}

func (a app) launchProfileSkills(home string, doc registeredDoc, ids []string) ([]skills.Skill, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if err := a.installToolSkills(home, []registeredDoc{doc}, false, ids); err != nil {
		return nil, err
	}
	found, _, err := a.discoverManifestSkills(home, doc.ref.Name, false, false, ids)
	if err != nil {
		return nil, err
	}
	byID := map[string]skills.Skill{}
	for _, skill := range found {
		byID[skill.CanonicalID] = skill
	}
	selected := make([]skills.Skill, 0, len(ids))
	for _, id := range ids {
		skill, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("selected skill %q did not materialize from manifest %q", id, doc.ref.Name)
		}
		selected = append(selected, skill)
	}
	return selected, nil
}

func materializeLaunchSkillDir(dir string, selected []skills.Skill) error {
	selectedByName := map[string]skills.Skill{}
	for _, skill := range selected {
		selectedByName[skill.Name] = skill
	}
	if err := preflightLaunchSkillCollisions(dir, selectedByName); err != nil {
		return err
	}
	if err := removeStaleLaunchSkills(dir, selectedByName); err != nil {
		return err
	}
	if len(selected) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, skill := range selected {
		target := filepath.Join(dir, skill.Name)
		if info, err := os.Lstat(target); err == nil {
			if err := removeLaunchSkill(target, info); err != nil {
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := skills.CopyDir(skill.SourcePath, target); err != nil {
			return err
		}
		if err := writeLaunchManagedMarker(target, skill); err != nil {
			return err
		}
	}
	return nil
}

func preflightLaunchSkillCollisions(dir string, selected map[string]skills.Skill) error {
	for name := range selected {
		target := filepath.Join(dir, name)
		info, err := os.Lstat(target)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !launchSkillManaged(target, info) {
			return fmt.Errorf("launch skill %q collides with non-Our entry at %s", name, target)
		}
	}
	return nil
}

func removeStaleLaunchSkills(dir string, selected map[string]skills.Skill) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if _, ok := selected[name]; ok {
			continue
		}
		target := filepath.Join(dir, name)
		info, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if !launchSkillManaged(target, info) {
			continue
		}
		if err := removeLaunchSkill(target, info); err != nil {
			return err
		}
	}
	return nil
}

func launchSkillManaged(target string, info fs.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	data, err := os.ReadFile(filepath.Join(target, bundle.MarkerName))
	if err != nil {
		return false
	}
	var marker bundle.Marker
	if err := json.Unmarshal(data, &marker); err != nil {
		return false
	}
	return marker.Installer == "our" || marker.Installer == "our-ai"
}

func removeLaunchSkill(target string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(target)
	}
	return os.RemoveAll(target)
}

func writeLaunchManagedMarker(dir string, skill skills.Skill) error {
	marker := bundle.Marker{
		Installer:   "our",
		Version:     bundle.Version(),
		Mode:        "copy",
		Source:      skill.SourceRoot,
		CanonicalID: skill.CanonicalID,
		Scope:       "launch",
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, bundle.MarkerName), data, 0o644)
}
