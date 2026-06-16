package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
)

// contractEntry is one organization contract rule with its manifest and
// 1-based position, the handle `my admin contract remove` accepts.
type contractEntry struct {
	Manifest string `json:"manifest"`
	Index    int    `json:"index"`
	Rule     string `json:"rule"`
}

type adminContractResult struct {
	Action       string   `json:"action"`
	Rule         string   `json:"rule"`
	ManifestPath string   `json:"manifest_path"`
	Contract     []string `json:"contract"`
	Message      string   `json:"message,omitempty"`
	NextCommands []string `json:"next_commands,omitempty"`
}

func (a app) runContract(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing contract subcommand")
	}
	switch args[0] {
	case "list":
		return a.runContractList(args[1:])
	case "add", "remove":
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

The organization contract is the manifest's list of short, binding rules,
rendered into generated AGENTS.md. Edit it with my admin contract add|remove.`)
}

func (a app) runContractList(args []string) error {
	var home, manifestName string
	var jsonOut bool
	fs := newFlagSet("my contract list", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "limit to one registered manifest")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
	})
	if err != nil {
		return err
	}
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

func loadManifestContracts(home, manifestName string) ([]contractEntry, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	entries := []contractEntry{}
	for _, doc := range docs {
		for i, rule := range doc.doc.Contract {
			entries = append(entries, contractEntry{
				Manifest: doc.ref.Name,
				Index:    i + 1,
				Rule:     rule,
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
	case "add":
		return a.runAdminContractAdd(args[1:])
	case "remove":
		return a.runAdminContractRemove(args[1:])
	case "list":
		return adminOperationalReadError("contract", args[0])
	case "-h", "--help", "help":
		a.printAdminUsage()
		return nil
	default:
		return fmt.Errorf("unknown admin contract subcommand %q", args[0])
	}
}

func (a app) runAdminContractAdd(args []string) error {
	manifestDir, force, jsonOut, rest, err := parseAdminContractOpts("my admin contract add", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || manifestDir == "" {
		return fmt.Errorf("usage: my admin contract add \"RULE TEXT\" --manifest-dir DIR")
	}
	result, err := a.adminContractAdd(rest[0], manifestDir, force)
	if err != nil {
		return err
	}
	return a.printAdminContractResult(result, jsonOut)
}

func (a app) runAdminContractRemove(args []string) error {
	manifestDir, force, jsonOut, rest, err := parseAdminContractOpts("my admin contract remove", a.stderr, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 || manifestDir == "" {
		return fmt.Errorf("usage: my admin contract remove <index|\"RULE TEXT\"> --manifest-dir DIR")
	}
	result, err := a.adminContractRemove(rest[0], manifestDir, force)
	if err != nil {
		return err
	}
	return a.printAdminContractResult(result, jsonOut)
}

func parseAdminContractOpts(name string, stderr io.Writer, args []string) (manifestDir string, force, jsonOut bool, rest []string, err error) {
	fs := newFlagSet(name, stderr)
	fs.StringVar(&manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.BoolVar(&force, "force", false, "allow dirty checkout")
	fs.BoolVar(&jsonOut, "json", false, "print JSON result")
	rest, err = parseInterspersed(fs, args, map[string]bool{"manifest-dir": true})
	return manifestDir, force, jsonOut, rest, err
}

func (a app) adminContractAdd(rule, manifestDir string, force bool) (adminContractResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminContractResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminContractResult{}, err
	}
	rule = strings.TrimSpace(rule)
	for _, existing := range doc.Contract {
		if strings.TrimSpace(existing) == rule {
			return adminContractResult{}, fmt.Errorf("contract rule already exists: %q", rule)
		}
	}
	doc.Contract = append(doc.Contract, rule)
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminContractResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminContractResult{}, err
	}
	return adminContractResult{
		Action:       "added",
		Rule:         rule,
		ManifestPath: manifestPath,
		Contract:     doc.Contract,
		Message:      "added contract rule; it renders in generated AGENTS.md after the next derived reconcile",
		NextCommands: adminNextCommands(root),
	}, nil
}

func (a app) adminContractRemove(target, manifestDir string, force bool) (adminContractResult, error) {
	doc, manifestPath, root, err := loadAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminContractResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminContractResult{}, err
	}
	idx := contractRuleIndex(doc.Contract, target)
	if idx == -1 {
		return adminContractResult{}, fmt.Errorf("contract rule %q not found; run my contract list", target)
	}
	removed := doc.Contract[idx]
	doc.Contract = append(doc.Contract[:idx], doc.Contract[idx+1:]...)
	if len(doc.Contract) == 0 {
		doc.Contract = nil
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return adminContractResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(manifestPath, doc); err != nil {
		return adminContractResult{}, err
	}
	return adminContractResult{
		Action:       "removed",
		Rule:         removed,
		ManifestPath: manifestPath,
		Contract:     doc.Contract,
		Message:      "removed contract rule",
		NextCommands: adminNextCommands(root),
	}, nil
}

// contractRuleIndex resolves a removal target that is either the 1-based
// position shown by `my contract list` or the exact rule text.
func contractRuleIndex(rules []string, target string) int {
	target = strings.TrimSpace(target)
	if n, err := strconv.Atoi(target); err == nil {
		if n >= 1 && n <= len(rules) {
			return n - 1
		}
		return -1
	}
	for i, rule := range rules {
		if strings.TrimSpace(rule) == target {
			return i
		}
	}
	return -1
}

func (a app) printAdminContractResult(result adminContractResult, jsonOut bool) error {
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	fmt.Fprintf(a.stdout, "%s\t%s\n", result.Action, result.Rule)
	if result.Message != "" {
		fmt.Fprintln(a.stdout, result.Message)
	}
	printAdminNextCommands(a.stdout, result.NextCommands)
	return nil
}
