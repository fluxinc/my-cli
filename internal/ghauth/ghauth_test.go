package ghauth

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckGitURLSkipsNonHTTPSGitHub(t *testing.T) {
	called := false
	err := CheckGitURL("git@github.com:example/acme-ai-manifest.git", func(name string, args ...string) ([]byte, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("runner called for SSH URL")
	}
}

func TestCheckGitURLRunsGHAuthStatus(t *testing.T) {
	var gotName string
	var gotArgs []string
	err := CheckGitURL("https://github.com/example/acme-ai-manifest.git", func(name string, args ...string) ([]byte, error) {
		gotName = name
		gotArgs = args
		return []byte("ok"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "gh" || strings.Join(gotArgs, " ") != "auth status --hostname github.com" {
		t.Fatalf("command = %s %v", gotName, gotArgs)
	}
}

func TestCheckGitURLReportsLoginCommand(t *testing.T) {
	err := CheckGitURL("https://github.com/example/acme-ai-manifest.git", func(name string, args ...string) ([]byte, error) {
		return []byte("not logged in"), errors.New("exit 1")
	})
	if err == nil || !strings.Contains(err.Error(), "gh auth login") || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v", err)
	}
}
