package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractSourceCompatibility(t *testing.T) {
	root := t.TempDir()
	writeTestManifestFile(t, root, "guidance/contract.md", "# Rules\n\n## Practice\n- One.\n- Two.\n")
	doc := Document{ManifestVersion: 1, Organization: Organization{ID: "acme", Name: "Acme"}, ContractSource: "guidance/contract.md", Contract: []string{"One.", "Two."}}
	c, err := LoadContract(root, doc)
	if err != nil || c.Warning != "" || c.File == "" || len(c.Rules()) != 2 {
		t.Fatalf("load: %#v %v", c, err)
	}
	if floor, _ := FeatureMinMyVersion(doc); floor != "" {
		t.Fatalf("compatibility introduced floor %s", floor)
	}
	if RaiseMinMyVersion(&doc) {
		t.Fatal("compatibility raised floor")
	}
	for _, tc := range []struct{ name, body, path string }{
		{"drift", "- Changed.\n", doc.ContractSource},
		{"invalid", "not a rule\n", doc.ContractSource},
		{"missing", "", "guidance/missing.md"},
		{"unsafe", "", "../secret.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := doc
			candidate.ContractSource = tc.path
			if tc.body != "" {
				writeTestManifestFile(t, root, tc.path, tc.body)
			}
			got, err := LoadContract(root, candidate)
			if err != nil || got.Warning == "" || got.File != "" || strings.Join(got.Rules(), "/") != "One./Two." {
				t.Fatalf("fallback: %#v %v", got, err)
			}
			result := ValidateDocument(root, candidate)
			if len(result.Errors) != 0 || len(result.Warnings) == 0 {
				t.Fatalf("validation: %#v", result)
			}
			if RequireContractSource(root, candidate) == nil {
				t.Fatal("authoring accepted drift")
			}
		})
	}
}

func TestContractSourceRequiresMirrorAndPreservesEmpty(t *testing.T) {
	doc := Document{ContractSource: "guidance/contract.md"}
	if err := CheckContractSource(doc); err == nil {
		t.Fatal("missing mirror accepted")
	}
	doc.Contract = []string{}
	data, err := EncodeDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Document
	if err := json.Unmarshal(data, &decoded); err != nil || decoded.Contract == nil {
		t.Fatalf("empty mirror lost: %s %v", data, err)
	}
	if strings.Contains(string(data), "min_my_version") {
		t.Fatalf("floor introduced: %s", data)
	}
	doc.ContractFile = "guidance/other.md"
	if CheckContractSource(doc) == nil {
		t.Fatal("ambiguous sources accepted")
	}
}

func TestContractSourceSymlinkDoesNotReadTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.md")
	if err := os.WriteFile(outside, []byte("- Private data.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "guidance"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "guidance", "contract.md")); err != nil {
		t.Skip(err)
	}
	c, err := LoadContract(root, Document{ContractSource: "guidance/contract.md", Contract: []string{"Public rule."}})
	if err != nil || c.Warning == "" || strings.Contains(string(c.Bytes()), "Private") {
		t.Fatalf("unsafe fallback: %#v %v", c, err)
	}
}
