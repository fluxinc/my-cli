package syncer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAutoPushesPrivateContentAndPullsCleanSibling(t *testing.T) {
	remote, content, manifest := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	adoptFile(t, content, "meetings/2026-06-08-sync.md")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	contentResult := findResult(t, report, "handbook")
	if contentResult.Status != "pushed" {
		t.Fatalf("content status = %q, want pushed; report = %#v", contentResult.Status, report)
	}
	manifestResult := findResult(t, report, "manifest")
	if manifestResult.Status != "pulled" {
		t.Fatalf("manifest status = %q, want pulled; report = %#v", manifestResult.Status, report)
	}
	if got := gitOut(t, manifest, "rev-parse", "HEAD"); got != gitOut(t, content, "rev-parse", "HEAD") {
		t.Fatalf("sibling checkout did not fast-forward: manifest %s content %s", got, gitOut(t, content, "rev-parse", "HEAD"))
	}
	if _, err := os.Stat(filepath.Join(manifest, "meetings", "2026-06-08-sync.md")); err != nil {
		t.Fatalf("manifest checkout missed pushed content: %v", err)
	}
}

func TestRunHoldsUnadoptedUntrackedContent(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-draft.md"), "draft\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" || !strings.Contains(result.Message, "unadopted untracked content") {
		t.Fatalf("result = %#v, want unadopted content hold", result)
	}
	if result.ReasonCode != "unadopted_content" {
		t.Fatalf("reason code = %q, want unadopted_content", result.ReasonCode)
	}
	if result.NextCommand != "my record adopt meetings/2026-06-08-draft.md" {
		t.Fatalf("next command = %q, want record adopt", result.NextCommand)
	}
	if !strings.Contains(result.Message, "meetings/2026-06-08-draft.md") ||
		!strings.Contains(result.Message, "my record adopt") {
		t.Fatalf("message = %q, want file name and adopt remediation", result.Message)
	}
	if log := gitOut(t, content, "log", "--oneline", "--all"); strings.Contains(log, "Add meeting note") {
		t.Fatalf("content change was committed despite unadopted hold:\n%s", log)
	}
}

func TestRunPublishesExplicitlyStagedNewContent(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	runGit(t, content, "add", "meetings/2026-06-08-sync.md")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "pushed" {
		t.Fatalf("result = %#v, want staged new content to publish", result)
	}
}

func TestRunPRDelegatesOnlyAfterSyncSafetyGates(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-07-17-governed.md"), "governed\n")
	runGit(t, content, "add", "meetings/2026-07-17-governed.md")
	called := 0
	report := Run([]Entry{{
		ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote,
		LocalPath: content, ContentPaths: []string{"meetings"},
	}}, Options{
		Publish: "pr", Message: "Add governed meeting",
		PRPublisher: func(request PRRequest) PRResult {
			called++
			if request.Entry.ID != "handbook" || request.Branch == "" || request.Upstream == "" ||
				request.Head == "" || request.Message != "Add governed meeting" || len(request.Dirty) != 1 {
				t.Fatalf("request = %#v", request)
			}
			return PRResult{
				Status: "pull request opened", ReasonCode: "governance_pr_opened",
				Message: "https://github.com/example/handbook/pull/1", Changed: request.Dirty,
				PRURL: "https://github.com/example/handbook/pull/1", PRHeadSHA: strings.Repeat("a", 40), PRBase: "master",
			}
		},
	})
	result := findResult(t, report, "handbook")
	if called != 1 || result.Status != "pull request opened" || result.ReasonCode != "governance_pr_opened" ||
		result.PRURL == "" || result.PRHeadSHA == "" || result.PRBase != "master" {
		t.Fatalf("called=%d result=%#v", called, result)
	}
}

func TestRunPRRefusesMissingPublisherAndOutsideContent(t *testing.T) {
	t.Run("publisher missing", func(t *testing.T) {
		remote, content, _ := setupTwoCheckoutRemote(t)
		writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
		runGit(t, content, "add", "meetings/new.md")
		report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{Publish: "pr"})
		result := findResult(t, report, "handbook")
		if result.Status != "held back" || result.ReasonCode != "pr_publisher_unavailable" {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("outside content", func(t *testing.T) {
		remote, content, _ := setupTwoCheckoutRemote(t)
		writeFile(t, filepath.Join(content, "scratch", "new.md"), "new\n")
		runGit(t, content, "add", "scratch/new.md")
		called := false
		report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{
			Publish: "pr", PRPublisher: func(PRRequest) PRResult { called = true; return PRResult{} },
		})
		result := findResult(t, report, "handbook")
		if called || result.Status != "held back" || result.ReasonCode != "pr_outside_content_paths" {
			t.Fatalf("called=%t result=%#v", called, result)
		}
	})
}

func TestRunAutoHoldsWorkspaceRoleEvenWhenContentIsAdopted(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	adoptFile(t, content, "meetings/2026-06-08-sync.md")

	report := Run([]Entry{
		{ID: "workspace", Role: "workspace", Kind: "workspace", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	result := findResult(t, report, "workspace")
	if result.Status != "held back" || !strings.Contains(result.Message, "content mounts") {
		t.Fatalf("result = %#v, want workspace role held", result)
	}
	if result.ReasonCode != "auto_non_content" || result.NextCommand != "my sync --publish direct --print" {
		t.Fatalf("result = %#v, want auto_non_content with direct-print next command", result)
	}
	if log := gitOut(t, content, "log", "--oneline", "--all"); strings.Contains(log, "Add meeting note") {
		t.Fatalf("workspace-role change was committed despite auto hold:\n%s", log)
	}
}

func TestRunAutoHoldNextCommandsForPolicyAndContentScope(t *testing.T) {
	t.Run("not content-only", func(t *testing.T) {
		remote, content, _ := setupTwoCheckoutRemote(t)
		writeFile(t, filepath.Join(content, "scratch", "local.txt"), "local\n")

		report := Run([]Entry{
			{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		}, Options{
			Publish:    "auto",
			Message:    "Add local file",
			Visibility: privateVisibility,
		})

		result := findResult(t, report, "handbook")
		if result.Status != "held back" || result.ReasonCode != "not_content_only" {
			t.Fatalf("result = %#v, want not_content_only hold", result)
		}
		if result.NextCommand != "my doctor" {
			t.Fatalf("next command = %q, want my doctor", result.NextCommand)
		}
	})

	t.Run("privacy unconfirmed", func(t *testing.T) {
		remote, content, _ := setupTwoCheckoutRemote(t)
		writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
		adoptFile(t, content, "meetings/2026-06-08-sync.md")

		report := Run([]Entry{
			{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		}, Options{
			Publish: "auto",
			Message: "Add meeting note",
		})

		result := findResult(t, report, "handbook")
		if result.Status != "held back" || result.ReasonCode != "privacy_unconfirmed" {
			t.Fatalf("result = %#v, want privacy_unconfirmed hold", result)
		}
		if result.NextCommand != "my doctor" {
			t.Fatalf("next command = %q, want my doctor (must not steer toward the gate-bypassing direct publish)", result.NextCommand)
		}
	})
}

func TestRunHoldsDirtyBehindCheckoutWithBaseFirstNextCommand(t *testing.T) {
	remote, content, writer := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(writer, "meetings", "2026-06-08-remote.md"), "remote\n")
	runGit(t, writer, "add", ".")
	runGit(t, writer, "commit", "-q", "-m", "remote meeting")
	runGit(t, writer, "push", "-q", "origin", "HEAD:master")
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-local.md"), "local\n")
	adoptFile(t, content, "meetings/2026-06-08-local.md")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Message:    "Add local meeting",
		Visibility: privateVisibility,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" || result.ReasonCode != "dirty_behind" {
		t.Fatalf("result = %#v, want dirty_behind hold", result)
	}
	wantNext := "git -C " + shellArg(content) + " status --short"
	if result.NextCommand != wantNext {
		t.Fatalf("next command = %q, want %q", result.NextCommand, wantNext)
	}
	for _, want := range []string{
		"remote has new commits and checkout has uncommitted files",
		"commit, stash, or discard local files",
		"then run my sync",
	} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("message = %q, want substring %q", result.Message, want)
		}
	}
}

func TestRunHoldsDivergedCheckoutWithDoctorNextCommand(t *testing.T) {
	remote, content, writer := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-local.md"), "local\n")
	runGit(t, content, "add", ".")
	runGit(t, content, "commit", "-q", "-m", "local meeting")
	writeFile(t, filepath.Join(writer, "meetings", "2026-06-08-remote.md"), "remote\n")
	runGit(t, writer, "add", ".")
	runGit(t, writer, "commit", "-q", "-m", "remote meeting")
	runGit(t, writer, "push", "-q", "origin", "HEAD:master")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Message:    "Add local meeting",
		Visibility: privateVisibility,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" || result.ReasonCode != "diverged" {
		t.Fatalf("result = %#v, want diverged hold", result)
	}
	if result.NextCommand != "my doctor" {
		t.Fatalf("next command = %q, want my doctor", result.NextCommand)
	}
	for _, want := range []string{
		"local and remote both have commits",
		"ahead 1, behind 1",
		"reconcile divergent history",
	} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("message = %q, want substring %q", result.Message, want)
		}
	}
}

func TestRunHoldsDuplicateRemoteWhenBothCheckoutsHavePendingChanges(t *testing.T) {
	remote, content, manifest := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	writeFile(t, filepath.Join(manifest, "catalog", "products.json"), "[]\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	for _, id := range []string{"handbook", "manifest"} {
		result := findResult(t, report, id)
		if result.Status != "held back" || !strings.Contains(result.Message, "another checkout of the same remote") {
			t.Fatalf("%s result = %#v, want duplicate-remote hold", id, result)
		}
	}
	if log := gitOut(t, content, "log", "--oneline", "--all"); strings.Contains(log, "Add meeting note") {
		t.Fatalf("content change was committed despite duplicate hold:\n%s", log)
	}
}

func TestRunReportsLocalOnlyRepoWithoutOrigin(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "meetings", "README.md"), "seed\n")
	runGit(t, repo, "init", "-q")
	configGitUser(t, repo)
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "seed")

	report := Run([]Entry{
		{ID: "workspace", Role: "content", Kind: "handbook", GitURL: repo, LocalPath: repo, ContentPaths: []string{"meetings"}},
	}, Options{
		Publish:    "auto",
		Visibility: privateVisibility,
	})

	result := findResult(t, report, "workspace")
	if result.Status != "local-only" {
		t.Fatalf("result = %#v, want local-only (unpublished init repo must not fail)", result)
	}
	if !strings.Contains(result.Message, "no origin remote") {
		t.Fatalf("message = %q, want no-origin explanation", result.Message)
	}
}

func TestRunHeldBackReasonCodesForInspectHolds(t *testing.T) {
	t.Run("not cloned", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		report := Run([]Entry{
			{ID: "handbook", Role: "content", Kind: "handbook", GitURL: "https://github.com/acme/handbook.git", LocalPath: missing},
		}, Options{Publish: "auto"})

		result := findResult(t, report, "handbook")
		if result.Status != "held back" || result.ReasonCode != "not_cloned" {
			t.Fatalf("result = %#v, want not_cloned hold", result)
		}
		if result.NextCommand != "my setup" {
			t.Fatalf("next command = %q, want my setup", result.NextCommand)
		}
	})

	t.Run("detached head", func(t *testing.T) {
		remote, content, _ := setupTwoCheckoutRemote(t)
		runGit(t, content, "checkout", "-q", "--detach")
		report := Run([]Entry{
			{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		}, Options{
			Publish:    "auto",
			Visibility: privateVisibility,
		})

		result := findResult(t, report, "handbook")
		if result.Status != "held back" || result.ReasonCode != "detached_head" {
			t.Fatalf("result = %#v, want detached_head hold", result)
		}
		if result.NextCommand != "my doctor" {
			t.Fatalf("next command = %q, want my doctor", result.NextCommand)
		}
	})
}

func TestRunGnitDryRunPlansApprovedContentThroughGnit(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	gnitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	setupGnitControlRoot(t, gnitRoot)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	adoptFile(t, content, "meetings/2026-06-08-sync.md")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		DryRun:     true,
		Message:    "Add meeting note",
		Visibility: privateVisibility,
	})

	if report.Backend != "gnit" {
		t.Fatalf("backend = %q, want gnit", report.Backend)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "dry-run" || result.Direction != "outbound" {
		t.Fatalf("result = %#v, want outbound dry-run", result)
	}
	for _, want := range []string{"commit My AI-approved", "publish with gnit"} {
		if !strings.Contains(result.Message, want) {
			t.Fatalf("message = %q, missing %q", result.Message, want)
		}
	}
}

func TestRunGnitPublishesWithWorkspaceRelativePathsAndCommit(t *testing.T) {
	remote, gnitRoot, content, _ := setupGnitWorkspaceWithDuplicateRemote(t)
	setupGnitControlRoot(t, gnitRoot)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	adoptFile(t, content, "meetings/2026-06-08-sync.md")
	var calls []string

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		Message:    "Add meeting note",
		Visibility: privateVisibility,
		DirRunner: func(dir, name string, args ...string) ([]byte, error) {
			calls = append(calls, dir+" "+name+" "+strings.Join(args, " "))
			return []byte("ok\n"), nil
		},
	})

	result := findResult(t, report, "handbook")
	if result.Status != "pushed" {
		t.Fatalf("result = %#v, want pushed", result)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %#v, want add/commit/push", calls)
	}
	if !strings.Contains(calls[0], " gnit add handbook/meetings") || strings.Contains(calls[0], content) {
		t.Fatalf("add call = %q, want workspace-relative path", calls[0])
	}
	if !strings.Contains(calls[1], " gnit commit -m Add meeting note") {
		t.Fatalf("commit call = %q, want gnit commit", calls[1])
	}
	if !strings.Contains(calls[2], " gnit push") {
		t.Fatalf("push call = %q, want gnit push", calls[2])
	}
}

func TestRunGnitStagesApprovedRootsForRenames(t *testing.T) {
	remote, gnitRoot, content, _ := setupGnitWorkspaceWithDuplicateRemote(t)
	setupGnitControlRoot(t, gnitRoot)
	writeFile(t, filepath.Join(content, "meetings", "old.md"), "old\n")
	runGit(t, content, "add", "meetings/old.md")
	runGit(t, content, "commit", "-q", "-m", "seed old meeting")
	runGit(t, content, "push", "-q", "origin", "HEAD:master")
	runGit(t, content, "mv", "meetings/old.md", "meetings/new.md")
	var calls []string

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		Message:    "Rename meeting note",
		Visibility: privateVisibility,
		DirRunner: func(dir, name string, args ...string) ([]byte, error) {
			calls = append(calls, dir+" "+name+" "+strings.Join(args, " "))
			return []byte("ok\n"), nil
		},
	})

	result := findResult(t, report, "handbook")
	if result.Status != "pushed" {
		t.Fatalf("result = %#v, want pushed", result)
	}
	if len(calls) == 0 {
		t.Fatalf("calls = %#v, want gnit add call", calls)
	}
	if !strings.Contains(calls[0], " gnit add handbook/meetings") {
		t.Fatalf("add call = %q, want approved root staging for rename", calls[0])
	}
	if strings.Contains(calls[0], "handbook/meetings/new.md") || strings.Contains(calls[0], "handbook/meetings/old.md") {
		t.Fatalf("add call = %q, want root staging instead of individual rename paths", calls[0])
	}
}

func TestRunGnitDirectHoldsDirtyNonContent(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	gnitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(content, "scratch", "local.txt"), "local\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:  "gnit",
		GnitRoot: gnitRoot,
		Publish:  "direct",
		DryRun:   true,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" || !strings.Contains(result.Message, "dirty changes are outside declared content paths") {
		t.Fatalf("result = %#v, want dirty non-content hold", result)
	}
	if result.ReasonCode != "outside_content_paths" {
		t.Fatalf("reason code = %q, want outside_content_paths", result.ReasonCode)
	}
	if result.NextCommand != "my doctor" {
		t.Fatalf("next command = %q, want my doctor", result.NextCommand)
	}
}

func TestRunGnitHoldsWhenWorkspaceIsNotInitialized(t *testing.T) {
	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: "https://github.com/acme/handbook.git", LocalPath: "/tmp/handbook"},
	}, Options{Backend: "gnit", GnitRoot: t.TempDir(), Publish: "auto"})

	if report.Backend != "gnit" || !strings.Contains(report.BackendMessage, "not a Gnit control workspace") {
		t.Fatalf("report = %#v, want Gnit initialization hold", report)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "held back" || !strings.Contains(result.Message, "not a Gnit control workspace") {
		t.Fatalf("result = %#v, want held back", result)
	}
	if result.ReasonCode != "gnit_not_control_workspace" {
		t.Fatalf("reason code = %q, want gnit_not_control_workspace", result.ReasonCode)
	}
	if result.NextCommand != "my doctor" {
		t.Fatalf("next command = %q, want doctor diagnosis", result.NextCommand)
	}
}

func TestRunGnitHoldsCheckoutOutsideWorkspaceWithNextCommand(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	gnitRoot := filepath.Join(t.TempDir(), "umbrella")
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: handbook\n")
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	adoptFile(t, content, "meetings/2026-06-08-sync.md")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		Visibility: privateVisibility,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" || result.ReasonCode != "gnit_not_member" {
		t.Fatalf("result = %#v, want gnit_not_member hold", result)
	}
	if result.NextCommand != "my doctor" {
		t.Fatalf("next command = %q, want my doctor", result.NextCommand)
	}
}

func TestRunGnitAllowsCanonicalContentWithCleanDuplicateSibling(t *testing.T) {
	remote, gnitRoot, content, manifest := setupGnitWorkspaceWithDuplicateRemote(t)
	setupGnitControlRoot(t, gnitRoot)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	adoptFile(t, content, "meetings/2026-06-08-sync.md")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		DryRun:     true,
		Visibility: privateVisibility,
	})

	if report.BackendMessage != "" {
		t.Fatalf("backend message = %q, want clean duplicate to be tolerated", report.BackendMessage)
	}
	result := findResult(t, report, "handbook")
	if result.Status != "dry-run" || result.Direction != "outbound" || !strings.Contains(result.Message, "commit My AI-approved") {
		t.Fatalf("result = %#v, want canonical outbound dry-run", result)
	}
	sibling := findResult(t, report, "manifest")
	if sibling.Status != "already landed" {
		t.Fatalf("sibling = %#v, want already landed", sibling)
	}
}

func TestRunGnitHoldsWhenDuplicateSiblingHasPendingChanges(t *testing.T) {
	remote, gnitRoot, content, manifest := setupGnitWorkspaceWithDuplicateRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-sync.md"), "sync\n")
	writeFile(t, filepath.Join(manifest, "catalog", "products.json"), "[]\n")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
		{ID: "manifest", Role: "manifest", Kind: "manifest", GitURL: remote, LocalPath: manifest},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		DryRun:     true,
		Visibility: privateVisibility,
	})

	if result := findResult(t, report, "handbook"); result.Status != "held back" || result.ReasonCode != "gnit_preflight_failed" {
		t.Fatalf("handbook result = %#v, want forced-backend preflight hold", result)
	}
	if result := findResult(t, report, "manifest"); result.Status != "held back" || result.ReasonCode != "gnit_not_member" {
		t.Fatalf("manifest result = %#v, want exact-membership hold", result)
	}
}

func TestRunGnitPullsCleanBehindRepo(t *testing.T) {
	remote, content, writer := setupTwoCheckoutRemote(t)
	gnitRoot := filepath.Dir(content)
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: content\n")
	writeFile(t, filepath.Join(writer, "meetings", "2026-06-09-remote.md"), "remote\n")
	runGit(t, writer, "add", ".")
	runGit(t, writer, "commit", "-q", "-m", "remote meeting")
	runGit(t, writer, "push", "-q", "origin", "HEAD:master")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:  "gnit",
		GnitRoot: gnitRoot,
		Publish:  "auto",
	})

	result := findResult(t, report, "handbook")
	if result.Status != "pulled" || result.Direction != "inbound" {
		t.Fatalf("result = %#v, want inbound pull", result)
	}
	if got := gitOut(t, content, "rev-parse", "HEAD"); got != gitOut(t, writer, "rev-parse", "HEAD") {
		t.Fatalf("content did not fast-forward: content %s writer %s", got, gitOut(t, writer, "rev-parse", "HEAD"))
	}
}

func TestRunGnitHoldsDivergedCheckoutWithDoctorNextCommand(t *testing.T) {
	remote, gnitRoot, content, writer := setupGnitWorkspaceWithDuplicateRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "2026-06-08-local.md"), "local\n")
	runGit(t, content, "add", ".")
	runGit(t, content, "commit", "-q", "-m", "local meeting")
	writeFile(t, filepath.Join(writer, "meetings", "2026-06-08-remote.md"), "remote\n")
	runGit(t, writer, "add", ".")
	runGit(t, writer, "commit", "-q", "-m", "remote meeting")
	runGit(t, writer, "push", "-q", "origin", "HEAD:master")

	report := Run([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}},
	}, Options{
		Backend:    "gnit",
		GnitRoot:   gnitRoot,
		Publish:    "auto",
		Visibility: privateVisibility,
	})

	result := findResult(t, report, "handbook")
	if result.Status != "held back" || result.ReasonCode != "diverged" {
		t.Fatalf("result = %#v, want diverged hold", result)
	}
	if result.NextCommand != "my doctor" {
		t.Fatalf("next command = %q, want my doctor", result.NextCommand)
	}
}

func TestRunAutoUsesBuiltinForUnrosteredTargetWithUnrelatedGnitMember(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	otherRemote, _ := setupGnitMemberRepo(t, root, "other")
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers:\n- id: other\n  path: other\n  remote: "+otherRemote+"\n")
	writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
	adoptFile(t, content, "meetings/new.md")

	report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{
		Backend: "auto", GnitRoot: root, Publish: "auto", DryRun: true, Visibility: privateVisibility,
	})
	result := findResult(t, report, "handbook")
	if result.Status != "dry-run" || result.Backend != "builtin" || strings.Contains(result.Message, "gnit") || strings.Contains(result.Message, "coordinated") {
		t.Fatalf("result = %#v, want quiet built-in dry-run", result)
	}
}

func TestRunAutoNeverMarksUnrosteredTargetPushedByGnitWithPublishableRoot(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	otherRemote, _ := setupGnitMemberRepo(t, root, "other")
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers:\n- id: other\n  path: other\n  remote: "+otherRemote+"\n")
	setupGnitControlRoot(t, root)
	writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
	adoptFile(t, content, "meetings/new.md")
	var gnitCalls int

	report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{
		Backend: "auto", GnitRoot: root, Publish: "auto", Visibility: privateVisibility,
		DirRunner: func(string, string, ...string) ([]byte, error) {
			gnitCalls++
			return []byte("unexpected"), nil
		},
	})
	result := findResult(t, report, "handbook")
	if result.Status != "pushed" || result.Backend != "builtin" || gnitCalls != 0 {
		t.Fatalf("result = %#v calls=%d, want built-in publication only", result, gnitCalls)
	}
	if out, err := exec.Command("git", "--git-dir", remote, "show", "master:meetings/new.md").CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "new" {
		t.Fatalf("remote content = %q err=%v", out, err)
	}
}

func TestRunAutoHoldsRosteredTargetWhenRootHasNoOrigin(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers:\n- id: handbook\n  path: content\n  remote: "+remote+"\n")
	runGit(t, root, "init", "-q")
	writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
	adoptFile(t, content, "meetings/new.md")

	entry := Entry{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}
	for _, dryRun := range []bool{true, false} {
		report := Run([]Entry{entry}, Options{Backend: "auto", GnitRoot: root, Publish: "auto", DryRun: dryRun, Visibility: privateVisibility})
		result := findResult(t, report, "handbook")
		if result.Status != "held back" || result.ReasonCode != "gnit_root_unpublishable" || result.NextCommand != "my doctor" {
			t.Fatalf("dryRun=%v result=%#v", dryRun, result)
		}
	}
}

func TestRunAutoHoldsRosteredTargetOnRemoteIdentityMismatch(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers:\n- id: handbook\n  path: content\n  remote: https://github.com/acme/different.git\n")
	writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
	adoptFile(t, content, "meetings/new.md")

	report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{
		Backend: "auto", GnitRoot: root, Publish: "auto", DryRun: true, Visibility: privateVisibility,
	})
	if result := findResult(t, report, "handbook"); result.Status != "held back" || result.ReasonCode != "gnit_member_identity_mismatch" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunAutoHoldsWhenGnitScopeWouldPublishUnselectedMember(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	otherRemote, other := setupGnitMemberRepo(t, root, "other")
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers:\n- id: handbook\n  path: content\n  remote: "+remote+"\n- id: other\n  path: other\n  remote: "+otherRemote+"\n")
	setupGnitControlRoot(t, root)
	writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
	adoptFile(t, content, "meetings/new.md")
	writeFile(t, filepath.Join(other, "ahead.txt"), "ahead\n")
	runGit(t, other, "add", "ahead.txt")
	runGit(t, other, "commit", "-q", "-m", "Ahead outside selection")

	report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{
		Backend: "auto", GnitRoot: root, Publish: "auto", DryRun: true, Visibility: privateVisibility,
	})
	if result := findResult(t, report, "handbook"); result.Status != "held back" || result.ReasonCode != "gnit_scope_exceeds_selection" || !strings.Contains(result.Message, "other") {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunAutoHoldsWhenRosterMemberCheckoutIsMissing(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers:\n- id: handbook\n  path: content\n  remote: "+remote+"\n- id: ghost\n  path: ghost\n  remote: https://github.com/acme/ghost.git\n")
	setupGnitControlRoot(t, root)
	writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
	adoptFile(t, content, "meetings/new.md")

	entry := Entry{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}
	for _, dryRun := range []bool{true, false} {
		report := Run([]Entry{entry}, Options{Backend: "auto", GnitRoot: root, Publish: "auto", DryRun: dryRun, Visibility: privateVisibility})
		result := findResult(t, report, "handbook")
		if result.Status != "held back" || result.ReasonCode != "gnit_workspace_unhealthy" || !strings.Contains(result.Message, "ghost") {
			t.Fatalf("dryRun=%v result=%#v, want gnit_workspace_unhealthy hold naming ghost", dryRun, result)
		}
	}
}

func TestRunAutoInvalidRosterFailsClosedForTargetUnderRoot(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 2\nmode: control\nmembers: []\n")
	writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
	adoptFile(t, content, "meetings/new.md")
	report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{
		Backend: "auto", GnitRoot: root, Publish: "auto", DryRun: true, Visibility: privateVisibility,
	})
	if result := findResult(t, report, "handbook"); result.ReasonCode != "gnit_roster_invalid" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunBuiltinExplicitlyAllowsRosteredTargetWithWarning(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers:\n- id: handbook\n  path: content\n  remote: "+remote+"\n")
	writeFile(t, filepath.Join(content, "meetings", "new.md"), "new\n")
	adoptFile(t, content, "meetings/new.md")
	report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{
		Backend: "builtin", GnitRoot: root, Publish: "auto", DryRun: true, Visibility: privateVisibility,
	})
	result := findResult(t, report, "handbook")
	if result.Status != "dry-run" || result.Backend != "builtin" || !strings.Contains(report.BackendMessage, "bypassed") {
		t.Fatalf("report = %#v result=%#v", report, result)
	}
}

func TestRunAutoKeepsPRPublicationInMyPolicyLayerInsideGnitUmbrella(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	writeFile(t, filepath.Join(root, ".gnit", "roster.yaml"), "version: 1\nmode: control\nmembers:\n- id: handbook\n  path: content\n  remote: "+remote+"\n")
	writeFile(t, filepath.Join(content, "meetings", "pr.md"), "pr\n")
	adoptFile(t, content, "meetings/pr.md")
	report := Run([]Entry{{ID: "handbook", Role: "content", GitURL: remote, LocalPath: content, ContentPaths: []string{"meetings"}}}, Options{
		Backend: "auto", GnitRoot: root, Publish: "pr", DryRun: true,
		PRPublisher: func(PRRequest) PRResult { return PRResult{Status: "dry-run", Message: "would open governed PR"} },
	})
	result := findResult(t, report, "handbook")
	if report.Backend != "builtin" || result.Backend != "builtin" || result.Status != "dry-run" {
		t.Fatalf("report = %#v result=%#v", report, result)
	}
}

func TestFastForwardHeldBackCarriesNextCommand(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(content, "meetings", "local.md"), "local\n")

	result := FastForward(Entry{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content}, FastForwardOptions{})
	if result.Status != "held back" || result.ReasonCode != "not_fast_forward" {
		t.Fatalf("result = %#v, want not_fast_forward hold", result)
	}
	if result.NextCommand != "my doctor" {
		t.Fatalf("next command = %q, want my doctor", result.NextCommand)
	}
}

func TestInspectNoFetchUsesLocalTrackingRefs(t *testing.T) {
	remote, content, writer := setupTwoCheckoutRemote(t)
	writeFile(t, filepath.Join(writer, "meetings", "2026-06-09-remote.md"), "remote\n")
	runGit(t, writer, "add", ".")
	runGit(t, writer, "commit", "-q", "-m", "remote meeting")
	runGit(t, writer, "push", "-q", "origin", "HEAD:master")

	entries := []Entry{{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content}}
	local := Inspect(entries, InspectOptions{Fetch: false})
	localResult := local[0]
	if localResult.Status != "already landed" || localResult.Behind != 0 {
		t.Fatalf("local result = %#v, want stale local tracking ref to look landed", localResult)
	}

	fetched := Inspect(entries, InspectOptions{Fetch: true})
	fetchedResult := fetched[0]
	if fetchedResult.Status != "pending" || fetchedResult.Behind != 1 {
		t.Fatalf("fetched result = %#v, want behind=1 pending", fetchedResult)
	}
}

func TestInspectReportsUnknownWhenFetchFails(t *testing.T) {
	remote, content, _ := setupTwoCheckoutRemote(t)
	results := Inspect([]Entry{
		{ID: "handbook", Role: "content", Kind: "handbook", GitURL: remote, LocalPath: content},
	}, InspectOptions{
		Fetch:  true,
		Runner: fetchFailingRunner,
	})

	result := results[0]
	if result.Status != "unknown" || !result.BehindUnknown {
		t.Fatalf("result = %#v, want unknown with behind_unknown", result)
	}
	if !strings.Contains(result.FetchError, "offline") || result.Error != "" {
		t.Fatalf("result = %#v, want fetch_error only", result)
	}
	if result.Branch == "" || result.Head == "" {
		t.Fatalf("result = %#v, want branch and head preserved", result)
	}
}

func setupTwoCheckoutRemote(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	remote := filepath.Join(root, "remote.git")
	content := filepath.Join(root, "content")
	manifest := filepath.Join(root, "manifest")
	if err := os.MkdirAll(filepath.Join(seed, "meetings"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(seed, "README.md"), "seed\n")
	runGit(t, seed, "init", "-q")
	configGitUser(t, seed)
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-q", "-m", "seed")
	runGit(t, root, "init", "--bare", "-q", remote)
	runGit(t, seed, "remote", "add", "origin", remote)
	runGit(t, seed, "push", "-q", "origin", "HEAD:master")
	runGit(t, root, "clone", "-q", remote, content)
	runGit(t, root, "clone", "-q", remote, manifest)
	configGitUser(t, content)
	configGitUser(t, manifest)
	return remote, content, manifest
}

func fetchFailingRunner(name string, args ...string) ([]byte, error) {
	if name == "git" && len(args) >= 3 && args[2] == "fetch" {
		return []byte("offline\n"), errors.New("offline")
	}
	return exec.Command(name, args...).CombinedOutput()
}

func setupGnitWorkspaceWithDuplicateRemote(t *testing.T) (string, string, string, string) {
	t.Helper()
	remote, content, manifest := setupTwoCheckoutRemote(t)
	root := filepath.Dir(content)
	gnitRoot := filepath.Join(root, "umbrella")
	gnitContent := filepath.Join(gnitRoot, "handbook")
	if err := os.MkdirAll(gnitRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(content, gnitContent); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(gnitRoot, ".gnit", "roster.yaml"), "version: 1\nmode: shared\nmembers:\n- id: handbook\n  path: handbook\n")
	return remote, gnitRoot, gnitContent, manifest
}

func setupGnitControlRoot(t *testing.T, root string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "gnit-control.git")
	runGit(t, filepath.Dir(remote), "init", "--bare", "-q", remote)
	runGit(t, root, "init", "-q")
	configGitUser(t, root)
	runGit(t, root, "add", ".gnit/roster.yaml")
	runGit(t, root, "commit", "-q", "-m", "Initialize Gnit control workspace")
	runGit(t, root, "remote", "add", "origin", remote)
	runGit(t, root, "push", "-q", "-u", "origin", "HEAD:master")
	return remote
}

func setupGnitMemberRepo(t *testing.T, root, id string) (string, string) {
	t.Helper()
	remote := filepath.Join(t.TempDir(), id+".git")
	runGit(t, filepath.Dir(remote), "init", "--bare", "-q", remote)
	repo := filepath.Join(root, id)
	runGit(t, root, "clone", "-q", remote, repo)
	configGitUser(t, repo)
	writeFile(t, filepath.Join(repo, "README.md"), id+"\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-q", "-m", "Initialize "+id)
	runGit(t, repo, "push", "-q", "-u", "origin", "HEAD:master")
	return remote, repo
}

func privateVisibility(string) (string, error) {
	return "PRIVATE", nil
}

func findResult(t *testing.T, report Report, id string) Result {
	t.Helper()
	for _, result := range report.Results {
		if result.ID == id {
			return result
		}
	}
	t.Fatalf("missing result %q in %#v", id, report)
	return Result{}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func configGitUser(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "config", "user.name", "My AI Test")
	runGit(t, dir, "config", "user.email", "my-test@example.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out := runGit(t, dir, args...)
	return strings.TrimSpace(out)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func adoptFile(t *testing.T, dir, path string) {
	t.Helper()
	runGit(t, dir, "add", "-N", path)
}
