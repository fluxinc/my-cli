// Package workspace manages organization workspaces declared by manifests.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fluxinc/our-ai/internal/ghauth"
	"github.com/fluxinc/our-ai/internal/manifest"
	"github.com/fluxinc/our-ai/internal/umbrella"
)

// Runner executes external commands. Tests can replace it.
type Runner func(name string, args ...string) ([]byte, error)

// Entry is one workspace resolved from one registered manifest.
type Entry struct {
	Manifest     string   `json:"manifest"`
	Organization string   `json:"organization"`
	ID           string   `json:"id"`
	Kind         string   `json:"kind,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	GitURL       string   `json:"git_url"`
	IncludePaths []string `json:"include_paths,omitempty"`
	LocalPath    string   `json:"local_path"`
	UmbrellaRoot string   `json:"umbrella_root,omitempty"`
	SourceRef    string   `json:"source_ref,omitempty"`
}

// SyncResult describes one workspace sync action.
type SyncResult struct {
	Manifest     string   `json:"manifest"`
	Workspace    string   `json:"workspace"`
	Kind         string   `json:"kind,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	GitURL       string   `json:"git_url"`
	IncludePaths []string `json:"include_paths,omitempty"`
	LocalPath    string   `json:"local_path"`
	UmbrellaRoot string   `json:"umbrella_root,omitempty"`
	SourceRef    string   `json:"source_ref,omitempty"`
	Status       string   `json:"status"`
	Message      string   `json:"message,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// List returns workspaces from registered and synced manifests.
func List(home, manifestName string) ([]Entry, error) {
	refs, err := selectedManifestRefs(home, manifestName)
	if err != nil {
		return nil, err
	}
	homeDir, err := resolveHome(home)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, ref := range refs {
		doc, err := loadManifest(ref)
		if err != nil {
			return nil, err
		}
		for _, w := range doc.Workspaces {
			local, err := expandLocalPath(homeDir, w.LocalPath)
			if err != nil {
				return nil, fmt.Errorf("%s:%s local_path: %w", ref.Name, w.ID, err)
			}
			entries = append(entries, Entry{
				Manifest:     ref.Name,
				Organization: doc.Organization.ID,
				ID:           w.ID,
				GitURL:       w.GitURL,
				LocalPath:    local,
			})
		}
	}
	return entries, nil
}

// ListMounts returns manifest-declared mounts resolved inside an umbrella.
func ListMounts(home, manifestName, umbrellaRoot string) ([]Entry, error) {
	refs, err := selectedManifestRefs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for _, ref := range refs {
		doc, err := loadManifest(ref)
		if err != nil {
			return nil, err
		}
		root, err := umbrella.ResolveRoot(home, "", umbrellaRoot, doc)
		if err != nil {
			return nil, err
		}
		for _, mount := range manifest.EffectiveMounts(doc) {
			entries = append(entries, Entry{
				Manifest:     ref.Name,
				Organization: doc.Organization.ID,
				ID:           mount.ID,
				Kind:         mount.Kind,
				Mode:         mount.Mode,
				GitURL:       mount.GitURL,
				IncludePaths: mount.IncludePaths,
				LocalPath:    umbrella.MountPath(root, mount.ID),
				UmbrellaRoot: root,
				SourceRef:    "manifest:" + ref.Name + ":" + mount.ID,
			})
		}
	}
	return entries, nil
}

// Sync clones or fast-forwards selected workspaces.
func Sync(home, manifestName string, workspaceIDs []string, all bool, dryRun bool, runner Runner) ([]SyncResult, error) {
	if runner == nil {
		runner = execCommand
	}
	entries, err := List(home, manifestName)
	if err != nil {
		return nil, err
	}
	selected, err := selectedWorkspaces(entries, workspaceIDs, all)
	if err != nil {
		return nil, err
	}
	results := make([]SyncResult, 0, len(selected))
	results = append(results, SyncEntries(selected, dryRun, runner)...)
	return results, nil
}

// SyncMounts clones or fast-forwards manifest mounts.
func SyncMounts(home, manifestName, umbrellaRoot string, mountIDs []string, all bool, modes []string, dryRun bool, runner Runner) ([]SyncResult, error) {
	if runner == nil {
		runner = execCommand
	}
	entries, err := ListMounts(home, manifestName, umbrellaRoot)
	if err != nil {
		return nil, err
	}
	if len(modes) != 0 {
		entries = filterMountModes(entries, modes)
	}
	selected, err := selectedWorkspaces(entries, mountIDs, all || len(modes) != 0)
	if err != nil {
		return nil, err
	}
	results := make([]SyncResult, 0, len(selected))
	results = append(results, SyncEntries(selected, dryRun, runner)...)
	return results, nil
}

// SyncEntry clones or fast-forwards one resolved workspace entry.
func SyncEntry(entry Entry, dryRun bool, runner Runner) SyncResult {
	if runner == nil {
		runner = execCommand
	}
	return syncOne(entry, dryRun, runner)
}

// SyncEntries clones or fast-forwards resolved workspace entries.
func SyncEntries(entries []Entry, dryRun bool, runner Runner) []SyncResult {
	if runner == nil {
		runner = execCommand
	}
	results := make([]SyncResult, 0, len(entries))
	for _, entry := range entries {
		results = append(results, syncOne(entry, dryRun, runner))
	}
	return results
}

func filterMountModes(entries []Entry, modes []string) []Entry {
	allowed := map[string]bool{}
	for _, mode := range modes {
		allowed[mode] = true
	}
	var out []Entry
	for _, entry := range entries {
		if allowed[entry.Mode] {
			out = append(out, entry)
		}
	}
	return out
}

func selectedManifestRefs(home, manifestName string) ([]manifest.Ref, error) {
	if manifestName != "" {
		ref, ok, err := manifest.Find(home, manifestName)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("manifest %q is not registered", manifestName)
		}
		return []manifest.Ref{ref}, nil
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return nil, err
	}
	return reg.Manifests, nil
}

func loadManifest(ref manifest.Ref) (manifest.Document, error) {
	doc, _, err := manifest.LoadDocument(ref.LocalPath)
	if err != nil {
		return manifest.Document{}, fmt.Errorf("manifest %q is not synced; run our manifests sync %s: %w", ref.Name, ref.Name, err)
	}
	result := manifest.ValidateFile(ref.LocalPath)
	if len(result.Errors) != 0 {
		return manifest.Document{}, fmt.Errorf("manifest %q is invalid: %s", ref.Name, strings.Join(result.Errors, "; "))
	}
	return doc, nil
}

func selectedWorkspaces(entries []Entry, ids []string, all bool) ([]Entry, error) {
	if all {
		if len(ids) != 0 {
			return nil, fmt.Errorf("--all cannot be combined with explicit workspace IDs")
		}
		return entries, nil
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("select a workspace ID or pass --all")
	}
	var selected []Entry
	for _, id := range ids {
		matches := matchingWorkspaces(entries, id)
		if len(matches) == 0 {
			return nil, fmt.Errorf("workspace %q is not declared by any selected manifest", id)
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("workspace %q is ambiguous; pass --manifest", id)
		}
		selected = append(selected, matches[0])
	}
	return selected, nil
}

func matchingWorkspaces(entries []Entry, id string) []Entry {
	var out []Entry
	if i := strings.Index(id, ":"); i > 0 {
		manifestName := id[:i]
		workspaceID := id[i+1:]
		for _, entry := range entries {
			if entry.Manifest == manifestName && entry.ID == workspaceID {
				out = append(out, entry)
			}
		}
		return out
	}
	for _, entry := range entries {
		if entry.ID == id {
			out = append(out, entry)
		}
	}
	return out
}

func syncOne(entry Entry, dryRun bool, runner Runner) SyncResult {
	res := SyncResult{
		Manifest:     entry.Manifest,
		Workspace:    entry.ID,
		Kind:         entry.Kind,
		Mode:         entry.Mode,
		GitURL:       entry.GitURL,
		IncludePaths: entry.IncludePaths,
		LocalPath:    entry.LocalPath,
		UmbrellaRoot: entry.UmbrellaRoot,
		SourceRef:    entry.SourceRef,
	}
	if info, err := os.Stat(entry.LocalPath); err == nil && !isGitDir(entry.LocalPath) {
		res.Status = "failed"
		if info.IsDir() {
			res.Error = "target exists but is not a git repository"
		} else {
			res.Error = "target exists and is not a directory"
		}
		return res
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}

	sparse := len(entry.IncludePaths) != 0
	if isGitDir(entry.LocalPath) {
		if dryRun {
			res.Status = "dry-run"
			if sparse {
				res.Message = fmt.Sprintf("would run git -C %s sparse-checkout set --no-cone %s && git -C %s pull --ff-only", entry.LocalPath, strings.Join(entry.IncludePaths, " "), entry.LocalPath)
			} else {
				res.Message = fmt.Sprintf("would run git -C %s pull --ff-only", entry.LocalPath)
			}
			return res
		}
		if _, err := runner("git", "-C", entry.LocalPath, "remote", "get-url", "origin"); err != nil {
			res.Status = "local-only"
			res.Message = "no origin remote configured; nothing to pull until the repository is published"
			return res
		}
		if err := ghauth.CheckGitURL(entry.GitURL, ghauth.Runner(runner)); err != nil {
			res.Status = "failed"
			res.Error = err.Error()
			return res
		}
		var messages []string
		if sparse {
			out, err := runner("git", sparseCheckoutArgs(entry)...)
			if err != nil {
				res.Status = "failed"
				res.Error = commandError(out, err)
				return res
			}
			if msg := strings.TrimSpace(string(out)); msg != "" {
				messages = append(messages, msg)
			}
		}
		out, err := runner("git", "-C", entry.LocalPath, "pull", "--ff-only")
		if err != nil {
			res.Status = "failed"
			res.Error = commandError(out, err)
			return res
		}
		res.Status = "synced"
		if msg := strings.TrimSpace(string(out)); msg != "" {
			messages = append(messages, msg)
		}
		res.Message = strings.Join(messages, "\n")
		return res
	}

	if dryRun {
		res.Status = "dry-run"
		if sparse {
			res.Message = fmt.Sprintf("would run git clone --sparse %s %s && git -C %s sparse-checkout set --no-cone %s", entry.GitURL, entry.LocalPath, entry.LocalPath, strings.Join(entry.IncludePaths, " "))
		} else {
			res.Message = fmt.Sprintf("would run git clone %s %s", entry.GitURL, entry.LocalPath)
		}
		return res
	}
	if err := ghauth.CheckGitURL(entry.GitURL, ghauth.Runner(runner)); err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}
	if err := os.MkdirAll(filepath.Dir(entry.LocalPath), 0o755); err != nil {
		res.Status = "failed"
		res.Error = err.Error()
		return res
	}
	out, err := runner("git", cloneArgs(entry, sparse)...)
	if err != nil {
		res.Status = "failed"
		res.Error = commandError(out, err)
		return res
	}
	var messages []string
	if msg := strings.TrimSpace(string(out)); msg != "" {
		messages = append(messages, msg)
	}
	if sparse {
		out, err := runner("git", sparseCheckoutArgs(entry)...)
		if err != nil {
			res.Status = "failed"
			res.Error = commandError(out, err)
			return res
		}
		if msg := strings.TrimSpace(string(out)); msg != "" {
			messages = append(messages, msg)
		}
	}
	res.Status = "synced"
	res.Message = strings.Join(messages, "\n")
	return res
}

func cloneArgs(entry Entry, sparse bool) []string {
	args := []string{"clone"}
	if sparse {
		args = append(args, "--sparse")
	}
	return append(args, entry.GitURL, entry.LocalPath)
}

func sparseCheckoutArgs(entry Entry) []string {
	args := []string{"-C", entry.LocalPath, "sparse-checkout", "set", "--no-cone"}
	return append(args, entry.IncludePaths...)
}

func isGitDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

func commandError(out []byte, err error) string {
	msg := strings.TrimSpace(string(out))
	if msg != "" {
		return msg
	}
	return err.Error()
}

func expandLocalPath(home, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("is required")
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Abs(path)
}

func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func resolveHome(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}
	return os.UserHomeDir()
}
