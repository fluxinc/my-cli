// Package skills discovers and installs manifest-managed skills into
// AI agent harnesses. Filesystem harnesses receive a symlink (default)
// or a directory copy.
package skills

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxinc/my-cli/internal/bundle"
	"github.com/fluxinc/my-cli/internal/harness"
)

type Skill struct {
	Name        string   // portable install slug / directory name
	SkillName   string   // SKILL.md `name:` field
	SourcePath  string   // absolute path to the skill directory
	SourceRoot  string   // root considered My AI-managed for provenance
	CanonicalID string   // manifest namespace:name identity, when known
	Description string   // first line / folded scalar from SKILL.md
	Requires    []string // manifest workspace:/tool:/service: dependencies
	Warnings    []string
}

// DeclaredSkill is the manifest subset needed to discover a skill source.
type DeclaredSkill struct {
	ID           string
	InstallSlug  string
	Path         string
	SourceRoot   string
	SourceLabel  string
	Requires     []string
	AllowMissing bool
}

// Discover walks skillsDir and returns one Skill per immediate subdirectory
// that contains a SKILL.md.
func Discover(skillsDir string) ([]Skill, error) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("read skills dir %s: %w", skillsDir, err)
	}
	abs, err := filepath.Abs(skillsDir)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(abs, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			continue
		}
		name, desc := parseFrontmatter(filepath.Join(dir, "SKILL.md"))
		var warnings []string
		if name == "" {
			name = e.Name()
		} else if name != e.Name() {
			warnings = append(warnings, fmt.Sprintf("SKILL.md name %q does not match directory %q", name, e.Name()))
		}
		out = append(out, Skill{
			Name:        e.Name(),
			SkillName:   name,
			SourcePath:  dir,
			SourceRoot:  abs,
			Description: desc,
			Warnings:    warnings,
		})
	}
	return out, nil
}

// DiscoverDeclared returns skills declared by an organization manifest. The
// install slug remains the portable harness directory name; the canonical ID is
// retained for provenance and future harness adapters.
func DiscoverDeclared(root string, declared []DeclaredSkill) ([]Skill, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, declaredSkill := range declared {
		sourceRoot := absRoot
		if declaredSkill.SourceRoot != "" {
			sourceRoot, err = filepath.Abs(declaredSkill.SourceRoot)
			if err != nil {
				return nil, err
			}
		}
		sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(declaredSkill.Path))
		if !pathWithin(sourcePath, sourceRoot) {
			label := "source root"
			if declaredSkill.SourceLabel != "" {
				label = declaredSkill.SourceLabel
			}
			return nil, fmt.Errorf("skill %q path escapes %s", declaredSkill.ID, label)
		}
		if _, err := os.Stat(filepath.Join(sourcePath, "SKILL.md")); err != nil {
			if declaredSkill.AllowMissing && errors.Is(err, os.ErrNotExist) {
				out = append(out, Skill{
					Name:        declaredSkill.InstallSlug,
					SkillName:   declaredSkill.InstallSlug,
					SourcePath:  sourcePath,
					SourceRoot:  sourceRoot,
					CanonicalID: declaredSkill.ID,
					Requires:    declaredSkill.Requires,
				})
				continue
			}
			return nil, fmt.Errorf("skill %q missing SKILL.md at %s: %w", declaredSkill.ID, sourcePath, err)
		}
		name, desc := parseFrontmatter(filepath.Join(sourcePath, "SKILL.md"))
		var warnings []string
		if name == "" {
			name = declaredSkill.InstallSlug
		} else if name != declaredSkill.InstallSlug {
			warnings = append(warnings, fmt.Sprintf("SKILL.md name %q does not match install slug %q", name, declaredSkill.InstallSlug))
		}
		out = append(out, Skill{
			Name:        declaredSkill.InstallSlug,
			SkillName:   name,
			SourcePath:  sourcePath,
			SourceRoot:  sourceRoot,
			CanonicalID: declaredSkill.ID,
			Description: desc,
			Requires:    declaredSkill.Requires,
			Warnings:    warnings,
		})
	}
	return out, nil
}

// parseFrontmatter pulls `name:` and `description:` from YAML-ish
// frontmatter. Handles plain scalars and `>`/`|` folded blocks.
// Empty strings are returned on any parse trouble; callers fall back.
func parseFrontmatter(path string) (name, description string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return
	}

	var key string
	var val strings.Builder
	flush := func() {
		if key == "" {
			return
		}
		switch key {
		case "name":
			name = strings.TrimSpace(val.String())
		case "description":
			description = strings.TrimSpace(val.String())
		}
		key = ""
		val.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			flush()
			return
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			if i := strings.Index(line, ":"); i > 0 {
				flush()
				key = strings.TrimSpace(line[:i])
				rest := strings.TrimSpace(line[i+1:])
				if rest == ">" || rest == "|" || rest == ">-" || rest == "|-" {
					continue
				}
				val.WriteString(rest)
				continue
			}
		}
		if val.Len() > 0 {
			val.WriteString(" ")
		}
		val.WriteString(strings.TrimSpace(line))
	}
	flush()
	return
}

type InstallOpts struct {
	Link        bool     // symlink (default) vs copy
	DryRun      bool     // print plan only
	SkipMissing bool     // skip harnesses whose config dir doesn't exist
	Home        string   // override; defaults to os.UserHomeDir()
	Force       bool     // replace/remove non-My AI-managed targets
	SourceRoot  string   // resolved skills source root for provenance checks
	SourceRoots []string // additional managed source roots
	Scope       string   // provenance scope: manual, launch, or empty legacy
}

type Result struct {
	Harness     harness.Harness
	Skill       string
	CanonicalID string `json:"canonical_id,omitempty"`
	TargetPath  string
	Status      string // installed | updated | removed | skipped | dry-run | failed | not-installed | blocked
	Message     string
	Err         error
}

// InstalledSkill describes one materialized skill entry under a filesystem
// harness skill directory.
type InstalledSkill struct {
	Harness     harness.Harness `json:"harness"`
	Skill       string          `json:"skill"`
	CanonicalID string          `json:"canonical_id,omitempty"`
	TargetPath  string          `json:"target_path"`
	Kind        string          `json:"kind"`
	LinkTarget  string          `json:"link_target,omitempty"`
	Managed     bool            `json:"managed"`
	Source      string          `json:"source,omitempty"`
	Scope       string          `json:"scope,omitempty"`
}

const (
	StatusInstalled    = "installed"
	StatusUpdated      = "updated"
	StatusRemoved      = "removed"
	StatusSkipped      = "skipped"
	StatusDryRun       = "dry-run"
	StatusFailed       = "failed"
	StatusNotInstalled = "not-installed"
	StatusBlocked      = "blocked"
)

// Install places the skill into the harness. Returns the outcome.
func Install(s Skill, h harness.Harness, opts InstallOpts) Result {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return Result{Harness: h, Skill: s.Name, Status: StatusFailed, Err: err}
	}

	res := Result{Harness: h, Skill: s.Name, CanonicalID: s.CanonicalID}

	configDir := h.ConfigDir(home)
	if opts.SkipMissing {
		if _, err := os.Stat(configDir); errors.Is(err, fs.ErrNotExist) {
			res.Status = StatusSkipped
			res.Message = fmt.Sprintf("harness not present: %s", configDir)
			return res
		}
	}

	target := h.SkillTargetPath(home, s.Name)
	res.TargetPath = target

	updated := false
	info, err := os.Lstat(target)
	if err == nil {
		updated = true
		if !opts.Force && !isMyManagedTarget(target, info, managedSourceRoots(sourceRootFor(s, opts), opts.SourceRoots, home)) {
			res.Status = StatusBlocked
			res.Message = "target exists and is not My AI-managed; re-run with --force to replace it"
			return res
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		res.Status = StatusFailed
		res.Err = err
		return res
	}

	if opts.DryRun {
		verb := "link"
		if !opts.Link {
			verb = "copy"
		}
		res.Status = StatusDryRun
		res.Message = fmt.Sprintf("would %s %s -> %s", verb, s.SourcePath, target)
		return res
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		res.Status = StatusFailed
		res.Err = err
		return res
	}

	if updated {
		if err := removePath(target, info); err != nil {
			res.Status = StatusFailed
			res.Err = err
			return res
		}
	}

	if opts.Link {
		if err := os.Symlink(s.SourcePath, target); err != nil {
			res.Status = StatusFailed
			res.Err = err
			return res
		}
	} else {
		if err := copyDir(s.SourcePath, target); err != nil {
			res.Status = StatusFailed
			res.Err = err
			return res
		}
		if err := writeManagedMarker(target, "copy", sourceRootFor(s, opts), s.CanonicalID, opts.Scope); err != nil {
			res.Status = StatusFailed
			res.Err = err
			return res
		}
	}
	if opts.Scope != "" {
		if err := updateManagedIndex(filepath.Dir(target), s.Name, s.CanonicalID, opts.Scope); err != nil {
			res.Status = StatusFailed
			res.Err = err
			return res
		}
	}

	if updated {
		res.Status = StatusUpdated
	} else {
		res.Status = StatusInstalled
	}
	return res
}

// Uninstall removes an installed skill from the harness.
func Uninstall(skillName string, h harness.Harness, opts InstallOpts) Result {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return Result{Harness: h, Skill: skillName, Status: StatusFailed, Err: err}
	}
	res := Result{Harness: h, Skill: skillName}

	target := h.SkillTargetPath(home, skillName)
	res.TargetPath = target

	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			res.Status = StatusNotInstalled
			return res
		}
		res.Status = StatusFailed
		res.Err = err
		return res
	}

	if !opts.Force && !isMyManagedTarget(target, info, managedSourceRoots(opts.SourceRoot, opts.SourceRoots, home)) {
		res.Status = StatusBlocked
		res.Message = "target exists and is not My AI-managed; re-run with --force to remove it"
		return res
	}

	if opts.DryRun {
		res.Status = StatusDryRun
		res.Message = fmt.Sprintf("would remove %s", target)
		return res
	}

	if err := removePath(target, info); err != nil {
		res.Status = StatusFailed
		res.Err = err
		return res
	}
	if err := removeManagedIndexEntry(filepath.Dir(target), skillName); err != nil {
		res.Status = StatusFailed
		res.Err = err
		return res
	}
	res.Status = StatusRemoved
	return res
}

// InstalledKind describes what's at the harness skill path: symlink, copy, or absent.
type InstalledKind struct {
	Kind   string // "symlink" | "copy" | "absent"
	Target string // for symlinks, the link target; otherwise empty
}

type DeclaredInspection struct {
	Kind        InstalledKind
	Stale       bool
	StaleReason string
}

// Inspect reports what's currently installed for a given skill name in a harness.
func Inspect(skillName string, h harness.Harness, home string) (InstalledKind, error) {
	if home == "" {
		var err error
		home, err = resolveHome("")
		if err != nil {
			return InstalledKind{}, err
		}
	}
	target := h.SkillTargetPath(home, skillName)
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return InstalledKind{Kind: "absent"}, nil
		}
		return InstalledKind{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(target)
		if err != nil {
			return InstalledKind{Kind: "symlink"}, nil
		}
		return InstalledKind{Kind: "symlink", Target: link}, nil
	}
	return InstalledKind{Kind: "copy"}, nil
}

// InspectDeclared reports installed state for a declared skill and marks stale
// copy-mode materializations whose marker or content no longer matches source.
func InspectDeclared(s Skill, h harness.Harness, opts InstallOpts) (DeclaredInspection, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return DeclaredInspection{}, err
	}
	kind, err := Inspect(s.Name, h, home)
	if err != nil {
		return DeclaredInspection{}, err
	}
	inspection := DeclaredInspection{Kind: kind}
	if kind.Kind != "copy" {
		return inspection, nil
	}
	target := h.SkillTargetPath(home, s.Name)
	if marker, ok := readManagedMarker(target); ok {
		expectedSource := sourceRootFor(s, opts)
		if marker.Source != "" && expectedSource != "" && !sameFilesystemPath(marker.Source, expectedSource) {
			inspection.Stale = true
			inspection.StaleReason = fmt.Sprintf("source changed from %s to %s", marker.Source, expectedSource)
			return inspection, nil
		}
		if marker.CanonicalID != "" && s.CanonicalID != "" && marker.CanonicalID != s.CanonicalID {
			inspection.Stale = true
			inspection.StaleReason = fmt.Sprintf("canonical id changed from %s to %s", marker.CanonicalID, s.CanonicalID)
			return inspection, nil
		}
	}
	if differs, err := dirContentDiffers(s.SourcePath, target); err != nil {
		return DeclaredInspection{}, err
	} else if differs {
		inspection.Stale = true
		inspection.StaleReason = "copy differs from source"
	}
	return inspection, nil
}

// ListInstalled returns the current filesystem skill materializations for a
// harness.
func ListInstalled(h harness.Harness, opts InstallOpts) ([]InstalledSkill, error) {
	home, err := resolveHome(opts.Home)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(h.ConfigDir(home), "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	sourceRoots := managedSourceRoots(opts.SourceRoot, opts.SourceRoots, home)
	index := readManagedIndex(dir)
	var out []InstalledSkill
	for _, entry := range entries {
		if entry.Name() == bundle.MarkerName {
			continue
		}
		target := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(target)
		if err != nil {
			return nil, err
		}
		if !entry.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		installed := InstalledSkill{
			Harness:    h,
			Skill:      entry.Name(),
			TargetPath: target,
			Kind:       "copy",
		}
		if info.Mode()&os.ModeSymlink != 0 {
			installed.Kind = "symlink"
			if link, err := os.Readlink(target); err == nil {
				if !filepath.IsAbs(link) {
					link = filepath.Join(filepath.Dir(target), link)
				}
				installed.LinkTarget = link
				if marker, ok := readManagedMarker(link); ok {
					installed.CanonicalID = marker.CanonicalID
					installed.Source = marker.Source
					installed.Scope = marker.Scope
				}
			}
		} else if marker, ok := readManagedMarker(target); ok {
			installed.CanonicalID = marker.CanonicalID
			installed.Source = marker.Source
			installed.Scope = marker.Scope
		}
		if indexed, ok := index.Skills[entry.Name()]; ok {
			if installed.CanonicalID == "" {
				installed.CanonicalID = indexed.CanonicalID
			}
			if installed.Scope == "" {
				installed.Scope = indexed.Scope
			}
		}
		installed.Managed = isMyManagedTarget(target, info, sourceRoots)
		out = append(out, installed)
	}
	return out, nil
}

func sourceRootFor(s Skill, opts InstallOpts) string {
	if opts.SourceRoot != "" {
		return opts.SourceRoot
	}
	if s.SourceRoot != "" {
		return s.SourceRoot
	}
	return filepath.Dir(s.SourcePath)
}

func managedSourceRoots(sourceRoot string, sourceRoots []string, home string) []string {
	roots := []string{
		filepath.Join(home, ".local", "share", "my-cli", "skills"),
		filepath.Join(home, ".local", "share", "my-cli", "skills"),
	}
	if sourceRoot != "" {
		roots = append(roots, sourceRoot)
	}
	roots = append(roots, sourceRoots...)
	return roots
}

func isMyManagedTarget(target string, info fs.FileInfo, sourceRoots []string) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(target)
		if err != nil {
			return false
		}
		if !filepath.IsAbs(link) {
			link = filepath.Join(filepath.Dir(target), link)
		}
		for _, sourceRoot := range sourceRoots {
			if sourceRoot != "" && pathWithin(link, sourceRoot) {
				return true
			}
		}
		return false
	}
	return hasManagedMarker(target)
}

func hasManagedMarker(dir string) bool {
	_, ok := readManagedMarker(dir)
	return ok
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
	if marker.Installer != "my" && marker.Installer != "my-cli" {
		return bundle.Marker{}, false
	}
	return marker, true
}

func pathWithin(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}
	if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = resolved
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func sameFilesystemPath(a, b string) bool {
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

func writeManagedMarker(dir, mode, source, canonicalID, scope string) error {
	marker := bundle.Marker{
		Installer:   "my",
		Version:     bundle.Version(),
		Mode:        mode,
		Source:      source,
		CanonicalID: canonicalID,
		Scope:       scope,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, bundle.MarkerName), data, 0o644)
}

type managedIndex struct {
	Installer string                      `json:"installer"`
	Version   string                      `json:"version"`
	Mode      string                      `json:"mode"`
	Skills    map[string]managedIndexItem `json:"skills,omitempty"`
}

type managedIndexItem struct {
	CanonicalID string `json:"canonical_id,omitempty"`
	Scope       string `json:"scope,omitempty"`
}

func readManagedIndex(dir string) managedIndex {
	data, err := os.ReadFile(filepath.Join(dir, bundle.MarkerName))
	if err != nil {
		return managedIndex{Skills: map[string]managedIndexItem{}}
	}
	var index managedIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return managedIndex{Skills: map[string]managedIndexItem{}}
	}
	if index.Installer != "my" && index.Installer != "my-cli" {
		return managedIndex{Skills: map[string]managedIndexItem{}}
	}
	if index.Mode != "index" {
		return managedIndex{Skills: map[string]managedIndexItem{}}
	}
	if index.Skills == nil {
		index.Skills = map[string]managedIndexItem{}
	}
	return index
}

func updateManagedIndex(dir, skillName, canonicalID, scope string) error {
	index := readManagedIndex(dir)
	index.Installer = "my"
	index.Version = bundle.Version()
	index.Mode = "index"
	if index.Skills == nil {
		index.Skills = map[string]managedIndexItem{}
	}
	index.Skills[skillName] = managedIndexItem{CanonicalID: canonicalID, Scope: scope}
	return writeManagedIndex(dir, index)
}

func removeManagedIndexEntry(dir, skillName string) error {
	index := readManagedIndex(dir)
	if len(index.Skills) == 0 {
		return nil
	}
	delete(index.Skills, skillName)
	if len(index.Skills) == 0 {
		err := os.Remove(filepath.Join(dir, bundle.MarkerName))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeManagedIndex(dir, index)
}

func writeManagedIndex(dir string, index managedIndex) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, bundle.MarkerName), data, 0o644)
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return os.UserHomeDir()
}

func envWithHome(home string) []string {
	env := os.Environ()
	for i, entry := range env {
		if strings.HasPrefix(entry, "HOME=") {
			env[i] = "HOME=" + home
			return env
		}
	}
	return append(env, "HOME="+home)
}

func removePath(target string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(target)
	}
	return os.RemoveAll(target)
}

// CopyDir copies a skill directory tree.
func CopyDir(src, dst string) error {
	return copyDir(src, dst)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if d.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		return copyFile(path, target)
	})
}

func dirContentDiffers(src, dst string) (bool, error) {
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		targetInfo, err := os.Lstat(target)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return errContentDiffers
			}
			return err
		}
		if d.IsDir() {
			if !targetInfo.IsDir() {
				return errContentDiffers
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			if targetInfo.Mode()&os.ModeSymlink == 0 {
				return errContentDiffers
			}
			srcLink, err := os.Readlink(path)
			if err != nil {
				return err
			}
			dstLink, err := os.Readlink(target)
			if err != nil {
				return err
			}
			if srcLink != dstLink {
				return errContentDiffers
			}
			return nil
		}
		if targetInfo.IsDir() {
			return errContentDiffers
		}
		srcData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dstData, err := os.ReadFile(target)
		if err != nil {
			return err
		}
		if !bytes.Equal(srcData, dstData) {
			return errContentDiffers
		}
		return nil
	}); err != nil {
		if errors.Is(err, errContentDiffers) {
			return true, nil
		}
		return false, err
	}
	if err := filepath.WalkDir(dst, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dst, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == bundle.MarkerName {
			return nil
		}
		if _, err := os.Lstat(filepath.Join(src, rel)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return errContentDiffers
			}
			return err
		}
		return nil
	}); err != nil {
		if errors.Is(err, errContentDiffers) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

var errContentDiffers = errors.New("content differs")

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode().Perm())
}
