//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fluxinc/my-cli/internal/manifest"
	"github.com/fluxinc/my-cli/internal/umbrella"
)

func TestReviewPinOfflineFallbackOverridesTransportSpecificAllow(t *testing.T) {
	_, origin, mount, _ := pinFixture(t)
	missing, _ := partialCloneMissing(t, origin, mount, false)
	pinGit(t, mount, "config", "protocol.file.allow", "always")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	// Simulate a supported older Git that ignores GIT_NO_LAZY_FETCH. The
	// transport fallback must independently enforce the offline promise.
	quotedGit := "'" + strings.ReplaceAll(realGit, "'", "'\\''") + "'"
	script := "#!/bin/sh\nunset GIT_NO_LAZY_FETCH\nexec " + quotedGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, exit, err := runPinGit(mount, "cat-file", "-e", missing); err != nil || exit == 0 {
		t.Fatalf("offline runner fetched a missing object with protocol.file.allow=always: exit=%d err=%v", exit, err)
	}
}

func TestReviewCompileUmbrellaRejectsCopiedIdentity(t *testing.T) {
	root, _, _, _ := pinFixture(t)
	ws, err := umbrella.LoadWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(root, "sessions", "copied-identity")
	if err := os.MkdirAll(filepath.Join(session, ".my-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := umbrella.SaveWorkspace(session, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCompileUmbrella(session, manifest.Document{Organization: manifest.Organization{ID: "acme"}}); err == nil {
		t.Fatal("accepted copied identity whose workspace_root names the base umbrella, not this session")
	}
}
