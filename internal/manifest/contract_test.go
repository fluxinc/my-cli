package manifest

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const sampleContractFile = `# Organization contract

- Keep answers short.

## Logs
- Service logs live under the install's logs/ directory.

## Communication
- Answer technically able people in technical language.
- Answer clinical staff in clinical language.
`

func TestParseContractFileGroupsRulesUnderHeadings(t *testing.T) {
	c, err := ParseContractFile("guidance/contract.md", []byte(sampleContractFile))
	if err != nil {
		t.Fatal(err)
	}
	wantSections := []ContractSection{
		{Rules: []string{"Keep answers short."}},
		{Heading: "Logs", Rules: []string{"Service logs live under the install's logs/ directory."}},
		{Heading: "Communication", Rules: []string{"Answer technically able people in technical language.", "Answer clinical staff in clinical language."}},
	}
	if got := c.Sections(); !reflect.DeepEqual(got, wantSections) {
		t.Fatalf("sections = %#v", got)
	}
	entries := c.Entries()
	if len(entries) != 4 || entries[1].Index != 2 || entries[1].Heading != "Logs" || entries[3].Heading != "Communication" {
		t.Fatalf("entries = %#v", entries)
	}
	if c.Blob != gitHashObject(t, []byte(sampleContractFile)) {
		t.Fatalf("blob %s does not match git hash-object", c.Blob)
	}
	if string(c.Bytes()) != sampleContractFile {
		t.Fatalf("round trip changed the file:\n%s", c.Bytes())
	}
}

func TestParseContractFileRejectsNonListContent(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"prose":             {"- ok\nSome prose paragraph.\n", "line 2: expected a `- ` rule"},
		"nested bullet":     {"- ok\n  - nested\n", "line 2: indented lines are not allowed"},
		"continuation":      {"- ok\n  continued\n", "line 2: indented lines are not allowed"},
		"star bullet":       {"* star\n", "line 1: expected a `- ` rule"},
		"deep heading":      {"### Deep\n", "use `## ` for group headings"},
		"duplicate rule":    {"- same\n## G\n-   same  \n", "line 3: rule duplicates line 1"},
		"duplicate heading": {"## G\n- a\n## G\n- b\n", `line 3: heading "G" duplicates line 1`},
		"late title":        {"- a\n# Title\n", "line 2: a `# ` title may appear only once"},
		"blank rule":        {"- a\n-\n", "line 2: rule must not be blank"},
		"control char":      {"- bell\ainside\n", "control character"},
		"stray CR":          {"- a\rb\n", "carriage return"},
		"invalid UTF-8":     {"- \xff\n", "not valid UTF-8"},
		"byte-order mark":   {"\xef\xbb\xbf- a\n", "byte-order mark"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseContractFile("guidance/contract.md", []byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestContractFileEditsTouchOnlyTheirLines(t *testing.T) {
	// Trailing spaces, mixed endings and a missing final newline survive
	// edits to other lines.
	body := "# Title  \r\n\r\n- first  \r\n## Logs\n- logs rule\n- last"
	c, err := ParseContractFile("guidance/contract.md", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if string(c.Bytes()) != body {
		t.Fatalf("untouched round trip = %q", c.Bytes())
	}
	added, err := c.AddRule("second", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(added.Bytes()), "# Title  \r\n\r\n- first  \r\n- second\r\n## Logs\n- logs rule\n- last"; got != want {
		t.Fatalf("add = %q, want %q", got, want)
	}
	appended, err := c.AddRule("more logs", "Logs")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(appended.Bytes()), "# Title  \r\n\r\n- first  \r\n## Logs\n- logs rule\n- last\r\n- more logs\r\n"; got != want {
		t.Fatalf("add under = %q, want %q", got, want)
	}
	replaced, old, err := c.ReplaceRule(1, "logs moved")
	if err != nil || old != "logs rule" {
		t.Fatalf("replace old=%q err=%v", old, err)
	}
	if got, want := string(replaced.Bytes()), "# Title  \r\n\r\n- first  \r\n## Logs\n- logs moved\n- last"; got != want {
		t.Fatalf("replace = %q, want %q", got, want)
	}
	removed, text, err := c.RemoveRule(0)
	if err != nil || text != "first" {
		t.Fatalf("remove text=%q err=%v", text, err)
	}
	if got, want := string(removed.Bytes()), "# Title  \r\n\r\n## Logs\n- logs rule\n- last"; got != want {
		t.Fatalf("remove = %q, want %q", got, want)
	}
	if removed.Blob == c.Blob || removed.Blob != ContractBlob(removed.Bytes()) {
		t.Fatal("edit did not recompute the blob")
	}
}

func TestContractAddRulePlacement(t *testing.T) {
	for name, tc := range map[string]struct{ body, text, under, want string }{
		"empty file":                 {"", "a", "", "- a\n"},
		"title only":                 {"# T\n", "a", "", "# T\n\n- a\n"},
		"title and blank":            {"# T\n\n", "a", "", "# T\n\n- a\n"},
		"leading group before group": {"# T\n\n## G\n- g\n", "a", "", "# T\n\n- a\n\n## G\n- g\n"},
		"missing heading is created": {"- a\n", "b", "New", "- a\n\n## New\n\n- b\n"},
		"empty heading at end":       {"- a\n\n## G\n", "b", "G", "- a\n\n## G\n\n- b\n"},
		"empty heading before next":  {"## G\n\n## H\n- h\n", "b", "G", "## G\n\n- b\n\n## H\n- h\n"},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := ParseContractFile("guidance/contract.md", []byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.AddRule(tc.text, tc.under)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Bytes()) != tc.want {
				t.Fatalf("got %q, want %q", got.Bytes(), tc.want)
			}
			if _, err := ParseContractFile("guidance/contract.md", got.Bytes()); err != nil {
				t.Fatalf("result does not parse: %v", err)
			}
		})
	}
	if _, err := ParseContractFile("guidance/contract.md", []byte("- tab\tinside\n")); err != nil {
		t.Fatalf("inner tab rejected: %v", err)
	}
	c, _ := ParseContractFile("guidance/contract.md", []byte("- a\n"))
	if _, err := c.AddRule("  a ", ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate add err = %v", err)
	}
	if _, err := inlineContract([]string{"a"}).AddRule("b", "G"); err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("inline --under err = %v", err)
	}
	if _, _, err := c.ReplaceRule(0, "a"); err == nil {
		t.Fatal("replace with identical text succeeded")
	}
}

func TestResolveContractTargetRefusesAmbiguousNumbers(t *testing.T) {
	c := inlineContract([]string{"2", "second", "3"})
	if idx, err := c.ResolveContractTarget("second"); err != nil || idx != 1 {
		t.Fatalf("text target = %d, %v", idx, err)
	}
	if idx, err := c.ResolveContractTarget("3"); err != nil || idx != 2 {
		// "3" is both position 3 and rule 3: the same entry.
		t.Fatalf("matching position/text = %d, %v", idx, err)
	}
	if _, err := c.ResolveContractTarget("2"); err == nil || !strings.Contains(err.Error(), "both position 2") {
		t.Fatalf("ambiguous err = %v", err)
	}
	if _, err := c.ResolveContractTarget("9"); err == nil {
		t.Fatal("out-of-range position resolved")
	}
	if _, err := c.ResolveContractTarget("missing"); err == nil {
		t.Fatal("missing text resolved")
	}
}

func TestValidContractFilePath(t *testing.T) {
	for value, want := range map[string]bool{
		"guidance/contract.md":      true,
		"guidance/org/contract.md":  true,
		"contract.md":               false,
		"guidance/contract.txt":     false,
		"/guidance/contract.md":     false,
		"../guidance/contract.md":   false,
		"guidance/../x/contract.md": false,
		"guidance//contract.md":     false,
		"guidance\\contract.md":     false,
		"https://x/guidance/c.md":   false,
		"guidance/":                 false,
		"":                          false,
	} {
		if got := ValidContractFilePath(value); got != want {
			t.Errorf("ValidContractFilePath(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestLoadContractRefusesSymlinks(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "contract.md"), []byte("- outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	doc := Document{ContractFile: "guidance/contract.md"}

	fileLink := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fileLink, "guidance"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "contract.md"), filepath.Join(fileLink, "guidance", "contract.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContract(fileLink, doc); err == nil || !strings.Contains(err.Error(), "guidance/contract.md is a symlink") {
		t.Fatalf("file symlink err = %v", err)
	}

	dirLink := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dirLink, "guidance")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContract(dirLink, doc); err == nil || !strings.Contains(err.Error(), "guidance is a symlink") {
		t.Fatalf("parent symlink err = %v", err)
	}

	if _, err := LoadContract(t.TempDir(), doc); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing file err = %v", err)
	}
}

func TestValidateContractFileSource(t *testing.T) {
	stubRunningMyVersion(t, "0.47.0", true)
	root := t.TempDir()
	writeTestManifestFile(t, root, "guidance/contract.md", "- one\n")
	valid := `{"manifest_version":1,"min_my_version":"0.47.0","organization":{"id":"acme","name":"Acme"},"contract_file":"guidance/contract.md"}`
	writeTestManifestFile(t, root, "manifest.json", valid)
	if result := ValidateFile(root); len(result.Errors) != 0 || len(result.Warnings) != 0 {
		t.Fatalf("valid contract_file: %#v", result)
	}

	for name, tc := range map[string]struct{ body, want string }{
		"both keys":       {`{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":["x"],"contract_file":"guidance/contract.md"}`, "both contract and contract_file"},
		"explicit empty":  {`{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract":[],"contract_file":"guidance/contract.md"}`, "both contract and contract_file"},
		"outside dir":     {`{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract_file":"contract.md"}`, "under guidance/"},
		"missing file":    {`{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract_file":"guidance/missing.md"}`, "does not exist"},
		"invalid content": {`{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract_file":"guidance/bad.md"}`, "expected a `- ` rule"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeTestManifestFile(t, dir, "guidance/contract.md", "- one\n")
			writeTestManifestFile(t, dir, "guidance/bad.md", "prose\n")
			writeTestManifestFile(t, dir, "manifest.json", tc.body)
			result := ValidateFile(dir)
			if !strings.Contains(strings.Join(result.Errors, "; "), tc.want) {
				t.Fatalf("errors = %v, want %q", result.Errors, tc.want)
			}
		})
	}

	t.Run("missing floor warns", func(t *testing.T) {
		dir := t.TempDir()
		writeTestManifestFile(t, dir, "guidance/contract.md", "- one\n")
		writeTestManifestFile(t, dir, "manifest.json", `{"manifest_version":1,"organization":{"id":"acme","name":"Acme"},"contract_file":"guidance/contract.md"}`)
		result := ValidateFile(dir)
		if len(result.Errors) != 0 || !strings.Contains(strings.Join(result.Warnings, "; "), "contract_file, which needs my v0.47.0") {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("budget counts headings", func(t *testing.T) {
		dir := t.TempDir()
		writeTestManifestFile(t, dir, "guidance/contract.md", "## "+strings.Repeat("h", ContractByteBudget)+"\n- one\n")
		writeTestManifestFile(t, dir, "manifest.json", valid)
		result := ValidateFile(dir)
		if len(result.Errors) != 0 || !strings.Contains(strings.Join(result.Warnings, "; "), "keep it under 150 rules") {
			t.Fatalf("result = %#v", result)
		}
	})
}

func TestContractFileFeatureFloor(t *testing.T) {
	floor, feature := FeatureMinMyVersion(Document{ContractFile: "guidance/contract.md"})
	if floor != "0.47.0" || feature != "contract_file" {
		t.Fatalf("floor = %q %q", floor, feature)
	}
	data, err := EncodeDocument(Document{ManifestVersion: 1, ContractFile: "guidance/contract.md"})
	if err != nil || !strings.Contains(string(data), `"min_my_version": "0.47.0"`) {
		t.Fatalf("EncodeDocument did not stamp the floor: %s %v", data, err)
	}
}

func TestPatchManifestKeysPreservesUnknownKeysAndOrder(t *testing.T) {
	in := `{
  "manifest_version": 1,
  "organization": {"id": "acme", "name": "Acme", "future_nested": {"b": 2, "a": [1, {"z": true}]}},
  "future_top": {"keep": "me"},
  "contract": ["one", "two"],
  "skills": []
}`
	fileValue, _ := json.Marshal("guidance/contract.md")
	out, err := PatchManifestKeys([]byte(in), []KeyEdit{
		{Key: "contract"},
		{Key: "contract_file", Value: fileValue, After: "contract"},
		{Key: "min_my_version", Value: json.RawMessage(`"0.47.0"`), After: "manifest_version"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "manifest_version": 1,
  "min_my_version": "0.47.0",
  "organization": {
    "id": "acme",
    "name": "Acme",
    "future_nested": {
      "b": 2,
      "a": [
        1,
        {
          "z": true
        }
      ]
    }
  },
  "future_top": {
    "keep": "me"
  },
  "contract_file": "guidance/contract.md",
  "skills": []
}
`
	if string(out) != want {
		t.Fatalf("patched:\n%s\nwant:\n%s", out, want)
	}
	// An existing key keeps its position and value is replaced.
	out, err = PatchManifestKeys([]byte(`{"a":1,"min_my_version":"0.45.0","b":2}`), []KeyEdit{{Key: "min_my_version", Value: json.RawMessage(`"0.47.0"`), After: "a"}})
	if err != nil || string(out) != "{\n  \"a\": 1,\n  \"min_my_version\": \"0.47.0\",\n  \"b\": 2\n}\n" {
		t.Fatalf("in-place replace = %s %v", out, err)
	}
	for name, body := range map[string]string{
		"truncated":     `{"manifest_version": 1, "contract": ["a"]`,
		"trailing JSON": `{"manifest_version": 1}{"extra": true}`,
		"not an object": `["a"]`,
		"repeated key":  `{"contract": ["a"], "contract": ["b"]}`,
	} {
		if _, err := PatchManifestKeys([]byte(body), []KeyEdit{{Key: "contract"}}); err == nil {
			t.Errorf("%s: patch accepted malformed input", name)
		}
	}
}

func writeTestManifestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitHashObject(t *testing.T, data []byte) string {
	t.Helper()
	cmd := exec.Command("git", "hash-object", "--stdin")
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git hash-object unavailable: %v", err)
	}
	return strings.TrimSpace(string(out))
}
