package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/fluxinc/my-cli/internal/selfupdate"
	"github.com/fluxinc/my-cli/internal/version"
)

// featureFloor records the first my release that reads one manifest feature.
// Add an entry whenever a release teaches validation a new value, so authoring
// commands stamp min_my_version and older binaries ask for an upgrade instead
// of rejecting the manifest as invalid.
type featureFloor struct {
	Version string
	Feature string
	Uses    func(Document) bool
}

var featureFloors = []featureFloor{
	{Version: "0.45.0", Feature: `mount mode "role"`, Uses: func(doc Document) bool {
		for _, m := range doc.Mounts {
			if m.Mode == "role" {
				return true
			}
		}
		return false
	}},
}

// releaseVersionPattern is the min_my_version grammar: a plain X.Y.Z release,
// with no "v" prefix, prerelease, or build metadata.
var releaseVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ReleaseVersion reports whether value is a valid min_my_version.
func ReleaseVersion(value string) bool {
	return releaseVersionPattern.MatchString(value)
}

// runningMyVersion reports the running my version; tests replace it.
var runningMyVersion = version.Current

// InvalidError reports a registered manifest that failed validation.
type InvalidError struct {
	Name   string
	Result ValidationResult
}

func (e *InvalidError) Error() string {
	if e.Result.RequiresMyVersion != "" && len(e.Result.Errors) == 1 {
		return fmt.Sprintf("manifest %q %s", e.Name, e.Result.Errors[0])
	}
	return fmt.Sprintf("manifest %q is invalid: %s", e.Name, strings.Join(e.Result.Errors, "; "))
}

// FeatureMinMyVersion returns the oldest my release that reads every feature
// doc uses, and the feature that sets it. It returns empty strings when any
// release reads the manifest.
func FeatureMinMyVersion(doc Document) (string, string) {
	floor, feature := "", ""
	for _, f := range featureFloors {
		if !f.Uses(doc) {
			continue
		}
		if floor == "" {
			floor, feature = f.Version, f.Feature
			continue
		}
		if cmp, err := selfupdate.CompareVersions(floor, f.Version); err == nil && cmp < 0 {
			floor, feature = f.Version, f.Feature
		}
	}
	return floor, feature
}

// RaiseMinMyVersion raises doc.MinMyVersion to the feature floor and reports
// whether it changed. A higher declared value is kept, and a malformed one is
// left for validation to report rather than silently replaced.
func RaiseMinMyVersion(doc *Document) bool {
	floor, _ := FeatureMinMyVersion(*doc)
	if floor == "" {
		return false
	}
	if doc.MinMyVersion == "" {
		doc.MinMyVersion = floor
		return true
	}
	if !ReleaseVersion(doc.MinMyVersion) {
		return false
	}
	if cmp, err := selfupdate.CompareVersions(doc.MinMyVersion, floor); err == nil && cmp < 0 {
		doc.MinMyVersion = floor
		return true
	}
	return false
}

// EncodeDocument returns canonical manifest.json bytes. It first raises
// min_my_version to the features doc uses, so every authoring path records
// the release a reader needs.
func EncodeDocument(doc Document) ([]byte, error) {
	RaiseMinMyVersion(&doc)
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// SetMinMyVersion writes min_my_version into the manifest.json at path and
// changes nothing else: unknown keys (from a newer my) and key order survive,
// unlike a round trip through Document. A new key goes after manifest_version.
func SetMinMyVersion(path, version string) error {
	if !ReleaseVersion(version) {
		return fmt.Errorf("min_my_version %q must be a release version like 0.45.0", version)
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	value, err := json.Marshal(version)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return fmt.Errorf("%s: manifest must be a JSON object", path)
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	fields, wrote := 0, false
	write := func(key string, raw []byte) {
		if fields > 0 {
			buf.WriteByte(',')
		}
		encoded, _ := json.Marshal(key)
		buf.Write(encoded)
		buf.WriteByte(':')
		buf.Write(raw)
		fields++
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%s: invalid JSON: %w", path, err)
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("%s: invalid JSON: %w", path, err)
		}
		if key == "min_my_version" {
			if !wrote {
				write(key, value)
				wrote = true
			}
			continue
		}
		write(key, raw)
		if key == "manifest_version" && !wrote {
			write("min_my_version", value)
			wrote = true
		}
	}
	if !wrote {
		write("min_my_version", value)
	}
	buf.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "  "); err != nil {
		return err
	}
	out.WriteByte('\n')
	return os.WriteFile(path, out.Bytes(), info.Mode().Perm())
}

// checkMinMyVersion runs before every other manifest check. It returns false
// when the running my is older than min_my_version: the remaining checks would
// only report the newer features this binary cannot read.
func checkMinMyVersion(doc Document, result *ValidationResult) bool {
	declared := ""
	if doc.MinMyVersion != "" {
		if !ReleaseVersion(doc.MinMyVersion) {
			result.Errors = append(result.Errors, fmt.Sprintf("min_my_version %q must be a release version like 0.45.0", doc.MinMyVersion))
			return true
		}
		declared = doc.MinMyVersion
		running, stamped := runningMyVersion()
		if !stamped {
			result.Warnings = append(result.Warnings, fmt.Sprintf("this my build is unstamped (%s); min_my_version %s was not checked", running, declared))
		} else if cmp, err := selfupdate.CompareVersions(running, declared); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("cannot compare my version %q with min_my_version %s", running, declared))
		} else if cmp < 0 {
			result.RequiresMyVersion = declared
			result.Errors = append(result.Errors, fmt.Sprintf("requires my v%s or later (running v%s); run `my update`", declared, strings.TrimPrefix(running, "v")))
			return false
		}
	}
	floor, feature := FeatureMinMyVersion(doc)
	if floor == "" {
		return true
	}
	if declared == "" {
		result.Warnings = append(result.Warnings, fmt.Sprintf("manifest uses %s, which needs my v%s or later; declare it with `my admin min-version --manifest-dir <manifest checkout>` so my releases that check min_my_version ask for an upgrade instead of rejecting the manifest", feature, floor))
	} else if cmp, err := selfupdate.CompareVersions(declared, floor); err == nil && cmp < 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("min_my_version %s is older than v%s, which %s needs; raise it with `my admin min-version --manifest-dir <manifest checkout>`", declared, floor, feature))
	}
	return true
}
