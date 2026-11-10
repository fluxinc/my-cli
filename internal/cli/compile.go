package cli

import (
	"fmt"

	"github.com/fluxinc/my-cli/internal/launchplan"
	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func (a app) runCompile(args []string) error {
	var home, manifestName, role, umbrellaDir string
	fs := newFlagSet("my compile", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "select a registered manifest")
	fs.StringVar(&role, "role", "", "select a manifest role")
	fs.StringVar(&umbrellaDir, "umbrella", "", "add a commit pin to each projected mount from this umbrella's base checkouts (local Git only)")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"role":     true,
		"umbrella": true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my compile --role <id> [--manifest NAME] [--umbrella DIR] [--home DIR]")
	}
	var pinRoot string
	var pinWorkspace umbrella.Workspace
	if umbrellaDir != "" {
		pinRoot, pinWorkspace, err = loadCompileUmbrella(umbrellaDir)
		if err != nil {
			return err
		}
		// The umbrella records which manifest it belongs to; use it unless
		// the caller named one. A named one must still match the umbrella's
		// organization, checked below.
		if manifestName == "" {
			manifestName = pinWorkspace.ManifestRef
		}
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return err
	}
	var pinFor func(manifest.Mount) launchplan.MountPin
	if umbrellaDir != "" {
		if err := checkCompileUmbrella(pinRoot, pinWorkspace, doc.doc); err != nil {
			return err
		}
		pinFor = func(m manifest.Mount) launchplan.MountPin { return observeMountPin(pinRoot, m) }
	}
	a.printContractCompatibilityWarnings(home, doc.ref.Name)
	// The commit is best effort: a manifest outside Git still compiles.
	commit := ""
	if doc.doc.ContractPath() != "" {
		if head, err := gitPRText(doc.ref.LocalPath, nil, "rev-parse", "HEAD^{commit}"); err == nil {
			commit = head
		}
	}
	projection, err := launchplan.Compile(doc.doc, launchplan.Options{Role: role, ManifestRoot: doc.ref.LocalPath, ManifestCommit: commit, PinFor: pinFor})
	if err != nil {
		return err
	}
	data, err := launchplan.Marshal(projection)
	if err != nil {
		return err
	}
	_, err = a.stdout.Write(data)
	return err
}
