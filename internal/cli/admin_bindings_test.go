package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestAdminBindingsSetReplaceAndRemove(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `,
  "services": [{"id":"registry","kind":"http","purpose":"Registry","auth_ref":"env://REGISTRY_TOKEN","connection":{"url":"https://registry.example.test/api/v1"}}],
  "data_bindings":{"customers":{"surface":"service:registry"}}`)
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	run := func(args ...string) {
		t.Helper()
		if err := a.run(append([]string{"my", "admin", "bindings"}, append(args, "--manifest-dir", dir, "--json")...)); err != nil {
			t.Fatal(err)
		}
	}
	run("set", "fleet", "--surface", "service:registry", "--field-alias", "device=model")
	doc, _, _ := manifest.LoadDocument(dir)
	if !reflect.DeepEqual(doc.DataBindings["fleet"].SurfaceRefs(), []string{"service:registry"}) || doc.DataBindings["fleet"].FieldAliases["device"] != "model" {
		t.Fatalf("binding: %#v", doc.DataBindings["fleet"])
	}
	run("set", "fleet", "--surface", "service:registry")
	doc, _, _ = manifest.LoadDocument(dir)
	if len(doc.DataBindings["fleet"].FieldAliases) != 0 {
		t.Fatal("replacement kept omitted aliases")
	}
	run("remove", "fleet")
	doc, _, _ = manifest.LoadDocument(dir)
	if _, exists := doc.DataBindings["fleet"]; exists || doc.DataBindings["customers"].Surface != "service:registry" {
		t.Fatalf("remove changed wrong binding: %#v", doc.DataBindings)
	}
}

func TestAdminBindingsRefusalsPreserveManifest(t *testing.T) {
	for _, args := range [][]string{
		{"set", "fleet", "--surface", "service:missing"},
		{"set", "fleet"},
		{"set", "bogus", "--surface", "service:missing"},
		{"remove", "fleet", "--surface", "service:missing"},
		{"remove", "fleet"},
		{"set", "fleet", "--surface", "service:missing", "--field-alias", "a=b", "--field-alias", "a=c"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			writeAdminManifest(t, dir, "")
			path := filepath.Join(dir, "manifest.json")
			before, _ := os.ReadFile(path)
			a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
			err := a.run(append([]string{"my", "admin", "bindings"}, append(args, "--manifest-dir", dir)...))
			if err == nil {
				t.Fatal("expected refusal")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("refusal changed manifest bytes")
			}
		})
	}
}

func TestAdminBindingsCannotBypassGovernedAuthoring(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, `,"governance":{"authorization":{"provider":"github","manifest_repository":"example/control","admin_permission":"admin"}}`)
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, accessRunner: governedAdminRunner("write")}
	err := a.run([]string{"my", "admin", "bindings", "set", "fleet", "--surface", "service:missing", "--manifest-dir", dir, "--force"})
	if err == nil || !strings.Contains(err.Error(), "governed manifest authoring denied") {
		t.Fatalf("expected admin denial: %v", err)
	}
}

func TestAdminHTTPServiceAcceptsURLButRefusesCommand(t *testing.T) {
	dir := t.TempDir()
	writeAdminManifest(t, dir, "")
	a := app{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	base := []string{"my", "admin", "services", "add", "registry", "--manifest-dir", dir, "--kind", "http", "--purpose", "Registry", "--auth-ref", "env://REGISTRY_TOKEN"}
	if err := a.run(append(base, "--connection-url", "https://registry.example.test/api/v1")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	for _, flags := range [][]string{
		{"--connection-command", "bad-command"},
		{"--connection-url", "http://registry.example.test/api/v1"},
		{"--connection-url", "https://user:pw@registry.example.test/api/v1"},
		{"--connection-url", "https://registry.example.test/api/v1?q=1"},
		{"--connection-header", "Authorization=Bearer fictional-secret"},
	} {
		err := a.run(append(append(append([]string{}, base...), "--force"), flags...))
		if err == nil {
			t.Fatalf("HTTP service accepted invalid connection: %v", flags)
		}
		after, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
		if !bytes.Equal(before, after) {
			t.Fatal("invalid HTTP connection mutated manifest")
		}
	}
}
