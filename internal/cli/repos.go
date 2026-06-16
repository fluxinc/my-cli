package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

type repoCommonOpts struct {
	home         string
	manifestName string
	umbrellaRoot string
	jsonOut      bool
}

func bindRepoCommonFlags(fs *flag.FlagSet, opts *repoCommonOpts) {
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON output")
}

func repoValueFlags() map[string]bool {
	return map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
	}
}

func (a app) runRepos(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: my repos list|add|remove [flags]")
	}
	switch args[0] {
	case "list":
		return a.runReposList(args[1:])
	case "add":
		return a.runReposAdd(args[1:])
	case "remove":
		return a.runReposRemove(args[1:])
	default:
		return fmt.Errorf("unknown repos subcommand %q (expected list|add|remove)", args[0])
	}
}

// repoListEntry is one catalog repo plus its local clone state.
type repoListEntry struct {
	ID          string `json:"id"`
	GitURL      string `json:"git_url"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
	Selected    bool   `json:"selected"`
	Cloned      bool   `json:"cloned"`
	Path        string `json:"path,omitempty"`
}

func (a app) runReposList(args []string) error {
	var opts repoCommonOpts
	fs := newFlagSet("my repos list", a.stderr)
	bindRepoCommonFlags(fs, &opts)
	rest, err := parseInterspersed(fs, args, repoValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my repos list [--json]")
	}
	repos, err := manifest.LoadRepoCatalog(opts.home, opts.manifestName)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	root, state, rootErr := loadRepoUmbrellaState(opts.home, opts.umbrellaRoot)
	selected := map[string]bool{}
	if rootErr == nil {
		for _, id := range state.SelectedRepos {
			selected[id] = true
		}
	}
	entries := make([]repoListEntry, 0, len(repos))
	for _, repo := range repos {
		entry := repoListEntry{
			ID:          repo.ID,
			GitURL:      repo.GitURL,
			Description: repo.Description,
			Default:     repo.Default,
			Selected:    selected[repo.ID],
		}
		if rootErr == nil {
			path := umbrella.RepoPath(root, repo.ID)
			if isGitCheckout(path) {
				entry.Cloned = true
				entry.Path = path
			}
		}
		entries = append(entries, entry)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(a.stdout, "no repos declared in catalog/repos.json")
		return nil
	}
	for _, entry := range entries {
		status := "available"
		if entry.Cloned {
			status = "cloned"
		} else if entry.Selected {
			status = "selected"
		}
		line := fmt.Sprintf("%s\t%s\t%s", entry.ID, status, entry.GitURL)
		if entry.Default {
			line += "\tdefault"
		}
		fmt.Fprintln(a.stdout, line)
		if entry.Description != "" {
			fmt.Fprintf(a.stdout, "  %s\n", entry.Description)
		}
	}
	return nil
}

func (a app) runReposAdd(args []string) error {
	var opts repoCommonOpts
	var printOnly bool
	fs := newFlagSet("my repos add", a.stderr)
	bindRepoCommonFlags(fs, &opts)
	fs.BoolVar(&printOnly, "print", false, "print the planned clone without changing files")
	rest, err := parseInterspersed(fs, args, repoValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my repos add <id>")
	}
	return a.repoAddByID(opts.home, opts.manifestName, opts.umbrellaRoot, rest[0], printOnly, opts.jsonOut)
}

func (a app) runReposRemove(args []string) error {
	var opts repoCommonOpts
	var force bool
	fs := newFlagSet("my repos remove", a.stderr)
	bindRepoCommonFlags(fs, &opts)
	fs.BoolVar(&force, "force", false, "also delete the local clone directory")
	rest, err := parseInterspersed(fs, args, repoValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my repos remove <id> [--force]")
	}
	id := rest[0]
	root, err := resolveUmbrellaRoot(opts.home, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if err := removeMountsFromState(root, nil, []string{id}); err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	path := umbrella.RepoPath(root, id)
	removed := false
	if force {
		if err := os.RemoveAll(path); err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		removed = true
	}
	if opts.jsonOut {
		return printJSON(a.stdout, map[string]any{
			"id":            id,
			"deselected":    true,
			"clone_removed": removed,
			"path":          path,
		})
	}
	if removed {
		fmt.Fprintf(a.stdout, "%s\tdeselected\tclone removed\n", id)
	} else {
		fmt.Fprintf(a.stdout, "%s\tdeselected\tclone kept at %s (pass --force to delete)\n", id, path)
	}
	return nil
}

func loadRepoUmbrellaState(home, umbrellaRoot string) (string, umbrella.State, error) {
	root, err := resolveUmbrellaRoot(home, umbrellaRoot)
	if err != nil {
		return "", umbrella.State{}, err
	}
	state, err := umbrella.LoadState(root)
	if errors.Is(err, os.ErrNotExist) {
		return root, umbrella.State{SchemaVersion: umbrella.SchemaVersion}, nil
	}
	if err != nil {
		return "", umbrella.State{}, err
	}
	return root, state, nil
}
