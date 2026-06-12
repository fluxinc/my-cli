// Package guidance writes generated AGENTS.md files into our umbrellas.
package guidance

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxinc/our-ai/internal/manifest"
)

//go:embed baseline/AGENTS.md
var baselineFS embed.FS

const (
	agentsFile = "AGENTS.md"
	claudeFile = "CLAUDE.md"
	marker     = "<!-- our:generated workspace-guidance v1 -->"
)

// Options controls workspace guidance generation.
type Options struct {
	Force             bool
	DryRun            bool
	RoleGuidancePaths []string
}

// Result describes generated workspace guidance status.
type Result struct {
	TargetPath string `json:"target_path"`
	ClaudePath string `json:"claude_path"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

// CheckResult describes whether generated workspace guidance is current.
type CheckResult struct {
	AgentsPath string `json:"agents_path"`
	ClaudePath string `json:"claude_path"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

// Ensure writes AGENTS.md and makes CLAUDE.md an alias for it.
func Ensure(root, manifestRoot string, doc manifest.Document, opts Options) (Result, error) {
	agentsPath := filepath.Join(root, agentsFile)
	claudePath := filepath.Join(root, claudeFile)
	res := Result{
		TargetPath: agentsPath,
		ClaudePath: claudePath,
	}

	content, err := ComposeWithOptions(manifestRoot, doc, opts)
	if err != nil {
		return res, err
	}
	if opts.DryRun {
		res.Status = "dry-run"
		res.Message = "would write AGENTS.md and make CLAUDE.md point to it"
		return res, nil
	}
	if blocked, message, err := blockedByExistingFiles(agentsPath, claudePath, opts.Force); err != nil {
		return res, err
	} else if blocked {
		res.Status = "blocked"
		res.Message = message
		return res, nil
	}

	existed := fileExists(agentsPath)
	if err := writeFileIfChanged(agentsPath, content, 0o644); err != nil {
		return res, err
	}
	if err := ensureClaudeAlias(claudePath, opts.Force, content); err != nil {
		return res, err
	}
	if existed {
		res.Status = "updated"
	} else {
		res.Status = "installed"
	}
	res.Message = "workspace guidance ready"
	return res, nil
}

// Check reports whether AGENTS.md and its CLAUDE.md alias match the generated
// guidance for the supplied manifest. It never writes files.
func Check(root, manifestRoot string, doc manifest.Document) (CheckResult, error) {
	return CheckWithOptions(root, manifestRoot, doc, Options{})
}

// CheckWithOptions reports whether AGENTS.md and its CLAUDE.md alias match the
// generated guidance for the supplied manifest and local role options.
func CheckWithOptions(root, manifestRoot string, doc manifest.Document, opts Options) (CheckResult, error) {
	agentsPath := filepath.Join(root, agentsFile)
	claudePath := filepath.Join(root, claudeFile)
	res := CheckResult{
		AgentsPath: agentsPath,
		ClaudePath: claudePath,
	}

	expected, err := ComposeWithOptions(manifestRoot, doc, opts)
	if err != nil {
		return res, err
	}

	agents, err := os.ReadFile(agentsPath)
	if os.IsNotExist(err) {
		res.Status = "missing"
		res.Message = "run our setup"
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if !isManaged(agents) {
		res.Status = "unmanaged"
		res.Message = "run our setup --force"
		return res, nil
	}
	if !bytes.Equal(agents, expected) {
		res.Status = "stale"
		res.Message = "run our setup"
		return res, nil
	}

	if !claudeAliasOK(claudePath, expected) {
		res.Status = "alias-broken"
		res.Message = "run our setup"
		return res, nil
	}

	res.Status = "ok"
	return res, nil
}

// Compose returns generated AGENTS.md content from the public baseline plus
// manifest-declared fragments.
func Compose(manifestRoot string, doc manifest.Document) ([]byte, error) {
	return ComposeWithOptions(manifestRoot, doc, Options{})
}

// ComposeWithOptions returns generated AGENTS.md content from the public
// baseline plus manifest-declared and locally selected role fragments.
func ComposeWithOptions(manifestRoot string, doc manifest.Document, opts Options) ([]byte, error) {
	baseline, err := baselineFS.ReadFile("baseline/AGENTS.md")
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.WriteString(marker)
	out.WriteString("\n\n")
	out.Write(bytes.TrimSpace(baseline))
	out.WriteString("\n")
	paths := append([]string{}, doc.AgentGuidance.Paths...)
	paths = append(paths, opts.RoleGuidancePaths...)
	for _, path := range paths {
		fragmentPath := filepath.Join(manifestRoot, filepath.FromSlash(path))
		if !pathWithin(fragmentPath, manifestRoot) {
			return nil, fmt.Errorf("agent guidance path %q escapes manifest root", path)
		}
		data, err := os.ReadFile(fragmentPath)
		if err != nil {
			return nil, fmt.Errorf("read agent guidance %s: %w", path, err)
		}
		out.WriteString("\n## Manifest Guidance: ")
		out.WriteString(path)
		out.WriteString("\n\n")
		out.Write(bytes.TrimSpace(data))
		out.WriteString("\n")
	}
	return out.Bytes(), nil
}

func blockedByExistingFiles(agentsPath, claudePath string, force bool) (bool, string, error) {
	if force {
		return false, "", nil
	}
	if data, err := os.ReadFile(agentsPath); err == nil {
		if !isManaged(data) {
			return true, fmt.Sprintf("%s exists and is not Our AI-managed; re-run with --force to replace it", agentsPath), nil
		}
	} else if !os.IsNotExist(err) {
		return false, "", err
	}

	info, err := os.Lstat(claudePath)
	if os.IsNotExist(err) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if data, err := os.ReadFile(claudePath); err == nil && isManaged(data) {
			return false, "", nil
		}
		return true, fmt.Sprintf("%s exists and is not a symlink; re-run with --force to replace it", claudePath), nil
	}
	target, err := os.Readlink(claudePath)
	if err != nil {
		return false, "", err
	}
	if target != agentsFile {
		return true, fmt.Sprintf("%s points to %s; re-run with --force to replace it", claudePath, target), nil
	}
	return false, "", nil
}

func ensureClaudeAlias(path string, force bool, content []byte) error {
	if force {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return writeFileIfChanged(path, content, 0o644)
	}
	if err := os.Symlink(agentsFile, path); err == nil || os.IsExist(err) {
		return nil
	}

	// Some platforms/users cannot create symlinks. A managed copy is a portable
	// fallback, while Unix-like systems still get the preferred symlink path.
	return writeFileIfChanged(path, content, 0o644)
}

func claudeAliasOK(path string, expected []byte) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return err == nil && target == agentsFile
	}
	data, err := os.ReadFile(path)
	return err == nil && isManaged(data) && bytes.Equal(data, expected)
}

func writeFileIfChanged(path string, data []byte, perm fs.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

func isManaged(data []byte) bool {
	return strings.Contains(string(data), marker)
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
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
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}
