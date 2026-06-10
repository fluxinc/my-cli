package record

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const setScalarsFixture = `---
id: acme-box-1
customer: sampleco.example.com
partner:
status: wait # org vocabulary
identifiers:
  - "SO 100045"
access:
  node: acme-box-1
  ip: 10.0.0.12
inline: [a, b]
source: fleet
---

# acme-box-1

Body stays byte-identical.
`

func writeSetScalarsFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "acme-box-1.md")
	if err := os.WriteFile(path, []byte(setScalarsFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetScalarsReplacesAndAppends(t *testing.T) {
	path := writeSetScalarsFixture(t)
	changes, err := SetScalars(path, map[string]string{
		"status":        "live",
		"partner":       "samplepartner",
		"deployed_site": "Springfield",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %#v", changes)
	}
	if changes[1].Key != "partner" || changes[1].Old != "" || changes[1].New != "samplepartner" {
		t.Fatalf("partner change = %#v", changes[1])
	}
	if changes[2].Key != "status" || changes[2].Old != "wait" || changes[2].New != "live" {
		t.Fatalf("status change = %#v", changes[2])
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"status: live\n",
		"partner: samplepartner\n",
		"deployed_site: Springfield\n---\n",
		"  - \"SO 100045\"\n",
		"  node: acme-box-1\n",
		"  ip: 10.0.0.12\n",
		"Body stays byte-identical.\n",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "org vocabulary") {
		t.Fatalf("replaced line kept its old comment:\n%s", content)
	}
}

func TestSetScalarsRejectsListsNestedBlocksAndID(t *testing.T) {
	path := writeSetScalarsFixture(t)
	for key, value := range map[string]string{
		"identifiers": "SO 1",
		"access":      "x",
		"inline":      "x",
		"id":          "other",
	} {
		if _, err := SetScalars(path, map[string]string{key: value}); err == nil {
			t.Fatalf("SetScalars(%q) did not error", key)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != setScalarsFixture {
		t.Fatalf("rejected updates modified the file:\n%s", data)
	}
}

func TestSetScalarsNoOpLeavesFileUntouched(t *testing.T) {
	path := writeSetScalarsFixture(t)
	changes, err := SetScalars(path, map[string]string{"status": "wait", "missing": ""})
	if err != nil {
		t.Fatal(err)
	}
	if changes != nil {
		t.Fatalf("changes = %#v", changes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != setScalarsFixture {
		t.Fatalf("no-op modified the file:\n%s", data)
	}
}

func TestSetScalarsClearsValue(t *testing.T) {
	path := writeSetScalarsFixture(t)
	changes, err := SetScalars(path, map[string]string{"customer": ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Old != "sampleco.example.com" || changes[0].New != "" {
		t.Fatalf("changes = %#v", changes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\ncustomer:\n") {
		t.Fatalf("customer not cleared without trailing space:\n%s", data)
	}
}

func TestSetScalarsRoundTripsHashValues(t *testing.T) {
	path := writeSetScalarsFixture(t)
	changes, err := SetScalars(path, map[string]string{"ship_to": "Suite #100"})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].New != "Suite #100" {
		t.Fatalf("changes = %#v", changes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `ship_to: "Suite #100"`) {
		t.Fatalf("hash value not quoted:\n%s", data)
	}
	frontmatter, _ := SplitFrontmatter(data)
	if FirstValue(frontmatter, "ship_to") != "Suite #100" {
		t.Fatalf("ship_to round-trip = %q", FirstValue(frontmatter, "ship_to"))
	}
	again, err := SetScalars(path, map[string]string{"ship_to": "Suite #100"})
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Fatalf("second identical set was not a no-op: %#v", again)
	}
}

func TestSetScalarsRejectsDuplicateKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dup.md")
	content := "---\nid: box-1\nstatus: wait\nstatus: live\n---\n\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetScalars(path, map[string]string{"status": "mourn"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("err = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("duplicate-key reject modified the file:\n%s", data)
	}
}

func TestSetScalarsPreservesCRLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crlf.md")
	content := strings.ReplaceAll("---\nid: box-1\nstatus: wait\ncustomer: sampleco\n---\n\nbody\n", "\n", "\r\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	changes, err := SetScalars(path, map[string]string{"status": "live", "partner": "samplepartner"})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %#v", changes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ReplaceAll(string(data), "\r\n", ""), "\n") {
		t.Fatalf("mixed line endings after set:\n%q", data)
	}
	for _, want := range []string{"status: live\r\n", "partner: samplepartner\r\n---\r\n"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("content missing %q:\n%q", want, data)
		}
	}
}

func TestSetScalarsRequiresFrontmatter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.md")
	if err := os.WriteFile(path, []byte("# no frontmatter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetScalars(path, map[string]string{"status": "live"}); err == nil {
		t.Fatal("SetScalars on plain file did not error")
	}
}

func TestScalarFields(t *testing.T) {
	frontmatter, _ := SplitFrontmatter([]byte(setScalarsFixture))
	fields := ScalarFields(frontmatter)
	if fields["status"] != "wait" {
		t.Fatalf("status field = %q", fields["status"])
	}
	if fields["identifiers"] != "SO 100045" {
		t.Fatalf("identifiers field = %q", fields["identifiers"])
	}
	if _, ok := fields["partner"]; ok {
		t.Fatalf("empty partner should be absent: %#v", fields)
	}
}
