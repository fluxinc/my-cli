package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/access"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/outbox"
	"github.com/fluxinc/my-cli/internal/safefs"
	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/workspace"
)

const policyAttestationSchemaVersion = 1

type policyContext struct {
	home         string
	manifestName string
	root         string
	doc          registeredDoc
	mounts       map[string]workspace.Entry
}

type policyStatusRow struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	Acceptance string `json:"acceptance"`
	Status     string `json:"status"`
	Path       string `json:"path,omitempty"`
}

type policyAcceptanceRow struct {
	Organization   string `json:"organization"`
	PolicyID       string `json:"policy_id"`
	PolicyVersion  string `json:"policy_version"`
	PolicySHA256   string `json:"policy_sha256"`
	SubjectID      int64  `json:"subject_id"`
	SubjectLogin   string `json:"subject_login"`
	AcceptedAt     string `json:"accepted_at"`
	ManifestCommit string `json:"manifest_commit"`
	State          string `json:"state"`
	Path           string `json:"path"`
	PRURL          string `json:"pr_url,omitempty"`
}

type policyAttestation struct {
	SchemaVersion   int    `json:"schema_version"`
	Organization    string `json:"organization"`
	PolicyID        string `json:"policy_id"`
	PolicyVersion   string `json:"policy_version"`
	PolicySHA256    string `json:"policy_sha256"`
	SubjectProvider string `json:"subject_provider"`
	SubjectID       int64  `json:"subject_id"`
	SubjectLogin    string `json:"subject_login"`
	AcceptedAt      string `json:"accepted_at"`
	ManifestCommit  string `json:"manifest_commit"`
}

type policySupersession struct {
	SchemaVersion   int    `json:"schema_version"`
	EventType       string `json:"event_type"`
	Organization    string `json:"organization"`
	PolicyID        string `json:"policy_id"`
	PolicyVersion   string `json:"policy_version"`
	PolicySHA256    string `json:"policy_sha256"`
	SubjectProvider string `json:"subject_provider"`
	SubjectID       int64  `json:"subject_id"`
	Supersedes      string `json:"supersedes"`
	ActorProvider   string `json:"actor_provider"`
	ActorID         int64  `json:"actor_id"`
	ActorLogin      string `json:"actor_login"`
	Reason          string `json:"reason"`
	SupersededAt    string `json:"superseded_at"`
	ManifestCommit  string `json:"manifest_commit"`
}

func (a app) runPolicy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing policy subcommand")
	}
	switch args[0] {
	case "list":
		return a.runPolicyList(args[1:])
	case "show":
		return a.runPolicyShow(args[1:])
	case "status":
		return a.runPolicyStatus(args[1:])
	case "accept":
		return a.runPolicyAccept(args[1:])
	case "acceptances":
		return a.runPolicyAcceptances(args[1:])
	case "supersede":
		return a.runPolicySupersede(args[1:])
	default:
		return fmt.Errorf("unknown policy subcommand %q; supported subcommands are list, show, status, accept, acceptances, and supersede", args[0])
	}
}

func (a app) runPolicyList(args []string) error {
	home, manifestName, umbrellaRoot, jsonOut, _, rest, err := parsePolicyFlags("my policy list", a.stderr, args, false)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("policy list does not accept positional arguments")
	}
	ctx, err := loadPolicyContext(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, ctx.doc.doc.Governance.Policies)
	}
	for _, policy := range ctx.doc.doc.Governance.Policies {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\t%s\n", policy.ID, policy.Version, policy.Acceptance, policy.SHA256, policy.Title)
	}
	return nil
}

func (a app) runPolicyShow(args []string) error {
	home, manifestName, umbrellaRoot, jsonOut, _, rest, err := parsePolicyFlags("my policy show", a.stderr, args, false)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my policy show <id>")
	}
	ctx, err := loadPolicyContext(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	policy, err := findGovernancePolicy(ctx.doc.doc, rest[0])
	if err != nil {
		return err
	}
	content, _, err := verifiedPolicyBlob(ctx, policy)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, struct {
			Policy  manifest.Policy `json:"policy"`
			Content string          `json:"content"`
		}{Policy: policy, Content: string(content)})
	}
	_, err = a.stdout.Write(content)
	return err
}

func (a app) runPolicyStatus(args []string) error {
	home, manifestName, umbrellaRoot, jsonOut, _, rest, err := parsePolicyFlags("my policy status", a.stderr, args, false)
	if err != nil {
		return err
	}
	if len(rest) > 1 {
		return fmt.Errorf("usage: my policy status [id]")
	}
	ctx, err := loadPolicyContext(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	actor, err := policyActor(ctx.doc.doc, a.accessRunner)
	if err != nil {
		return err
	}
	policies := ctx.doc.doc.Governance.Policies
	if len(rest) == 1 {
		policy, err := findGovernancePolicy(ctx.doc.doc, rest[0])
		if err != nil {
			return err
		}
		policies = []manifest.Policy{policy}
	}
	rows := make([]policyStatusRow, 0, len(policies))
	for _, policy := range policies {
		row, err := currentPolicyStatus(ctx, policy, actor)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	if jsonOut {
		return printJSON(a.stdout, rows)
	}
	for _, row := range rows {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", row.ID, row.Status, row.Version, row.Path)
	}
	return nil
}

func (a app) runPolicyAccept(args []string) error {
	home, manifestName, umbrellaRoot, jsonOut, yes, positional, err := parsePolicyFlags("my policy accept", a.stderr, args, true)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return fmt.Errorf("usage: my policy accept <id> --yes")
	}
	if !yes {
		return fmt.Errorf("policy acceptance requires --yes after reading the exact document with `my policy show %s`", positional[0])
	}
	ctx, err := loadPolicyContext(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	policy, err := findGovernancePolicy(ctx.doc.doc, positional[0])
	if err != nil {
		return err
	}
	if _, _, err := verifiedPolicyBlob(ctx, policy); err != nil {
		return err
	}
	actor, err := policyActor(ctx.doc.doc, a.accessRunner)
	if err != nil {
		return err
	}
	ledger, ok := ctx.mounts[ctx.doc.doc.Governance.Attestations.Mount]
	if !ok {
		return fmt.Errorf("attestation mount %q is not materialized", ctx.doc.doc.Governance.Attestations.Mount)
	}
	manifestCommit, err := gitPolicyText(ctx.doc.ref.LocalPath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	digestName := strings.TrimPrefix(policy.SHA256, "sha256:")
	relativePath := filepath.ToSlash(filepath.Join(
		ctx.doc.doc.Governance.Attestations.Path,
		strconv.FormatInt(actor.ID, 10), policy.ID, digestName+".json",
	))
	fullPath := filepath.Join(ledger.LocalPath, filepath.FromSlash(relativePath))
	attestation := policyAttestation{
		SchemaVersion: policyAttestationSchemaVersion, Organization: ctx.doc.doc.Organization.ID,
		PolicyID: policy.ID, PolicyVersion: policy.Version, PolicySHA256: policy.SHA256,
		SubjectProvider: "github", SubjectID: actor.ID, SubjectLogin: actor.Login,
		AcceptedAt: time.Now().UTC().Format(time.RFC3339Nano), ManifestCommit: manifestCommit,
	}
	data, err := json.Marshal(attestation)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, err := readRegularPolicyFile(fullPath); err == nil {
		var recorded policyAttestation
		if json.Unmarshal(existing, &recorded) != nil || !samePolicyAcceptance(recorded, attestation) {
			return fmt.Errorf("existing attestation path contains different evidence and will not be overwritten: %s", fullPath)
		}
		data = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		if err := ensurePolicyParent(ledger.LocalPath, fullPath); err != nil {
			return err
		}
		file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		if err := safefs.SyncDirectory(filepath.Dir(fullPath)); err != nil {
			return err
		}
	}
	if _, err := gitPolicyBytes(ledger.LocalPath, "add", "-N", "--", relativePath); err != nil {
		return fmt.Errorf("mark attestation intent-to-add: %w", err)
	}
	event, err := ensurePolicyAcceptanceQueued(ctx, ledger, relativePath, data)
	if err != nil {
		return fmt.Errorf("policy acceptance was written at %s but publication queueing failed: %w", fullPath, err)
	}
	if event.State == outbox.StateQueued || event.State == outbox.StateAttemptFailed {
		if _, publishErr := a.publishPolicyAcceptance(ctx, event); publishErr != nil {
			fmt.Fprintf(a.stderr, "warning: policy acceptance is durable locally and remains in the publication outbox: %v\n", publishErr)
			fmt.Fprintf(a.stderr, "next: my record flush --manifest %s --umbrella %s\n", ctx.manifestName, ctx.root)
		}
	}
	row, err := currentPolicyStatus(ctx, policy, actor)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, row)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", row.ID, row.Status, row.Path)
	return nil
}

func (a app) runPolicyAcceptances(args []string) error {
	home, manifestName, umbrellaRoot, jsonOut, _, rest, err := parsePolicyFlags("my policy acceptances", a.stderr, args, false)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("policy acceptances does not accept positional arguments")
	}
	ctx, err := loadPolicyContext(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	if _, issues := a.reconcileSubmittedOutbox(ctx.root); len(issues) != 0 {
		for _, issue := range issues {
			fmt.Fprintf(a.stderr, "warning: acceptance merge reconciliation: %v\n", issue)
		}
	}
	rows, rowIssues, err := policyAcceptanceRows(ctx)
	if err != nil {
		return err
	}
	for _, issue := range rowIssues {
		fmt.Fprintf(a.stderr, "warning: policy acceptance skipped: %v\n", issue)
	}
	if jsonOut {
		return printJSON(a.stdout, rows)
	}
	for _, row := range rows {
		fmt.Fprintf(a.stdout, "%d\t%s\t%s\t%s\t%s\n", row.SubjectID, row.PolicyID, row.PolicyVersion, row.State, row.PRURL)
	}
	return nil
}

func (a app) runPolicySupersede(args []string) error {
	var home, manifestName, umbrellaRoot, reason string
	var subjectID int64
	var yes, jsonOut bool
	fs := newFlagSet("my policy supersede", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "use a registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.Int64Var(&subjectID, "subject-id", 0, "immutable GitHub subject id to supersede")
	fs.StringVar(&reason, "reason", "", "administrative supersession reason")
	fs.BoolVar(&yes, "yes", false, "confirm append-only supersession")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home": true, "manifest": true, "umbrella": true, "subject-id": true, "reason": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 1 || subjectID <= 0 || strings.TrimSpace(reason) == "" || !yes {
		return fmt.Errorf("usage: my policy supersede <id> --subject-id GITHUB_NUMERIC_ID --reason TEXT --yes")
	}
	ctx, err := loadPolicyContext(home, manifestName, umbrellaRoot)
	if err != nil {
		return err
	}
	policy, err := findGovernancePolicy(ctx.doc.doc, rest[0])
	if err != nil {
		return err
	}
	decision := access.ResolveGitHub(ctx.doc.doc.Governance.Authorization.ManifestRepository, a.accessRunner)
	if err := access.Require(decision, access.PermissionAdmin); err != nil {
		return fmt.Errorf("policy supersession requires manifest administrator authority: %w", err)
	}
	ledger, ok := ctx.mounts[ctx.doc.doc.Governance.Attestations.Mount]
	if !ok {
		return fmt.Errorf("attestation mount %q is not materialized", ctx.doc.doc.Governance.Attestations.Mount)
	}
	target := policyAttestationRelativePath(ctx.doc.doc, subjectID, policy)
	targetData, err := readRegularPolicyFile(filepath.Join(ledger.LocalPath, filepath.FromSlash(target)))
	if err != nil {
		return fmt.Errorf("read acceptance to supersede: %w", err)
	}
	if _, err := validatePolicyAcceptanceFile(ctx, target, targetData, true); err != nil {
		return err
	}
	manifestCommit, err := gitPolicyText(ctx.doc.ref.LocalPath, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return err
	}
	relativePath := policySupersessionRelativePath(ctx.doc.doc, subjectID, policy)
	fullPath := filepath.Join(ledger.LocalPath, filepath.FromSlash(relativePath))
	event := policySupersession{
		SchemaVersion: policyAttestationSchemaVersion, EventType: "supersession",
		Organization: ctx.doc.doc.Organization.ID, PolicyID: policy.ID,
		PolicyVersion: policy.Version, PolicySHA256: policy.SHA256,
		SubjectProvider: "github", SubjectID: subjectID, Supersedes: target,
		ActorProvider: "github", ActorID: decision.Actor.ID, ActorLogin: decision.Actor.Login,
		Reason: strings.TrimSpace(reason), SupersededAt: time.Now().UTC().Format(time.RFC3339Nano),
		ManifestCommit: manifestCommit,
	}
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if existing, err := readRegularPolicyFile(fullPath); err == nil {
		var recorded policySupersession
		if json.Unmarshal(existing, &recorded) != nil || !samePolicySupersession(recorded, event) {
			return fmt.Errorf("existing supersession path contains different evidence and will not be overwritten: %s", fullPath)
		}
		data = existing
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else {
		if err := ensurePolicyParent(ledger.LocalPath, fullPath); err != nil {
			return err
		}
		file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		if _, err = file.Write(data); err == nil {
			err = file.Sync()
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		if err := safefs.SyncDirectory(filepath.Dir(fullPath)); err != nil {
			return err
		}
	}
	if _, err := gitPolicyBytes(ledger.LocalPath, "add", "-N", "--", relativePath); err != nil {
		return fmt.Errorf("mark supersession intent-to-add: %w", err)
	}
	queued, err := ensurePolicyAcceptanceQueued(ctx, ledger, relativePath, data)
	if err != nil {
		return err
	}
	result := queued
	if queued.State == outbox.StateQueued || queued.State == outbox.StateAttemptFailed {
		result, err = a.publishPolicyAcceptance(ctx, queued)
		if err != nil {
			fmt.Fprintf(a.stderr, "warning: policy supersession is durable locally and remains in the publication outbox: %v\n", err)
		}
	}
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\t%d\t%s\t%s\n", policy.ID, subjectID, result.State, relativePath)
	return nil
}

func parsePolicyFlags(name string, stderr interface{ Write([]byte) (int, error) }, args []string, accept bool) (string, string, string, bool, bool, []string, error) {
	var home, manifestName, umbrellaRoot string
	var jsonOut, yes bool
	fs := newFlagSet(name, stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "use a registered manifest")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	known := map[string]bool{"home": true, "manifest": true, "umbrella": true}
	if accept {
		fs.BoolVar(&yes, "yes", false, "confirm acceptance of the exact policy bytes")
	}
	rest, err := parseInterspersed(fs, args, known)
	if err != nil {
		return "", "", "", false, false, nil, err
	}
	return home, manifestName, umbrellaRoot, jsonOut, yes, rest, nil
}

func loadPolicyContext(home, manifestName, umbrellaRoot string) (policyContext, error) {
	manifestName, err := defaultManifestName(home, manifestName, umbrellaRoot)
	if err != nil {
		return policyContext{}, err
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return policyContext{}, err
	}
	root, err := resolveMyRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		return policyContext{}, err
	}
	entries, err := workspace.ListMounts(home, manifestName, root)
	if err != nil {
		return policyContext{}, err
	}
	mounts := make(map[string]workspace.Entry, len(entries))
	for _, entry := range entries {
		mounts[entry.ID] = entry
	}
	return policyContext{home: home, manifestName: manifestName, root: root, doc: doc, mounts: mounts}, nil
}

func findGovernancePolicy(doc manifest.Document, id string) (manifest.Policy, error) {
	for _, policy := range doc.Governance.Policies {
		if policy.ID == id {
			return policy, nil
		}
	}
	return manifest.Policy{}, fmt.Errorf("unknown governance policy %q", id)
}

func verifiedPolicyBlob(ctx policyContext, policy manifest.Policy) ([]byte, string, error) {
	mount, ok := ctx.mounts[policy.Mount]
	if !ok {
		return nil, "", fmt.Errorf("policy mount %q is not materialized", policy.Mount)
	}
	content, err := gitPolicyBytes(mount.LocalPath, "cat-file", "blob", "HEAD:"+filepath.ToSlash(policy.Path))
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(content)
	actual := "sha256:" + hex.EncodeToString(digest[:])
	if actual != policy.SHA256 {
		return nil, actual, fmt.Errorf("policy %s digest mismatch: manifest declares %s but committed blob is %s", policy.ID, policy.SHA256, actual)
	}
	return content, actual, nil
}

func policyActor(doc manifest.Document, runner access.Runner) (access.Actor, error) {
	decision := access.ResolveGitHub(doc.Governance.Authorization.ManifestRepository, runner)
	if decision.Actor.ID == 0 || decision.Actor.NodeID == "" {
		return access.Actor{}, fmt.Errorf("cannot establish immutable GitHub identity for policy acceptance: %s", decision.ReasonCode)
	}
	if decision.State != access.StateAllowed {
		return access.Actor{}, fmt.Errorf("cannot establish readable manifest authority for policy acceptance: %s", decision.ReasonCode)
	}
	return decision.Actor, nil
}

func currentPolicyStatus(ctx policyContext, policy manifest.Policy, actor access.Actor) (policyStatusRow, error) {
	if _, _, err := verifiedPolicyBlob(ctx, policy); err != nil {
		return policyStatusRow{}, err
	}
	ledger, ok := ctx.mounts[ctx.doc.doc.Governance.Attestations.Mount]
	if !ok {
		return policyStatusRow{}, fmt.Errorf("attestation mount %q is not materialized", ctx.doc.doc.Governance.Attestations.Mount)
	}
	digestName := strings.TrimPrefix(policy.SHA256, "sha256:")
	relativePath := filepath.ToSlash(filepath.Join(
		ctx.doc.doc.Governance.Attestations.Path, strconv.FormatInt(actor.ID, 10), policy.ID, digestName+".json",
	))
	fullPath := filepath.Join(ledger.LocalPath, filepath.FromSlash(relativePath))
	row := policyStatusRow{
		ID: policy.ID, Title: policy.Title, Version: policy.Version, SHA256: policy.SHA256,
		Acceptance: policy.Acceptance, Status: "missing", Path: fullPath,
	}
	data, err := readRegularPolicyFile(fullPath)
	if errors.Is(err, os.ErrNotExist) {
		return row, nil
	}
	if err != nil {
		return row, err
	}
	var recorded policyAttestation
	if err := json.Unmarshal(data, &recorded); err != nil {
		return row, fmt.Errorf("read policy attestation %s: %w", fullPath, err)
	}
	expected := policyAttestation{
		SchemaVersion:   policyAttestationSchemaVersion,
		Organization:    ctx.doc.doc.Organization.ID,
		PolicyID:        policy.ID,
		PolicyVersion:   policy.Version,
		PolicySHA256:    policy.SHA256,
		SubjectProvider: "github",
		SubjectID:       actor.ID,
		SubjectLogin:    actor.Login,
	}
	if !samePolicyAcceptance(recorded, expected) {
		return row, fmt.Errorf("policy attestation does not match current actor and policy: %s", fullPath)
	}
	if _, err := gitPolicyBytes(ledger.LocalPath, "cat-file", "-e", "HEAD:"+relativePath); err == nil {
		row.Status = "published"
	} else {
		row.Status = "accepted-locally"
	}
	digest := outbox.ContentDigest(data)
	itemID := outbox.ItemID(ctx.doc.doc.Organization.ID, manifest.ReservedPolicyAcceptanceDomain, ledger.ID, relativePath, digest)
	if event, ok, err := outbox.Current(ctx.root, itemID); err != nil {
		return row, err
	} else if ok {
		switch event.State {
		case outbox.StateSubmitted:
			row.Status = "submitted"
		case outbox.StateMerged:
			row.Status = "merged"
		}
	}
	supersessionPath := filepath.Join(ledger.LocalPath, filepath.FromSlash(policySupersessionRelativePath(ctx.doc.doc, actor.ID, policy)))
	if supersessionData, err := readRegularPolicyFile(supersessionPath); err == nil {
		rel, _ := relativePathUnder(ledger.LocalPath, supersessionPath)
		if _, err := validatePolicySupersessionFile(ctx, filepath.ToSlash(rel), supersessionData, true); err != nil {
			return row, err
		}
		row.Status = "superseded"
	} else if !errors.Is(err, os.ErrNotExist) {
		return row, err
	}
	return row, nil
}

func ensurePolicyAcceptanceQueued(ctx policyContext, ledger workspace.Entry, relativePath string, data []byte) (outbox.Event, error) {
	digest := outbox.ContentDigest(data)
	itemID := outbox.ItemID(ctx.doc.doc.Organization.ID, manifest.ReservedPolicyAcceptanceDomain, ledger.ID, relativePath, digest)
	if current, ok, err := outbox.Current(ctx.root, itemID); err != nil {
		return outbox.Event{}, err
	} else if ok {
		return current, nil
	}
	event := outbox.Event{
		ItemID: itemID, Organization: ctx.doc.doc.Organization.ID, Manifest: ctx.doc.ref.Name,
		Domain: manifest.ReservedPolicyAcceptanceDomain, Mount: ledger.ID, RepoPath: ledger.LocalPath,
		RelativePath: filepath.ToSlash(relativePath), ContentSHA256: digest, State: outbox.StateQueued,
		Message: "policy acceptance queued for isolated governed publication",
	}
	return outbox.Append(ctx.root, event, time.Now())
}

func (a app) publishPolicyAcceptance(ctx policyContext, item outbox.Event) (outbox.Event, error) {
	if item.Domain != manifest.ReservedPolicyAcceptanceDomain {
		return item, fmt.Errorf("outbox item is not a policy acceptance")
	}
	if err := verifyOutboxContent(item); err != nil {
		return appendOutboxFailure(ctx.root, item, err)
	}
	if err := a.requireGovernedLaunchAccess(ctx.home, ctx.doc, ctx.root); err != nil {
		return appendOutboxFailure(ctx.root, item, err)
	}
	refreshed, err := loadPolicyContext(ctx.home, ctx.doc.ref.Name, ctx.root)
	if err != nil {
		return appendOutboxFailure(ctx.root, item, err)
	}
	ledger, ok := refreshed.mounts[refreshed.doc.doc.Governance.Attestations.Mount]
	if !ok || !samePath(ledger.LocalPath, item.RepoPath) || ledger.ID != item.Mount {
		return appendOutboxFailure(ctx.root, item, fmt.Errorf("attestation mount no longer matches queued acceptance"))
	}
	prefix := strings.Trim(filepath.ToSlash(refreshed.doc.doc.Governance.Attestations.Path), "/")
	if item.RelativePath != prefix && !strings.HasPrefix(item.RelativePath, prefix+"/") {
		return appendOutboxFailure(ctx.root, item, fmt.Errorf("queued acceptance is outside the attestation path"))
	}
	data, err := os.ReadFile(filepath.Join(item.RepoPath, filepath.FromSlash(item.RelativePath)))
	if err != nil {
		return appendOutboxFailure(ctx.root, item, err)
	}
	policyID, subjectID, eventActorID, requiresAdmin, err := validatePolicyPublicationFile(refreshed, item.RelativePath, data)
	if err != nil {
		return appendOutboxFailure(ctx.root, item, err)
	}
	actor, err := policyActor(refreshed.doc.doc, a.accessRunner)
	if err != nil {
		return appendOutboxFailure(ctx.root, item, err)
	}
	if requiresAdmin {
		decision := access.ResolveGitHub(refreshed.doc.doc.Governance.Authorization.ManifestRepository, a.accessRunner)
		if err := access.Require(decision, access.PermissionAdmin); err != nil {
			return appendOutboxFailure(ctx.root, item, fmt.Errorf("publish policy supersession: %w", err))
		}
		if eventActorID != decision.Actor.ID {
			return appendOutboxFailure(ctx.root, item, fmt.Errorf("supersession actor %d does not match current GitHub actor %d", eventActorID, decision.Actor.ID))
		}
	} else if subjectID != actor.ID {
		return appendOutboxFailure(ctx.root, item, fmt.Errorf("queued acceptance subject %d does not match current GitHub actor %d", subjectID, actor.ID))
	}
	if out, err := gitPolicyBytes(ledger.LocalPath, "fetch", "--prune", "origin"); err != nil {
		return appendOutboxFailure(ctx.root, item, fmt.Errorf("refresh attestation repository: %s", commandMessage(out, err)))
	}
	upstream, err := gitPolicyText(ledger.LocalPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return appendOutboxFailure(ctx.root, item, fmt.Errorf("attestation repository has no trusted upstream: %w", err))
	}
	baseCommit, err := gitPolicyText(ledger.LocalPath, "rev-parse", upstream+"^{commit}")
	if err != nil {
		return appendOutboxFailure(ctx.root, item, fmt.Errorf("resolve attestation upstream: %w", err))
	}
	if blob, showErr := gitPolicyBytes(ledger.LocalPath, "show", upstream+":"+item.RelativePath); showErr == nil && outbox.ContentDigest(blob) == item.ContentSHA256 {
		item.State = outbox.StateMerged
		item.MergedCommit = baseCommit
		item.Message = "exact acceptance already published at the trusted upstream; no pull request needed"
		return outbox.Append(ctx.root, item, time.Now())
	}
	result := a.publishPullRequest(ctx.home, syncer.PRRequest{
		Entry: syncer.Entry{
			Manifest: refreshed.doc.ref.Name, ID: ledger.ID, Role: "content", Kind: ledger.Kind,
			GitURL: ledger.GitURL, LocalPath: ledger.LocalPath, UmbrellaRoot: ctx.root, ContentPaths: []string{item.RelativePath},
		},
		Branch: pullRequestBaseBranch(upstream, "master"), Upstream: upstream, Head: baseCommit,
		Dirty: []string{item.RelativePath}, Message: "Update policy ledger for " + policyID,
		PreserveCheckout: true,
	}, false)
	if result.Status != "pull request opened" {
		message := strings.TrimSpace(result.Message + " " + result.Error)
		if message == "" {
			message = "isolated acceptance publication did not open a pull request"
		}
		return appendOutboxFailure(ctx.root, item, fmt.Errorf("%s", message))
	}
	if result.PRURL == "" || result.PRHeadSHA == "" || result.PRBase == "" || len(result.Changed) != 1 || filepath.ToSlash(result.Changed[0]) != item.RelativePath {
		return appendOutboxFailure(ctx.root, item, fmt.Errorf("acceptance publisher did not return exact structural PR proof"))
	}
	item.State = outbox.StateSubmitted
	item.PRURL = result.PRURL
	item.PRHeadSHA = result.PRHeadSHA
	item.PRBase = result.PRBase
	item.PublishedPaths = []string{item.RelativePath}
	item.Message = "isolated acceptance pull request verified; awaiting checks and merge"
	return outbox.Append(ctx.root, item, time.Now())
}

func policyAcceptanceRows(ctx policyContext) ([]policyAcceptanceRow, []error, error) {
	var rowIssues []error
	ledger, ok := ctx.mounts[ctx.doc.doc.Governance.Attestations.Mount]
	if !ok {
		return nil, nil, fmt.Errorf("attestation mount %q is not materialized", ctx.doc.doc.Governance.Attestations.Mount)
	}
	paths, err := listPolicyAcceptancePaths(ledger.LocalPath, ctx.doc.doc.Governance.Attestations.Path)
	if err != nil {
		return nil, nil, err
	}
	events, issues, err := outbox.ListWithIssues(ctx.root)
	if err != nil {
		return nil, nil, err
	}
	for _, issue := range issues {
		rowIssues = append(rowIssues, fmt.Errorf("outbox item %s is unreadable and stays blocked: %w", issue.ItemID, issue.Err))
	}
	byID := make(map[string]outbox.Event, len(events))
	for _, event := range events {
		byID[event.ItemID] = event
	}
	upstream := ""
	if _, fetchErr := gitPolicyBytes(ledger.LocalPath, "fetch", "--prune", "origin"); fetchErr == nil {
		upstream, _ = gitPolicyText(ledger.LocalPath, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	}
	rows := make([]policyAcceptanceRow, 0, len(paths))
	superseded := map[string]bool{}
	for _, relativePath := range paths {
		data, readErr := readRegularPolicyFile(filepath.Join(ledger.LocalPath, filepath.FromSlash(relativePath)))
		if readErr != nil {
			rowIssues = append(rowIssues, readErr)
			continue
		}
		var envelope struct {
			EventType string `json:"event_type"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.EventType == "supersession" {
			event, eventErr := validatePolicySupersessionFile(ctx, relativePath, data, false)
			if eventErr != nil {
				rowIssues = append(rowIssues, eventErr)
				continue
			}
			superseded[event.Supersedes] = true
		}
	}
	for _, relativePath := range paths {
		data, err := readRegularPolicyFile(filepath.Join(ledger.LocalPath, filepath.FromSlash(relativePath)))
		if err != nil {
			rowIssues = append(rowIssues, err)
			continue
		}
		var envelope struct {
			EventType string `json:"event_type"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.EventType == "supersession" {
			continue
		}
		attestation, err := validatePolicyAcceptanceFile(ctx, relativePath, data, false)
		if err != nil {
			rowIssues = append(rowIssues, err)
			continue
		}
		state := "local"
		prURL := ""
		itemID := outbox.ItemID(ctx.doc.doc.Organization.ID, manifest.ReservedPolicyAcceptanceDomain, ledger.ID, relativePath, outbox.ContentDigest(data))
		if event, ok := byID[itemID]; ok {
			prURL = event.PRURL
			switch event.State {
			case outbox.StateSubmitted:
				state = "submitted"
			case outbox.StateMerged:
				state = "merged"
			}
		}
		if upstream != "" {
			if mergedData, showErr := gitPolicyBytes(ledger.LocalPath, "show", upstream+":"+relativePath); showErr == nil && string(mergedData) == string(data) {
				state = "merged"
			}
		}
		if superseded[relativePath] {
			state = "superseded"
		}
		rows = append(rows, policyAcceptanceRow{
			Organization: attestation.Organization, PolicyID: attestation.PolicyID,
			PolicyVersion: attestation.PolicyVersion, PolicySHA256: attestation.PolicySHA256,
			SubjectID: attestation.SubjectID, SubjectLogin: attestation.SubjectLogin,
			AcceptedAt: attestation.AcceptedAt, ManifestCommit: attestation.ManifestCommit,
			State: state, Path: relativePath, PRURL: prURL,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].SubjectID != rows[j].SubjectID {
			return rows[i].SubjectID < rows[j].SubjectID
		}
		if rows[i].PolicyID != rows[j].PolicyID {
			return rows[i].PolicyID < rows[j].PolicyID
		}
		return rows[i].PolicySHA256 < rows[j].PolicySHA256
	})
	return rows, rowIssues, nil
}

func listPolicyAcceptancePaths(repo, prefix string) ([]string, error) {
	root := filepath.Join(repo, filepath.FromSlash(prefix))
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("policy acceptance path must not contain symlinks: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || filepath.Ext(path) != ".json" {
			return fmt.Errorf("policy acceptance ledger contains unsupported file: %s", path)
		}
		rel, ok := relativePathUnder(repo, path)
		if !ok {
			return fmt.Errorf("policy acceptance path escaped repository: %s", path)
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	sort.Strings(paths)
	return paths, err
}

func validatePolicyAcceptanceFile(ctx policyContext, relativePath string, data []byte, requireCurrentPolicy bool) (policyAttestation, error) {
	var attestation policyAttestation
	if err := json.Unmarshal(data, &attestation); err != nil {
		return policyAttestation{}, fmt.Errorf("invalid policy acceptance %s: %w", relativePath, err)
	}
	if attestation.SchemaVersion != policyAttestationSchemaVersion || attestation.Organization != ctx.doc.doc.Organization.ID ||
		attestation.SubjectProvider != "github" || attestation.SubjectID <= 0 || attestation.SubjectLogin == "" || !fullGitOID(attestation.ManifestCommit) {
		return policyAttestation{}, fmt.Errorf("policy acceptance %s has invalid schema, organization, identity, or manifest provenance", relativePath)
	}
	if _, err := time.Parse(time.RFC3339Nano, attestation.AcceptedAt); err != nil {
		return policyAttestation{}, fmt.Errorf("policy acceptance %s has invalid accepted_at", relativePath)
	}
	expected := filepath.ToSlash(filepath.Join(
		ctx.doc.doc.Governance.Attestations.Path, strconv.FormatInt(attestation.SubjectID, 10),
		attestation.PolicyID, strings.TrimPrefix(attestation.PolicySHA256, "sha256:")+".json",
	))
	if relativePath != expected {
		return policyAttestation{}, fmt.Errorf("policy acceptance path %s does not match its immutable evidence", relativePath)
	}
	if requireCurrentPolicy {
		policy, err := findGovernancePolicy(ctx.doc.doc, attestation.PolicyID)
		if err != nil || policy.Version != attestation.PolicyVersion || policy.SHA256 != attestation.PolicySHA256 {
			return policyAttestation{}, fmt.Errorf("policy acceptance %s does not match a current policy", relativePath)
		}
	}
	return attestation, nil
}

func validatePolicyPublicationFile(ctx policyContext, relativePath string, data []byte) (string, int64, int64, bool, error) {
	var envelope struct {
		EventType string `json:"event_type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return "", 0, 0, false, err
	}
	if envelope.EventType == "supersession" {
		event, err := validatePolicySupersessionFile(ctx, relativePath, data, true)
		return event.PolicyID, event.SubjectID, event.ActorID, true, err
	}
	attestation, err := validatePolicyAcceptanceFile(ctx, relativePath, data, true)
	return attestation.PolicyID, attestation.SubjectID, 0, false, err
}

func validatePolicySupersessionFile(ctx policyContext, relativePath string, data []byte, requireCurrentPolicy bool) (policySupersession, error) {
	var event policySupersession
	if err := json.Unmarshal(data, &event); err != nil {
		return policySupersession{}, fmt.Errorf("invalid policy supersession %s: %w", relativePath, err)
	}
	if event.SchemaVersion != policyAttestationSchemaVersion || event.EventType != "supersession" ||
		event.Organization != ctx.doc.doc.Organization.ID || event.SubjectProvider != "github" || event.SubjectID <= 0 ||
		event.ActorProvider != "github" || event.ActorID <= 0 || event.ActorLogin == "" || strings.TrimSpace(event.Reason) == "" ||
		!fullGitOID(event.ManifestCommit) {
		return policySupersession{}, fmt.Errorf("policy supersession %s has invalid schema, organization, identity, or manifest provenance", relativePath)
	}
	if _, err := time.Parse(time.RFC3339Nano, event.SupersededAt); err != nil {
		return policySupersession{}, fmt.Errorf("policy supersession %s has invalid superseded_at", relativePath)
	}
	policy, err := findGovernancePolicy(ctx.doc.doc, event.PolicyID)
	if err != nil || (requireCurrentPolicy && (policy.Version != event.PolicyVersion || policy.SHA256 != event.PolicySHA256)) {
		return policySupersession{}, fmt.Errorf("policy supersession %s does not match a current policy", relativePath)
	}
	if event.Supersedes != policyAttestationRelativePath(ctx.doc.doc, event.SubjectID, policy) ||
		relativePath != policySupersessionRelativePath(ctx.doc.doc, event.SubjectID, policy) {
		return policySupersession{}, fmt.Errorf("policy supersession path %s does not match its immutable evidence", relativePath)
	}
	return event, nil
}

func reconcilePolicyAcceptanceOutbox(ctx policyContext) ([]outbox.Event, []error) {
	ledger, ok := ctx.mounts[ctx.doc.doc.Governance.Attestations.Mount]
	if !ok {
		return nil, []error{fmt.Errorf("attestation mount %q is not materialized", ctx.doc.doc.Governance.Attestations.Mount)}
	}
	paths, err := listPolicyAcceptancePaths(ledger.LocalPath, ctx.doc.doc.Governance.Attestations.Path)
	if err != nil {
		return nil, []error{err}
	}
	var queued []outbox.Event
	var issues []error
	for _, relativePath := range paths {
		fullPath := filepath.Join(ledger.LocalPath, filepath.FromSlash(relativePath))
		data, err := readRegularPolicyFile(fullPath)
		if err != nil {
			issues = append(issues, err)
			continue
		}
		if _, _, _, _, err := validatePolicyPublicationFile(ctx, relativePath, data); err != nil {
			issues = append(issues, err)
			continue
		}
		needsPublication, err := recordNeedsPublication(ledger.LocalPath, fullPath)
		if err != nil {
			issues = append(issues, fmt.Errorf("inspect policy acceptance %s: %w", relativePath, err))
			continue
		}
		if !needsPublication {
			continue
		}
		event, err := ensurePolicyAcceptanceQueued(ctx, ledger, relativePath, data)
		if err != nil {
			issues = append(issues, fmt.Errorf("queue policy acceptance %s: %w", relativePath, err))
			continue
		}
		if event.State == outbox.StateQueued {
			queued = append(queued, event)
		}
	}
	return queued, issues
}

func requiredPoliciesForRole(doc manifest.Document, role string) []manifest.Policy {
	var required []manifest.Policy
	for _, policy := range doc.Governance.Policies {
		if policy.Acceptance != "required" {
			continue
		}
		if len(policy.Roles) == 0 || stringSliceContains(policy.Roles, role) {
			required = append(required, policy)
		}
	}
	return required
}

func stringSliceContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (a app) requireGovernedPolicyAcceptances(home string, doc registeredDoc, root string) error {
	if len(doc.doc.Governance.Policies) == 0 {
		return nil
	}
	role, err := selectedRoleForRoot(root)
	if err != nil {
		return err
	}
	policies := requiredPoliciesForRole(doc.doc, role)
	if len(policies) == 0 {
		return nil
	}
	actor, err := policyActorFromInventory(home, doc)
	if err != nil {
		return err
	}
	ctx, err := loadPolicyContext(home, doc.ref.Name, root)
	if err != nil {
		return err
	}
	var missing, superseded []manifest.Policy
	for _, policy := range policies {
		row, err := currentPolicyStatus(ctx, policy, actor)
		if err != nil {
			return fmt.Errorf("governed operation blocked while verifying required policy %q: %w", policy.ID, err)
		}
		switch row.Status {
		case "missing":
			missing = append(missing, policy)
		case "superseded":
			superseded = append(superseded, policy)
		}
	}
	if len(missing) == 0 && len(superseded) == 0 {
		return nil
	}
	var remediation strings.Builder
	for _, policy := range missing {
		fmt.Fprintf(&remediation, "\n  my policy show %s --manifest %s", policy.ID, doc.ref.Name)
		fmt.Fprintf(&remediation, "\n  my policy accept %s --yes --manifest %s", policy.ID, doc.ref.Name)
	}
	if len(missing) != 0 {
		fmt.Fprintf(&remediation, "\n  my record flush --manifest %s", doc.ref.Name)
		fmt.Fprintf(&remediation, "\n  my policy acceptances --manifest %s", doc.ref.Name)
	}
	for _, policy := range superseded {
		fmt.Fprintf(&remediation, "\n  policy %s: your acceptance was administratively superseded; re-running accept cannot restore it. Ask a manifest administrator; a new policy version is required before you can accept again.", policy.ID)
	}
	return fmt.Errorf("governed operation blocked: GitHub actor %d has not accepted %d required current policy document(s):%s", actor.ID, len(missing)+len(superseded), remediation.String())
}

func policyActorFromInventory(home string, doc registeredDoc) (access.Actor, error) {
	inventory, err := access.LoadInventory(home)
	if err != nil {
		return access.Actor{}, err
	}
	entry, ok := managedInventoryEntry(inventory, doc.ref.LocalPath)
	if !ok {
		return access.Actor{}, fmt.Errorf("governed operation blocked: manifest has no positive access baseline; run `my access activate --yes`")
	}
	baseline, ok := newestPositiveBaseline(entry.Baselines)
	if !ok || baseline.Actor.ID == 0 || baseline.Actor.NodeID == "" {
		return access.Actor{}, fmt.Errorf("governed operation blocked: manifest has no immutable actor baseline; run `my access activate --yes`")
	}
	return baseline.Actor, nil
}

func (a app) reviewRequiredPolicies(home string, doc registeredDoc, root string) (bool, error) {
	role, err := selectedRoleForRoot(root)
	if err != nil {
		return false, err
	}
	policies := requiredPoliciesForRole(doc.doc, role)
	if len(policies) == 0 {
		return true, nil
	}
	if !a.interactive {
		a.printRequiredPolicyCommands(home, doc, root, policies)
		return false, nil
	}
	ctx, err := loadPolicyContext(home, doc.ref.Name, root)
	if err != nil {
		return false, err
	}
	actor, err := policyActor(doc.doc, a.accessRunner)
	if err != nil {
		return false, err
	}
	for _, policy := range policies {
		row, err := currentPolicyStatus(ctx, policy, actor)
		if err != nil {
			return false, err
		}
		if row.Status != "missing" {
			continue
		}
		content, _, err := verifiedPolicyBlob(ctx, policy)
		if err != nil {
			return false, err
		}
		fmt.Fprintf(a.stdout, "\nRequired policy: %s (%s, version %s)\n", policy.Title, policy.ID, policy.Version)
		fmt.Fprintln(a.stdout, strings.Repeat("-", 72))
		if _, err := a.stdout.Write(content); err != nil {
			return false, err
		}
		if len(content) == 0 || content[len(content)-1] != '\n' {
			fmt.Fprintln(a.stdout)
		}
		fmt.Fprintln(a.stdout, strings.Repeat("-", 72))
		accepted, answered, err := a.promptConfirm("Accept this exact policy document?", false)
		if err != nil {
			return false, err
		}
		if !answered || !accepted {
			reason := "no input received"
			if answered {
				reason = "acceptance declined"
			}
			fmt.Fprintf(a.stdout, "Policy onboarding incomplete (%s).\n", reason)
			a.printRequiredPolicyCommands(home, doc, root, []manifest.Policy{policy})
			return false, nil
		}
		args := append([]string{policy.ID, "--yes"}, policyCommandFlags(home, doc.ref.Name, root)...)
		if err := a.runPolicyAccept(args); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (a app) printRequiredPolicyCommands(home string, doc registeredDoc, root string, policies []manifest.Policy) {
	fmt.Fprintln(a.stdout, "Required policy acceptance is incomplete. Review and accept each exact document:")
	flags := policyCommandFlags(home, doc.ref.Name, root)
	for _, policy := range policies {
		fmt.Fprintf(a.stdout, "  %s\n", shellCommandLine("", "my", append([]string{"policy", "show", policy.ID}, flags...)))
		fmt.Fprintf(a.stdout, "  %s\n", shellCommandLine("", "my", append([]string{"policy", "accept", policy.ID, "--yes"}, flags...)))
	}
	fmt.Fprintf(a.stdout, "  %s\n", shellCommandLine("", "my", append([]string{"record", "flush"}, flags...)))
	fmt.Fprintf(a.stdout, "  %s\n", shellCommandLine("", "my", append([]string{"policy", "acceptances"}, flags...)))
}

func policyCommandFlags(home, manifestName, root string) []string {
	args := []string{"--manifest", manifestName, "--umbrella", root}
	if home != "" {
		args = append(args, "--home", home)
	}
	return args
}

func samePolicyAcceptance(left, right policyAttestation) bool {
	return left.SchemaVersion == right.SchemaVersion &&
		left.Organization == right.Organization && left.PolicyID == right.PolicyID &&
		left.PolicyVersion == right.PolicyVersion && left.PolicySHA256 == right.PolicySHA256 &&
		left.SubjectProvider == right.SubjectProvider && left.SubjectID == right.SubjectID
}

func samePolicySupersession(left, right policySupersession) bool {
	return left.SchemaVersion == right.SchemaVersion && left.EventType == right.EventType &&
		left.Organization == right.Organization && left.PolicyID == right.PolicyID &&
		left.PolicyVersion == right.PolicyVersion && left.PolicySHA256 == right.PolicySHA256 &&
		left.SubjectProvider == right.SubjectProvider && left.SubjectID == right.SubjectID &&
		left.Supersedes == right.Supersedes && left.ActorProvider == right.ActorProvider &&
		left.ActorID == right.ActorID && left.Reason == right.Reason
}

func policyAttestationRelativePath(doc manifest.Document, subjectID int64, policy manifest.Policy) string {
	return filepath.ToSlash(filepath.Join(
		doc.Governance.Attestations.Path, strconv.FormatInt(subjectID, 10), policy.ID,
		strings.TrimPrefix(policy.SHA256, "sha256:")+".json",
	))
}

func policySupersessionRelativePath(doc manifest.Document, subjectID int64, policy manifest.Policy) string {
	return filepath.ToSlash(filepath.Join(
		doc.Governance.Attestations.Path, "supersessions", strconv.FormatInt(subjectID, 10), policy.ID,
		strings.TrimPrefix(policy.SHA256, "sha256:")+".json",
	))
}

func readRegularPolicyFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("policy attestation must be a regular file, not a symlink or special file: %s", path)
	}
	return os.ReadFile(path)
}

func ensurePolicyParent(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootInfo, err := os.Lstat(rootAbs)
	if err != nil {
		return err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return fmt.Errorf("attestation mount must be a real directory: %s", rootAbs)
	}
	parent := filepath.Dir(target)
	rel, err := filepath.Rel(rootAbs, parent)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("attestation path escapes mount: %s", target)
	}
	current := rootAbs
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, statErr := os.Lstat(current)
			if errors.Is(statErr, os.ErrNotExist) {
				if mkdirErr := os.Mkdir(current, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
					return mkdirErr
				}
				info, statErr = os.Lstat(current)
			}
			if statErr != nil {
				return statErr
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("attestation path traverses symlink or non-directory: %s", current)
			}
		}
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return err
	}
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	realRel, err := filepath.Rel(rootReal, parentReal)
	if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("attestation path resolves outside mount: %s", target)
	}
	return nil
}

func gitPolicyText(repo string, args ...string) (string, error) {
	out, err := gitPolicyBytes(repo, args...)
	return strings.TrimSpace(string(out)), err
}

func gitPolicyBytes(repo string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), monitorCommandMessage(out, err))
	}
	return out, nil
}
