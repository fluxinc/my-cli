package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubRunningMyVersion(t *testing.T, version string, stamped bool) {
	t.Helper()
	previous := runningMyVersion
	runningMyVersion = func() (string, bool) { return version, stamped }
	t.Cleanup(func() { runningMyVersion = previous })
}

func TestValidateManifestMinMyVersionReportsOnlyTheUpgrade(t *testing.T) {
	stubRunningMyVersion(t, "0.45.0", true)
	doc := roleMountTestDoc("future", "ops-docs")
	doc.MinMyVersion = "0.46.0"
	result := ValidateDocument("", doc)
	if len(result.Errors) != 1 || result.RequiresMyVersion != "0.46.0" {
		t.Fatalf("result = %#v, want only the upgrade requirement", result)
	}
	if want := "requires my v0.46.0 or later (running v0.45.0); run `my update`"; result.Errors[0] != want {
		t.Fatalf("error = %q, want %q", result.Errors[0], want)
	}
	err := &InvalidError{Name: "acme", Result: result}
	if got := err.Error(); got != `manifest "acme" `+result.Errors[0] {
		t.Fatalf("InvalidError = %q", got)
	}
}

func TestValidateFileMinMyVersionSkipsCatalogChecks(t *testing.T) {
	stubRunningMyVersion(t, "v0.45.0", true)
	dir := t.TempDir()
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "min_my_version": "0.46.0",
  "organization": { "id": "acme", "name": "Acme Example" },
  "mounts": [{ "id": "ops", "kind": "docs", "git_url": "https://github.com/acme/ops.git", "mode": "future" }]
}`)
	if err := os.MkdirAll(filepath.Join(dir, "catalog"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "catalog", "products.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := ValidateFile(dir)
	if len(result.Errors) != 1 || result.RequiresMyVersion != "0.46.0" {
		t.Fatalf("result = %#v, want only the upgrade requirement", result)
	}
}

func TestValidateManifestMinMyVersionComparesRunningVersions(t *testing.T) {
	for _, tc := range []struct {
		running string
		tooOld  bool
	}{
		{"0.46.0", false},
		{"v0.47.2", false},
		{"v0.46.1-0.20261016200849-86fa6dfb18be", false},
		{"v0.46.1-0.20261016200849-86fa6dfb18be+dirty", false},
		{"v0.46.0-0.20261016200849-86fa6dfb18be", true},
		{"0.45.9", true},
	} {
		stubRunningMyVersion(t, tc.running, true)
		doc := roleMountTestDoc("role", "ops-docs")
		doc.MinMyVersion = "0.46.0"
		result := ValidateDocument("", doc)
		if got := result.RequiresMyVersion != ""; got != tc.tooOld {
			t.Fatalf("running %s: result = %#v, want too old %v", tc.running, result, tc.tooOld)
		}
		if !tc.tooOld && len(result.Errors) != 0 {
			t.Fatalf("running %s: errors = %#v", tc.running, result.Errors)
		}
	}
}

func TestValidateManifestMinMyVersionUnstampedBuildWarns(t *testing.T) {
	stubRunningMyVersion(t, "0.1.0", false)
	doc := roleMountTestDoc("role", "ops-docs")
	doc.MinMyVersion = "9.0.0"
	result := ValidateDocument("", doc)
	if len(result.Errors) != 0 || !containsValidationError(result.Warnings, "unstamped") {
		t.Fatalf("result = %#v, want unstamped warning only", result)
	}
}

func TestValidateManifestRejectsMalformedMinMyVersion(t *testing.T) {
	stubRunningMyVersion(t, "0.46.0", true)
	for _, value := range []string{"v0.46.0", "0.46", "0.46.0-rc1", "0.46.0+meta", "00.46.0", " 0.46.0"} {
		doc := roleMountTestDoc("role", "ops-docs")
		doc.MinMyVersion = value
		result := ValidateDocument("", doc)
		if !containsValidationError(result.Errors, "must be a release version like 0.45.0") || result.RequiresMyVersion != "" {
			t.Fatalf("min_my_version %q: result = %#v", value, result)
		}
	}
}

func TestValidateManifestWarnsWhenMinMyVersionMissesFeatureFloor(t *testing.T) {
	stubRunningMyVersion(t, "0.46.0", true)
	missing := ValidateDocument("", roleMountTestDoc("role", "ops-docs"))
	if !containsValidationError(missing.Warnings, `uses mount mode "role", which needs my v0.45.0 or later`) ||
		!containsValidationError(missing.Warnings, "my admin min-version --manifest-dir") {
		t.Fatalf("warnings = %#v, want missing floor warning", missing.Warnings)
	}
	low := roleMountTestDoc("role", "ops-docs")
	low.MinMyVersion = "0.44.0"
	if result := ValidateDocument("", low); !containsValidationError(result.Warnings, "min_my_version 0.44.0 is older than v0.45.0") {
		t.Fatalf("warnings = %#v, want low floor warning", result.Warnings)
	}
	covered := roleMountTestDoc("role", "ops-docs")
	covered.MinMyVersion = "0.45.0"
	if result := ValidateDocument("", covered); len(result.Warnings) != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %#v, want clean", result)
	}
	if result := ValidateDocument("", roleMountTestDoc("optional")); len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none without gated features", result.Warnings)
	}
}

func TestRaiseMinMyVersion(t *testing.T) {
	for _, tc := range []struct {
		mode, declared, want string
		changed              bool
	}{
		{"role", "", "0.45.0", true},
		{"role", "0.44.0", "0.45.0", true},
		{"role", "0.45.0", "0.45.0", false},
		{"role", "0.50.0", "0.50.0", false},
		{"role", "v0.40", "v0.40", false},
		{"optional", "", "", false},
		{"optional", "0.30.0", "0.30.0", false},
	} {
		doc := roleMountTestDoc(tc.mode, "ops-docs")
		doc.MinMyVersion = tc.declared
		if changed := RaiseMinMyVersion(&doc); changed != tc.changed || doc.MinMyVersion != tc.want {
			t.Fatalf("%s/%q: changed=%v min=%q, want %v %q", tc.mode, tc.declared, changed, doc.MinMyVersion, tc.changed, tc.want)
		}
	}
}

func TestSaveDocumentStampsFeatureFloor(t *testing.T) {
	dir := t.TempDir()
	if err := SaveDocument(dir, roleMountTestDoc("role", "ops-docs")); err != nil {
		t.Fatal(err)
	}
	doc, _, err := LoadDocument(dir)
	if err != nil {
		t.Fatal(err)
	}
	if doc.MinMyVersion != "0.45.0" {
		t.Fatalf("min_my_version = %q, want stamped floor", doc.MinMyVersion)
	}
}

func TestSetMinMyVersionPatchesOnlyThatKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, manifestFile)
	writeManifest(t, dir, `{
  "manifest_version": 1,
  "organization": { "id": "acme", "name": "Acme <Example>" },
  "future_field": { "nested": [1, 2], "keep": true },
  "mounts": []
}`)
	if err := SetMinMyVersion(path, "0.45.0"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "manifest_version": 1,
  "min_my_version": "0.45.0",
  "organization": {
    "id": "acme",
    "name": "Acme <Example>"
  },
  "future_field": {
    "nested": [
      1,
      2
    ],
    "keep": true
  },
  "mounts": []
}
`
	if string(data) != want {
		t.Fatalf("patched manifest =\n%s\nwant\n%s", data, want)
	}
	if err := SetMinMyVersion(path, "0.46.0"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Count(string(data), "min_my_version") != 1 || !strings.Contains(string(data), `"min_my_version": "0.46.0"`) {
		t.Fatalf("replaced manifest =\n%s", data)
	}
	if err := SetMinMyVersion(path, "v0.47.0"); err == nil {
		t.Fatal("SetMinMyVersion accepted a non-release version")
	}
}
