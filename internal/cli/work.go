package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/fluxinc/our-ai/internal/umbrella"
	"github.com/fluxinc/our-ai/internal/worksession"
	"github.com/fluxinc/our-ai/internal/workspace"
)

type workCommonOpts struct {
	home         string
	manifestName string
	umbrellaRoot string
	jsonOut      bool
}

func bindWorkCommonFlags(fs *flag.FlagSet, opts *workCommonOpts) {
	fs.StringVar(&opts.home, "home", "", "override home directory (testing)")
	fs.StringVar(&opts.manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON output")
}

func workValueFlags() map[string]bool {
	return map[string]bool{
		"home":     true,
		"manifest": true,
		"umbrella": true,
		"slug":     true,
	}
}

func (a app) runWork(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: our work start|status [flags]")
	}
	switch args[0] {
	case "start":
		return a.runWorkStart(args[1:])
	case "status":
		return a.runWorkStatus(args[1:])
	default:
		return fmt.Errorf("unknown work subcommand %q (expected start|status)", args[0])
	}
}

func (a app) runWorkStart(args []string) error {
	var opts workCommonOpts
	var slug string
	fs := newFlagSet("our work start", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.StringVar(&slug, "slug", "", "short session slug (lowercase, digits, hyphens)")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: our work start [--slug SLUG] [--json]")
	}

	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	specs, err := sessionMountSpecs(opts.home, opts.manifestName, root)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if len(specs) == 0 {
		return a.maybeJSONError(opts.jsonOut, structuredCommandError{
			code:        "no_session_mounts",
			message:     "no synced content mounts eligible for a session worktree under " + root,
			remediation: "run our setup to clone the manifest's content mounts first",
		})
	}

	session, err := worksession.Start(worksession.StartOptions{
		Root:   root,
		Slug:   slug,
		Mounts: specs,
	})
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, session)
	}
	fmt.Fprintf(a.stdout, "started session %s\n", session.ID)
	fmt.Fprintf(a.stdout, "  path: %s\n", session.Path)
	for _, m := range session.Mounts {
		fmt.Fprintf(a.stdout, "  %s -> %s (from %s)\n", m.ID, m.Branch, m.BaseBranch)
	}
	fmt.Fprintf(a.stdout, "finish with: our work finish --land | --publish | --discard\n")
	return nil
}

func (a app) runWorkStatus(args []string) error {
	var opts workCommonOpts
	var all bool
	fs := newFlagSet("our work status", a.stderr)
	bindWorkCommonFlags(fs, &opts)
	fs.BoolVar(&all, "all", false, "include finished and discarded sessions")
	rest, err := parseInterspersed(fs, args, workValueFlags())
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: our work status [--all] [--json]")
	}

	root, err := resolveWorkUmbrella(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	sessions, err := worksession.List(root)
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	statuses := []worksession.SessionStatus{}
	for _, session := range sessions {
		if !all && session.Status != worksession.StatusActive {
			continue
		}
		status, err := worksession.Inspect(session, nil)
		if err != nil {
			return a.maybeJSONError(opts.jsonOut, err)
		}
		statuses = append(statuses, status)
	}
	if opts.jsonOut {
		return printJSON(a.stdout, statuses)
	}
	if len(statuses) == 0 {
		fmt.Fprintln(a.stdout, "no active sessions")
		return nil
	}
	for _, status := range statuses {
		fmt.Fprintf(a.stdout, "%s  %s  created %s\n", status.ID, status.Status, status.CreatedAt)
		for _, m := range status.Mounts {
			line := fmt.Sprintf("  %s  dirty=%d unlanded=%d", m.ID, len(m.Dirty), m.Unlanded)
			if m.Error != "" {
				line += "  error=" + m.Error
			}
			fmt.Fprintln(a.stdout, line)
		}
	}
	return nil
}

// resolveWorkUmbrella locates the umbrella root for work commands: explicit
// flag, walk-up discovery, then the configured root of registered manifests.
func resolveWorkUmbrella(home, manifestName, explicit string) (string, error) {
	if explicit != "" {
		return resolveUmbrellaRoot(home, explicit)
	}
	if root, ok := umbrella.FindRoot("."); ok {
		return root, nil
	}
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, doc := range docs {
		root, err := umbrella.ResolveRoot(home, "", "", doc.doc)
		if err != nil {
			return "", err
		}
		if _, err := umbrella.LoadWorkspace(root); err == nil {
			if !stringInSlice(candidates, root) {
				candidates = append(candidates, root)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return "", noUmbrellaError("no our umbrella found; run our setup or pass --umbrella", "run our setup or pass --umbrella <path>")
	default:
		return "", structuredCommandError{
			code:        "ambiguous_umbrella",
			message:     fmt.Sprintf("multiple umbrellas configured (%v); pass --umbrella or --manifest", candidates),
			remediation: "pass --umbrella <path> to select one umbrella",
		}
	}
}

// sessionMountSpecs returns the content mounts under root that are eligible
// for session worktrees: every locally cloned mount except repo-kind code
// mounts, which keep their own product-style flow.
func sessionMountSpecs(home, manifestName, root string) ([]worksession.MountSpec, error) {
	mounts, err := workspace.ListMounts(home, manifestName, root)
	if err != nil {
		return nil, err
	}
	var specs []worksession.MountSpec
	seen := map[string]bool{}
	for _, mount := range mounts {
		if mount.UmbrellaRoot != root || mount.Kind == "repo" || seen[mount.LocalPath] {
			continue
		}
		if !isGitCheckout(mount.LocalPath) {
			continue
		}
		seen[mount.LocalPath] = true
		specs = append(specs, worksession.MountSpec{
			ID:       mount.ID,
			Kind:     mount.Kind,
			RepoPath: mount.LocalPath,
		})
	}
	return specs, nil
}
