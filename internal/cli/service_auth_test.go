package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/serviceauth"
)

func TestPersonalServiceAuthVerifiesAndOverridesFleet(t *testing.T) {
	const personal = "fake-personal-token"
	t.Setenv("PERSONAL_REGISTRY_TOKEN", personal)
	var observed []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = append(observed, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/me":
			if r.Header.Get("Authorization") != "Bearer "+personal {
				w.WriteHeader(401)
				fmt.Fprint(w, `{"error":"unauthorized"}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"issuer": map[string]any{"name": "Fictional User " + personal}, "token": map[string]any{"name": "Laptop", "scopes": []string{"read"}, "expires_at": time.Now().Add(30 * 24 * time.Hour)}})
		case "/api/v1/records":
			fmt.Fprint(w, `{"records":[],"next_page":null}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	home, a, stdout, _ := setupServiceFleet(t, server.URL)
	if err := a.runServicesAuth([]string{"set", "registry", "--ref", "env://PERSONAL_REGISTRY_TOKEN", "--home", home}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), personal) {
		t.Fatal("credential echoed")
	}
	svc, err := boundFleetService(home, "acme", "")
	if err != nil {
		t.Fatal(err)
	}
	client, err := svc.connect()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := serviceContext()
	defer cancel()
	if _, err = client.Search(ctx, "query", false); err != nil {
		t.Fatal(err)
	}
	if observed[len(observed)-1] != "Bearer "+personal {
		t.Fatal("fleet did not use personal credential")
	}
	path, _ := serviceauth.Path(home)
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), personal) {
		t.Fatal("credential stored")
	}
	docs, err := loadRegisteredDocs(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	service := docs[0].doc.Services[0]
	if service.AuthRef != "env://ACME_REGISTRY_TOKEN" {
		t.Fatal("shared auth changed")
	}
	if err := a.runServicesAuth([]string{"unset", "registry", "--home", home}); err != nil {
		t.Fatal(err)
	}
	svc, _ = boundFleetService(home, "acme", "")
	client, _ = svc.connect()
	client.Search(ctx, "query", false)
	if observed[len(observed)-1] != "Bearer "+fakeRegistryToken {
		t.Fatal("unset did not restore org reference")
	}
}
func TestServiceAuthRefusalLeavesPriorReference(t *testing.T) {
	t.Setenv("REJECTED_REGISTRY_TOKEN", "fake-rejected-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"unauthorized"}`)
	}))
	defer server.Close()
	home, a, _, _ := setupServiceFleet(t, server.URL)
	docs, _ := loadRegisteredDocs(home, "acme")
	b := serviceBinding(docs[0].ref, docs[0].doc.Services[0])
	serviceauth.Set(home, b, serviceauth.Entry{Ref: "env://ACME_REGISTRY_TOKEN"})
	if err := a.runServicesAuth([]string{"set", "registry", "--ref", "env://REJECTED_REGISTRY_TOKEN", "--home", home}); err == nil {
		t.Fatal("rejected key saved")
	}
	entry, _, _ := serviceauth.Lookup(home, b)
	if entry.Ref != "env://ACME_REGISTRY_TOKEN" {
		t.Fatal("prior reference changed")
	}
}
func TestDoctorUsesPersonalReferencesAndWarnsBeforeExpiry(t *testing.T) {
	home := t.TempDir()
	ref := manifest.Ref{Name: "acme", GitURL: "https://example.test/manifest.git"}
	service := manifest.Service{ID: "registry", Kind: "http", AuthRef: "env://UNSET_ORG_REF", AuthSetupURL: "https://example.test/keys", Connection: manifest.ServiceConnection{URL: "https://example.test/api/v1"}}
	t.Setenv("PERSONAL_REGISTRY_TOKEN", "not-read-by-doctor")
	serviceauth.Set(home, serviceBinding(ref, service), serviceauth.Entry{Ref: "env://PERSONAL_REGISTRY_TOKEN", ExpiresAt: time.Now().Add(24 * time.Hour)})
	got := doctorPersonalServices(home, ref, []manifest.Service{service})
	detail := fmt.Sprint(got)
	if strings.Contains(detail, "UNSET_ORG_REF") || !strings.Contains(detail, "expires") || !strings.Contains(detail, "https://example.test/keys") {
		t.Fatalf("doctor: %s", detail)
	}
	if strings.Contains(detail, "not-read-by-doctor") {
		t.Fatal("doctor resolved credential")
	}
}

func TestServiceAuthSetupURLAuthoringAndOnboarding(t *testing.T) {
	manifestDir := t.TempDir()
	writeAdminManifest(t, manifestDir, adminRoleServiceFixtureExtra())
	var stdout, stderr strings.Builder
	a := app{stdout: &stdout, stderr: &stderr}
	if err := a.run([]string{"my", "admin", "services", "add", "registry", "--manifest-dir", manifestDir, "--kind", "http", "--purpose", "Example registry", "--auth-ref", "env://REGISTRY_TOKEN", "--connection-url", "https://registry.example.test/api/v1", "--auth-setup-url", "https://registry.example.test/keys"}); err != nil {
		t.Fatal(err)
	}
	doc, _, err := manifest.LoadDocument(manifestDir)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Services[len(doc.Services)-1].AuthSetupURL != "https://registry.example.test/keys" {
		t.Fatalf("setup URL not saved: %v", doc.Services)
	}
	stdout.Reset()
	a.printOnboardIntro(registeredDoc{ref: manifest.Ref{Name: "acme"}, doc: doc}, "/example", true)
	if !strings.Contains(stdout.String(), "https://registry.example.test/keys") || !strings.Contains(stdout.String(), "my services auth set registry") {
		t.Fatalf("onboarding missing setup: %s", stdout.String())
	}
	if err := a.run([]string{"my", "admin", "services", "edit", "registry", "--manifest-dir", manifestDir, "--auth-setup-url", "https://user:password@example.test/keys"}); err == nil {
		t.Fatal("credential-bearing setup URL accepted")
	}
}
