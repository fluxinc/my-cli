// Package selfskill installs and refreshes My AI's bundled CLI skill.
package selfskill

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fluxinc/my-cli/internal/bundle"
	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/safefs"
	"github.com/fluxinc/my-cli/internal/skills"
)

const (
	Name        = "my-cli"
	CanonicalID = "my:self"
	legacyName  = "my"
)

// Options controls bundled self-skill operations.
type Options struct {
	Home        string
	Link        bool
	DryRun      bool
	SkipMissing bool
	Force       bool
}

// Status describes the installed self-skill state for one harness.
type Status struct {
	Harness     harness.Harness `json:"harness"`
	Skill       string          `json:"skill"`
	CanonicalID string          `json:"canonical_id"`
	TargetPath  string          `json:"target_path"`
	Status      string          `json:"status"`
	Kind        string          `json:"kind,omitempty"`
	LinkTarget  string          `json:"link_target,omitempty"`
	Message     string          `json:"message,omitempty"`
	Remedy      string          `json:"remedy,omitempty"`
}

// Materialize writes the embedded skills tree and returns the bundled My AI
// skill as an installable skill source.
func Materialize(home string) (skills.Skill, string, error) {
	source, err := bundle.MaterializeEmbedded(home)
	if err != nil {
		return skills.Skill{}, "", err
	}
	if err := writeSelfSkillMarker(source.SkillsDir); err != nil {
		return skills.Skill{}, "", err
	}
	found, err := skills.DiscoverDeclared(source.SkillsDir, []skills.DeclaredSkill{
		{
			ID:          CanonicalID,
			InstallSlug: Name,
			Path:        Name,
			SourceRoot:  source.SkillsDir,
			SourceLabel: "embedded My AI skill bundle",
		},
	})
	if err != nil {
		return skills.Skill{}, "", err
	}
	if len(found) != 1 {
		return skills.Skill{}, "", fmt.Errorf("embedded My AI self-skill not found")
	}
	return found[0], source.SkillsDir, nil
}

func writeSelfSkillMarker(sourceRoot string) error {
	marker := bundle.Marker{
		Installer:   Name,
		Version:     bundle.Version(),
		Mode:        "symlink",
		Source:      sourceRoot,
		CanonicalID: CanonicalID,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(sourceRoot, Name, bundle.MarkerName)
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(data) {
		return nil
	}
	return os.WriteFile(path, data, 0o644)
}

// Install installs the bundled self-skill into the selected harnesses.
func Install(hs []harness.Harness, opts Options) ([]skills.Result, error) {
	self, sourceRoot, err := resolveSource(opts.Home, opts.DryRun)
	if err != nil {
		return nil, err
	}
	installOpts := installOptions(opts, sourceRoot)
	var results []skills.Result
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}
	migrationResults, skipInstall := migrateLegacySelfSkills(hs, home, opts)
	results = append(results, migrationResults...)
	for _, h := range hs {
		if skipInstall[h] {
			continue
		}
		results = append(results, skills.Install(self, h, installOpts))
	}
	return results, nil
}

// Uninstall removes the bundled self-skill from the selected harnesses.
func Uninstall(hs []harness.Harness, opts Options) ([]skills.Result, error) {
	_, sourceRoot, err := resolveSource(opts.Home, opts.DryRun)
	if err != nil {
		return nil, err
	}
	installOpts := installOptions(opts, sourceRoot)
	var results []skills.Result
	for _, h := range hs {
		results = append(results, skills.Uninstall(Name, h, installOpts))
	}
	return results, nil
}

func resolveSource(home string, dryRun bool) (skills.Skill, string, error) {
	if !dryRun {
		return Materialize(home)
	}
	sourceRoot, err := bundle.SkillsRoot(home)
	if err != nil {
		return skills.Skill{}, "", err
	}
	return skills.Skill{
		Name:        Name,
		SkillName:   Name,
		SourcePath:  filepath.Join(sourceRoot, Name),
		SourceRoot:  sourceRoot,
		CanonicalID: CanonicalID,
		Description: "My AI CLI self-skill",
	}, sourceRoot, nil
}

// Inspect reports the bundled self-skill state for the selected harnesses.
func Inspect(hs []harness.Harness, opts Options) ([]Status, error) {
	self, sourceRoot, err := Materialize(opts.Home)
	if err != nil {
		return nil, err
	}
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}
	installOpts := installOptions(opts, sourceRoot)
	rows := make([]Status, 0, len(hs))
	for _, h := range hs {
		row := Status{
			Harness:     h,
			Skill:       Name,
			CanonicalID: CanonicalID,
		}
		row.TargetPath = h.SkillTargetPath(home, Name)
		if _, err := os.Stat(h.ConfigDir(home)); errors.Is(err, fs.ErrNotExist) {
			row.Status = "missing-harness"
			row.Message = "harness config directory not found"
			row.Remedy = installRemedy(h, opts.Home)
			rows = append(rows, row)
			continue
		} else if err != nil {
			row.Status = skills.StatusFailed
			row.Message = err.Error()
			rows = append(rows, row)
			continue
		}
		inspection, err := skills.InspectDeclared(self, h, installOpts)
		if err != nil {
			row.Status = skills.StatusFailed
			row.Message = err.Error()
			rows = append(rows, row)
			continue
		}
		row.Kind = inspection.Kind.Kind
		row.LinkTarget = inspection.Kind.Target
		switch {
		case inspection.Kind.Kind == "absent":
			row.Status = "absent"
			row.Remedy = installRemedy(h, opts.Home)
		case inspection.Stale:
			row.Status = "stale"
			row.Message = inspection.StaleReason
			row.Remedy = installRemedy(h, opts.Home)
		default:
			row.Status = "installed"
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// SyncExisting quietly refreshes already-installed filesystem self-skills. It
// does not create new harness config directories.
func SyncExisting(opts Options) ([]skills.Result, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}
	existing := map[harness.Harness]fs.FileInfo{}
	legacy := map[harness.Harness]fs.FileInfo{}
	for _, h := range harness.All() {
		target := h.SkillTargetPath(home, Name)
		info, err := os.Lstat(target)
		if errors.Is(err, fs.ErrNotExist) {
			legacyTarget := h.SkillTargetPath(home, legacyName)
			legacyInfo, legacyErr := os.Lstat(legacyTarget)
			if errors.Is(legacyErr, fs.ErrNotExist) {
				continue
			}
			if legacyErr != nil {
				return []skills.Result{{
					Harness:    h,
					Skill:      Name,
					TargetPath: legacyTarget,
					Status:     skills.StatusFailed,
					Err:        legacyErr,
				}}, nil
			}
			legacy[h] = legacyInfo
			continue
		}
		if err != nil {
			return []skills.Result{{
				Harness:    h,
				Skill:      Name,
				TargetPath: target,
				Status:     skills.StatusFailed,
				Err:        err,
			}}, nil
		}
		existing[h] = info
		legacyTarget := h.SkillTargetPath(home, legacyName)
		legacyInfo, legacyErr := os.Lstat(legacyTarget)
		if legacyErr == nil {
			legacy[h] = legacyInfo
		} else if !errors.Is(legacyErr, fs.ErrNotExist) {
			return []skills.Result{{
				Harness:    h,
				Skill:      Name,
				TargetPath: legacyTarget,
				Status:     skills.StatusFailed,
				Err:        legacyErr,
			}}, nil
		}
	}
	if len(existing) == 0 && len(legacy) == 0 {
		return nil, nil
	}

	self, sourceRoot, err := Materialize(opts.Home)
	if err != nil {
		return nil, err
	}
	baseOpts := installOptions(opts, sourceRoot)
	baseOpts.SkipMissing = true

	var results []skills.Result
	preferLink := map[harness.Harness]bool{}
	ensure := map[harness.Harness]bool{}
	for h, info := range existing {
		preferLink[h] = info.Mode()&os.ModeSymlink != 0
		ensure[h] = true
	}
	for h, info := range legacy {
		migrated, shouldEnsure := migrateLegacySelfSkill(h, home, info, opts)
		if migrated.Status != "" {
			results = append(results, migrated)
		}
		if shouldEnsure {
			preferLink[h] = info.Mode()&os.ModeSymlink != 0
			ensure[h] = true
		}
	}
	for _, h := range harness.All() {
		if !ensure[h] {
			continue
		}
		target := h.SkillTargetPath(home, Name)
		info, err := os.Lstat(target)
		if errors.Is(err, fs.ErrNotExist) {
			installOpts := baseOpts
			installOpts.Link = preferLink[h]
			results = append(results, skills.Install(self, h, installOpts))
			continue
		}
		if err != nil {
			results = append(results, skills.Result{
				Harness:    h,
				Skill:      Name,
				TargetPath: target,
				Status:     skills.StatusFailed,
				Err:        err,
			})
			continue
		}
		_, ok := existing[h]
		if !ok {
			existing[h] = info
		}
		info, ok = existing[h]
		if !ok {
			continue
		}
		inspection, err := skills.InspectDeclared(self, h, baseOpts)
		if err != nil {
			results = append(results, skills.Result{
				Harness:    h,
				Skill:      Name,
				TargetPath: target,
				Status:     skills.StatusFailed,
				Err:        err,
			})
			continue
		}
		if !inspection.Stale && (info.Mode()&os.ModeSymlink == 0 || currentSelfSkillLink(target, info, self.SourcePath)) {
			continue
		}
		installOpts := baseOpts
		installOpts.Link = info.Mode()&os.ModeSymlink != 0
		results = append(results, skills.Install(self, h, installOpts))
	}
	if result := removeLegacySource(sourceRoot); result.Status != "" {
		results = append(results, result)
	}
	return results, nil
}

func migrateLegacySelfSkills(hs []harness.Harness, home string, opts Options) ([]skills.Result, map[harness.Harness]bool) {
	var results []skills.Result
	skipInstall := map[harness.Harness]bool{}
	for _, h := range hs {
		oldTarget := h.SkillTargetPath(home, legacyName)
		info, err := os.Lstat(oldTarget)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			results = append(results, skills.Result{
				Harness:    h,
				Skill:      Name,
				TargetPath: oldTarget,
				Status:     skills.StatusFailed,
				Err:        err,
			})
			skipInstall[h] = true
			continue
		}
		result, shouldEnsure := migrateLegacySelfSkill(h, home, info, opts)
		if result.Status != "" {
			results = append(results, result)
		}
		if !shouldEnsure && (result.Status == skills.StatusBlocked || result.Status == skills.StatusFailed) {
			skipInstall[h] = true
		}
	}
	return results, skipInstall
}

func migrateLegacySelfSkill(h harness.Harness, home string, oldInfo fs.FileInfo, opts Options) (skills.Result, bool) {
	oldTarget := h.SkillTargetPath(home, legacyName)
	if !isManagedSelfSkill(oldTarget) {
		return skills.Result{}, false
	}
	newTarget := h.SkillTargetPath(home, Name)
	result := skills.Result{
		Harness:     h,
		Skill:       Name,
		CanonicalID: CanonicalID,
		TargetPath:  newTarget,
	}
	if opts.DryRun {
		result.Status = skills.StatusDryRun
		result.Message = fmt.Sprintf("would migrate legacy self-skill %s -> %s", oldTarget, newTarget)
		return result, false
	}

	_, err := os.Lstat(newTarget)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(oldTarget, newTarget); err == nil {
			result.Status = skills.StatusMigrated
			result.Message = fmt.Sprintf("migrated legacy self-skill from %s", oldTarget)
			return result, true
		}
		if err := removePath(oldTarget, oldInfo); err != nil {
			result.Status = skills.StatusFailed
			result.Err = err
			return result, false
		}
		result.Status = skills.StatusMigrated
		result.Message = fmt.Sprintf("removed legacy self-skill at %s; reinstalling", oldTarget)
		return result, true
	}
	if err != nil {
		result.Status = skills.StatusFailed
		result.Err = err
		return result, false
	}
	if !opts.Force && !isManagedSelfSkill(newTarget) {
		result.TargetPath = oldTarget
		result.Status = skills.StatusBlocked
		result.Message = fmt.Sprintf("legacy self-skill is managed, but %s exists and is not My AI-managed; re-run with --force to replace it", newTarget)
		return result, false
	}
	if err := removePath(oldTarget, oldInfo); err != nil {
		result.Status = skills.StatusFailed
		result.Err = err
		return result, false
	}
	result.Status = skills.StatusMigrated
	if isManagedSelfSkill(newTarget) {
		result.Message = fmt.Sprintf("removed legacy duplicate at %s", oldTarget)
	} else {
		result.Message = fmt.Sprintf("removed legacy self-skill at %s; replacing forced target", oldTarget)
	}
	return result, true
}

func removeLegacySource(sourceRoot string) skills.Result {
	legacyPath := filepath.Join(sourceRoot, legacyName)
	info, err := os.Lstat(legacyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return skills.Result{}
	}
	result := skills.Result{
		Skill:       Name,
		CanonicalID: CanonicalID,
		TargetPath:  legacyPath,
	}
	if err != nil {
		result.Status = skills.StatusFailed
		result.Err = err
		return result
	}
	if !isManagedSelfSkill(legacyPath) {
		return skills.Result{}
	}
	if err := removePath(legacyPath, info); err != nil {
		result.Status = skills.StatusFailed
		result.Err = err
		return result
	}
	result.Status = skills.StatusMigrated
	result.Message = "removed legacy embedded self-skill source"
	return result
}

func isManagedSelfSkill(dir string) bool {
	marker, ok := readManagedMarker(dir)
	return ok && marker.CanonicalID == CanonicalID
}

func readManagedMarker(dir string) (bundle.Marker, bool) {
	data, err := os.ReadFile(filepath.Join(dir, bundle.MarkerName))
	if err != nil {
		return bundle.Marker{}, false
	}
	var marker bundle.Marker
	if err := json.Unmarshal(data, &marker); err != nil {
		return bundle.Marker{}, false
	}
	if marker.Installer != legacyName && marker.Installer != Name {
		return bundle.Marker{}, false
	}
	return marker, true
}

func removePath(target string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(target)
	}
	return safefs.RemoveAll(target)
}

func installOptions(opts Options, sourceRoot string) skills.InstallOpts {
	return skills.InstallOpts{
		Link:        opts.Link,
		DryRun:      opts.DryRun,
		SkipMissing: opts.SkipMissing,
		Home:        opts.Home,
		Force:       opts.Force,
		SourceRoot:  sourceRoot,
		SourceRoots: []string{sourceRoot},
	}
}

func currentSelfSkillLink(target string, info fs.FileInfo, sourcePath string) bool {
	if info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	link, err := os.Readlink(target)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(target), link)
	}
	return samePath(link, sourcePath)
}

func samePath(a, b string) bool {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absA); err == nil {
		absA = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absB); err == nil {
		absB = resolved
	}
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func installRemedy(h harness.Harness, home string) string {
	parts := []string{"my", "skills", "self", "install", string(h)}
	if home != "" {
		parts = append(parts, "--home", home)
	}
	return joinCommand(parts)
}

func joinCommand(parts []string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += " "
		}
		out += part
	}
	return out
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return os.UserHomeDir()
}
