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
		{Kind: "guidance", Action: "updated", ID: "roles/printer.md"},
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
	repo, _ := ParseRemote("https://github.com/acme/manifest.git")
	if got := Links(repo, true, "aaa", "bbb", []int{12, 14}); !reflect.DeepEqual(got, []string{"https://github.com/acme/manifest/pull/12", "https://github.com/acme/manifest/pull/14"}) {
		t.Fatalf("Links = %v", got)
	}
	if got := Links(repo, true, "aaa", "bbb", nil); !reflect.DeepEqual(got, []string{"https://github.com/acme/manifest/compare/aaa...bbb"}) {
		t.Fatalf("compare link = %v", got)
	}
	many := make([]int, maxLinks+1)
	for i := range many {
		many[i] = i + 1
	}
	if got := Links(repo, true, "aaa", "bbb", many); len(got) != 1 || !strings.Contains(got[0], "/compare/") {
		t.Fatalf("too many PRs should give the compare view: %v", got)
	}
	for _, remote := range []string{"/srv/git/manifest.git", "https://gitlab.com/acme/manifest.git", "https://github.com/acme"} {
		if _, ok := ParseRemote(remote); ok {
			t.Fatalf("ParseRemote(%q) accepted a remote without browsable pull requests", remote)
		}
	}
	if got := Links(WebRepository{}, false, "aaa", "bbb", []int{1}); got != nil {
		t.Fatalf("links without a web repository: %v", got)
	}
}

func TestRenderIsBoundedAndPointsAtTheFullList(t *testing.T) {
	n := Notice{Manifest: "acme", From: "1111111aaaa", To: "2222222bbbb", Links: []string{"https://github.com/acme/manifest/pull/3"}}
	for i := 0; i < maxLines+5; i++ {
		n.Changes = append(n.Changes, Change{Kind: "contract", Action: "added", Text: fmt.Sprintf("Rule %d.", i)})
	}
	n.Changes[0].Text = strings.Repeat("x", maxRuleChars+50)
	out := Render(n)
	for _, want := range []string{"acme organization manifest since your last session (1111111..2222222)", "Contract rules:", "... and 5 more; run `my changes --since 1111111`", "Details: https://github.com/acme/manifest/pull/3", "…"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, strings.Repeat("x", maxRuleChars+1)) {
		t.Fatal("Render did not clip a long rule")
	}
	if prompt := AgentPrompt(n); !strings.HasPrefix(prompt, "Before anything else, tell me") || !strings.Contains(prompt, out) {
		t.Fatalf("AgentPrompt does not carry the notice:\n%s", prompt)
	}
}

func TestStateIsPerHomeAndSurvivesReload(t *testing.T) {
	home := t.TempDir()
	if _, found, err := Load(home, "acme"); err != nil || found {
		t.Fatalf("fresh home: found=%v err=%v", found, err)
	}
	if err := Save(home, "acme", "abc123", true, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	s, found, err := Load(home, "acme")
	if err != nil || !found || s.Noticed != "abc123" || !s.Baseline || s.Schema != 1 {
		t.Fatalf("Load = %#v %v %v", s, found, err)
	}
	if _, found, _ := Load(t.TempDir(), "acme"); found {
		t.Fatal("another home sees this person's state")
	}
	for _, bad := range []string{"", "..", "a/b"} {
		if err := Save(home, bad, "x", false, time.Now()); err == nil {
			t.Fatalf("Save accepted manifest name %q", bad)
		}
	}
}
