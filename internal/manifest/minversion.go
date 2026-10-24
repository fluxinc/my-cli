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
	{Version: "0.47.0", Feature: "contract_file", Uses: func(doc Document) bool { return doc.ContractFile != "" }},
	{Version: "0.50.0", Feature: "contract_review", Uses: func(doc Document) bool { return doc.ContractReview != (ContractReview{}) }},
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
	if doc.ContractSource != "" && len(doc.Contract) == 0 {
		return PatchManifestKeys(data, []KeyEdit{{Key: "contract", Value: json.RawMessage("[]"), After: "contract_source"}})
	}
	return append(data, '\n'), nil
}

// PatchValue encodes a PatchManifestKeys value without HTML escaping, so a
// rule with & or < keeps the bytes an administrator wrote.
func PatchValue(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
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
	value, _ := json.Marshal(version)
	out, err := PatchManifestKeys(data, []KeyEdit{{Key: "min_my_version", Value: value, After: "manifest_version"}})
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return os.WriteFile(path, out, info.Mode().Perm())
}

// KeyEdit sets or deletes one top-level manifest key. A nil Value deletes it.
// A key that already exists keeps its position; a new one goes right after
// After (even when After is deleted in the same patch), else at the end.
type KeyEdit struct {
	Key   string
	Value json.RawMessage
	After string
}

// PatchManifestKeys applies edits to manifest.json bytes and changes nothing
// else: unknown keys (from a newer my), nested values and key order survive,
// unlike a round trip through Document.
func PatchManifestKeys(data []byte, edits []KeyEdit) ([]byte, error) {
	if !json.Valid(data) {
		return nil, fmt.Errorf("manifest is not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("manifest must be a JSON object")
	}
	byKey := map[string]KeyEdit{}
	for _, edit := range edits {
		byKey[edit.Key] = edit
	}
	written := map[string]bool{}
	var buf bytes.Buffer
	buf.WriteByte('{')
	fields := 0
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
	writeEdit := func(edit KeyEdit) {
		if !written[edit.Key] && edit.Value != nil {
			write(edit.Key, edit.Value)
		}
		written[edit.Key] = true
	}
	present := map[string]bool{}
	var keys []string
	raws := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		key, _ := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		if present[key] {
			return nil, fmt.Errorf("manifest repeats key %q", key)
		}
		present[key] = true
		keys = append(keys, key)
		raws[key] = raw
	}
	for _, key := range keys {
		if edit, ok := byKey[key]; ok {
			writeEdit(edit)
		} else {
			write(key, raws[key])
		}
		for _, edit := range edits {
			if edit.After == key && !present[edit.Key] {
				writeEdit(edit)
			}
		}
	}
	for _, edit := range edits {
		writeEdit(edit)
	}
	buf.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
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
