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
	if result.ReasonCode != "active_session" {
		t.Fatalf("reason code = %q, want active_session", result.ReasonCode)
	}
	wantNext := "git -C " + shellArg(content) + " status --short"
	if result.NextCommand != wantNext {
		t.Fatalf("next command = %q, want %q", result.NextCommand, wantNext)
	}
	for _, want := range []string{"2026-06-11-fix-ab12", "my session finish 2026-06-11-fix-ab12", "my session status"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("message = %q, want %q", result.Message, want)
		}
	}
}

func TestSessionHoldMessageSequencesBaseDirtyBeforeFinish(t *testing.T) {
	hold := SessionHold{
		SessionID:     "2026-06-18-example-7426",
		SessionPath:   "/tmp/my/sessions/2026-06-18-example-7426",
		MountID:       "workspace",
		DirtyCount:    0,
		UnlandedCount: 2,
	}

	// Clean base: point straight at finish, no base-first detour.
	clean := sessionHoldMessage(hold, nil)
	if strings.Contains(clean, "base checkout has uncommitted files") {
		t.Fatalf("clean-base message must not mention base dirt: %q", clean)
	}
	if !strings.Contains(clean, "my session finish 2026-06-18-example-7426 --land|--publish") {
		t.Fatalf("clean-base message = %q, want direct finish guidance", clean)
	}
	if got := sessionHoldNextCommand(hold, "/tmp/my/workspace", nil); got != "my session finish 2026-06-18-example-7426 --land" {
		t.Fatalf("clean-base next command = %q, want session finish", got)
	}

	// Dirty base: `my session finish --land` and `--publish` would refuse via
	// requireBaseReady, so the guidance must sequence the base cleanup first
	// (#28), not bounce the operator into a finish that fails.
	dirty := sessionHoldMessage(hold, []string{"customers/exampleco.md", "fleet/example-device-4.md"})
	for _, want := range []string{
		"base checkout has uncommitted files (customers/exampleco.md, fleet/example-device-4.md)",
		"will block --land/--publish",
		"commit, stash, or discard those base files first",
		"then run my session finish 2026-06-18-example-7426 --land",
		"run my session finish 2026-06-18-example-7426 --discard",
		"my session status",
	} {
		if !strings.Contains(dirty, want) {
			t.Fatalf("dirty-base message = %q, want substring %q", dirty, want)
		}
	}
	if got := sessionHoldNextCommand(hold, "/tmp/my/workspace", []string{"customers/exampleco.md"}); got != "git -C /tmp/my/workspace status --short" {
		t.Fatalf("dirty-base next command = %q, want base status", got)
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
