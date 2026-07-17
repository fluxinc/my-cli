package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/access"
	"github.com/fluxinc/my-cli/internal/manifest"
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
	default:
		return fmt.Errorf("unknown policy subcommand %q; supported subcommands are list, show, status, and accept", args[0])
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
	}
	if _, err := gitPolicyBytes(ledger.LocalPath, "add", "-N", "--", relativePath); err != nil {
		return fmt.Errorf("mark attestation intent-to-add: %w", err)
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
	return row, nil
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
	var missing []manifest.Policy
	for _, policy := range policies {
		row, err := currentPolicyStatus(ctx, policy, actor)
		if err != nil {
			return fmt.Errorf("governed operation blocked while verifying required policy %q: %w", policy.ID, err)
		}
		if row.Status == "missing" {
			missing = append(missing, policy)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var remediation strings.Builder
	for _, policy := range missing {
		fmt.Fprintf(&remediation, "\n  my policy show %s --manifest %s", policy.ID, doc.ref.Name)
		fmt.Fprintf(&remediation, "\n  my policy accept %s --yes --manifest %s", policy.ID, doc.ref.Name)
	}
	return fmt.Errorf("governed operation blocked: GitHub actor %d has not accepted %d required current policy document(s); review and accept each exact document:%s", actor.ID, len(missing), remediation.String())
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
