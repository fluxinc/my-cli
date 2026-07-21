package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/syncer"
	"github.com/fluxinc/my-cli/internal/workspace"
)

type adminPolicyOpts struct {
	manifestDir  string
	home         string
	manifestName string
	umbrellaRoot string
	title        string
	mount        string
	path         string
	version      string
	sha256       string
	acceptance   string
	roles        stringListFlag
	force        bool
	jsonOut      bool
}

type adminPolicyResult struct {
	Action       string          `json:"action"`
	Policy       manifest.Policy `json:"policy"`
	ManifestPath string          `json:"manifest_path"`
	Message      string          `json:"message,omitempty"`
	NextCommands []string        `json:"next_commands,omitempty"`
	Publication  string          `json:"publication,omitempty"`
	PRURL        string          `json:"pr_url,omitempty"`
	PRHeadSHA    string          `json:"pr_head_sha,omitempty"`
}

func (a app) runAdminPolicy(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin policy subcommand")
	}
	switch args[0] {
	case "add":
		return a.runAdminPolicyAdd(args[1:])
	case "remove":
		return a.runAdminPolicyRemove(args[1:])
	case "list", "show", "status", "accept", "acceptances":
		return adminOperationalReadError("policy", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin policy subcommand %q", args[0])
	}
}

func (a app) runAdminPolicyAdd(args []string) error {
	opts, rest, err := parseAdminPolicyOpts("my admin policy add", a.stderr, args, true)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my admin policy add <id> --title TEXT --mount ID --path PATH --version VERSION --acceptance required|optional [--role ID] [--manifest NAME --umbrella DIR | --manifest-dir DIR --sha256 sha256:HEX]")
	}
	if err := validateAdminPolicyAddOpts(opts); err != nil {
		return err
	}
	var result adminPolicyResult
	if opts.manifestDir != "" {
		if opts.home != "" || opts.manifestName != "" || opts.umbrellaRoot != "" {
			return fmt.Errorf("--manifest-dir cannot be combined with --home, --manifest, or --umbrella")
		}
		result, err = a.adminPolicyExplicit("add", rest[0], opts)
	} else {
		if opts.sha256 != "" {
			return fmt.Errorf("--sha256 is only available with --manifest-dir; registered authoring hashes the committed policy blob")
		}
		result, err = a.adminPolicyRegistered("add", rest[0], opts)
	}
	if err != nil {
		return err
	}
	return a.printAdminPolicyResult(result, opts.jsonOut)
}

func (a app) runAdminPolicyRemove(args []string) error {
	opts, rest, err := parseAdminPolicyOpts("my admin policy remove", a.stderr, args, false)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my admin policy remove <id> [--manifest NAME --umbrella DIR | --manifest-dir DIR]")
	}
	if opts.manifestDir != "" {
		if opts.home != "" || opts.manifestName != "" || opts.umbrellaRoot != "" {
			return fmt.Errorf("--manifest-dir cannot be combined with --home, --manifest, or --umbrella")
		}
		result, err := a.adminPolicyExplicit("remove", rest[0], opts)
		if err != nil {
			return err
		}
		return a.printAdminPolicyResult(result, opts.jsonOut)
	}
	if opts.force {
		return fmt.Errorf("--force is only available with the compatibility --manifest-dir path")
	}
	result, err := a.adminPolicyRegistered("remove", rest[0], opts)
	if err != nil {
		return err
	}
	return a.printAdminPolicyResult(result, opts.jsonOut)
}

func parseAdminPolicyOpts(name string, stderr io.Writer, args []string, add bool) (opts adminPolicyOpts, rest []string, err error) {
	fs := newFlagSet(name, stderr)
	fs.StringVar(&opts.manifestDir, "manifest-dir", "", "compatibility: explicit maintainer manifest checkout")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use a registered manifest")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.BoolVar(&opts.force, "force", false, "allow dirty explicit --manifest-dir checkout")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON result")
	known := map[string]bool{"manifest-dir": true, "home": true, "manifest": true, "umbrella": true}
	if add {
		fs.StringVar(&opts.title, "title", "", "human-readable policy title")
		fs.StringVar(&opts.mount, "mount", "", "manifest mount containing the policy")
		fs.StringVar(&opts.path, "path", "", "policy path relative to its mount")
		fs.StringVar(&opts.version, "version", "", "policy version")
		fs.StringVar(&opts.sha256, "sha256", "", "explicit sha256: digest for --manifest-dir compatibility")
		fs.StringVar(&opts.acceptance, "acceptance", "", "required or optional")
		fs.Var(&opts.roles, "role", "selected local role requiring the policy (repeatable)")
		for _, key := range []string{"title", "mount", "path", "version", "sha256", "acceptance", "role"} {
			known[key] = true
		}
	}
	rest, err = parseInterspersed(fs, args, known)
	return opts, rest, err
}

func validateAdminPolicyAddOpts(opts adminPolicyOpts) error {
	for name, value := range map[string]string{
		"--title": opts.title, "--mount": opts.mount, "--path": opts.path,
		"--version": opts.version, "--acceptance": opts.acceptance,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if opts.manifestDir != "" && strings.TrimSpace(opts.sha256) == "" {
		return fmt.Errorf("--sha256 is required with --manifest-dir because no registered mounted policy blob is available")
	}
	if opts.manifestDir == "" && opts.force {
		return fmt.Errorf("--force is only available with the compatibility --manifest-dir path")
	}
	return nil
}

func (a app) adminPolicyRegistered(action, id string, opts adminPolicyOpts) (adminPolicyResult, error) {
	name, err := defaultManifestName(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return adminPolicyResult{}, err
	}
	docRef, err := loadSingleRegisteredDoc(opts.home, name)
	if err != nil {
		return adminPolicyResult{}, err
	}
	doc, manifestPath, repo, err := a.loadAuthorizedAdminManifestCheckout(docRef.ref.LocalPath)
	if err != nil {
		return adminPolicyResult{}, err
	}
	if err := ensureAdminManifestClean(repo, false); err != nil {
		return adminPolicyResult{}, fmt.Errorf("registered manifest cache must remain clean: %w", err)
	}
	root, err := resolveMyRoot(opts.home, name, opts.umbrellaRoot)
	if err != nil {
		return adminPolicyResult{}, err
	}
	if out, err := gitPRBytes(repo, nil, "fetch", "--prune", "origin"); err != nil {
		return adminPolicyResult{}, fmt.Errorf("refresh registered manifest: %s", commandMessage(out, err))
	}
	branch, upstream, head, err := trustedRegisteredManifestRefs(repo)
	if err != nil {
		return adminPolicyResult{}, err
	}
	trusted, err := gitPRText(repo, nil, "rev-parse", upstream+"^{commit}")
	if err != nil {
		return adminPolicyResult{}, err
	}
	if head != trusted {
		return adminPolicyResult{}, fmt.Errorf("registered manifest is not at its trusted upstream; run my manifests sync %s before authoring", name)
	}

	var changed manifest.Policy
	switch action {
	case "add":
		if _, ok := adminPolicyIndex(doc.Governance.Policies, id); ok {
			return adminPolicyResult{}, fmt.Errorf("governance policy %q already exists", id)
		}
		digest, err := committedRegisteredPolicyDigest(opts.home, name, root, doc, opts.mount, opts.path)
		if err != nil {
			return adminPolicyResult{}, err
		}
		changed = manifest.Policy{ID: strings.TrimSpace(id), Title: strings.TrimSpace(opts.title), Mount: strings.TrimSpace(opts.mount), Path: filepath.ToSlash(strings.TrimSpace(opts.path)), Version: strings.TrimSpace(opts.version), SHA256: digest, Acceptance: strings.TrimSpace(opts.acceptance), Roles: append([]string(nil), opts.roles...)}
		doc.Governance.Policies = append(doc.Governance.Policies, changed)
	case "remove":
		idx, ok := adminPolicyIndex(doc.Governance.Policies, id)
		if !ok {
			return adminPolicyResult{}, fmt.Errorf("unknown governance policy %q", id)
		}
		changed = doc.Governance.Policies[idx]
		doc.Governance.Policies = append(doc.Governance.Policies[:idx], doc.Governance.Policies[idx+1:]...)
		if len(doc.Governance.Policies) == 0 {
			doc.Governance.Policies = nil
		}
	default:
		return adminPolicyResult{}, fmt.Errorf("unknown policy action %q", action)
	}
	if validation := manifest.ValidateDocument(repo, doc); len(validation.Errors) != 0 {
		return adminPolicyResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return adminPolicyResult{}, err
	}
	data = append(data, '\n')
	result := a.publishPullRequest(opts.home, syncer.PRRequest{
		Entry: syncer.Entry{
			Manifest: name, ID: name, Role: "manifest", Kind: "manifest",
			GitURL: docRef.ref.GitURL, LocalPath: repo, UmbrellaRoot: root,
			ContentPaths: manifestControlPaths(),
		},
		Branch: branch, Upstream: upstream, Head: head, Dirty: []string{"manifest.json"},
		FileContents: map[string][]byte{"manifest.json": data},
		Message:      "Update governance policy", PreserveCheckout: true,
	}, false)
	if result.Status != "pull request opened" {
		message := result.Message
		if message == "" {
			message = result.Error
		}
		return adminPolicyResult{}, fmt.Errorf("policy change was not published (%s): %s", result.ReasonCode, message)
	}
	verb := "added"
	if action == "remove" {
		verb = "removed"
	}
	next := []string{}
	if result.NextCommand != "" {
		next = append(next, result.NextCommand)
	}
	return adminPolicyResult{Action: verb, Policy: changed, ManifestPath: manifestPath, Message: "policy change proposed without modifying the sync-managed manifest cache", NextCommands: next, Publication: result.Status, PRURL: result.PRURL, PRHeadSHA: result.PRHeadSHA}, nil
}

func trustedRegisteredManifestRefs(repo string) (branch, upstream, head string, err error) {
	branch, err = gitPRText(repo, nil, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("registered manifest must be on a branch: %w", err)
	}
	upstream, err = gitPRText(repo, nil, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return "", "", "", fmt.Errorf("registered manifest has no trusted upstream: %w", err)
	}
	head, err = gitPRText(repo, nil, "rev-parse", "HEAD^{commit}")
	return branch, upstream, head, err
}

func committedRegisteredPolicyDigest(home, name, root string, doc manifest.Document, mountID, policyPath string) (string, error) {
	var declared bool
	for _, mount := range manifest.EffectiveMounts(doc) {
		if mount.ID == mountID {
			declared = true
			break
		}
	}
	if !declared {
		return "", fmt.Errorf("policy mount %q is not declared", mountID)
	}
	entries, err := workspace.ListMounts(home, name, root)
	if err != nil {
		return "", err
	}
	var local string
	for _, entry := range entries {
		if entry.ID == mountID {
			local = entry.LocalPath
			break
		}
	}
	if local == "" {
		return "", fmt.Errorf("policy mount %q is not materialized", mountID)
	}
	if out, err := gitPRBytes(local, nil, "fetch", "--prune", "origin"); err != nil {
		return "", fmt.Errorf("refresh policy mount %q: %s", mountID, commandMessage(out, err))
	}
	upstream, err := gitPRText(local, nil, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return "", fmt.Errorf("policy mount %q has no trusted upstream: %w", mountID, err)
	}
	path := filepath.ToSlash(strings.TrimSpace(policyPath))
	data, err := gitPRBytes(local, nil, "show", upstream+":"+path)
	if err != nil {
		return "", fmt.Errorf("read committed policy %s:%s: %s", mountID, path, commandMessage(data, err))
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func (a app) adminPolicyExplicit(action, id string, opts adminPolicyOpts) (adminPolicyResult, error) {
	doc, manifestPath, root, err := a.loadAuthorizedAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminPolicyResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminPolicyResult{}, err
	}
	var changed manifest.Policy
	switch action {
	case "add":
		if _, ok := adminPolicyIndex(doc.Governance.Policies, id); ok {
			return adminPolicyResult{}, fmt.Errorf("governance policy %q already exists", id)
		}
		changed = manifest.Policy{ID: strings.TrimSpace(id), Title: strings.TrimSpace(opts.title), Mount: strings.TrimSpace(opts.mount), Path: filepath.ToSlash(strings.TrimSpace(opts.path)), Version: strings.TrimSpace(opts.version), SHA256: strings.TrimSpace(opts.sha256), Acceptance: strings.TrimSpace(opts.acceptance), Roles: append([]string(nil), opts.roles...)}
		doc.Governance.Policies = append(doc.Governance.Policies, changed)
	case "remove":
		idx, ok := adminPolicyIndex(doc.Governance.Policies, id)
		if !ok {
			return adminPolicyResult{}, fmt.Errorf("unknown governance policy %q", id)
		}
		changed = doc.Governance.Policies[idx]
		doc.Governance.Policies = append(doc.Governance.Policies[:idx], doc.Governance.Policies[idx+1:]...)
		if len(doc.Governance.Policies) == 0 {
			doc.Governance.Policies = nil
		}
	default:
		return adminPolicyResult{}, fmt.Errorf("unknown policy action %q", action)
	}
	if validation := manifest.ValidateDocument(root, doc); len(validation.Errors) != 0 {
		return adminPolicyResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminPolicyResult{}, err
	}
	verb := "added"
	if action == "remove" {
		verb = "removed"
	}
	return adminPolicyResult{Action: verb, Policy: changed, ManifestPath: manifestPath, Message: verb + " governance policy", NextCommands: adminNextCommands(root)}, nil
}

func adminPolicyIndex(policies []manifest.Policy, id string) (int, bool) {
	id = strings.TrimSpace(id)
	for i, policy := range policies {
		if policy.ID == id {
			return i, true
		}
	}
	return -1, false
}

func (a app) printAdminPolicyResult(result adminPolicyResult, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", result.Action, result.Policy.ID, result.Policy.Version, result.Policy.SHA256)
	if result.Message != "" {
		fmt.Fprintln(a.stdout, result.Message)
	}
	printAdminNextCommands(a.stdout, result.NextCommands)
	return nil
}
