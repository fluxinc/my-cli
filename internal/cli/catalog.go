package cli

import (
	"fmt"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func (a app) runTools(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing tools subcommand")
	}
	switch args[0] {
	case "list":
		return a.runToolsList(args[1:])
	case "info":
		return a.runToolsInfo(args[1:])
	case "-h", "--help", "help":
		a.printToolsUsage()
		return nil
	default:
		return fmt.Errorf("unknown tools subcommand %q", args[0])
	}
}

func (a app) printToolsUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my tools list [--manifest NAME] [--home DIR] [--json]
  my tools info <name> [--manifest NAME] [--home DIR] [--json]

Tool entries are operator-facing hints from synced organization manifests.`)
}

type toolInfo struct {
	Manifest string        `json:"manifest"`
	Tool     manifest.Tool `json:"tool"`
}

func (a app) runToolsList(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("my tools list", a.stderr)
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
		return fmt.Errorf("tools list does not accept positional arguments")
	}
	infos, err := a.listToolInfo(home, manifestName)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, infos)
	}
	for _, info := range infos {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", info.Manifest, info.Tool.ID, info.Tool.Mode, info.Tool.Purpose)
	}
	return nil
}

func (a app) runToolsInfo(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("my tools info", a.stderr)
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
	if len(rest) != 1 {
		return fmt.Errorf("usage: my tools info <name>")
	}
	infos, err := a.findToolInfo(home, manifestName, rest[0])
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, infos)
	}
	for _, info := range infos {
		fmt.Fprintf(a.stdout, "%s\t%s\t%s\t%s\n", info.Manifest, info.Tool.ID, info.Tool.Mode, info.Tool.Purpose)
		for _, command := range info.Tool.Install.Commands {
			fmt.Fprintf(a.stdout, "install\t%s\n", command)
		}
		if info.Tool.Install.DocsURL != "" {
			fmt.Fprintf(a.stdout, "docs\t%s\n", info.Tool.Install.DocsURL)
		}
	}
	return nil
}

func (a app) runProducts(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing products subcommand")
	}
	switch args[0] {
	case "list":
		return a.runProductsList(args[1:])
	case "-h", "--help", "help":
		a.printProductsUsage()
		return nil
	default:
		return fmt.Errorf("unknown products subcommand %q", args[0])
	}
}

func (a app) printProductsUsage() {
	fmt.Fprintln(a.stdout, `Usage:
  my products list [--manifest NAME] [--home DIR] [--json]

Catalog data comes from synced organization manifests.`)
}

func (a app) runProductsList(args []string) error {
	var home string
	var manifestName string
	var jsonOut bool
	fs := newFlagSet("my products list", a.stderr)
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
		return fmt.Errorf("usage: my products list")
	}
	manifestName, err = defaultManifestName(home, manifestName, "")
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	products, err := manifest.LoadCatalog(home, manifestName)
	if err != nil {
		return a.maybeJSONError(jsonOut, err)
	}
	if jsonOut {
		return printJSON(a.stdout, products)
	}
	a.printProducts(products)
	return nil
}

func (a app) printProducts(products []manifest.Product) {
	for i, product := range products {
		if i != 0 {
			fmt.Fprintln(a.stdout)
		}
		fmt.Fprintf(a.stdout, "%s", product.ID)
		if product.Name != "" {
			fmt.Fprintf(a.stdout, " - %s", product.Name)
		}
		fmt.Fprintln(a.stdout)
		if len(product.Repos) != 0 {
			printHumanField(a.stdout, "repos", strings.Join(product.Repos, ", "))
		}
		if product.Purpose != "" {
			printHumanField(a.stdout, "purpose", product.Purpose)
		} else if product.Description != "" {
			printHumanField(a.stdout, "description", product.Description)
		}
		if len(product.RelatedSkills) != 0 {
			printHumanField(a.stdout, "skills", strings.Join(product.RelatedSkills, ", "))
		}
	}
}

func (a app) listToolInfo(home, manifestName string) ([]toolInfo, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var out []toolInfo
	for _, doc := range docs {
		for _, tool := range doc.doc.Tools {
			out = append(out, toolInfo{Manifest: doc.ref.Name, Tool: tool})
		}
	}
	return out, nil
}

func (a app) findToolInfo(home, manifestName, toolID string) ([]toolInfo, error) {
	docs, err := loadRegisteredDocs(home, manifestName)
	if err != nil {
		return nil, err
	}
	var out []toolInfo
	for _, doc := range docs {
		for _, tool := range doc.doc.Tools {
			if tool.ID == toolID {
				out = append(out, toolInfo{Manifest: doc.ref.Name, Tool: tool})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("tool %q is not declared by any selected manifest", toolID)
	}
	return out, nil
}
