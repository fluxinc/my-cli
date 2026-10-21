package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFleetCommentResolvesAndUsesCurrentRevisions(t *testing.T) {
	registry := newFakeRegistry()
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/comments") {
			posts++
			if r.Header.Get("Authorization") != "Bearer "+fakeRegistryToken {
				t.Error("missing authentication")
			}
			if r.Header.Get("Idempotency-Key") != "unique-comment-key-123" {
				t.Error("missing key")
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["revision"] != float64(3) || body["order_revision"] != float64(1) || body["body"] != "Hello @alex" {
				t.Errorf("wrong payload: %v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			fmt.Fprint(w, `{"note":{"body":"Hello @alex"},"revision":4}`)
			return
		}
		registry.ServeHTTP(w, r)
	}))
	defer server.Close()
	home, a, out, _ := setupServiceFleet(t, server.URL)
	a.stdin = strings.NewReader("Hello @alex\n")
	err := a.runFleetComment([]string{"SO-1001", "--body-file", "-", "--idempotency-key", "unique-comment-key-123", "--home", home, "--manifest", "acme", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if posts != 1 || !strings.Contains(out.String(), `"created": true`) {
		t.Fatalf("posts=%d output=%s", posts, out)
	}
	if strings.Contains(out.String(), fakeRegistryToken) {
		t.Fatal("credential echoed")
	}
}
func TestFleetCommentRejectsAmbiguousIdentifiersWithoutPosting(t *testing.T) {
	registry := newFakeRegistry()
	posts := 0
	// Both records claim the same identifier.
	registry.records["reg:box:1"]["order"].(map[string]any)["so_number"] = "SHARED-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
		}
		registry.ServeHTTP(w, r)
	}))
	defer server.Close()
	home, a, _, _ := setupServiceFleet(t, server.URL)
	a.stdin = strings.NewReader("Hello")
	err := a.runFleetComment([]string{"SHARED-1", "--body-file", "-", "--idempotency-key", "unique-comment-key-123", "--home", home, "--manifest", "acme"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || posts != 0 {
		t.Fatalf("err=%v posts=%d", err, posts)
	}
}
