package cli

import (
	"fmt"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/selfupdate"
)

// my admin min-version declares the manifest's min_my_version: the oldest my
// release that reads it. Older binaries then ask for `my update` instead of
// rejecting newer features as an invalid manifest.

type adminMinVersionResult struct {
	Action       string   `json:"action"`
	MinMyVersion string   `json:"min_my_version,omitempty"`
	Previous     string   `json:"previous,omitempty"`
	FeatureFloor string   `json:"feature_floor,omitempty"`
	Feature      string   `json:"feature,omitempty"`
	ManifestPath string   `json:"manifest_path"`
	Message      string   `json:"message,omitempty"`
	NextCommands []string `json:"next_commands,omitempty"`
}

const adminMinVersionUsage = "usage: my admin min-version --manifest-dir DIR [--set X.Y.Z] [--force] [--json]"

func (a app) runAdminMinVersion(args []string) error {
	var manifestDir string
	var set string
	var force bool
	var jsonOut bool
	fs := newFlagSet("my admin min-version", a.stderr)
	fs.StringVar(&manifestDir, "manifest-dir", "", "maintainer manifest checkout")
	fs.StringVar(&set, "set", "", "declare this version instead of the feature floor")
	fs.BoolVar(&force, "force", false, "allow dirty checkout")
	fs.BoolVar(&jsonOut, "json", false, "print JSON result")
	rest, err := parseInterspersed(fs, args, map[string]bool{"manifest-dir": true, "set": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 || manifestDir == "" {
		return fmt.Errorf(adminMinVersionUsage)
	}
	result, err := a.adminMinVersion(manifestDir, set, force)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(a.stdout, result)
	}
	version := result.MinMyVersion
	if version == "" {
		version = "-"
	}
	fmt.Fprintf(a.stdout, "%s\t%s\t%s\n", result.Action, version, result.ManifestPath)
	if result.Message != "" {
		fmt.Fprintln(a.stdout, result.Message)
	}
	for _, next := range result.NextCommands {
		fmt.Fprintf(a.stdout, "next\t%s\n", next)
	}
	return nil
}

// adminMinVersion raises min_my_version to the feature floor (never lowering
// a higher declared value), or with set declares an explicit version, which
// must not be older than the floor. It patches only that key, so fields this
// binary does not know survive.
func (a app) adminMinVersion(manifestDir, set string, force bool) (adminMinVersionResult, error) {
	doc, manifestPath, root, err := a.loadAuthorizedAdminManifestCheckout(manifestDir)
	if err != nil {
		return adminMinVersionResult{}, err
	}
	if err := ensureAdminManifestClean(root, force); err != nil {
		return adminMinVersionResult{}, err
	}
	floor, feature := manifest.FeatureMinMyVersion(doc)
	result := adminMinVersionResult{Previous: doc.MinMyVersion, FeatureFloor: floor, Feature: feature, ManifestPath: manifestPath}
	if set = strings.TrimSpace(set); set != "" {
		version := strings.TrimPrefix(set, "v")
		if !manifest.ReleaseVersion(version) {
			return adminMinVersionResult{}, fmt.Errorf("--set %q must be a release version like 0.45.0", set)
		}
		if floor != "" {
			if cmp, err := selfupdate.CompareVersions(version, floor); err == nil && cmp < 0 {
				return adminMinVersionResult{}, fmt.Errorf("--set v%s is older than v%s, which %s needs", version, floor, feature)
			}
		}
		doc.MinMyVersion = version
	} else {
		manifest.RaiseMinMyVersion(&doc)
	}
	if doc.MinMyVersion == result.Previous {
		result.Action, result.MinMyVersion = "unchanged", doc.MinMyVersion
		switch {
		case floor == "" && doc.MinMyVersion == "":
			result.Message = "manifest uses no features that need a newer my"
		case floor == "":
			result.Message = "min_my_version already declared"
		default:
			result.Message = fmt.Sprintf("min_my_version already covers v%s (%s)", floor, feature)
		}
		return result, nil
	}
	if validation := manifest.ValidateDocument(root, doc); len(validation.Errors) != 0 {
		return adminMinVersionResult{}, fmt.Errorf("updated manifest is invalid: %s", strings.Join(validation.Errors, "; "))
	}
	if err := manifest.SetMinMyVersion(manifestPath, doc.MinMyVersion); err != nil {
		return adminMinVersionResult{}, err
	}
	result.Action, result.MinMyVersion = "set", doc.MinMyVersion
	result.Message = fmt.Sprintf("declared min_my_version %s; my releases that check min_my_version and are older report the required upgrade instead of rejecting the manifest (releases before the check ignore it)", doc.MinMyVersion)
	result.NextCommands = adminNextCommands(root)
	return result, nil
}
