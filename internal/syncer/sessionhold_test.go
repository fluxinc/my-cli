package syncer

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHoldsContentWithActiveSessionWork(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-11-note.md"), "note\n")
	adoptFile(t, content, "meetings/2026-06-11-note.md")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
		SessionHolds: []SessionHold{
			{
				SessionID:     "2026-06-11-fix-ab12",
				SessionPath:   filepath.Join(filepath.Dir(content), "work", "2026-06-11-fix-ab12"),
				MountID:       "handbook",
				RepoPath:      content,
				DirtyCount:    2,
				UnlandedCount: 1,
			},
		},
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" {
		t.Fatalf("result = %#v, want held back", result)
	}
	for _, want := range []string{"2026-06-11-fix-ab12", "my session finish 2026-06-11-fix-ab12", "my session status"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("message = %q, want %q", result.Message, want)
		}
	}
}

func TestRunSessionHoldDoesNotBlockInboundPull(t *testing.T) {
	remote, content, manifest := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(manifest, "meetings", "2026-06-11-remote.md"), "remote\n")
	runGit(t, manifest, "add", ".")
	runGit(t, manifest, "commit", "-q", "-m", "remote note")
	runGit(t, manifest, "push", "-q", "origin", "HEAD:master")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Message:    "Sync",
		Visibility: privateVisibility,
		SessionHolds: []SessionHold{
			{SessionID: "2026-06-11-fix-ab12", MountID: "handbook", RepoPath: content, DirtyCount: 1},
		},
	})

	result := findResult(t, report, "handbook")
	if result.Status != "pulled" {
		t.Fatalf("result = %#v, want pulled (session hold must not block inbound)", result)
	}
}

func TestRunSessionHoldOnOtherRepoDoesNotHold(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-11-note.md"), "note\n")
	adoptFile(t, content, "meetings/2026-06-11-note.md")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
		SessionHolds: []SessionHold{
			{SessionID: "2026-06-11-other-cd34", MountID: "docs", RepoPath: filepath.Join(filepath.Dir(content), "docs"), DirtyCount: 1},
		},
	})

	result := findResult(t, report, "handbook")
	if result.Status != "pushed" {
		t.Fatalf("result = %#v, want pushed (unrelated session hold)", result)
	}
}
