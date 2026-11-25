package changenotice

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fluxinc/my-cli/internal/manifest"
)

func TestDiffRulesNamesAddedRetractedAndAmendedRules(t *testing.T) {
	old := []string{
		"Ask before publishing an image.",
		"Keep customer names out of commit messages.",
		"Record every delivery in the log.",
	}
	new := []string{
		"Ask before publishing an image or its source files.",
		"Record every delivery in the log.",
		"Credit the photographer in every caption.",
	}
	got := DiffRules(old, new)
	want := []Change{
		{Kind: "contract", Action: "amended", Before: "Ask before publishing an image.", Text: "Ask before publishing an image or its source files."},
		{Kind: "contract", Action: "retracted", Text: "Keep customer names out of commit messages."},
		{Kind: "contract", Action: "added", Text: "Credit the photographer in every caption."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffRules =\n%#v\nwant\n%#v", got, want)
	}
}

func TestDiffRulesDoesNotCallAnUnrelatedReplacementAnAmendment(t *testing.T) {
	got := DiffRules([]string{"Ask before publishing an image."}, []string{"Use the shared calendar for studio bookings."})
	want := []Change{
		{Kind: "contract", Action: "added", Text: "Use the shared calendar for studio bookings."},
		{Kind: "contract", Action: "retracted", Text: "Ask before publishing an image."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffRules = %#v, want %#v", got, want)
	}
	if got := DiffRules([]string{"A rule."}, []string{"A rule."}); len(got) != 0 {
		t.Fatalf("unchanged rules reported: %#v", got)
	}
}

func TestDiffReportsItemsGuidanceAndSkillFiles(t *testing.T) {
	old := Revision{Doc: manifest.Document{
		Skills:        []manifest.Skill{{ID: "acme:handbook", Path: "skills/handbook"}, {ID: "acme:old", Path: "skills/old"}},
		Tools:         []manifest.Tool{{ID: "gh", Purpose: "GitHub"}},
		AgentGuidance: manifest.AgentGuidance{Paths: []string{"agent-guidance/acme.md"}},
		Governance:    manifest.Governance{Policies: []manifest.Policy{{ID: "security", Title: "Security", Version: "1"}}},
	}}
	new := Revision{Doc: manifest.Document{
		Skills:        []manifest.Skill{{ID: "acme:handbook", Path: "skills/handbook"}, {ID: "acme:prints", Path: "skills/prints"}},
		Tools:         []manifest.Tool{{ID: "gh", Purpose: "GitHub"}, {ID: "magick", Purpose: "Convert images"}},
		AgentGuidance: manifest.AgentGuidance{Paths: []string{"agent-guidance/acme.md"}},
		Governance:    manifest.Governance{Policies: []manifest.Policy{{ID: "security", Title: "Security", Version: "2"}}},
		Roles:         []manifest.Role{{ID: "printer", Purpose: "Prepares print files", GuidancePaths: []string{"roles/printer.md"}}},
	}}
	changed := []string{"skills/handbook/SKILL.md", "agent-guidance/acme.md", "roles/printer.md", "README.md"}
	got := Diff(old, new, changed)
	want := []Change{
		{Kind: "guidance", Action: "updated", ID: "agent-guidance/acme.md"},
		{Kind: "guidance", Action: "added", ID: "roles/printer.md"},
		{Kind: "skill", Action: "updated", ID: "acme:handbook", Text: "skill files changed"},
		{Kind: "skill", Action: "added", ID: "acme:prints"},
		{Kind: "skill", Action: "removed", ID: "acme:old"},
		{Kind: "tool", Action: "added", ID: "magick", Text: "Convert images"},
		{Kind: "policy", Action: "updated", ID: "security", Text: "Security (version 1) → Security (version 2)"},
		{Kind: "role", Action: "added", ID: "printer", Text: "Prepares print files"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff =\n%#v\nwant\n%#v", got, want)
	}
}

func TestPullRequestLinksFromMergeAndSquashSubjects(t *testing.T) {
	subjects := []string{
		"Merge pull request #14 from acme/prints",
		"Add the print role",
		"Ask for source files too (#12)",
		"Merge pull request #12 from acme/source",
	}
	if got := PullRequests(subjects); !reflect.DeepEqual(got, []int{12, 14}) {
		t.Fatalf("PullRequests = %v", got)
	}
	for _, remote := range []string{"https://github.com/acme/manifest.git", "git@github.com:acme/manifest.git", "ssh://git@github.com/acme/manifest"} {
		repo, ok := ParseRemote(remote)
		if !ok || repo != (WebRepository{Host: "github.com", Owner: "acme", Name: "manifest"}) {
			t.Fatalf("ParseRemote(%q) = %#v %v", remote, repo, ok)
		}
	}
	for _, remote := range []string{"/srv/git/manifest.git", "/tmp/art:repo@studio", "./art:repo@studio", "studio@:repo", "https://gitlab.com/acme/manifest.git", "https://github.com/acme"} {
		if _, ok := ParseRemote(remote); ok {
			t.Fatalf("ParseRemote(%q) accepted a remote without browsable pull requests", remote)
		}
	}
}

func TestLinksCoverTheWholeRangeTheSummaryDescribes(t *testing.T) {
	repo, _ := ParseRemote("https://github.com/acme/manifest.git")
	base := "https://github.com/acme/manifest"
	if got := Links(repo, true, "aaa", "bbb", []int{12, 14}, true); !reflect.DeepEqual(got, []string{base + "/pull/12", base + "/pull/14", base + "/compare/aaa...bbb"}) {
		t.Fatalf("pull requests plus range = %v", got)
	}
	if got := Links(repo, true, "aaa", "bbb", nil, true); !reflect.DeepEqual(got, []string{base + "/compare/aaa...bbb"}) {
		t.Fatalf("direct commits = %v", got)
	}
	many := make([]int, maxLinks+1)
	for i := range many {
		many[i] = i + 1
	}
	if got := Links(repo, true, "aaa", "bbb", many, true); !reflect.DeepEqual(got, []string{base + "/compare/aaa...bbb"}) {
		t.Fatalf("too many pull requests = %v", got)
	}
	// A three-dot compare of diverged revisions starts at their merge base,
	// which is not what the summary compared.
	if got := Links(repo, true, "aaa", "bbb", []int{12}, false); !reflect.DeepEqual(got, []string{base + "/commit/aaa", base + "/commit/bbb"}) {
		t.Fatalf("diverged = %v", got)
	}
	if got := Links(WebRepository{}, false, "aaa", "bbb", []int{1}, true); got != nil {
		t.Fatalf("links without a web repository: %v", got)
	}
}

func TestDiffNoticesDeclarationChangesWithoutFileChanges(t *testing.T) {
	old := Revision{
		Doc: manifest.Document{
			AgentGuidance: manifest.AgentGuidance{Paths: []string{"guidance/painting.md"}},
			Skills:        []manifest.Skill{{ID: "acme:prints", Path: "skills/prints"}},
			Roles:         []manifest.Role{{ID: "printer", Purpose: "Prepares print files", Skills: []string{"acme:prints"}}},
		},
		Raw: []byte(`{"manifest_version":1,"profiles":[{"id":"a"}],"governance":{"policies":[{"id":"p","version":"1"}]}}`),
	}
	new := Revision{
		Doc: manifest.Document{
			AgentGuidance: manifest.AgentGuidance{Paths: []string{"guidance/printing.md"}},
			Skills:        []manifest.Skill{{ID: "acme:prints", Path: "skills/prints-v2"}},
			Roles:         []manifest.Role{{ID: "printer", Purpose: "Prepares print files"}},
		},
		Raw: []byte(`{"manifest_version":1,"profiles":[{"id":"b"}],"governance":{"policies":[{"id":"p","version":"2"}]}}`),
	}
	got := Diff(old, new, []string{"manifest.json"})
	want := []Change{
		{Kind: "guidance", Action: "added", ID: "guidance/printing.md"},
		{Kind: "guidance", Action: "removed", ID: "guidance/painting.md"},
		{Kind: "skill", Action: "updated", ID: "acme:prints", Text: "changed path"},
		{Kind: "role", Action: "updated", ID: "printer", Text: "changed skills"},
		{Kind: "manifest", Action: "updated", Text: "settings changed: profiles"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff =\n%#v\nwant\n%#v", got, want)
	}
}

func TestRenderCapsTheLaunchSummaryAndRenderAllListsEverything(t *testing.T) {
	n := Notice{Manifest: "acme", From: "1111111aaaa", To: "2222222bbbb", Links: []string{"https://github.com/acme/manifest/pull/3"}}
	for i := 0; i < maxLines+5; i++ {
		n.Changes = append(n.Changes, Change{Kind: "tool", Action: "added", ID: fmt.Sprintf("tool-%d", i)})
	}
	n.Changes = append(n.Changes, Change{Kind: "contract", Action: "added", Text: strings.Repeat("x", maxRuleChars+50)})
	out := Render(n)
	for _, want := range []string{"acme organization manifest since your last session (1111111..2222222)", "Contract rules:", "... and 6 more; run `my changes --manifest acme --since 1111111 --to 2222222 --all`", "Details: https://github.com/acme/manifest/pull/3", "…"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render missing %q:\n%s", want, out)
		}
	}
	all := RenderAll(n)
	if !strings.Contains(all, "tool-44") || !strings.Contains(all, strings.Repeat("x", maxRuleChars+50)) || strings.Contains(all, "more; run") {
		t.Fatalf("RenderAll is not complete:\n%s", all)
	}
}

func TestStateAdvancesOnlyFromTheRevisionItWasComputedFrom(t *testing.T) {
	home := t.TempDir()
	if _, found, err := Load(home, "acme"); err != nil || found {
		t.Fatalf("fresh home: found=%v err=%v", found, err)
	}
	if err := Record(home, "acme", "a", true, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if ok, err := Advance(home, "acme", "a", "c", "", time.Now()); err != nil || !ok {
		t.Fatalf("advance a→c: %v %v", ok, err)
	}
	// An older notice (a→b) finishing late must not move c back to b.
	if ok, _ := Advance(home, "acme", "a", "b", "", time.Now()); ok {
		t.Fatal("a late, older notice moved the record")
	}
	s, _, _ := Load(home, "acme")
	if s.Noticed != "c" || s.Baseline {
		t.Fatalf("state = %#v", s)
	}
	if _, found, _ := Load(t.TempDir(), "acme"); found {
		t.Fatal("another home sees this person's state")
	}
	for _, bad := range []string{"", "..", "a/b"} {
		if err := Record(home, bad, "x", false, time.Now()); err == nil {
			t.Fatalf("Record accepted manifest name %q", bad)
		}
	}
}

func TestReservationCleanupLeavesAnotherLaunchsReservation(t *testing.T) {
	home := t.TempDir()
	if err := Record(home, "acme", "a", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	reserve := func(token string) {
		t.Helper()
		if _, err := Update(home, "acme", func(s *State, _ bool) (bool, error) {
			s.Reserved = &Reservation{Token: token, From: "a", To: "b", PID: 1, At: time.Now().UTC().Format(time.RFC3339)}
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reserve("A")
	reserve("B") // B replaced an expired A
	if err := Release(home, "acme", "A"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Advance(home, "acme", "x", "y", "A", time.Now()); ok {
		t.Fatal("advanced from the wrong revision")
	}
	s, _, _ := Load(home, "acme")
	if s.Reserved == nil || s.Reserved.Token != "B" {
		t.Fatalf("A's late cleanup touched B: %#v", s.Reserved)
	}
	if ok, err := Advance(home, "acme", "a", "b", "B", time.Now()); err != nil || !ok {
		t.Fatalf("B advance: %v %v", ok, err)
	}
	if s, _, _ := Load(home, "acme"); s.Reserved != nil || s.Noticed != "b" {
		t.Fatalf("after B: %#v", s)
	}
}

func TestReservationLiveness(t *testing.T) {
	now := time.Now()
	r := Reservation{PID: 42, ProcessStart: "Mon", At: now.Add(-time.Minute).UTC().Format(time.RFC3339)}
	running := func(int) (string, bool, error) { return "Mon", true, nil }
	gone := func(int) (string, bool, error) { return "", false, nil }
	reused := func(int) (string, bool, error) { return "Tue", true, nil }
	unknown := func(int) (string, bool, error) { return "", false, fmt.Errorf("ps failed") }
	if !r.Live(now, running) || r.Live(now, gone) || r.Live(now, reused) || !r.Live(now, unknown) {
		t.Fatal("liveness by process is wrong")
	}
	old := r
	old.At = now.Add(-ReservationTTL - time.Minute).UTC().Format(time.RFC3339)
	if old.Live(now, running) {
		t.Fatal("an expired reservation still holds")
	}
}
