package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/record"
	"github.com/fluxinc/my-cli/internal/syncer"
)

func (a app) runRecord(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing record subcommand")
	}
	switch args[0] {
	case "adopt":
		return a.runRecordAdopt(args[1:])
	case "-h", "--help", "help":
		a.printRecordUsage()
		return nil
	default:
		return fmt.Errorf("unknown record subcommand %q", args[0])
	}
}

func (a app) printRecordUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my record adopt <path> [--manifest NAME] [--workspace ID] [--home DIR] [--umbrella DIR] [--json]

Record commands operate on local markdown records under declared content
mounts. adopt marks an existing untracked file as intentional publish content
using Git intent-to-add; my sync still stages the final content when it
publishes.`)
}

type recordAdoptResult struct {
	Path         string `json:"path"`
	Repo         string `json:"repo"`
	RelativePath string `json:"relative_path"`
	Status       string `json:"status"`
}

func (a app) runRecordAdopt(args []string) error {
	var opts meetingCommonOpts
	fs := newFlagSet("my record adopt", a.stderr)
	bindMeetingCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, meetingValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my record adopt <path>")
	}
	path, err := filepath.Abs(rest[0])
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("record adopt requires a file path, got directory %s", path)
	}
	entry, rel, err := a.recordAdoptTarget(opts.home, opts.manifestName, opts.workspaceID, opts.umbrellaRoot, path)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := gitIntentToAdd(entry.LocalPath, path, true); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	result := recordAdoptResult{
		Path:         path,
		Repo:         entry.LocalPath,
		RelativePath: rel,
		Status:       "adopted",
	}
	if opts.jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\n", path)
	return nil
}

func (a app) recordAdoptTarget(home, manifestName, workspaceID, umbrellaRoot, path string) (syncer.Entry, string, error) {
	entries, err := a.collectAdoptEntries(home, manifestName, umbrellaRoot)
	if err != nil {
		return syncer.Entry{}, "", err
	}
	var matches []struct {
		entry syncer.Entry
		rel   string
	}
	for _, entry := range entries {
		if workspaceID != "" && entry.ID != workspaceID {
			continue
		}
		rel, ok := relativePathUnder(entry.LocalPath, path)
		if !ok {
			continue
		}
		rel = filepath.ToSlash(rel)
		if !pathsWithinContent([]string{rel}, entry.ContentPaths) {
			continue
		}
		matches = append(matches, struct {
			entry syncer.Entry
			rel   string
		}{entry: entry, rel: rel})
	}
	if len(matches) == 0 {
		return syncer.Entry{}, "", fmt.Errorf("path %s is not under a declared content path; run my mounts list or pass --workspace", path)
	}
	sort.Slice(matches, func(i, j int) bool {
		return len(matches[i].entry.LocalPath) > len(matches[j].entry.LocalPath)
	})
	return matches[0].entry, matches[0].rel, nil
}

func (a app) collectAdoptEntries(home, manifestName, umbrellaRoot string) ([]syncer.Entry, error) {
	entries, err := a.collectSyncEntries(home, manifestName, umbrellaRoot, "content")
	if err != nil {
		return nil, err
	}
	var out []syncer.Entry
	for _, entry := range entries {
		if entry.Role == "content" {
			out = append(out, entry)
		}
	}
	return dedupeSyncEntries(out), nil
}

func markRecordIntentToAdd(root record.Root, path string) error {
	return gitIntentToAdd(root.Path, path, false)
}

func gitIntentToAdd(repo, path string, requireGit bool) error {
	if !isGitWorkTree(repo) {
		if requireGit {
			return fmt.Errorf("content root %s is not a git checkout", repo)
		}
		return nil
	}
	rel, ok := relativePathUnder(repo, path)
	if !ok {
		return fmt.Errorf("path %s is outside content root %s", path, repo)
	}
	return runGit(repo, "add", "-N", "--", filepath.FromSlash(filepath.ToSlash(rel)))
}

func isGitWorkTree(path string) bool {
	out, err := gitCmdOutput(path, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

func relativePathUnder(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	rel = filepath.Clean(rel)
	if rel == "." || strings.HasPrefix(filepath.ToSlash(rel), "../") || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func pathsWithinContent(paths, prefixes []string) bool {
	if len(paths) == 0 || len(prefixes) == 0 {
		return false
	}
	for _, path := range paths {
		matched := false
		for _, prefix := range prefixes {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
