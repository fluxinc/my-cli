package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/access"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
	"github.com/fluxinc/my-cli/internal/workspace"
)

type accessCheckReport struct {
	DryRun      bool                 `json:"dry_run"`
	Writes      bool                 `json:"writes"`
	Targets     []accessTargetReport `json:"targets"`
	NextCommand string               `json:"next_command,omitempty"`
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
	JournalPath      string          `json:"journal_path,omitempty"`
	Error            string          `json:"error,omitempty"`
}

type accessTarget struct {
	Manifest     string
	Organization string
	SourceRef    string
	Kind         string
	Repository   string
	Path         string
	AllowedRoot  string
	Umbrella     string
}

func (a app) runAccess(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing access subcommand")
	}
	switch args[0] {
	case "check":
		return a.runAccessCheck(args[1:])
	case "activate":
		return a.runAccessActivate(args[1:])
	case "enforce":
		return a.runAccessEnforce(args[1:])
	case "status":
		return a.runAccessStatus(args[1:])
	case "monitor":
		return a.runAccessMonitor(args[1:])
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
	my access activate --yes [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]
	my access enforce [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]
	my access status [--manifest NAME] [--home DIR] [--umbrella DIR] [--json]
	my access monitor install|uninstall|run [--manifest NAME] [--home DIR] [--umbrella DIR]

The dry run performs live provider checks and prints future block/quarantine
eligibility. It never writes baselines, inventory, workspace state, or files.
Activation records positive per-repository baselines only after every mounted
target passes a live check. Enforcement persists observations and atomically
quarantines only confirmed revocations.`)
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
	report := accessCheckReport{DryRun: true, Writes: false, NextCommand: "my access activate --yes --manifest " + manifestName}
	for _, target := range targets {
		decision := resolveAccessTarget(target, inventory, a.accessRunner)
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
	fmt.Fprintf(a.stdout, "next\t%s\n", report.NextCommand)
	return nil
}

func (a app) runAccessActivate(args []string) error {
	home, manifestName, umbrellaRoot, jsonOut, yes, err := parseAccessMutationFlags("my access activate", a.stderr, args, true)
	if err != nil {
		return err
	}
	if !yes {
		return fmt.Errorf("access activation requires --yes after reviewing `my access check --dry-run`")
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
	type checkedTarget struct {
		target   accessTarget
		decision access.Decision
	}
	var checked []checkedTarget
	report := accessCheckReport{Writes: true, NextCommand: "my access monitor install --manifest " + manifestName}
	for _, target := range targets {
		mounted := pathExists(target.Path)
		if !mounted {
			report.Targets = append(report.Targets, accessTargetReport{
				Manifest: target.Manifest, Organization: target.Organization, SourceRef: target.SourceRef,
				Kind: target.Kind, Repository: target.Repository, Path: target.Path,
				Mounted: false, FutureAction: "not-mounted-no-baseline",
			})
			continue
		}
		decision := resolveAccessTarget(target, inventory, a.accessRunner)
		if !decision.Allows(access.PermissionRead) {
			return fmt.Errorf("activation made no changes: %s is not positively readable (%s)", target.Repository, decision.ReasonCode)
		}
		checked = append(checked, checkedTarget{target: target, decision: decision})
		report.Targets = append(report.Targets, accessTargetReport{
			Manifest: target.Manifest, Organization: target.Organization, SourceRef: target.SourceRef,
			Kind: target.Kind, Repository: target.Repository, Path: target.Path, Mounted: true,
			Decision: decision, PositiveBaseline: true, FutureAction: "positive-baseline-recorded",
		})
	}
	inputs := make([]access.RecordInput, 0, len(checked))
	for _, item := range checked {
		inputs = append(inputs, access.RecordInput{
			Home: home, Path: item.target.Path, AllowedRoot: item.target.AllowedRoot,
			Organization: item.target.Organization, Manifest: item.target.Manifest,
			Umbrella: item.target.Umbrella, SourceRef: item.target.SourceRef,
			Kind: item.target.Kind, Decision: item.decision, CheckedAt: time.Now(),
		})
	}
	if len(inputs) != 0 {
		if _, err := access.RecordPositiveBatch(inputs); err != nil {
			return err
		}
	}
	monitorInterval := 5 * time.Minute
	if value := strings.TrimSpace(doc.doc.Governance.Access.CheckInterval); value != "" {
		monitorInterval, err = time.ParseDuration(value)
		if err != nil {
			return err
		}
	}
	if _, err := a.installAccessMonitor(home, manifestName, umbrellaRoot, monitorInterval); err != nil {
		return fmt.Errorf("positive baselines were recorded, but proactive access monitor installation failed: %w; retry `my access monitor install --manifest %s`", err, manifestName)
	}
	report.NextCommand = "my access status --manifest " + manifestName
	if jsonOut {
		return printJSON(a.stdout, report)
	}
	for _, target := range report.Targets {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", target.Kind, target.Repository, target.FutureAction, target.Path)
	}
	fmt.Fprintf(a.stdout, "next\t%s\n", report.NextCommand)
	return nil
}

func (a app) runAccessEnforce(args []string) error {
	home, manifestName, umbrellaRoot, jsonOut, _, err := parseAccessMutationFlags("my access enforce", a.stderr, args, false)
	if err != nil {
		return err
	}
	manifestName, err = defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return err
	}
	confirmations, interval, err := accessRevocationPolicy(doc.doc.Governance.Access)
	if err != nil {
		return err
	}
	targets, err := collectAccessTargets(home, doc, umbrellaRoot)
	if err != nil {
		return err
	}
	report := accessCheckReport{Writes: true}
	var enforcementErrors []string
	for _, target := range targets {
		mounted := pathExists(target.Path)
		inventory, loadErr := access.LoadInventory(home)
		if loadErr != nil {
			return loadErr
		}
		decision := resolveAccessTarget(target, inventory, a.accessRunner)
		baseline := hasPositiveBaseline(inventory, target.Path, decision)
		action := "not-mounted"
		journalPath := ""
		targetError := ""
		if mounted {
			switch {
			case !baseline:
				action = "blocked-activation-required"
			case decision.Actor.ID == 0:
				action = "blocked-identity-unknown"
			default:
				observation, observeErr := access.RecordObservation(access.ObservationInput{
					Home: home, Path: target.Path, Decision: decision, CheckedAt: time.Now(),
					RequiredConfirmations: confirmations, ConfirmationInterval: interval,
				})
				if observeErr != nil {
					return observeErr
				}
				switch {
				case observation.CleanupEligible:
					quarantined, quarantineErr := access.QuarantineConfirmed(access.QuarantineInput{
						Home: home, Path: target.Path, ActorID: decision.Actor.ID, Now: time.Now(),
					})
					if quarantineErr != nil {
						action = "blocked-quarantine-failed"
						journalPath = quarantined.JournalPath
						targetError = quarantineErr.Error()
						enforcementErrors = append(enforcementErrors, target.Repository+": "+quarantineErr.Error())
					} else {
						action = "quarantined"
						journalPath = quarantined.JournalPath
					}
				case decision.State == access.StateAllowed:
					action = "keep-mounted"
				case decision.State == access.StateDenied:
					action = "blocked-revocation-pending"
				default:
					action = "blocked-while-unknown"
				}
			}
		}
		report.Targets = append(report.Targets, accessTargetReport{
			Manifest: target.Manifest, Organization: target.Organization, SourceRef: target.SourceRef,
			Kind: target.Kind, Repository: target.Repository, Path: target.Path, Mounted: mounted,
			Decision: decision, PositiveBaseline: baseline, FutureAction: action, JournalPath: journalPath,
			Error: targetError,
		})
	}
	if jsonOut {
		if err := printJSON(a.stdout, report); err != nil {
			return err
		}
	} else {
		for _, target := range report.Targets {
			fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", target.Kind, target.Repository, target.FutureAction, target.Path)
		}
	}
	if len(enforcementErrors) != 0 {
		return fmt.Errorf("one or more confirmed revocations could not be quarantined: %s", strings.Join(enforcementErrors, "; "))
	}
	return nil
}

func parseAccessMutationFlags(name string, stderr interface{ Write([]byte) (int, error) }, args []string, includeYes bool) (string, string, string, bool, bool, error) {
	var home, manifestName, umbrellaRoot string
	var jsonOut, yes bool
	fs := newFlagSet(name, stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	if includeYes {
		fs.BoolVar(&yes, "yes", false, "record live positive baselines")
	}
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "manifest": true, "umbrella": true})
	if err != nil {
		return "", "", "", false, false, err
	}
	if len(rest) != 0 {
		return "", "", "", false, false, fmt.Errorf("%s does not accept positional arguments", name)
	}
	return home, manifestName, umbrellaRoot, jsonOut, yes, nil
}

func accessRevocationPolicy(config manifest.GovernanceAccess) (int, time.Duration, error) {
	confirmations := config.RevocationConfirmations
	if confirmations == 0 {
		confirmations = 2
	}
	interval := 15 * time.Minute
	if strings.TrimSpace(config.ConfirmationInterval) != "" {
		parsed, err := time.ParseDuration(config.ConfirmationInterval)
		if err != nil {
			return 0, 0, err
		}
		interval = parsed
	}
	return confirmations, interval, nil
}

func accessPositiveTTL(config manifest.GovernanceAccess) (time.Duration, error) {
	if strings.TrimSpace(config.PositiveTTL) == "" {
		return 15 * time.Minute, nil
	}
	return time.ParseDuration(config.PositiveTTL)
}

func (a app) requireGovernedLaunchAccess(home string, doc registeredDoc, root string) error {
	if !manifest.GovernanceConfigured(doc.doc.Governance) {
		return nil
	}
	ttl, err := accessPositiveTTL(doc.doc.Governance.Access)
	if err != nil {
		return err
	}
	confirmations, interval, err := accessRevocationPolicy(doc.doc.Governance.Access)
	if err != nil {
		return err
	}
	targets, err := collectAccessTargets(home, doc, root)
	if err != nil {
		return err
	}
	inventory, err := access.LoadInventory(home)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, target := range targets {
		if !pathExists(target.Path) {
			continue
		}
		entry, ok := managedInventoryEntry(inventory, target.Path)
		if !ok {
			if err := requireLiveReadableAccess(target, inventory, a.accessRunner); err != nil {
				return err
			}
			continue
		}
		baseline, ok := newestPositiveBaseline(entry.Baselines)
		if !ok {
			if err := requireLiveReadableAccess(target, inventory, a.accessRunner); err != nil {
				return err
			}
			continue
		}
		checkedAt, err := time.Parse(time.RFC3339Nano, baseline.CheckedAt)
		if err != nil {
			return fmt.Errorf("governed launch blocked: invalid positive baseline for %s", target.Repository)
		}
		if progressBlocksCachedAccess(entry.Revocations, baseline, checkedAt) {
			return fmt.Errorf("governed launch blocked: %s has a newer non-allowed access observation; run `my access enforce`", target.Repository)
		}
		if !now.Before(checkedAt.Add(ttl)) {
			decision := access.ResolveGitHubKnown(target.Repository, entry.Repository, a.accessRunner)
			if !hasPositiveBaseline(inventory, target.Path, decision) {
				return fmt.Errorf("governed launch blocked: current GitHub actor has no positive baseline for %s", target.Repository)
			}
			if decision.Actor.ID == 0 {
				return fmt.Errorf("governed launch blocked: access to %s is unknown (%s)", target.Repository, decision.ReasonCode)
			}
			observation, err := access.RecordObservation(access.ObservationInput{
				Home: home, Path: target.Path, Decision: decision, CheckedAt: now,
				RequiredConfirmations: confirmations, ConfirmationInterval: interval,
			})
			if err != nil {
				return err
			}
			if observation.CleanupEligible {
				if _, err := access.QuarantineConfirmed(access.QuarantineInput{
					Home: home, Path: target.Path, ActorID: decision.Actor.ID, Now: now,
				}); err != nil {
					return fmt.Errorf("governed launch blocked and quarantine failed for %s: %w", target.Repository, err)
				}
				return fmt.Errorf("governed launch blocked: confirmed revocation quarantined %s", target.Repository)
			}
			if decision.State != access.StateAllowed {
				return fmt.Errorf("governed launch blocked: access to %s is %s (%s)", target.Repository, decision.State, decision.ReasonCode)
			}
		}
	}
	return nil
}

func (a app) checkGovernedLaunchAccessReadOnly(home string, doc registeredDoc, root string) error {
	if !manifest.GovernanceConfigured(doc.doc.Governance) {
		return nil
	}
	ttl, err := accessPositiveTTL(doc.doc.Governance.Access)
	if err != nil {
		return err
	}
	targets, err := collectAccessTargets(home, doc, root)
	if err != nil {
		return err
	}
	inventory, err := access.LoadInventory(home)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, target := range targets {
		if !pathExists(target.Path) {
			continue
		}
		if entry, ok := managedInventoryEntry(inventory, target.Path); ok {
			if baseline, ok := newestPositiveBaseline(entry.Baselines); ok {
				checkedAt, parseErr := time.Parse(time.RFC3339Nano, baseline.CheckedAt)
				if parseErr == nil && now.Before(checkedAt.Add(ttl)) && !progressBlocksCachedAccess(entry.Revocations, baseline, checkedAt) {
					continue
				}
			}
		}
		if err := requireLiveReadableAccess(target, inventory, a.accessRunner); err != nil {
			return errors.New(strings.Replace(err.Error(), "governed launch blocked: ", "launch will be blocked: ", 1))
		}
	}
	return nil
}

func requireLiveReadableAccess(target accessTarget, inventory access.Inventory, runner access.Runner) error {
	decision := resolveAccessTarget(target, inventory, runner)
	if decision.Allows(access.PermissionRead) {
		// A live check permits this launch only. It deliberately does not write a
		// positive baseline or activate monitoring/quarantine.
		return nil
	}
	if decision.State == access.StateDenied {
		return fmt.Errorf("governed launch blocked: current GitHub identity cannot read %s (%s)", target.Repository, decision.ReasonCode)
	}
	return fmt.Errorf("governed launch blocked: access to %s could not be verified because provider state is unknown (%s)", target.Repository, decision.ReasonCode)
}

func newestPositiveBaseline(baselines []access.PositiveBaseline) (access.PositiveBaseline, bool) {
	var newest access.PositiveBaseline
	var newestAt time.Time
	for _, baseline := range baselines {
		checkedAt, err := time.Parse(time.RFC3339Nano, baseline.CheckedAt)
		if err != nil {
			continue
		}
		if newestAt.IsZero() || checkedAt.After(newestAt) {
			newest, newestAt = baseline, checkedAt
		}
	}
	return newest, !newestAt.IsZero()
}

func progressBlocksCachedAccess(progresses []access.RevocationProgress, baseline access.PositiveBaseline, baselineAt time.Time) bool {
	for _, progress := range progresses {
		if progress.Actor.ID != baseline.Actor.ID || progress.LastState == access.StateAllowed {
			continue
		}
		checkedAt, err := time.Parse(time.RFC3339Nano, progress.LastCheckedAt)
		if err == nil && checkedAt.After(baselineAt) {
			return true
		}
	}
	return false
}

func resolveAccessTarget(target accessTarget, inventory access.Inventory, runner access.Runner) access.Decision {
	if entry, ok := managedInventoryEntry(inventory, target.Path); ok {
		return access.ResolveGitHubKnown(target.Repository, entry.Repository, runner)
	}
	return access.ResolveGitHub(target.Repository, runner)
}

func managedInventoryEntry(inventory access.Inventory, targetPath string) (access.ManagedRepository, bool) {
	abs, err := filepath.Abs(targetPath)
	if err != nil {
		return access.ManagedRepository{}, false
	}
	if real, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = real
	}
	for _, entry := range inventory.Repositories {
		if entry.CanonicalPath == abs {
			return entry, true
		}
	}
	return access.ManagedRepository{}, false
}

func collectAccessTargets(home string, doc registeredDoc, explicitRoot string) ([]accessTarget, error) {
	controlRepository := doc.ref.GitURL
	if manifest.GovernanceConfigured(doc.doc.Governance) {
		controlRepository = doc.doc.Governance.Authorization.ManifestRepository
	}
	targets := []accessTarget{{
		Manifest: doc.ref.Name, Organization: doc.doc.Organization.ID, SourceRef: "manifest:" + doc.ref.Name,
		Kind: "manifest", Repository: controlRepository, Path: doc.ref.LocalPath,
		AllowedRoot: filepath.Dir(doc.ref.LocalPath),
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
			AllowedRoot: root, Umbrella: root,
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
				AllowedRoot: root, Umbrella: root,
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
