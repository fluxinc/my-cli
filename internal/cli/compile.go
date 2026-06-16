package cli

import (
	"fmt"

	"github.com/fluxinc/my-cli/internal/launchplan"
)

func (a app) runCompile(args []string) error {
	var home, manifestName, role string
	fs := newFlagSet("my compile", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&manifestName, "manifest", "", "select a registered manifest")
	fs.StringVar(&role, "role", "", "select a manifest role")
	rest, err := parseInterspersed(fs, args, map[string]bool{
		"home":     true,
		"manifest": true,
		"role":     true,
	})
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return fmt.Errorf("usage: my compile --role <id> [--manifest NAME] [--home DIR]")
	}
	doc, err := loadSingleRegisteredDoc(home, manifestName)
	if err != nil {
		return err
	}
	projection, err := launchplan.Compile(doc.doc, launchplan.Options{Role: role})
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
