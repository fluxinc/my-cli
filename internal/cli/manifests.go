package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

type registeredDoc struct {
	ref manifest.Ref
	doc manifest.Document
}

func loadRegisteredDocs(home, manifestName string) ([]registeredDoc, error) {
	refs, err := manifestRefs(home, manifestName)
	if err != nil {
		return nil, err
	}
	return loadDocsForRefs(refs)
}

func loadAllRegisteredDocs(home string) ([]registeredDoc, error) {
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return nil, err
	}
	return loadDocsForRefs(reg.Manifests)
}

func loadDocsForRefs(refs []manifest.Ref) ([]registeredDoc, error) {
	docs := make([]registeredDoc, 0, len(refs))
	for _, ref := range refs {
		doc, _, err := manifest.LoadDocument(ref.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("manifest %q is not synced; run my manifests sync %s: %w", ref.Name, ref.Name, err)
		}
		result := manifest.ValidateFile(ref.LocalPath)
		if len(result.Errors) != 0 {
			return nil, fmt.Errorf("manifest %q is invalid: %s", ref.Name, strings.Join(result.Errors, "; "))
		}
		docs = append(docs, registeredDoc{ref: ref, doc: doc})
	}
	return docs, nil
}

func loadSingleRegisteredDoc(home, manifestName string) (registeredDoc, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return registeredDoc{}, err
	}
	if len(docs) == 0 {
		return registeredDoc{}, fmt.Errorf("my requires a registered manifest")
	}
	if len(docs) != 1 {
		return registeredDoc{}, fmt.Errorf("my requires exactly one manifest; pass --manifest")
	}
	return docs[0], nil
}

func manifestRefs(home, manifestName string) ([]manifest.Ref, error) {
	if manifestName != "" {
		ref, err := registeredManifestRef(home, manifestName)
		if err != nil {
			return nil, err
		}
		return []manifest.Ref{ref}, nil
	}
	if name, ok, err := manifestNameFromUmbrellaContext(home, ""); err != nil {
		return nil, err
	} else if ok {
		ref, err := registeredManifestRef(home, name)
		if err != nil {
			return nil, err
		}
		return []manifest.Ref{ref}, nil
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return nil, err
	}
	if ref, ok := reg.DefaultRef(); ok {
		return []manifest.Ref{ref}, nil
	}
	return nil, nil
}

func registeredManifestRef(home, name string) (manifest.Ref, error) {
	ref, ok, err := manifest.Find(home, name)
	if err != nil {
		return manifest.Ref{}, err
	}
	if !ok {
		return manifest.Ref{}, fmt.Errorf("manifest %q is not registered", name)
	}
	return ref, nil
}

func defaultManifestName(home, manifestName, umbrellaRoot string) (string, error) {
	if manifestName != "" {
		return manifestName, nil
	}
	if name, ok, err := manifestNameFromUmbrellaContext(home, umbrellaRoot); err != nil {
		return "", err
	} else if ok {
		return name, nil
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return "", err
	}
	ref, ok := reg.DefaultRef()
	if !ok {
		return "", fmt.Errorf("my requires a registered manifest")
	}
	return ref.Name, nil
}

func defaultManifestNameIfAny(home, manifestName, umbrellaRoot string) (string, bool, error) {
	if manifestName != "" {
		return manifestName, true, nil
	}
	if name, ok, err := manifestNameFromUmbrellaContext(home, umbrellaRoot); err != nil {
		return "", false, err
	} else if ok {
		return name, true, nil
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return "", false, err
	}
	ref, ok := reg.DefaultRef()
	if !ok {
		return "", false, nil
	}
	return ref.Name, true, nil
}

func manifestNameFromUmbrellaContext(home, explicit string) (string, bool, error) {
	var root string
	if explicit != "" {
		resolved, err := resolveUmbrellaRoot(home, explicit)
		if err != nil {
			return "", false, err
		}
		root = resolved
	} else {
		found, ok := umbrella.FindRoot(".")
		if !ok {
			return "", false, nil
		}
		root = found
	}
	ws, err := umbrella.LoadWorkspace(root)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if ws.ManifestRef == "" {
		return "", false, nil
	}
	return ws.ManifestRef, true, nil
}

func (a app) runManifest(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing manifest subcommand")
	}

	switch args[0] {
	case "add":
		return a.runManifestAdd(args[1:])
	case "list":
		return a.runManifestList(args[1:])
	case "default":
		return a.runManifestDefault(args[1:])
	case "sync":
		return a.runManifestSync(args[1:])
	case "validate":
		return a.runManifestValidate(args[1:])
	case "-h", "--help", "help":
		a.printManifestUsage()
		return nil
	default:
		return fmt.Errorf("unknown manifest subcommand %q", args[0])
	}
}

func (a app) printManifestUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my manifests add <name> <git-url> [--home DIR] [--json]
  my manifests list [--home DIR] [--json]
  my manifests default [<name>] [--clear] [--home DIR] [--json]
  my manifests sync [name...] | --all [--home DIR] [--umbrella DIR] [--no-derived] [--print] [--json]
  my manifests validate <name|path> [--home DIR] [--json]`)
}

func (a app) runManifestAdd(args []string) error {
	var home string
	var jsonOut bool
	fs := newFlagSet("my manifests add", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return fmt.Errorf("usage: my manifests add <name> <git-url>")
	}
	ref, err := manifest.Add(home, rest[0], rest[1])
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, ref)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", ref.Name, ref.GitURL, ref.LocalPath)
	return nil
}

func (a app) runManifestList(args []string) error {
	var home string
	var jsonOut bool
	fs := newFlagSet("my manifests list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("manifest list does not accept positional arguments")
	}
	reg, err := manifest.LoadRegistry(home)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, reg)
	}
	for _, ref := range reg.Manifests {
		line := fmt.Sprintf("%s\t%s\t%s", ref.Name, ref.GitURL, ref.LocalPath)
		if ref.Name == reg.DefaultManifest || (reg.DefaultManifest == "" && len(reg.Manifests) != 0 && ref.Name == reg.Manifests[0].Name) {
			line += "\tdefault"
		}
		fmt.Fprintln(a.stdout, line)
	}
	return nil
}

func (a app) runManifestDefault(args []string) error {
	var home string
	var jsonOut bool
	var clear bool
	fs := newFlagSet("my manifests default", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	fs.BoolVar(&clear, "clear", false, "revert the default to the first-added manifest")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if clear && len(rest) > 0 {
		return fmt.Errorf("usage: my manifests default [<name>] | --clear")
	}
	if len(rest) > 1 {
		return fmt.Errorf("usage: my manifests default [<name>] [--clear]")
	}
	if len(rest) == 0 && !clear {
		reg, err := manifest.LoadRegistry(home)
		if err != nil {
			return err
		}
		ref, ok := reg.DefaultRef()
		if !ok {
			return fmt.Errorf("no registered manifests; run my manifests add <name> <git-url>")
		}
		if jsonOut {
			return printJSON(a.stdout, ref)
		}
		fmt.Fprintln(a.stdout, ref.Name)
		return nil
	}
	name := ""
	if !clear {
		name = rest[0]
	}
	ref, err := manifest.SetDefault(home, name)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, ref)
	}
	fmt.Fprintf(a.stdout, "default manifest is now %s\n", ref.Name)
	return nil
}

func (a app) runManifestSync(args []string) error {
	var home string
	var umbrellaRoot string
	var all bool
	var noDerived bool
	var printOnly bool
	var jsonOut bool
	fs := newFlagSet("my manifests sync", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&umbrellaRoot, "umbrella", "", "override umbrella root for derived reconciliation")
	fs.BoolVar(&all, "all", false, "sync every registered manifest")
	fs.BoolVar(&noDerived, "no-derived", false, "skip guidance and skill reconciliation after manifest changes")
	fs.BoolVar(&printOnly, "print", false, "print planned git commands without changing files")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "umbrella": true})
	if err != nil {
		return err
	}
	if !all && len(rest) == 0 {
		if name, ok, err := defaultManifestNameIfAny(home, "", umbrellaRoot); err != nil {
			return err
		} else if ok {
			rest = []string{name}
		}
	}
	results, err := manifest.Sync(home, rest, all, printOnly, nil)
	if err != nil {
		return err
	}
	derivedManifest, derived, derivedNotices, derivedErr := a.manifestSyncDerived(home, umbrellaRoot, results, printOnly || noDerived)
	wrapped := wrapManifestSyncResults(results, derivedManifest, derived, derivedNotices)
	if jsonOut {
		if err := printJSON(a.stdout, wrapped); err != nil {
			return err
		}
	} else {
		for _, r := range results {
			line := fmt.Sprintf("%s\t%s\t%s", r.Name, r.Status, r.LocalPath)
			if r.Message != "" {
				line += "\t" + r.Message
			}
			if r.Error != "" {
				line += "\t" + r.Error
			}
			fmt.Fprintln(a.stdout, line)
		}
		if derived != nil {
			a.printDerivedReconcileReport(*derived, true)
		}
		printManifestSyncDerivedNotices(a.stdout, results, derivedNotices)
	}
	if manifestResultsFailed(results) {
		return fmt.Errorf("one or more manifest syncs failed")
	}
	if derivedErr != nil {
		return derivedErr
	}
	if derivedReportFailed(derived) {
		return fmt.Errorf("one or more derived reconciliation operations failed")
	}
	return nil
}

type manifestSyncCommandResult struct {
	manifest.SyncResult
	Derived        *derivedReconcileReport `json:"derived,omitempty"`
	DerivedStatus  string                  `json:"derived_status,omitempty"`
	DerivedMessage string                  `json:"derived_message,omitempty"`
}

type manifestSyncDerivedNotice struct {
	Status  string
	Message string
}

func wrapManifestSyncResults(results []manifest.SyncResult, derivedManifest string, derived *derivedReconcileReport, notices map[string]manifestSyncDerivedNotice) []manifestSyncCommandResult {
	wrapped := make([]manifestSyncCommandResult, 0, len(results))
	attached := false
	for _, result := range results {
		item := manifestSyncCommandResult{SyncResult: result}
		if derived != nil && !attached && result.Name == derivedManifest {
			item.Derived = derived
			attached = true
		}
		if notice, ok := notices[result.Name]; ok {
			item.DerivedStatus = notice.Status
			item.DerivedMessage = notice.Message
		}
		wrapped = append(wrapped, item)
	}
	return wrapped
}

func printManifestSyncDerivedNotices(w io.Writer, results []manifest.SyncResult, notices map[string]manifestSyncDerivedNotice) {
	if len(notices) == 0 {
		return
	}
	printed := map[string]bool{}
	for _, result := range results {
		if printed[result.Name] {
			continue
		}
		notice, ok := notices[result.Name]
		if !ok {
			continue
		}
		fmt.Fprintf(w, "derived\tmanifest:%s\t%s\t%s\n", result.Name, notice.Status, notice.Message)
		printed[result.Name] = true
	}
}

func (a app) manifestSyncDerived(home, umbrellaRoot string, results []manifest.SyncResult, skip bool) (string, *derivedReconcileReport, map[string]manifestSyncDerivedNotice, error) {
	notices := map[string]manifestSyncDerivedNotice{}
	if skip || manifestResultsFailed(results) {
		return "", nil, notices, nil
	}
	changed := changedManifestSyncResults(results)
	if len(changed) == 0 {
		return "", nil, notices, nil
	}
	if len(changed) != 1 {
		for _, manifestName := range changed {
			message, err := manifestSyncDerivedSkipMessage(home, umbrellaRoot, manifestName, "multiple changed manifests")
			if err != nil {
				return manifestName, nil, notices, err
			}
			notices[manifestName] = manifestSyncDerivedNotice{Status: "skipped", Message: message}
		}
		return "", nil, notices, nil
	}
	manifestName := changed[0]
	root, hasRoot, err := existingUmbrellaRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		if message, ok := manifestSyncUmbrellaMismatchMessage(err); ok {
			notices[manifestName] = manifestSyncDerivedNotice{Status: "skipped", Message: message}
			return manifestName, nil, notices, nil
		}
		return manifestName, nil, notices, err
	}
	if !hasRoot {
		notices[manifestName] = manifestSyncDerivedNotice{
			Status:  "skipped",
			Message: manifestSyncSetupRemediation("no existing umbrella found", manifestName, root),
		}
		return manifestName, nil, notices, nil
	}
	report, err := a.reconcileDerived(home, manifestName, root)
	if err != nil {
		return manifestName, nil, notices, err
	}
	return manifestName, &report, notices, nil
}

func manifestSyncDerivedSkipMessage(home, umbrellaRoot, manifestName, reason string) (string, error) {
	root, hasRoot, err := existingUmbrellaRoot(home, manifestName, umbrellaRoot)
	if err != nil {
		if message, ok := manifestSyncUmbrellaMismatchMessage(err); ok {
			return reason + "; " + message, nil
		}
		return "", err
	}
	if !hasRoot {
		return manifestSyncSetupRemediation(reason+"; no existing umbrella found", manifestName, root), nil
	}
	return manifestSyncSetupRemediation(reason, manifestName, root), nil
}

func manifestSyncUmbrellaMismatchMessage(err error) (string, bool) {
	var mismatch umbrellaManifestMismatchError
	if !errors.As(err, &mismatch) {
		return "", false
	}
	return fmt.Sprintf("umbrella %s uses manifest %q, not %q; pass --umbrella for the %s umbrella or run my setup --manifest %s", mismatch.Root, mismatch.Have, mismatch.Want, mismatch.Want, mismatch.Want), true
}

func manifestSyncSetupRemediation(reason, manifestName, root string) string {
	args := []string{"my", "setup", "--manifest", manifestName}
	if root != "" {
		args = append(args, "--umbrella", root)
	}
	return reason + "; run " + strings.Join(args, " ")
}

func changedManifestSyncResults(results []manifest.SyncResult) []string {
	seen := map[string]bool{}
	var names []string
	for _, result := range results {
		if !result.Changed || result.Name == "" {
			continue
		}
		if seen[result.Name] {
			continue
		}
		seen[result.Name] = true
		names = append(names, result.Name)
	}
	return names
}

func (a app) runManifestValidate(args []string) error {
	var home string
	var jsonOut bool
	fs := newFlagSet("my manifests validate", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return fmt.Errorf("usage: my manifests validate <name|path>")
	}
	path := rest[0]
	if ref, ok, err := manifest.Find(home, rest[0]); err != nil {
		return err
	} else if ok {
		path = ref.LocalPath
	}
	result := manifest.ValidateFile(path)
	if jsonOut {
		if err := printJSON(a.stdout, result); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(a.stdout, "%s\n", result.Path)
		for _, warning := range result.Warnings {
			fmt.Fprintf(a.stdout, "warning\t%s\n", warning)
		}
		for _, validationErr := range result.Errors {
			fmt.Fprintf(a.stdout, "error\t%s\n", validationErr)
		}
		if len(result.Errors) == 0 {
			fmt.Fprintln(a.stdout, "ok")
		}
	}
	if len(result.Errors) != 0 {
		return fmt.Errorf("manifest validation failed")
	}
	return nil
}
