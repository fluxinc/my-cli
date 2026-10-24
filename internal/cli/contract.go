package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/syncer"
)

// contractEntry is one organization contract rule with its manifest and
// 1-based position, the handle `my admin contract remove|replace` accepts.
type contractEntry struct {
	Manifest string `json:"manifest"`
	Index    int    `json:"index"`
	Heading  string `json:"heading,omitempty"`
	Rule     string `json:"rule"`
}

type contractShow struct {
	Manifest string                     `json:"manifest"`
	Source   string                     `json:"source"`
	File     string                     `json:"file,omitempty"`
	Blob     string                     `json:"blob,omitempty"`
	Sections []manifest.ContractSection `json:"sections"`
	text     []byte
}

type adminContractResult struct {
	Action       string   `json:"action"`
	Rule         string   `json:"rule,omitempty"`
	Previous     string   `json:"previous,omitempty"`
	Source       string   `json:"source"`
	ManifestPath string   `json:"manifest_path"`
	ContractFile string   `json:"contract_file,omitempty"`
	BaseCommit   string   `json:"base_commit,omitempty"`
	BaseBlob     string   `json:"base_blob,omitempty"`
	Blob         string   `json:"blob,omitempty"`
	MinMyVersion string   `json:"min_my_version,omitempty"`
	Contract     []string `json:"contract"`
	Message      string   `json:"message,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	NextCommands []string `json:"next_commands,omitempty"`
	Publication  string   `json:"publication,omitempty"`
	PRURL        string   `json:"pr_url,omitempty"`
	PRHeadSHA    string   `json:"pr_head_sha,omitempty"`
}

type adminContractOpts struct {
	manifestDir  string
	home         string
	manifestName string
	umbrellaRoot string
	under        string
	expectBlob   string
	force        bool
	jsonOut      bool
}

// contractEdit is one planned contract change: the files it writes, relative
// to the manifest root, and what the result reports.
type contractEdit struct {
	result   adminContractResult
	files    map[string][]byte
	creates  string
	contract manifest.Contract
	// originals holds every touched file's bytes at planning time, so a local
	// write refuses if the checkout changed underneath the edit.
	originals map[string][]byte
}

func (a app) runContract(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing contract subcommand")
	}
	switch args[0] {
	case "list":
		return a.runContractList(args[1:])
	case "show":
		return a.runContractShow(args[1:])
	case "add", "remove", "replace", "migrate":
		return fmt.Errorf("my contract %s edits the manifest; use my admin contract %s", args[0], args[0])
	case "-h", "--help", "help":
		a.printContractUsage()
		return nil
	default:
		return fmt.Errorf("unknown contract subcommand %q", args[0])
	}
}

func (a app) printContractUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my contract list [--manifest NAME] [--home DIR] [--json]
  my contract show [--manifest NAME] [--home DIR] [--json]

The organization contract is the manifest's list of short, binding rules,
rendered into generated AGENTS.md. It lives inline in manifest.json or, once
migrated, in a contract_file such as guidance/contract.md with one "- rule" per
line. Edit it with my admin contract add|remove|replace|migrate.`)
}

func (a app) runContractList(args []string) error {
	var home, manifestName string
	var jsonOut bool
	fs := newFlagSet("my contract list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	var noRefresh bool
	fs.BoolVar(&noRefresh, "no-refresh", false, "skip best-effort auto-refresh")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	a.maybeAutoRefreshReads(home, manifestName, noRefresh)
	if len(rest) != 0 {
		return fmt.Errorf("usage: my contract list")
	}
	entries, err := loadManifestContracts(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if jsonOut {
		return printJSON(a.stdout, entries)
	}
	for _, entry := range entries {
		fmt.Fprintf(a.stdout, "%s\t%d\t%s\n", entry.Manifest, entry.Index, entry.Rule)
	}
	return nil
}

func (a app) runContractShow(args []string) error {
	var home, manifestName string
	var jsonOut, noRefresh bool
	fs := newFlagSet("my contract show", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "select a registered manifest")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.BoolVar(&noRefresh, "no-refresh", false, "skip best-effort auto-refresh")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
	a.maybeAutoRefreshReads(home, manifestName, noRefresh)
	if len(rest) != 0 {
		return fmt.Errorf("usage: my contract show")
	}
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	shows := []contractShow{}
	for _, doc := range docs {
		contract, err := manifest.LoadContract(doc.ref.LocalPath, doc.doc)
		if err != nil {
			return a.maybeJSONError(jsonOut, fmt.Errorf("manifest %q: %w", doc.ref.Name, err))
		}
		show := contractShow{
			Manifest: doc.ref.Name, Source: contract.Source(), File: contract.File, Blob: contract.Blob,
			Sections: contract.Sections(),
		}
		if show.Sections == nil {
			show.Sections = []manifest.ContractSection{}
		}
		if contract.File != "" {
			show.text = contract.Bytes()
		} else {
			for _, rule := range contract.Rules() {
				show.text = append(show.text, "- "+rule+"\n"...)
			}
		}
		shows = append(shows, show)
	}
	if jsonOut {
		return printJSON(a.stdout, shows)
	}
	for i, show := range shows {
		if len(shows) > 1 {
			if i > 0 {
				fmt.Fprintln(a.stdout)
			}
			fmt.Fprintf(a.stdout, "==> %s (%s)\n", show.Manifest, contractSourceLabel(show.Source, show.File))
		}
		if _, err := a.stdout.Write(show.text); err != nil {
			return err
		}
	}
	return nil
}

func contractSourceLabel(source, file string) string {
	if file != "" {
		return file
	}
	return "inline " + source
}

func loadManifestContracts(home, manifestName string) ([]contractEntry, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	entries := []contractEntry{}
	for _, doc := range docs {
		contract, err := manifest.LoadContract(doc.ref.LocalPath, doc.doc)
		if err != nil {
			return nil, fmt.Errorf("manifest %q: %w", doc.ref.Name, err)
		}
		for _, entry := range contract.Entries() {
			entries = append(entries, contractEntry{
				Manifest: doc.ref.Name,
				Index:    entry.Index,
				Heading:  entry.Heading,
				Rule:     entry.Rule,
			})
		}
	}
	return entries, nil
}

func (a app) runAdminContract(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing admin contract subcommand")
	}
	switch args[0] {
	case "add", "remove", "replace", "migrate":
		return a.runAdminContractEdit(args[0], args[1:])
	case "list", "show":
		return adminOperationalReadError("contract", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin contract subcommand %q", args[0])
	}
}

var adminContractUsage = map[string]string{
	"add":     `usage: my admin contract add "RULE TEXT" [--under HEADING] [--manifest NAME --umbrella DIR | --manifest-dir DIR]`,
	"remove":  `usage: my admin contract remove <index|"RULE TEXT"> [--manifest NAME --umbrella DIR | --manifest-dir DIR]`,
	"replace": `usage: my admin contract replace <index|"OLD TEXT"> "NEW TEXT" [--manifest NAME --umbrella DIR | --manifest-dir DIR]`,
	"migrate": `usage: my admin contract migrate [--manifest NAME --umbrella DIR | --manifest-dir DIR]`,
}

var adminContractArgs = map[string]int{"add": 1, "remove": 1, "replace": 2, "migrate": 0}

func (a app) runAdminContractEdit(action string, args []string) error {
	opts, rest, err := parseAdminContractOpts("my admin contract "+action, a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != adminContractArgs[action] {
		return a.maybeJSONError(opts.jsonOut, errors.New(adminContractUsage[action]))
	}
	if opts.under != "" && action != "add" {
		return a.maybeJSONError(opts.jsonOut, fmt.Errorf("--under is only valid with my admin contract add"))
	}
	if opts.expectBlob != "" && action == "migrate" {
		return a.maybeJSONError(opts.jsonOut, fmt.Errorf("--expect-blob names a contract_file revision; migrate creates the file"))
	}
	var result adminContractResult
	if opts.manifestDir != "" {
		if opts.home != "" || opts.manifestName != "" || opts.umbrellaRoot != "" {
			return a.maybeJSONError(opts.jsonOut, fmt.Errorf("--manifest-dir cannot be combined with --home, --manifest, or --umbrella"))
		}
		result, err = a.adminContractLocal(action, rest, opts)
	} else {
		result, err = a.adminContractRegistered(action, rest, opts)
	}
	if err != nil {
		return a.maybeJSONError(opts.jsonOut, err)
	}
	return a.printAdminContractResult(result, opts.jsonOut)
}

func parseAdminContractOpts(name string, stderr io.Writer, args []string) (opts adminContractOpts, rest []string, err error) {
	fs := newFlagSet(name, stderr)
	fs.StringVar(&opts.manifestDir, "manifest-dir", "", "compatibility: explicit maintainer manifest checkout")
	fs.StringVar(&opts.home, "home", "", "override home directory")
	fs.StringVar(&opts.manifestName, "manifest", "", "use a registered manifest")
	fs.StringVar(&opts.umbrellaRoot, "umbrella", "", "override umbrella root")
	fs.StringVar(&opts.under, "under", "", "add the rule under this contract_file heading")
	fs.StringVar(&opts.expectBlob, "expect-blob", "", "refuse unless the contract_file is at this Git blob")
	fs.BoolVar(&opts.force, "force", false, "allow dirty explicit --manifest-dir checkout")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON result")
	rest, err = parseInterspersed(fs, args, map[string]bool{
		"manifest-dir": true, "home": true, "manifest": true, "umbrella": true, "under": true, "expect-blob": true,
	})
	return opts, rest, err
}

// planContractEdit computes one contract change against the manifest checkout
// at root without writing anything. It validates the prospective manifest and
// contract file before any caller writes or publishes them.
func planContractEdit(action string, args []string, opts adminContractOpts, root, manifestPath string, doc manifest.Document) (contractEdit, error) {
	current, err := manifest.LoadContract(root, doc)
	if err != nil {
		return contractEdit{}, err
	}
	if opts.expectBlob != "" {
		if current.File == "" {
			return contractEdit{}, fmt.Errorf("--expect-blob needs a contract_file; this manifest keeps its contract inline")
		}
		if current.Blob != strings.TrimSpace(opts.expectBlob) {
			return contractEdit{}, fmt.Errorf("%s is at blob %s, not the expected %s; it changed since you read it, so run my contract list and retry", current.File, current.Blob, opts.expectBlob)
		}
	}
	edit := contractEdit{files: map[string][]byte{}, originals: map[string][]byte{}}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return contractEdit{}, err
	}
	edit.originals["manifest.json"] = raw
	edit.result = adminContractResult{Source: current.Source(), ManifestPath: manifestPath, ContractFile: current.File, BaseBlob: current.Blob}
	if action == "migrate" {
		return planContractMigrate(edit, root, manifestPath, doc)
	}
	next := current
	switch action {
	case "add":
		next, err = current.AddRule(args[0], opts.under)
		edit.result.Action, edit.result.Rule = "added", strings.TrimSpace(args[0])
	case "remove":
		var idx int
		if idx, err = current.ResolveContractTarget(args[0]); err == nil {
			next, edit.result.Rule, err = current.RemoveRule(idx)
		}
		edit.result.Action = "removed"
	case "replace":
		var idx int
		if idx, err = current.ResolveContractTarget(args[0]); err == nil {
			next, edit.result.Previous, err = current.ReplaceRule(idx, args[1])
		}
		edit.result.Action, edit.result.Rule = "replaced", strings.TrimSpace(args[1])
	default:
		return contractEdit{}, fmt.Errorf("unknown contract action %q", action)
	}
	if err != nil {
		return contractEdit{}, err
	}
	if next.File != "" {
		data := next.Bytes()
		if _, err := manifest.ParseContractFile(next.File, data); err != nil {
			return contractEdit{}, fmt.Errorf("updated contract is invalid: %w", err)
		}
		edit.files[next.File] = data
		edit.originals[next.File] = current.Bytes()
		edit.result.Blob = next.Blob
		// A hand-written contract_file may predate its feature floor; stamp
		// it with a targeted patch so older readers ask for an upgrade.
		raised := doc
		if manifest.RaiseMinMyVersion(&raised) {
			value, _ := json.Marshal(raised.MinMyVersion)
			patched, err := manifest.PatchManifestKeys(raw, []manifest.KeyEdit{{Key: "min_my_version", Value: value, After: "manifest_version"}})
			if err != nil {
				return contractEdit{}, fmt.Errorf("%s: %w", manifestPath, err)
			}
			edit.files["manifest.json"] = patched
			edit.result.MinMyVersion = raised.MinMyVersion
			doc = raised
		}
		if validation := manifest.ValidateDocument(root, doc); len(validation.Errors) != 0 {
			return contractEdit{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
		}
	} else {
		doc.Contract = next.Rules()
		if validation := manifest.ValidateDocument(root, doc); len(validation.Errors) != 0 {
			return contractEdit{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
		}
		data, err := manifest.EncodeDocument(doc)
		if err != nil {
			return contractEdit{}, err
		}
		edit.files["manifest.json"] = data
	}
	edit.contract = next
	edit.result.Contract = next.Rules()
	return edit, nil
}

// planContractMigrate moves the inline contract into guidance/contract.md. The
// manifest is patched key by key, so unknown keys and key order survive, and
// the rendered AGENTS.md is unchanged.
func planContractMigrate(edit contractEdit, root, manifestPath string, doc manifest.Document) (contractEdit, error) {
	if doc.ContractFile != "" {
		return contractEdit{}, fmt.Errorf("contract already lives in %s", doc.ContractFile)
	}
	file := manifest.DefaultContractFile
	if _, err := manifest.ContractFilePath(root, file); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return contractEdit{}, err
		}
		return contractEdit{}, fmt.Errorf("%s already exists; move it aside or merge it into the manifest contract before migrating", file)
	}
	contract := manifest.NewContractFile(file, doc.Contract)
	contractData := contract.Bytes()
	if _, err := manifest.ParseContractFile(file, contractData); err != nil {
		return contractEdit{}, fmt.Errorf("migrated contract is invalid: %w", err)
	}
	raw := edit.originals["manifest.json"]
	migrated := doc
	migrated.Contract = nil
	migrated.ContractFile = file
	manifest.RaiseMinMyVersion(&migrated)
	fileValue, _ := json.Marshal(file)
	edits := []manifest.KeyEdit{
		{Key: "contract"},
		{Key: "contract_file", Value: fileValue, After: "contract"},
	}
	if migrated.MinMyVersion != doc.MinMyVersion {
		value, _ := json.Marshal(migrated.MinMyVersion)
		edits = append(edits, manifest.KeyEdit{Key: "min_my_version", Value: value, After: "manifest_version"})
	}
	manifestData, err := manifest.PatchManifestKeys(raw, edits)
	if err != nil {
		return contractEdit{}, fmt.Errorf("%s: %w", manifestPath, err)
	}
	var patched manifest.Document
	if err := json.Unmarshal(manifestData, &patched); err != nil {
		return contractEdit{}, fmt.Errorf("migrated manifest is invalid JSON: %w", err)
	}
	// The contract file does not exist yet, so validate the manifest without
	// reading it and check the file content on its own.
	if validation := manifest.ValidateDocument("", patched); len(validation.Errors) != 0 {
		return contractEdit{}, fmt.Errorf("migrated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
	}
	inline, _ := manifest.LoadContract(root, doc)
	if strings.Join(contract.Rules(), "\n") != strings.Join(inline.Rules(), "\n") {
		return contractEdit{}, fmt.Errorf("migrated contract does not match the inline rules")
	}
	edit.files[file] = contractData
	edit.files["manifest.json"] = manifestData
	edit.creates = file
	edit.contract = contract
	edit.result.Action = "migrated"
	edit.result.ContractFile = file
	edit.result.Blob = contract.Blob
	edit.result.MinMyVersion = patched.MinMyVersion
	edit.result.Contract = contract.Rules()
	edit.result.Warnings = append(edit.result.Warnings, fmt.Sprintf(
		"my releases before 0.46.0 ignore contract_file and min_my_version and would launch without the contract; confirm every member runs my v0.46.0 or later (my update) before merging. v0.46.x asks for v%s.",
		patched.MinMyVersion))
	return edit, nil
}

func (a app) adminContractLocal(action string, args []string, opts adminContractOpts) (adminContractResult, error) {
	doc, manifestPath, root, err := a.loadAuthorizedAdminManifestCheckout(opts.manifestDir)
	if err != nil {
		return adminContractResult{}, err
	}
	if err := ensureAdminManifestClean(root, opts.force); err != nil {
		return adminContractResult{}, err
	}
	head, _ := gitPRText(root, nil, "rev-parse", "HEAD^{commit}")
	edit, err := planContractEdit(action, args, opts, root, manifestPath, doc)
	if err != nil {
		return adminContractResult{}, err
	}
	edit.result.BaseCommit = head
	if err := writeContractEdit(root, edit); err != nil {
		return adminContractResult{}, err
	}
	edit.result.Message = "contract updated in the checkout; it renders in generated AGENTS.md after the change merges and members sync"
	edit.result.NextCommands = []string{
		"git -C " + shellQuote(root) + " status --short",
		"git -C " + shellQuote(root) + " diff -- " + strings.Join(sortedKeys(edit.files), " "),
	}
	return edit.result, nil
}

// writeContractEdit writes a planned edit into a maintainer checkout. A
// migration creates the contract file exclusively first and removes it again
// if manifest.json cannot be written, so a failure leaves the inline contract
// in force.
func writeContractEdit(root string, edit contractEdit) error {
	for _, rel := range sortedKeys(edit.originals) {
		current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(current, edit.originals[rel]) {
			return fmt.Errorf("%s changed while the contract edit was prepared; nothing was written, so retry", rel)
		}
	}
	if edit.creates != "" {
		target := filepath.Join(root, filepath.FromSlash(edit.creates))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if _, err := manifest.ContractFilePath(root, edit.creates); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return err
			}
			return fmt.Errorf("%s already exists", edit.creates)
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = f.Write(edit.files[edit.creates])
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(target)
			return err
		}
		if err := writeExistingFile(filepath.Join(root, "manifest.json"), edit.files["manifest.json"]); err != nil {
			_ = os.Remove(target)
			return fmt.Errorf("write manifest.json (removed %s again, inline contract unchanged): %w", edit.creates, err)
		}
		return nil
	}
	for rel, data := range edit.files {
		path := filepath.Join(root, "manifest.json")
		if rel != "manifest.json" {
			var err error
			if path, err = manifest.ContractFilePath(root, rel); err != nil {
				return err
			}
		}
		if err := writeExistingFile(path, data); err != nil {
			return err
		}
	}
	return nil
}

// writeExistingFile replaces a file through a sibling temp file and rename,
// keeping its mode.
func writeExistingFile(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), info.Mode().Perm())
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

func (a app) adminContractRegistered(action string, args []string, opts adminContractOpts) (adminContractResult, error) {
	if opts.force {
		return adminContractResult{}, fmt.Errorf("--force is only available with the compatibility --manifest-dir path")
	}
	name, err := defaultManifestName(opts.home, opts.manifestName, opts.umbrellaRoot)
	if err != nil {
		return adminContractResult{}, err
	}
	docRef, err := loadSingleRegisteredDoc(opts.home, name)
	if err != nil {
		return adminContractResult{}, err
	}
	doc, manifestPath, repo, err := a.loadAuthorizedAdminManifestCheckout(docRef.ref.LocalPath)
	if err != nil {
		return adminContractResult{}, err
	}
	if err := ensureAdminManifestClean(repo, false); err != nil {
		return adminContractResult{}, fmt.Errorf("registered manifest cache must remain clean: %w", err)
	}
	root, err := resolveMyRoot(opts.home, name, opts.umbrellaRoot)
	if err != nil {
		return adminContractResult{}, err
	}
	if out, err := gitPRBytes(repo, nil, "fetch", "--prune", "origin"); err != nil {
		return adminContractResult{}, fmt.Errorf("refresh registered manifest: %s", commandMessage(out, err))
	}
	branch, err := gitPRText(repo, nil, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return adminContractResult{}, fmt.Errorf("registered manifest must be on a branch: %w", err)
	}
	upstream, err := gitPRText(repo, nil, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return adminContractResult{}, fmt.Errorf("registered manifest has no trusted upstream: %w", err)
	}
	head, err := gitPRText(repo, nil, "rev-parse", "HEAD^{commit}")
	if err != nil {
		return adminContractResult{}, err
	}
	trusted, err := gitPRText(repo, nil, "rev-parse", upstream+"^{commit}")
	if err != nil {
		return adminContractResult{}, err
	}
	if head != trusted {
		return adminContractResult{}, fmt.Errorf("registered manifest is not at its trusted upstream; run my manifests sync %s before authoring", name)
	}

	// The cache is clean at the trusted upstream, so its working tree is the
	// base the edit is computed against.
	edit, err := planContractEdit(action, args, opts, repo, manifestPath, doc)
	if err != nil {
		return adminContractResult{}, err
	}
	edit.result.BaseCommit = head
	message := map[string]string{
		"added": "Add organization contract rule", "removed": "Remove organization contract rule",
		"replaced": "Replace organization contract rule", "migrated": "Move organization contract to " + manifest.DefaultContractFile,
	}[edit.result.Action]
	result := a.publishPullRequest(opts.home, syncer.PRRequest{
		Entry: syncer.Entry{
			Manifest: name, ID: name, Role: "manifest", Kind: "manifest",
			GitURL: docRef.ref.GitURL, LocalPath: repo, UmbrellaRoot: root,
			ContentPaths: manifestControlPaths(),
		},
		Branch: branch, Upstream: upstream, Head: head, Changed: sortedKeys(edit.files),
		FileContents: edit.files, Message: message, PreserveCheckout: true, RequireFreshUpstream: true,
	}, false)
	if result.Status != "pull request opened" {
		message := result.Message
		if message == "" {
			message = result.Error
		}
		return adminContractResult{}, fmt.Errorf("contract change was not published (%s): %s", result.ReasonCode, message)
	}
	if result.NextCommand != "" {
		edit.result.NextCommands = append(edit.result.NextCommands, result.NextCommand)
	}
	edit.result.Message = "contract change proposed without modifying the sync-managed manifest cache"
	edit.result.Publication, edit.result.PRURL, edit.result.PRHeadSHA = result.Status, result.PRURL, result.PRHeadSHA
	return edit.result, nil
}

func (a app) printAdminContractResult(result adminContractResult, jsonOut bool) error {
	if result.Contract == nil {
		result.Contract = []string{}
	}
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	switch {
	case result.Action == "migrated":
		fmt.Fprintf(a.stdout, "migrated\t%d rules to %s\n", len(result.Contract), result.ContractFile)
	case result.Previous != "":
		fmt.Fprintf(a.stdout, "replaced\t%s\nwith\t%s\n", result.Previous, result.Rule)
	default:
		fmt.Fprintf(a.stdout, "%s\t%s\n", result.Action, result.Rule)
	}
	if result.Message != "" {
		fmt.Fprintln(a.stdout, result.Message)
	}
	if result.PRURL != "" {
		fmt.Fprintf(a.stdout, "pr\t%s\n", result.PRURL)
	}
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.stderr, "warning: %s\n", warning)
	}
	printAdminNextCommands(a.stdout, result.NextCommands)
	return nil
}

func sortedKeys(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
