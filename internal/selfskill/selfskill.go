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
	"github.com/fluxinc/my-cli/internal/skills"
)

const (
	Name        = "my"
	CanonicalID = "my:self"
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
		Installer:   "my",
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
	for _, h := range hs {
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
	for _, h := range harness.All() {
		target := h.SkillTargetPath(home, Name)
		info, err := os.Lstat(target)
		if errors.Is(err, fs.ErrNotExist) {
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
	}
	if len(existing) == 0 {
		return nil, nil
	}

	self, sourceRoot, err := Materialize(opts.Home)
	if err != nil {
		return nil, err
	}
	baseOpts := installOptions(opts, sourceRoot)
	baseOpts.SkipMissing = true

	var results []skills.Result
	for _, h := range harness.All() {
		info, ok := existing[h]
		if !ok {
			continue
		}
		target := h.SkillTargetPath(home, Name)
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
	return results, nil
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
