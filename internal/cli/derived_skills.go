package cli

import (
	"fmt"

	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/selfskill"
	"github.com/fluxinc/my-cli/internal/skills"
)

type globalOrgSkill struct {
	Installed skills.InstalledSkill
}

const compatibilityGlobalSkillScope = "compat"

func (a app) collectLaunchScopedOrgSkillResults(opts skillsCommandOpts, hs []harness.Harness) ([]skills.Result, error) {
	launchHarnesses, compatibilityHarnesses := splitLaunchSkillHarnesses(hs)
	results := []skills.Result{}
	if len(compatibilityHarnesses) > 0 {
		local := opts
		local.quietSource = true
		local.allowMissingToolSkills = true
		compatResults, err := a.collectSkillSyncResultsWithScope(local, compatibilityHarnesses, false, compatibilityGlobalSkillScope)
		if err != nil {
			return nil, err
		}
		results = append(results, compatResults...)
	}

	found, leftovers, err := a.scanLegacyGlobalOrgSkills(opts, launchHarnesses)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return results, nil
	}
	for _, h := range launchHarnesses {
		results = append(results, skills.Result{
			Harness: h,
			Skill:   "*",
			Status:  skills.StatusSkipped,
			Message: launchScopedOrgSkillSummary(found) + "; my ai materializes them into the launch root",
		})
	}
	for _, leftover := range leftovers {
		existing := leftover.Installed
		results = append(results, skills.Result{
			Harness:     existing.Harness,
			Skill:       existing.Skill,
			CanonicalID: existing.CanonicalID,
			TargetPath:  existing.TargetPath,
			Status:      skills.StatusSkipped,
			Message:     "legacy user-global org skill remains; run my doctor --fix to remove it",
		})
	}
	return results, nil
}

func splitLaunchSkillHarnesses(hs []harness.Harness) ([]harness.Harness, []harness.Harness) {
	var launchHarnesses []harness.Harness
	var compatibilityHarnesses []harness.Harness
	for _, h := range hs {
		switch {
		case h.SupportsLaunchRootSkills():
			launchHarnesses = append(launchHarnesses, h)
		default:
			compatibilityHarnesses = append(compatibilityHarnesses, h)
		}
	}
	return launchHarnesses, compatibilityHarnesses
}

func (a app) scanLegacyGlobalOrgSkills(opts skillsCommandOpts, hs []harness.Harness) ([]skills.Skill, []globalOrgSkill, error) {
	local := opts
	local.quietSource = true
	local.allowMissingToolSkills = true
	bundled, sourceRoots, _, err := a.discoverSkills(local)
	if err != nil {
		return nil, nil, err
	}
	if len(bundled) == 0 {
		return nil, nil, nil
	}
	declaredNames := map[string]skills.Skill{}
	for _, skill := range bundled {
		declaredNames[skill.Name] = skill
	}
	installOpts := skills.InstallOpts{Home: opts.home, SourceRoots: sourceRoots}
	var leftovers []globalOrgSkill
	for _, h := range hs {
		if !h.SupportsLaunchRootSkills() {
			continue
		}
		installed, err := skills.ListInstalled(h, installOpts)
		if err != nil {
			return bundled, nil, err
		}
		for _, existing := range installed {
			if existing.CanonicalID == selfskill.CanonicalID {
				continue
			}
			if _, ok := declaredNames[existing.Skill]; !ok {
				continue
			}
			if !existing.Managed || existing.Scope == "manual" || existing.Scope == "launch" || existing.Scope == compatibilityGlobalSkillScope {
				continue
			}
			leftovers = append(leftovers, globalOrgSkill{Installed: existing})
		}
	}
	return bundled, leftovers, nil
}

func (a app) removeLegacyGlobalOrgSkills(opts skillsCommandOpts, hs []harness.Harness) ([]skills.Result, error) {
	_, leftovers, err := a.scanLegacyGlobalOrgSkills(opts, hs)
	if err != nil {
		return nil, err
	}
	if len(leftovers) == 0 {
		return nil, nil
	}
	local := opts
	local.quietSource = true
	local.allowMissingToolSkills = true
	_, sourceRoots, _, err := a.discoverSkills(local)
	if err != nil {
		return nil, err
	}
	installOpts := skills.InstallOpts{
		Home:        opts.home,
		DryRun:      opts.print,
		SourceRoots: sourceRoots,
	}
	var results []skills.Result
	for _, leftover := range leftovers {
		existing := leftover.Installed
		res := skills.Uninstall(existing.Skill, existing.Harness, installOpts)
		res.CanonicalID = existing.CanonicalID
		if res.Message == "" {
			res.Message = "removed legacy user-global org skill"
		} else {
			res.Message = "removed legacy user-global org skill; " + res.Message
		}
		results = append(results, res)
	}
	return results, nil
}

func launchScopedOrgSkillSummary(found []skills.Skill) string {
	if len(found) == 0 {
		return "no organization skills declared"
	}
	return fmt.Sprintf("%d organization skill(s) now launch-scoped", len(found))
}
