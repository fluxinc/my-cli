package serviceauth

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestReferenceOnlyEndpointScopedStorage(t *testing.T) {
	home := t.TempDir()
	b := Binding{Manifest: "https://example.test/org.git", Service: "registry", URL: "https://registry.example.test/api/v1"}
	e := Entry{Ref: "op://Private/registry/credential", ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	if err := Set(home, b, e); err != nil {
		t.Fatal(err)
	}
	got, ok, err := Lookup(home, b)
	if err != nil || !ok || got != e {
		t.Fatalf("lookup=%v,%v,%v", got, ok, err)
	}
	for _, changed := range []Binding{{Manifest: "https://example.test/other.git", Service: b.Service, URL: b.URL}, {Manifest: b.Manifest, Service: b.Service, URL: "https://other.example.test/api/v1"}} {
		if _, ok, err := Lookup(home, changed); ok || err != nil {
			t.Fatalf("credential escaped binding: %v %v", ok, err)
		}
	}
	path, _ := Path(home)
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	if err := Set(home, b, Entry{Ref: "literal-secret-value"}); err == nil || strings.Contains(err.Error(), "literal-secret-value") {
		t.Fatal("literal was accepted or echoed")
	}
	if err := Unset(home, b); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Lookup(home, b); ok {
		t.Fatal("unset retained reference")
	}
}
func TestMalformedConfigFailsClosed(t *testing.T) {
	home := t.TempDir()
	b := Binding{Manifest: "org", Service: "registry", URL: "https://example.test"}
	if err := Set(home, b, Entry{Ref: "env://REGISTRY_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	path, _ := Path(home)
	os.WriteFile(path, []byte(`{"key":{"auth_ref":"literal-secret"}}`), 0600)
	if _, _, err := Lookup(home, b); err == nil || strings.Contains(err.Error(), "literal-secret") {
		t.Fatal("bad config accepted or secret echoed")
	}
}
