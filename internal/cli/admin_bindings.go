package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
)

// set replaces one binding in full; omitted optional fields are cleared.
// All authoring uses the same authorization and validation as services.
func (a app) runAdminBindings(args []string) error {
	usage := "usage: my admin bindings set|remove <type> --manifest-dir DIR [--surface REF] [--guidance PATH] [--field-alias KEY=FIELD] [--force] [--json]"
	if len(args) == 0 || (args[0] != "set" && args[0] != "remove") {
		return fmt.Errorf("%s", usage)
	}
	op := args[0]
	var dir string
	var force, jsonOut bool
	var surfaces, guidance stringListFlag
	var aliases keyValueListFlag
	fs := newFlagSet("my admin bindings "+op, a.stderr)
	fs.StringVar(&dir, "manifest-dir", "", "maintainer manifest checkout")
	fs.Var(&surfaces, "surface", "mount:<id> or service:<id> (repeatable)")
	fs.Var(&guidance, "guidance", "manifest-relative guidance (repeatable)")
	fs.Var(&aliases, "field-alias", "local field to service field (repeatable)")
	fs.BoolVar(&force, "force", false, "allow a dirty checkout")
	fs.BoolVar(&jsonOut, "json", false, "print JSON")
	rest, err := parseInterspersed(fs, args[1:], map[string]bool{"manifest-dir": true, "surface": true, "guidance": true, "field-alias": true})
	if err != nil {
		return err
	}
	if len(rest) != 1 || dir == "" || (op == "set" && len(surfaces) == 0) || (op == "remove" && (len(surfaces)+len(guidance)+len(aliases) != 0)) {
		return fmt.Errorf("%s", usage)
	}
	key := rest[0]
	if !manifest.ValidDataType(key) {
		return fmt.Errorf("unsupported binding type %q", key)
	}
	doc, path, root, err := a.loadAuthorizedAdminManifestCheckout(dir)
	if err != nil {
		return err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return err
	}
	if op == "remove" {
		if _, ok := doc.DataBindings[key]; !ok {
			return fmt.Errorf("binding %q does not exist", key)
		}
		delete(doc.DataBindings, key)
	} else {
		binding := manifest.DataBinding{Surfaces: surfaces, Guidance: guidance}
		if len(aliases) > 0 {
			binding.FieldAliases = map[string]string{}
			for _, pair := range aliases {
				if _, exists := binding.FieldAliases[pair.Key]; exists {
					return fmt.Errorf("duplicate field alias %q", pair.Key)
				}
				binding.FieldAliases[pair.Key] = pair.Value
			}
		}
		if doc.DataBindings == nil {
			doc.DataBindings = map[string]manifest.DataBinding{}
		}
		doc.DataBindings[key] = binding
	}
	if result := manifest.ValidateDocument(root, doc); len(result.Errors) != 0 {
		return fmt.Errorf("updated manifest is invalid: %s", strings.Join(result.Errors, "; "))
	}
	if err := manifest.SaveDocument(path, doc); err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(a.stdout).Encode(map[string]any{"action": op, "type": key, "manifest_path": path, "binding": doc.DataBindings[key], "next_commands": adminNextCommands(root)})
	}
	fmt.Fprintf(a.stdout, "binding %s: %s %s\n", key, op, strings.Join(doc.DataBindings[key].SurfaceRefs(), ", "))
	return nil
}
