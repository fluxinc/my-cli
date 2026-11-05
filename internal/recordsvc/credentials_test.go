package recordsvc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeSource struct {
	next       string
	refreshErr error
	dropErr    error
	refreshes  int
	drops      int
}

func (f *fakeSource) Refresh() (string, error) {
	f.refreshes++
	return f.next, f.refreshErr
}

func (f *fakeSource) Drop() error {
	f.drops++
	return f.dropErr
}

func credentialServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(handle))
	client, err := NewClient("registry", server.URL+"/api/v1", "fake-old")
	if err != nil {
		t.Fatal(err)
	}
	return client, server.Close
}

func TestReadRefreshesOnceOn401AndRedactsBothKeys(t *testing.T) {
	var seen []string
	client, stop := credentialServer(t, func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if len(seen) == 1 {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"not_found","message":"fake-old and fake-new were echoed"}`)
	})
	defer stop()
	source := &fakeSource{next: "fake-new"}
	client.UseCredentialSource(source)
	_, err := client.Get(context.Background(), "x")
	var refused *Error
	if !errors.As(err, &refused) || refused.Status != 404 {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "fake-old") || strings.Contains(err.Error(), "fake-new") {
		t.Fatalf("key leaked: %v", err)
	}
	if source.refreshes != 1 || strings.Join(seen, ",") != "Bearer fake-old,Bearer fake-new" {
		t.Fatalf("refreshes=%d seen=%v", source.refreshes, seen)
	}
}

func TestSecond401DropsAndStops(t *testing.T) {
	requests := 0
	client, stop := credentialServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(401)
	})
	defer stop()
	source := &fakeSource{next: "fake-new"}
	client.UseCredentialSource(source)
	_, err := client.Get(context.Background(), "x")
	if err == nil || requests != 2 || source.refreshes != 1 || source.drops != 1 {
		t.Fatalf("%v requests=%d refreshes=%d drops=%d", err, requests, source.refreshes, source.drops)
	}
	// Later reads in this command do not ask again.
	if _, err = client.Get(context.Background(), "x"); err == nil || source.refreshes != 1 {
		t.Fatalf("%v refreshes=%d", err, source.refreshes)
	}
}

func TestWriteIsNeverReplayedOn401(t *testing.T) {
	requests := 0
	client, stop := credentialServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(401)
	})
	defer stop()
	source := &fakeSource{next: "fake-new"}
	client.UseCredentialSource(source)
	_, err := client.Patch(context.Background(), "x", map[string]string{"a": "b"}, nil, nil)
	if err == nil || requests != 1 || source.refreshes != 0 || source.drops != 1 {
		t.Fatalf("%v requests=%d refreshes=%d drops=%d", err, requests, source.refreshes, source.drops)
	}
	if !strings.Contains(err.Error(), "cached key was cleared") {
		t.Fatalf("%v", err)
	}
}

func TestWriteReportsAFailedDrop(t *testing.T) {
	client, stop := credentialServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	defer stop()
	client.UseCredentialSource(&fakeSource{dropErr: errors.New("broker gone")})
	_, err := client.Patch(context.Background(), "x", map[string]string{"a": "b"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "could not be cleared") {
		t.Fatalf("%v", err)
	}
}

func TestForbiddenNeverRefreshes(t *testing.T) {
	client, stop := credentialServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	defer stop()
	source := &fakeSource{next: "fake-new"}
	client.UseCredentialSource(source)
	if _, err := client.Get(context.Background(), "x"); err == nil || source.refreshes != 0 || source.drops != 0 {
		t.Fatalf("%v %+v", err, source)
	}
}

func TestFailedRefreshIsReported(t *testing.T) {
	requests := 0
	client, stop := credentialServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(401)
	})
	defer stop()
	client.UseCredentialSource(&fakeSource{refreshErr: errors.New("prompt cancelled")})
	_, err := client.Get(context.Background(), "x")
	if err == nil || requests != 1 || !strings.Contains(err.Error(), "prompt cancelled") {
		t.Fatalf("%v requests=%d", err, requests)
	}
}
