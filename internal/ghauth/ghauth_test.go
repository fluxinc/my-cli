package ghauth

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestCheckGitURLChecksSSHGitHub(t *testing.T) {
	var calls []string
	err := CheckGitURL("git@github.com:example/acme-ai-manifest.git", func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "api user":
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		case "api -i repos/example/acme-ai-manifest":
			return []byte("HTTP/2.0 200 OK\n\n{\"id\":29,\"node_id\":\"R_repo\",\"full_name\":\"example/acme-ai-manifest\",\"private\":true,\"permissions\":{\"pull\":true}}"), nil
		default:
			return nil, fmt.Errorf("unexpected command")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, "|") != "gh api user|gh api -i repos/example/acme-ai-manifest" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestCheckGitURLSkipsNonGitHub(t *testing.T) {
	called := false
	if err := CheckGitURL("/tmp/local.git", func(name string, args ...string) ([]byte, error) {
		called = true
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("runner called for a non-GitHub remote")
	}
}

func TestCheckGitURLReportsAuthenticationFailure(t *testing.T) {
	err := CheckGitURL("https://github.com/example/acme-ai-manifest.git", func(name string, args ...string) ([]byte, error) {
		return []byte("not logged in"), errors.New("exit 1")
	})
	if err == nil || !strings.Contains(err.Error(), "authentication_failed") || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v", err)
	}
}
