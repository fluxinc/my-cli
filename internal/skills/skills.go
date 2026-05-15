// Package skills discovers and installs manifest-managed skills into
// AI agent harnesses. Filesystem harnesses receive a symlink (default)
// or a directory copy; Gemini is delegated to the `gemini` CLI.
package skills

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fluxinc/flux-ai/internal/bundle"
	"github.com/fluxinc/flux-ai/internal/harness"
)

type Skill struct {
	Name        string // portable install slug / directory name
	SkillName   string // SKILL.md `name:` field
	SourcePath  string // absolute path to the skill directory
	SourceRoot  string // root considered Flux-managed for provenance
	CanonicalID string // manifest namespace:name identity, when known
	Description string // first line / folded scalar from SKILL.md
	Warnings    []string
}

// DeclaredSkill is the manifest subset needed to discover a skill source.
type DeclaredSkill struct {
	ID           string
	InstallSlug  string
	Path         string
	SourceRoot   string
	SourceLabel  string
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
	Force       bool     // replace/remove non-Flux-managed targets
	SourceRoot  string   // resolved skills source root for provenance checks
	SourceRoots []string // additional managed source roots
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

	if h == harness.Gemini {
		res.TargetPath = "(gemini CLI)"
		if opts.DryRun {
			res.Status = StatusDryRun
			res.Message = fmt.Sprintf("gemini skills link %s --scope user --consent", s.SourcePath)
			return res
		}
		if _, err := exec.LookPath("gemini"); err != nil {
			res.Message = "gemini CLI not in PATH"
			if opts.SkipMissing {
				res.Status = StatusSkipped
				return res
			}
			res.Status = StatusFailed
			res.Err = err
			return res
		}
		cmd := exec.Command("gemini", "skills", "link", s.SourcePath, "--scope", "user", "--consent")
		out, err := cmd.CombinedOutput()
		if err != nil {
			res.Status = StatusFailed
			res.Err = err
			res.Message = strings.TrimSpace(string(out))
			return res
		}
		res.Status = StatusInstalled
		return res
	}

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
		if !opts.Force && !isFluxManagedTarget(target, info, managedSourceRoots(sourceRootFor(s, opts), opts.SourceRoots, home)) {
			res.Status = StatusBlocked
			res.Message = "target exists and is not Flux-managed; re-run with --force to replace it"
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
		if err := writeManagedMarker(target, "copy", sourceRootFor(s, opts), s.CanonicalID); err != nil {
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

	if h == harness.Gemini {
		res.TargetPath = "(gemini CLI)"
		if opts.DryRun {
			res.Status = StatusDryRun
			res.Message = fmt.Sprintf("gemini skills uninstall %s --scope user", skillName)
			return res
		}
		if _, err := exec.LookPath("gemini"); err != nil {
			res.Message = "gemini CLI not in PATH"
			if opts.SkipMissing {
				res.Status = StatusSkipped
				return res
			}
			res.Status = StatusFailed
			res.Err = err
			return res
		}
		cmd := exec.Command("gemini", "skills", "uninstall", skillName, "--scope", "user")
		out, err := cmd.CombinedOutput()
		if err != nil {
			res.Status = StatusFailed
			res.Err = err
			res.Message = strings.TrimSpace(string(out))
			return res
		}
		res.Status = StatusRemoved
		return res
	}

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

	if !opts.Force && !isFluxManagedTarget(target, info, managedSourceRoots(opts.SourceRoot, opts.SourceRoots, home)) {
		res.Status = StatusBlocked
		res.Message = "target exists and is not Flux-managed; re-run with --force to remove it"
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
	res.Status = StatusRemoved
	return res
}

// InstalledKind describes what's at the harness skill path: symlink, copy, or absent.
type InstalledKind struct {
	Kind   string // "symlink" | "copy" | "absent" | "managed-by-gemini"
	Target string // for symlinks, the link target; otherwise empty
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
	if h == harness.Gemini {
		return InstalledKind{Kind: "managed-by-gemini"}, nil
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
	roots := []string{filepath.Join(home, ".local", "share", "flux-ai", "skills")}
	if sourceRoot != "" {
		roots = append(roots, sourceRoot)
	}
	roots = append(roots, sourceRoots...)
	return roots
}

func isFluxManagedTarget(target string, info fs.FileInfo, sourceRoots []string) bool {
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
	data, err := os.ReadFile(filepath.Join(dir, bundle.MarkerName))
	if err != nil {
		return false
	}
	var marker bundle.Marker
	if err := json.Unmarshal(data, &marker); err != nil {
		return false
	}
	return marker.Installer == "flux-ai"
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

func writeManagedMarker(dir, mode, source, canonicalID string) error {
	marker := bundle.Marker{
		Installer:   "flux-ai",
		Version:     bundle.Version(),
		Mode:        mode,
		Source:      source,
		CanonicalID: canonicalID,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
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

func removePath(target string, info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(target)
	}
	return os.RemoveAll(target)
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
		return copyFile(path, target)
	})
}

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
