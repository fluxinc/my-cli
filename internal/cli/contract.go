package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/contractreview"
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
	Warning  string                     `json:"warning,omitempty"`
	Sections []manifest.ContractSection `json:"sections"`
	text     []byte
}

type adminContractResult struct {
	Review       *contractreview.Report `json:"review,omitempty"`
	Action       string                 `json:"action"`
	Rule         string                 `json:"rule,omitempty"`
	Previous     string                 `json:"previous,omitempty"`
	Source       string                 `json:"source"`
	ManifestPath string                 `json:"manifest_path"`
	ContractFile string                 `json:"contract_file,omitempty"`
	BaseCommit   string                 `json:"base_commit,omitempty"`
	BaseBlob     string                 `json:"base_blob,omitempty"`
	Blob         string                 `json:"blob,omitempty"`
	MinMyVersion string                 `json:"min_my_version,omitempty"`
	Contract     []string               `json:"contract"`
	Message      string                 `json:"message,omitempty"`
	Warnings     []string               `json:"warnings,omitempty"`
	NextCommands []string               `json:"next_commands,omitempty"`
	Publication  string                 `json:"publication,omitempty"`
	PRURL        string                 `json:"pr_url,omitempty"`
	PRHeadSHA    string                 `json:"pr_head_sha,omitempty"`
}

type adminContractOpts struct {
	review        bool
	reviewSet     bool
	reviewHarness string
	reviewModel   string
	policyRoots   stringListFlag
	manifestDir   string
	home          string
	manifestName  string
	umbrellaRoot  string
	under         string
	expectBlob    string
	fromInline    bool
	force         bool
	jsonOut       bool
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
	case "pending":
		return a.runContractPending(args[1:])
	case "register":
		return a.runContractRegister(args[1:])
	case "status", "diff":
		return a.runContractStateCommand(args[0], args[1:])
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
  my contract pending [--manifest NAME] [--instance ID] [--hook claude|codex]
  my contract register --manifest NAME --harness NAME --root DIR [--home DIR]
  my contract status [--manifest NAME] [--home DIR] [--json]
  my contract diff [--from REV] [--to REV] [--manifest NAME] [--home DIR] [--json]

The organization contract is the manifest's list of short, binding rules,
rendered into generated AGENTS.md. It lives inline in manifest.json or, once
migrated, in a contract_source such as guidance/contract.md with one "- rule" per
line and an automatically maintained inline copy for older my releases. Edit it with my admin contract add|remove|replace|migrate.`)
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
			Sections: contract.Sections(), Warning: contract.Warning,
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
	case "review":
		return a.runContractReview(args[1:])
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
	"migrate": `usage: my admin contract migrate [--from-inline] [--manifest NAME --umbrella DIR | --manifest-dir DIR]`,
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
	if opts.fromInline && action != "migrate" {
		return a.maybeJSONError(opts.jsonOut, fmt.Errorf("--from-inline is only valid with migrate"))
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
	fs.BoolVar(&opts.fromInline, "from-inline", false, "migrate: restore the editable file from the inline compatibility copy")
	fs.BoolVar(&opts.force, "force", false, "allow dirty explicit --manifest-dir checkout")
	fs.BoolVar(&opts.jsonOut, "json", false, "print JSON result")
	fs.BoolVar(&opts.review, "review", false, "run advisory model review; --review=false overrides manifest opt-in")
	fs.StringVar(&opts.reviewHarness, "harness", "", "review harness: claude or codex")
	fs.StringVar(&opts.reviewModel, "model", "", "explicit review model")
	fs.Var(&opts.policyRoots, "policy-root", "review policy source MOUNT=DIR (repeatable)")
	rest, err = parseInterspersed(fs, args, map[string]bool{
		"manifest-dir": true, "home": true, "manifest": true, "umbrella": true, "under": true, "expect-blob": true, "harness": true, "model": true, "policy-root": true,
	})
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "review" {
			opts.reviewSet = true
		}
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
		return planContractMigrate(edit, root, manifestPath, doc, opts.fromInline)
	}
	if current.Warning != "" {
		return contractEdit{}, errors.New(current.Warning)
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
		raised := doc
		manifest.RaiseMinMyVersion(&raised)
		var patches []manifest.KeyEdit
		if doc.ContractSource != "" {
			raised.Contract = next.Rules()
			value, err := manifest.PatchValue(raised.Contract)
			if err != nil {
				return contractEdit{}, err
			}
			if len(raised.Contract) == 0 {
				value = []byte("[]")
				raised.Contract = []string{}
			}
			patches = append(patches, manifest.KeyEdit{Key: "contract", Value: value, After: "contract_source"})
		}
		if raised.MinMyVersion != doc.MinMyVersion {
			value, _ := json.Marshal(raised.MinMyVersion)
			patches = append(patches, manifest.KeyEdit{Key: "min_my_version", Value: value, After: "manifest_version"})
		}
		if len(patches) > 0 {
			patched, err := manifest.PatchManifestKeys(raw, patches)
			if err != nil {
				return contractEdit{}, err
			}
			edit.files["manifest.json"] = patched
		}
		edit.result.MinMyVersion = raised.MinMyVersion
		if validation := manifest.ValidateDocument("", raised); len(validation.Errors) != 0 {
			return contractEdit{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
		}
		if resolved, err := manifest.ResolveContractBytes(raised, data, nil); err != nil || resolved.Warning != "" {
			return contractEdit{}, fmt.Errorf("updated contract compatibility copy is invalid: %v %s", err, resolved.Warning)
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

// planContractMigrate creates or reconciles the editable file and its inline
// compatibility copy. Targeted patches preserve unrelated manifest keys.
func planContractMigrate(edit contractEdit, root, manifestPath string, doc manifest.Document, fromInline bool) (contractEdit, error) {
	if err := manifest.CheckContractSource(doc); err != nil {
		return contractEdit{}, err
	}
	file := doc.ContractPath()
	if file == "" {
		file = manifest.DefaultContractFile
	}
	// A file-only contract_file elsewhere is copied to guidance/contract.md,
	// the only compatible location; the old file stays for the admin to delete.
	var legacy []byte
	if file != manifest.DefaultContractFile {
		if doc.ContractFile == "" {
			return contractEdit{}, fmt.Errorf("contract_source must be %s; point it there, then rerun migrate", manifest.DefaultContractFile)
		}
		if fromInline {
			return contractEdit{}, fmt.Errorf("file-only contract has no inline copy; omit --from-inline")
		}
		from, err := manifest.ContractFilePath(root, file)
		if err == nil {
			legacy, err = os.ReadFile(from)
		}
		if err != nil {
			return contractEdit{}, err
		}
		file = manifest.DefaultContractFile
	}
	target, err := manifest.ContractFilePath(root, file)
	var original []byte
	if err == nil {
		original, err = os.ReadFile(target)
	}
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return contractEdit{}, err
	}
	if doc.ContractPath() == "" && exists && !fromInline {
		return contractEdit{}, fmt.Errorf("%s already exists; inspect it, then use migrate --from-inline to restore it from the current inline rules", file)
	}
	if legacy != nil && exists && !bytes.Equal(original, legacy) {
		return contractEdit{}, fmt.Errorf("%s already exists with other content; merge %s into it or move it aside, then rerun migrate", file, doc.ContractFile)
	}
	var contract manifest.Contract
	if legacy != nil {
		contract, err = manifest.ParseContractFile(file, legacy)
		if err != nil {
			return contractEdit{}, err
		}
	} else if doc.ContractPath() != "" && !fromInline {
		if err != nil {
			return contractEdit{}, fmt.Errorf("cannot read %s: %w; use --from-inline to restore it from the compatibility copy", file, err)
		}
		contract, err = manifest.ParseContractFile(file, original)
		if err != nil {
			return contractEdit{}, err
		}
	} else {
		if fromInline && doc.ContractFile != "" {
			return contractEdit{}, fmt.Errorf("file-only contract has no inline copy; omit --from-inline")
		}
		contract = manifest.NewContractFile(file, doc.Contract)
	}
	if _, err := manifest.ParseContractFile(file, contract.Bytes()); err != nil {
		return contractEdit{}, err
	}
	migrated := doc
	migrated.ContractFile = ""
	migrated.ContractSource = file
	migrated.Contract = contract.Rules()
	if migrated.Contract == nil {
		migrated.Contract = []string{}
	}
	// Compatibility metadata adds no feature floor. Preserve every declared floor.
	manifest.RaiseMinMyVersion(&migrated)
	fileValue, err := manifest.PatchValue(file)
	if err != nil {
		return contractEdit{}, err
	}
	rulesValue, err := manifest.PatchValue(migrated.Contract)
	if err != nil {
		return contractEdit{}, err
	}
	edits := []manifest.KeyEdit{
		{Key: "contract", Value: rulesValue}, {Key: "contract_file"},
		{Key: "contract_source", Value: fileValue, After: "contract"},
	}
	if migrated.MinMyVersion != doc.MinMyVersion {
		value, _ := json.Marshal(migrated.MinMyVersion)
		edits = append(edits, manifest.KeyEdit{Key: "min_my_version", Value: value, After: "manifest_version"})
	}
	manifestData, err := manifest.PatchManifestKeys(edit.originals["manifest.json"], edits)
	if err != nil {
		return contractEdit{}, err
	}
	if validation := manifest.ValidateDocument("", migrated); len(validation.Errors) != 0 {
		return contractEdit{}, fmt.Errorf("migrated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
	}
	if _, err := manifest.ResolveContractBytes(migrated, contract.Bytes(), nil); err != nil {
		return contractEdit{}, err
	}
	edit.files[file] = contract.Bytes()
	edit.files["manifest.json"] = manifestData
	if exists {
		edit.originals[file] = original
	} else {
		edit.creates = file
	}
	if legacy != nil {
		// Not written, but the copy is only valid while the source is unchanged.
		edit.originals[doc.ContractFile] = legacy
	}
	edit.contract = contract
	edit.result.Action = "migrated"
	edit.result.ContractFile = file
	edit.result.Blob = contract.Blob
	edit.result.MinMyVersion = migrated.MinMyVersion
	edit.result.Contract = contract.Rules()
	if legacy != nil {
		edit.result.Warnings = append(edit.result.Warnings, fmt.Sprintf("copied %s to %s; the old file is no longer read, so delete it in a later change", doc.ContractFile, file))
	}
	edit.result.Warnings = append(edit.result.Warnings, "The inline contract compatibility copy is retained for older my releases; no member upgrade is required by this migration. Older authoring commands may detach the file and keep the contract inline; rerun migrate --from-inline to reconnect it.")
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
	head, headErr := gitPRText(root, nil, "rev-parse", "HEAD^{commit}")
	if headErr != nil {
		head = ""
	}
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
	if wantsContractReview(opts, doc) {
		report := a.reviewContractEdit(opts, doc, root, head, edit.contract, currentContractText(root, doc, edit), "")
		edit.result.Review = &report
	}
	return edit.result, nil
}

// writeContractEdit writes a planned edit into a maintainer checkout. A
// migration creates the contract file exclusively first and removes it again
// if manifest.json cannot be written, so a failure leaves the inline contract
// in force.
func writeContractEdit(root string, edit contractEdit) error {
	if edit.result.BaseCommit != "" {
		current, err := gitPRText(root, nil, "rev-parse", "HEAD^{commit}")
		if err != nil || current != edit.result.BaseCommit {
			return fmt.Errorf("manifest commit changed while the contract edit was prepared; nothing was written, so retry")
		}
	}
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
	paths := sortedKeys(edit.files)
	// Publish the compatibility copy last. During a partial local write, members
	// continue reading the old complete copy rather than a half-updated contract.
	for i, rel := range paths {
		if rel == "manifest.json" {
			paths = append(append(paths[:i:i], paths[i+1:]...), rel)
			break
		}
	}
	var written []string
	for _, rel := range paths {
		path := filepath.Join(root, "manifest.json")
		if rel != "manifest.json" {
			var err error
			path, err = manifest.ContractFilePath(root, rel)
			if err != nil {
				return err
			}
		}
		if err := writeExistingFile(path, edit.files[rel]); err != nil {
			for i := len(written) - 1; i >= 0; i-- {
				old := written[i]
				if restoreErr := writeExistingFile(filepath.Join(root, filepath.FromSlash(old)), edit.originals[old]); restoreErr != nil {
					return fmt.Errorf("write %s: %v; restore %s: %w", rel, err, old, restoreErr)
				}
			}
			return err
		}
		written = append(written, rel)
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
		AdvisoryBody: func(candidate string) string {
			if !wantsContractReview(opts, doc) {
				return ""
			}
			report := a.reviewContractEdit(opts, doc, repo, candidate, edit.contract, currentContractText(repo, doc, edit), root)
			edit.result.Review = &report
			return contractreview.Markdown(report)
		},
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
	if result.Review != nil {
		fmt.Fprintln(a.stdout, contractreview.Markdown(*result.Review))
	}
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

// printContractCompatibilityWarnings keeps stdout (including JSON) clean.
func (a app) printContractCompatibilityWarnings(home, name string) {
	docs, err := loadRegisteredDocs(home, name)
	if err != nil {
		return
	}
	for _, doc := range docs {
		c, err := manifest.LoadContract(doc.ref.LocalPath, doc.doc)
		if err == nil && c.Warning != "" {
			fmt.Fprintf(a.stderr, "warning: manifest %s: %s\n", doc.ref.Name, c.Warning)
		}
		if warning := manifest.DetachedContractFileWarning(doc.ref.LocalPath, doc.doc); warning != "" {
			fmt.Fprintf(a.stderr, "warning: manifest %s: %s\n", doc.ref.Name, warning)
		}
	}
}
