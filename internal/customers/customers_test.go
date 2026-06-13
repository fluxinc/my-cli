package customers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListReadsAliasesAndFQDNIDs(t *testing.T) {
	root := Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCustomerTestFile(t, filepath.Join(root.Path, "customers", "sampleco.example.com.md"), `---
id: sampleco.example.com
name: SampleCo
domain: sampleco.example.com
domain_confirmed: true
aliases:
  - sampleco
  - sc
partners:
  - integratorco
---

# SampleCo
`)
	found, err := List([]Root{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].ID != "sampleco.example.com" || len(found[0].Aliases) != 2 {
		t.Fatalf("customers = %#v", found)
	}
	customer, ok := Find(found, "SC")
	if !ok || customer.ID != "sampleco.example.com" {
		t.Fatalf("customer = %#v, ok=%v", customer, ok)
	}
}

func TestListRejectsDuplicateAliasAcrossCustomers(t *testing.T) {
	root := Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCustomerTestFile(t, filepath.Join(root.Path, "customers", "first.example.com.md"), `---
id: first.example.com
aliases:
  - sampleco
---
`)
	writeCustomerTestFile(t, filepath.Join(root.Path, "customers", "second.example.com.md"), `---
id: second.example.com
aliases:
  - sampleco
---
`)
	_, err := List([]Root{root})
	if err == nil || !strings.Contains(err.Error(), "sampleco") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindResolvesIDDomainNameAlias(t *testing.T) {
	found := []Customer{{
		ID:      "sampleco.example.com",
		Name:    "SampleCo",
		Domain:  "sampleco.example.com",
		Aliases: []string{"sampleco", "sc"},
	}}
	for _, value := range []string{
		"sampleco.example.com", // id
		"SAMPLECO.EXAMPLE.COM", // id, case-insensitive
		"SampleCo",             // name
		"sc",                   // alias
		"  sampleco  ",         // alias, trimmed
	} {
		customer, ok := Find(found, value)
		if !ok || customer.ID != "sampleco.example.com" {
			t.Fatalf("Find(%q) = %#v, ok=%v", value, customer, ok)
		}
	}
	if _, ok := Find(found, "nope"); ok {
		t.Fatalf("Find(nope) unexpectedly matched")
	}
	if _, ok := Find(found, ""); ok {
		t.Fatalf("Find(empty) unexpectedly matched")
	}
}

func TestValidIDAndInvalidRecordRejected(t *testing.T) {
	for _, ok := range []string{"sampleco.example.com", "acme-co", "a", "co123"} {
		if !ValidID(ok) {
			t.Fatalf("ValidID(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "Has Space", "UPPER", "-leading", "trailing-", "a..b", "x_y", "with/slash"} {
		if ValidID(bad) {
			t.Fatalf("ValidID(%q) = true, want false", bad)
		}
	}

	root := Root{Manifest: "acme", Workspace: "handbook", Path: t.TempDir()}
	writeCustomerTestFile(t, filepath.Join(root.Path, "customers", "bad.md"), `---
id: Not A Valid ID
---
`)
	if _, err := List([]Root{root}); err == nil {
		t.Fatalf("List accepted an invalid customer id, want error")
	}
}

func writeCustomerTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
