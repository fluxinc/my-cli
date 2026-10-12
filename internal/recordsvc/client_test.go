package recordsvc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
)

const testToken = "test-secret-token-7f3a"

func TestResolveAuthNeverPutsTheSecretInErrors(t *testing.T) {
	t.Setenv("ACME_REGISTRY_TOKEN", testToken)
	got, err := ResolveAuth("env://ACME_REGISTRY_TOKEN", nil)
	if err != nil || got != testToken {
		t.Fatalf("env auth = %q, %v", got, err)
	}
	if _, err := ResolveAuth("env://ACME_MISSING_TOKEN", nil); err == nil || !strings.Contains(err.Error(), "ACME_MISSING_TOKEN is not set") {
		t.Fatalf("missing env err = %v", err)
	}

	got, err = ResolveAuth("op://Example/registry/token", func(name string, args ...string) ([]byte, error) {
		if name != "op" || strings.Join(args, " ") != "read --no-newline op://Example/registry/token" {
			t.Fatalf("ran %s %v", name, args)
		}
		return []byte(testToken + "\n"), nil
	})
	if err != nil || got != testToken {
		t.Fatalf("op auth = %q, %v", got, err)
	}
	_, err = ResolveAuth("op://Example/registry/token", func(string, ...string) ([]byte, error) {
		// A helper that fails after printing something secret-looking: the
		// error must not carry its output.
		cmd := exec.Command("sh", "-c", "echo "+testToken+"; exit 3")
		out, err := cmd.Output()
		_ = out
		return []byte(testToken), err
	})
	if err == nil || strings.Contains(err.Error(), testToken) || !strings.Contains(err.Error(), "exit 3") {
		t.Fatalf("op failure err = %v", err)
	}
	if _, err := ResolveAuth("broker://vault/registry", nil); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("broker err = %v", err)
	}
}

func TestClientRefusesUnsafeBaseURLs(t *testing.T) {
	for _, base := range []string{
		"https://user:pw@registry.example.test/api",
		"https://registry.example.test/api?key=x",
		"http://registry.example.test/api",
	} {
		if _, err := NewClient("registry", base, testToken); err == nil {
			t.Fatalf("NewClient(%q) accepted", base)
		}
	}
}

func TestCredentialNeverFollowsARedirectToAnotherOrigin(t *testing.T) {
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
		w.Write([]byte(`{"id":"x"}`))
	}))
	defer other.Close()
	// 127.0.0.1 and localhost are different origins for the same machine.
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, otherURL+"/steal", http.StatusFound)
	}))
	defer service.Close()

	client, err := NewClient("registry", service.URL+"/api/v1", testToken)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Get(context.Background(), "reg:1")
	var apiErr *Error
	if !errors.As(err, &apiErr) || !apiErr.Unavailable() || !strings.Contains(err.Error(), "another origin") {
		t.Fatalf("err = %v", err)
	}
	if leaked {
		t.Fatal("the credential reached another origin")
	}
}

func TestErrorsKeepStructuredFieldsAndScrubTheCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":"unauthorized","message":"bad token ` + r.Header.Get("Authorization") + `"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/records/reg:1":
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{"error": "stale", "message": "changed; you sent " + testToken,
				"record": map[string]any{"id": "reg:1", "revision": 4, "stage": "ship"}})
		default:
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("<html>stack trace with " + testToken + "</html>"))
		}
	}))
	defer server.Close()
	client, _ := NewClient("registry", server.URL+"/api/v1", testToken)

	_, err := client.Patch(context.Background(), "reg:1", map[string]string{"serial": "1"}, map[string]any{"revision": 3})
	var apiErr *Error
	if !errors.As(err, &apiErr) || !apiErr.Stale() || apiErr.Record.ID() != "reg:1" {
		t.Fatalf("stale err = %#v", err)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("error echoes the credential: %v", err)
	}

	_, err = client.Get(context.Background(), "other")
	if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "stack trace") {
		t.Fatalf("raw body leaked: %v", err)
	}

	wrong, _ := NewClient("registry", server.URL+"/api/v1", "wrong-token")
	_, err = wrong.Get(context.Background(), "reg:1")
	if err == nil || strings.Contains(err.Error(), "wrong-token") || !strings.Contains(err.Error(), "auth_ref") {
		t.Fatalf("401 err = %v", err)
	}
}

func TestRevisionsAndProjection(t *testing.T) {
	var rec Record
	json.Unmarshal([]byte(`{
		"id": "reg:box:7", "revision": 5, "order_revision": 2, "other": 1, "stage": "install", "archived": false,
		"site": "Main campus", "ship_to": "Dock 4",
		"customer": {"name": "Sample Clinic", "workspace_customer_id": "sampleco"},
		"order": {"so_number": "SO-1001", "po_number": "PO-9", "po_number_2": null, "partner": "integratorco"},
		"box": {"label": "acme-box-7", "serial_number": "SN-7", "device_model": "Unit X", "hostname": "acme-box-7.local",
		        "config_branch": "sample/site-7", "installed_on": "2026-09-30", "guid": ""},
		"identifiers": {"fl": [{"value": "400-1"}, {"value": "400-2"}], "mac": [{"value": "AA:BB:CC:00:11:22"}]},
		"shipments": [{"tracking_number": "000123"}],
		"sources": [{"source": "legacy", "source_id": "acme-box-7-old"}],
		"contacts": [{"role": "pm", "name": "Alex Doe"}]
	}`), &rec)

	if got := rec.Revisions(); len(got) != 2 || got["revision"] == nil || got["order_revision"] == nil {
		t.Fatalf("revisions = %v", got)
	}
	f := ToFleet("registry", rec)
	want := []string{"SO-1001", "PO-9", "SN-7", "acme-box-7", "acme-box-7.local", "400-1", "400-2", "AA:BB:CC:00:11:22", "000123", "acme-box-7-old"}
	if strings.Join(f.Identifiers, ",") != strings.Join(want, ",") {
		t.Fatalf("identifiers = %v, want %v", f.Identifiers, want)
	}
	if f.ID != "reg:box:7" || f.Customer != "sampleco" || f.Partner != "integratorco" || f.Status != "install" ||
		f.ShipTo != "Dock 4" || f.DeployedSite != "Main campus" || f.Surface != "service:registry" || f.Contacts[0] != "pm: Alex Doe" {
		t.Fatalf("projection = %+v", f)
	}
}

func TestOpaqueIDsAreEscapedExactlyOnce(t *testing.T) {
	id := "asset A/B%é"
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		json.NewEncoder(w).Encode(map[string]any{"id": id})
	}))
	defer server.Close()
	client, _ := NewClient("registry", server.URL+"/api", "")
	ctx := context.Background()
	if _, err := client.Get(ctx, id); err != nil {
		t.Fatal(err)
	}
	client.Patch(ctx, id, map[string]string{"serial": "1"}, map[string]any{"revision": 1})
	client.Transition(ctx, id, "advance", nil, map[string]any{"revision": 1})
	want := "/api/records/" + url.PathEscape(id)
	if len(paths) != 3 || paths[0] != want || paths[1] != want || paths[2] != want+"/transitions" {
		t.Fatalf("paths = %v, want %s (escaped once)", paths, want)
	}
}

func TestEveryStructuredErrorValueIsScrubbed(t *testing.T) {
	for _, code := range []string{"echo-" + testToken, "confirmation_required"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]any{
				"error": code, "affected": []string{"reg:" + testToken}, "review_url": "https://registry.example.test/?q=" + testToken,
				"record": map[string]any{"id": "reg:1", testToken: "key", "order": map[string]any{"so_number": testToken},
					"notes": []any{map[string]any{"body": testToken}}},
			})
		}))
		client, _ := NewClient("registry", server.URL+"/api", testToken)
		_, err := client.Get(context.Background(), "reg:1")
		server.Close()
		var apiErr *Error
		if !errors.As(err, &apiErr) {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(apiErr)
		if strings.Contains(err.Error(), testToken) || strings.Contains(string(raw), testToken) {
			t.Fatalf("%s: the credential survives in %s", code, raw)
		}
		if code == "confirmation_required" && (len(apiErr.Affected) != 1 || apiErr.ReviewURL == "") {
			t.Fatalf("affected/review_url dropped: %+v", apiErr)
		}
	}
}
