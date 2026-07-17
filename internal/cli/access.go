package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/fluxinc/my-cli/internal/access"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/workspace"
)

type accessCheckReport struct {
	DryRun  bool                 `json:"dry_run"`
	Writes  bool                 `json:"writes"`
	Targets []accessTargetReport `json:"targets"`
}

type accessTargetReport struct {
	Manifest         string          `json:"manifest"`
	Organization     string          `json:"organization"`
	SourceRef        string          `json:"source_ref"`
	Kind             string          `json:"kind"`
	Repository       string          `json:"repository"`
	Path             string          `json:"path"`
	Mounted          bool            `json:"mounted"`
	Decision         access.Decision `json:"decision"`
	PositiveBaseline bool            `json:"positive_baseline"`
	FutureAction     string          `json:"future_action"`
}

type accessTarget struct {
	Manifest     string
	Organization string
	SourceRef    string
	Kind         string
	Repository   string
	Path         string
}

func (a app) runAccess(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing access subcommand")
	}
	switch args[0] {
	case "check":
		return a.runAccessCheck(args[1:])
	case "-h", "--help", "help":
		a.printAccessUsage()
		return nil
	default:
		return fmt.Errorf("unknown access subcommand %q", args[0])
	}
}

func (a app) printAccessUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my access check --dry-run [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]

The dry run performs live provider checks and prints future block/quarantine
eligibility. It never writes baselines, inventory, workspace state, or files.`)
}

func (a app) runAccessCheck(args []string) error {
	var home string
	var manifestName string
	var umbrellaRoot string
	var dryRun bool
	var jsonOut bool
	fs := newFlagSet("my access check", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&dryRun, "dry-run", false, "perform a zero-write live-rights check")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home": true, "manifest": true, "umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("access check does not accept positional arguments")
	}
	if !dryRun {
		return fmt.Errorf("access check currently requires --dry-run; activation is a separate reviewed operation")
	}
	manifestName, err = defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return err
	}
	targets, err := collectAccessTargets(home, doc, umbrellaRoot)
	if err != nil {
		return err
	}
	inventory, err := access.LoadInventory(home)
	if err != nil {
		return err
	}
	report := accessCheckReport{DryRun: true, Writes: false}
	for _, target := range targets {
		decision := access.ResolveGitHub(target.Repository, a.accessRunner)
		mounted := pathExists(target.Path)
		baseline := hasPositiveBaseline(inventory, target.Path, decision)
		future := "none"
		switch {
		case !mounted:
			future = "not-mounted"
		case decision.State == access.StateAllowed:
			future = "keep-mounted"
		case !baseline:
			future = "block-only-no-positive-baseline"
		case decision.State == access.StateDenied:
			future = "revocation-pending-confirmation"
		default:
			future = "block-while-unknown"
		}
		report.Targets = append(report.Targets, accessTargetReport{
			Manifest: target.Manifest, Organization: target.Organization, SourceRef: target.SourceRef,
			Kind: target.Kind, Repository: target.Repository, Path: target.Path, Mounted: mounted,
			Decision: decision, PositiveBaseline: baseline, FutureAction: future,
		})
	}
	if jsonOut {
		return printJSON(a.stdout, report)
	}
	for _, target := range report.Targets {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\n", target.Kind, target.Repository, target.Decision.State, target.FutureAction, target.Path)
	}
	return nil
}

func collectAccessTargets(home string, doc registeredDoc, explicitRoot string) ([]accessTarget, error) {
	controlRepository := doc.ref.GitURL
	if manifest.GovernanceConfigured(doc.doc.Governance) {
		controlRepository = doc.doc.Governance.Authorization.ManifestRepository
	}
	targets := []accessTarget{{
		Manifest: doc.ref.Name, Organization: doc.doc.Organization.ID, SourceRef: "manifest:" + doc.ref.Name,
		Kind: "manifest", Repository: controlRepository, Path: doc.ref.LocalPath,
	}}
	root, err := umbrella.ResolveRoot(home, "", explicitRoot, doc.doc)
	if err != nil {
		return nil, err
	}
	mounts, err := workspace.ListMounts(home, doc.ref.Name, root)
	if err != nil {
		return nil, err
	}
	for _, mount := range mounts {
		if _, ok := access.GitHubRepositoryName(mount.GitURL); !ok {
			continue
		}
		targets = append(targets, accessTarget{
			Manifest: doc.ref.Name, Organization: doc.doc.Organization.ID, SourceRef: mount.SourceRef,
			Kind: mount.Kind, Repository: mount.GitURL, Path: mount.LocalPath,
		})
	}
	state, stateErr := umbrella.LoadState(root)
	if stateErr == nil {
		for _, id := range state.SelectedRepos {
			repo, ok, err := manifest.FindRepo(home, doc.ref.Name, id)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if _, github := access.GitHubRepositoryName(repo.GitURL); !github {
				continue
			}
			targets = append(targets, accessTarget{
				Manifest: doc.ref.Name, Organization: doc.doc.Organization.ID, SourceRef: "manifest:" + doc.ref.Name + ":repo:" + id,
				Kind: "repo", Repository: repo.GitURL, Path: umbrella.RepoPath(root, id),
			})
		}
	} else if !errors.Is(stateErr, os.ErrNotExist) {
		return nil, stateErr
	}
	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Kind+"\x00"+targets[i].Path < targets[j].Kind+"\x00"+targets[j].Path
	})
	return targets, nil
}

func hasPositiveBaseline(inventory access.Inventory, targetPath string, decision access.Decision) bool {
	abs, err := filepath.Abs(targetPath)
	if err != nil {
		return false
	}
	if real, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = real
	}
	for _, entry := range inventory.Repositories {
		if entry.CanonicalPath != abs {
			continue
		}
		if decision.Repository.NodeID != "" && entry.Repository.NodeID != decision.Repository.NodeID {
			return false
		}
		for _, baseline := range entry.Baselines {
			if baseline.Actor.ID == decision.Actor.ID && baseline.Actor.ID != 0 {
				return true
			}
		}
	}
	return false
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
