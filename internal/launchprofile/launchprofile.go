// Package launchprofile composes manifest-declared organization skills into a
// launch-root loadout. It is pure: callers provide the manifest, launch context,
// and selector, and handle any filesystem materialization separately.
package launchprofile

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/our-ai/internal/manifest"
)

// Target describes the launch root class.
type Target string

const (
	TargetUmbrella Target = "umbrella"
	TargetSession  Target = "session"
	TargetRepo     Target = "repo"
)

// SelectorKind describes how the caller selected skills.
type SelectorKind string

const (
	SelectorDefault  SelectorKind = ""
	SelectorAll      SelectorKind = "all"
	SelectorNone     SelectorKind = "none"
	SelectorExplicit SelectorKind = "explicit"
	SelectorProfile  SelectorKind = "profile"
)

// Selector is the parsed `our ai` skill selector.
type Selector struct {
	Kind      SelectorKind
	SkillRefs []string
	ProfileID string
}

// Context is the launch context available for closure checks.
type Context struct {
	Target       Target
	SelectedRole string
	Mounts       []string
	Services     []string
	Tools        []string
}

// Profile is the composed launch skill profile.
type Profile struct {
	Entries []Entry
}

// Entry is one selected organization skill.
type Entry struct {
	ID          string
	InstallSlug string
	Path        string
	Source      manifest.Source
	Requires    []string
}

// SkillIDs returns the selected manifest skill ids in profile order.
func (p Profile) SkillIDs() []string {
	out := make([]string, 0, len(p.Entries))
	for _, entry := range p.Entries {
		out = append(out, entry.ID)
	}
	return out
}

// Compose selects and closure-checks organization skills for a launch.
func Compose(doc manifest.Document, ctx Context, selector Selector) (Profile, error) {
	if ctx.Target == "" {
		ctx.Target = TargetUmbrella
	}
	skillsByID := skillByID(doc.Skills)
	role, hasRole, err := selectedRole(doc, ctx.SelectedRole)
	if err != nil {
		return Profile{}, err
	}
	availableMounts, availableServices, availableTools, err := availableScope(doc, ctx, role, hasRole)
	if err != nil {
		return Profile{}, err
	}

	selected, err := selectedSkillSet(doc, ctx, selector, skillsByID, role, hasRole, availableMounts)
	if err != nil {
		return Profile{}, err
	}
	entries := make([]manifest.Skill, 0, len(selected))
	for _, skill := range doc.Skills {
		if selected[skill.ID] {
			entries = append(entries, skill)
		}
	}
	if err := validateClosure(entries, availableMounts, availableServices, availableTools); err != nil {
		return Profile{}, err
	}

	profile := Profile{Entries: make([]Entry, 0, len(entries))}
	for _, skill := range entries {
		profile.Entries = append(profile.Entries, Entry{
			ID:          skill.ID,
			InstallSlug: skill.InstallSlug,
			Path:        filepath.ToSlash(skill.Path),
			Source:      skill.Source,
			Requires:    append([]string(nil), skill.Requires...),
		})
	}
	return profile, nil
}

func selectedSkillSet(doc manifest.Document, ctx Context, selector Selector, skillsByID map[string]manifest.Skill, role manifest.Role, hasRole bool, availableMounts map[string]bool) (map[string]bool, error) {
	out := map[string]bool{}
	add := func(id string) error {
		if _, ok := skillsByID[id]; !ok {
			return fmt.Errorf("skill %q is not declared by the manifest", id)
		}
		out[id] = true
		return nil
	}

	switch selector.Kind {
	case SelectorDefault:
		switch {
		case ctx.Target == TargetRepo:
			return out, nil
		case ctx.Target == TargetSession:
			for _, skill := range doc.Skills {
				if workspaceRequirementsSatisfied(skill, availableMounts) {
					out[skill.ID] = true
				}
			}
			if hasRole {
				for _, id := range role.Skills {
					if err := add(id); err != nil {
						return nil, err
					}
				}
			}
			return out, nil
		case hasRole:
			for _, id := range role.Skills {
				if err := add(id); err != nil {
					return nil, err
				}
			}
			return out, nil
		default:
			for _, skill := range doc.Skills {
				out[skill.ID] = true
			}
			return out, nil
		}
	case SelectorAll:
		if ctx.Target == TargetRepo {
			return nil, fmt.Errorf("repo-scoped skill profiles are not supported yet; omit --repo or omit --skills")
		}
		for _, skill := range doc.Skills {
			out[skill.ID] = true
		}
		return out, nil
	case SelectorNone:
		return out, nil
	case SelectorExplicit:
		if ctx.Target == TargetRepo {
			return nil, fmt.Errorf("repo-scoped skill profiles are not supported yet; omit --repo or omit --skills")
		}
		for _, id := range selector.SkillRefs {
			if err := add(id); err != nil {
				return nil, err
			}
		}
		return out, nil
	case SelectorProfile:
		if ctx.Target == TargetRepo {
			return nil, fmt.Errorf("repo-scoped skill profiles are not supported yet; omit --repo or omit --profile")
		}
		profile, ok := profileByID(doc.Profiles)[selector.ProfileID]
		if !ok {
			return nil, fmt.Errorf("profile %q not found", selector.ProfileID)
		}
		for _, id := range profile.Skills {
			if err := add(id); err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unknown selector kind %q", selector.Kind)
	}
}

func availableScope(doc manifest.Document, ctx Context, role manifest.Role, hasRole bool) (map[string]bool, map[string]bool, map[string]bool, error) {
	allMounts := idSetMounts(manifest.EffectiveMounts(doc))
	allServices := idSetServices(doc.Services)
	allTools := idSetTools(doc.Tools)

	mounts := allMounts
	services := allServices
	tools := allTools
	if ctx.Target == TargetSession {
		mounts = stringSet(ctx.Mounts)
	}
	if hasRole {
		if ctx.Target != TargetSession {
			mounts = stringSet(role.Mounts)
		}
		services = stringSet(role.Services)
		tools = stringSet(role.Tools)
	}
	if len(ctx.Mounts) != 0 && ctx.Target != TargetSession {
		mounts = stringSet(ctx.Mounts)
	}
	if len(ctx.Services) != 0 {
		services = stringSet(ctx.Services)
	}
	if len(ctx.Tools) != 0 {
		tools = stringSet(ctx.Tools)
	}
	if err := validateScopeIDs("mount", mounts, allMounts); err != nil {
		return nil, nil, nil, err
	}
	if err := validateScopeIDs("service", services, allServices); err != nil {
		return nil, nil, nil, err
	}
	if err := validateScopeIDs("tool", tools, allTools); err != nil {
		return nil, nil, nil, err
	}
	return mounts, services, tools, nil
}

func selectedRole(doc manifest.Document, roleID string) (manifest.Role, bool, error) {
	if strings.TrimSpace(roleID) == "" {
		return manifest.Role{}, false, nil
	}
	for _, role := range doc.Roles {
		if role.ID == roleID {
			return role, true, nil
		}
	}
	return manifest.Role{}, false, fmt.Errorf("role %q not found", roleID)
}

func validateClosure(skills []manifest.Skill, mounts, services, tools map[string]bool) error {
	for _, skill := range skills {
		if skill.Source.Type == "tool" && skill.Source.Tool != "" && !tools[skill.Source.Tool] {
			return fmt.Errorf("skill %q source tool %q outside selected launch scope", skill.ID, skill.Source.Tool)
		}
		for _, req := range skill.Requires {
			kind, id, ok := strings.Cut(req, ":")
			if !ok {
				return fmt.Errorf("skill %q requires invalid dependency %q", skill.ID, req)
			}
			switch kind {
			case "workspace":
				if !mounts[id] {
					return fmt.Errorf("skill %q requires workspace %q outside selected launch scope", skill.ID, id)
				}
			case "service":
				if !services[id] {
					return fmt.Errorf("skill %q requires service %q outside selected launch scope", skill.ID, id)
				}
			case "tool":
				if !tools[id] {
					return fmt.Errorf("skill %q requires tool %q outside selected launch scope", skill.ID, id)
				}
			default:
				return fmt.Errorf("skill %q requires unsupported dependency type %q", skill.ID, kind)
			}
		}
	}
	return nil
}

func workspaceRequirementsSatisfied(skill manifest.Skill, mounts map[string]bool) bool {
	for _, req := range skill.Requires {
		kind, id, ok := strings.Cut(req, ":")
		if ok && kind == "workspace" && !mounts[id] {
			return false
		}
	}
	return true
}

func validateScopeIDs(kind string, selected, declared map[string]bool) error {
	for id := range selected {
		if !declared[id] {
			return fmt.Errorf("launch scope references unknown %s %q", kind, id)
		}
	}
	return nil
}

func skillByID(items []manifest.Skill) map[string]manifest.Skill {
	out := make(map[string]manifest.Skill, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func profileByID(items []manifest.Profile) map[string]manifest.Profile {
	out := make(map[string]manifest.Profile, len(items))
	for _, item := range items {
		out[item.ID] = item
	}
	return out
}

func idSetMounts(items []manifest.Mount) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.ID] = true
	}
	return out
}

func idSetServices(items []manifest.Service) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.ID] = true
	}
	return out
}

func idSetTools(items []manifest.Tool) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item.ID] = true
	}
	return out
}

func stringSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			out[item] = true
		}
	}
	return out
}

// SortedSkillIDs returns a sorted copy of a composed profile's skill IDs. It is
// mainly useful in tests and diagnostics.
func SortedSkillIDs(p Profile) []string {
	out := p.SkillIDs()
	sort.Strings(out)
	return out
}
