// Package changenotice summarizes what changed in an organization manifest
// between two revisions, so a person is told about new or amended contract
// rules, skills, tools and guidance instead of having them arrive silently.
//
// Everything here is pure: callers read both revisions from Git and pass the
// documents, contract rules and changed paths in.
package changenotice

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
)

// Change is one line of a notice.
type Change struct {
	// Kind is contract, guidance, skill, tool, service, mount, policy or role.
	Kind string `json:"kind"`
	// Action is added, amended, retracted, removed or updated.
	Action string `json:"action"`
	ID     string `json:"id,omitempty"`
	// Text is the new rule text, or what changed about an item.
	Text string `json:"text,omitempty"`
	// Before is the earlier rule text of an amended rule.
	Before string `json:"before,omitempty"`
}

// Notice is the summary of one manifest's changes between two revisions.
type Notice struct {
	Manifest string   `json:"manifest"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to"`
	Changes  []Change `json:"changes"`
	Links    []string `json:"links,omitempty"`
	// Note explains a summary that could not be computed in full.
	Note string `json:"note,omitempty"`
}

// Empty reports whether there is nothing to tell the person.
func (n Notice) Empty() bool {
	return len(n.Changes) == 0 && n.Note == ""
}

// Revision is what a notice compares at one manifest commit.
type Revision struct {
	Doc   manifest.Document
	Rules []string
}

// Diff compares two revisions. changedPaths are the manifest repository paths
// that differ between them (git diff --name-only), used to spot edited
// guidance files and skill contents that manifest.json does not record.
func Diff(old, new Revision, changedPaths []string) []Change {
	var out []Change
	out = append(out, DiffRules(old.Rules, new.Rules)...)
	out = append(out, changedGuidance(new.Doc, changedPaths)...)
	out = append(out, diffItems("skill", skillItems(old.Doc), skillItems(new.Doc), skillUpdates(old.Doc, new.Doc, changedPaths))...)
	out = append(out, diffItems("tool", toolItems(old.Doc), toolItems(new.Doc), nil)...)
	out = append(out, diffItems("service", serviceItems(old.Doc), serviceItems(new.Doc), nil)...)
	out = append(out, diffItems("mount", mountItems(old.Doc), mountItems(new.Doc), nil)...)
	out = append(out, diffItems("policy", policyItems(old.Doc), policyItems(new.Doc), nil)...)
	out = append(out, diffItems("role", roleItems(old.Doc), roleItems(new.Doc), nil)...)
	return out
}

// DiffRules lists contract rules added, retracted and amended. A rule removed
// and another added at the same place count as one amendment only when they
// share enough words; otherwise each is reported on its own.
func DiffRules(old, new []string) []Change {
	var out []Change
	var removed, added []string
	flush := func() {
		paired := 0
		for paired < len(removed) && paired < len(added) && similar(removed[paired], added[paired]) {
			out = append(out, Change{Kind: "contract", Action: "amended", Before: removed[paired], Text: added[paired]})
			paired++
		}
		for _, rule := range added[paired:] {
			out = append(out, Change{Kind: "contract", Action: "added", Text: rule})
		}
		for _, rule := range removed[paired:] {
			out = append(out, Change{Kind: "contract", Action: "retracted", Text: rule})
		}
		removed, added = nil, nil
	}
	for _, op := range editScript(old, new) {
		switch op.kind {
		case opKeep:
			flush()
		case opRemove:
			removed = append(removed, op.text)
		case opAdd:
			added = append(added, op.text)
		}
	}
	flush()
	return out
}

type opKind int

const (
	opKeep opKind = iota
	opRemove
	opAdd
)

type editOp struct {
	kind opKind
	text string
}

// editScript is a longest-common-subsequence diff. Contracts are short lists,
// so the quadratic table is fine.
func editScript(a, b []string) []editOp {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []editOp
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, editOp{opKeep, a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, editOp{opRemove, a[i]})
			i++
		default:
			ops = append(ops, editOp{opAdd, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, editOp{opRemove, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, editOp{opAdd, b[j]})
	}
	return ops
}

// similar reports whether two rules share at least a third of their words.
func similar(a, b string) bool {
	wa, wb := words(a), words(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	union := len(wa) + len(wb) - shared
	return shared*3 >= union
}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		out[w] = true
	}
	return out
}

// diffItems compares id → description maps. updated names ids whose content
// changed even though their description did not.
func diffItems(kind string, old, new map[string]string, updated map[string]string) []Change {
	var out []Change
	for _, id := range sortedKeys(new) {
		before, existed := old[id]
		switch {
		case !existed:
			out = append(out, Change{Kind: kind, Action: "added", ID: id, Text: new[id]})
		case before != new[id]:
			out = append(out, Change{Kind: kind, Action: "updated", ID: id, Text: describeUpdate(before, new[id])})
		case updated[id] != "":
			out = append(out, Change{Kind: kind, Action: "updated", ID: id, Text: updated[id]})
		}
	}
	for _, id := range sortedKeys(old) {
		if _, kept := new[id]; !kept {
			out = append(out, Change{Kind: kind, Action: "removed", ID: id, Text: old[id]})
		}
	}
	return out
}

func describeUpdate(before, after string) string {
	if before == "" {
		return after
	}
	if after == "" {
		return "was: " + before
	}
	return before + " → " + after
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func skillItems(doc manifest.Document) map[string]string {
	out := map[string]string{}
	for _, s := range doc.Skills {
		out[s.ID] = ""
	}
	return out
}

// skillUpdates names manifest-repo skills whose files changed.
func skillUpdates(old, new manifest.Document, changedPaths []string) map[string]string {
	out := map[string]string{}
	previous := map[string]bool{}
	for _, s := range old.Skills {
		previous[s.ID] = true
	}
	for _, s := range new.Skills {
		if !previous[s.ID] || s.Source.Type != "" || s.Path == "" {
			continue
		}
		if anyUnder(changedPaths, s.Path) {
			out[s.ID] = "skill files changed"
		}
	}
	return out
}

func toolItems(doc manifest.Document) map[string]string {
	out := map[string]string{}
	for _, t := range doc.Tools {
		out[t.ID] = t.Purpose
	}
	return out
}

func serviceItems(doc manifest.Document) map[string]string {
	out := map[string]string{}
	for _, s := range doc.Services {
		out[s.ID] = s.Purpose
	}
	return out
}

func mountItems(doc manifest.Document) map[string]string {
	out := map[string]string{}
	for _, m := range doc.Mounts {
		out[m.ID] = ""
	}
	return out
}

func policyItems(doc manifest.Document) map[string]string {
	out := map[string]string{}
	for _, p := range doc.Governance.Policies {
		title := p.Title
		if title == "" {
			title = p.ID
		}
		out[p.ID] = title + " (version " + p.Version + ")"
	}
	return out
}

func roleItems(doc manifest.Document) map[string]string {
	out := map[string]string{}
	for _, r := range doc.Roles {
		out[r.ID] = r.Purpose
	}
	return out
}

// changedGuidance lists organization and role guidance files that changed.
func changedGuidance(doc manifest.Document, changedPaths []string) []Change {
	paths := append([]string{}, doc.AgentGuidance.Paths...)
	for _, r := range doc.Roles {
		paths = append(paths, r.GuidancePaths...)
	}
	seen := map[string]bool{}
	var out []Change
	for _, p := range paths {
		clean := path.Clean(p)
		if seen[clean] || !anyUnder(changedPaths, clean) {
			continue
		}
		seen[clean] = true
		out = append(out, Change{Kind: "guidance", Action: "updated", ID: clean})
	}
	return out
}

func anyUnder(paths []string, root string) bool {
	root = strings.TrimSuffix(path.Clean(root), "/")
	for _, p := range paths {
		p = path.Clean(p)
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// Short abbreviates a commit for display.
func Short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

// maxRuleChars bounds one rule in a notice; the full text is in the links and
// in my changes.
const maxRuleChars = 400

// maxLines bounds a notice so a launch prompt stays small.
const maxLines = 40

// Render is the plain-text notice shown on the terminal and handed to the
// agent. It never includes more than maxLines changes.
func Render(n Notice) string {
	var b strings.Builder
	switch {
	case n.From != "":
		fmt.Fprintf(&b, "Changes to the %s organization manifest since your last session (%s..%s):\n", n.Manifest, Short(n.From), Short(n.To))
	default:
		fmt.Fprintf(&b, "Changes to the %s organization manifest (now at %s):\n", n.Manifest, Short(n.To))
	}
	if n.Note != "" {
		fmt.Fprintf(&b, "%s\n", n.Note)
	}
	shown := 0
	for _, kind := range kindOrder {
		var lines []string
		for _, c := range n.Changes {
			if c.Kind == kind {
				lines = append(lines, renderChange(c))
			}
		}
		if len(lines) == 0 {
			continue
		}
		fmt.Fprintf(&b, "%s:\n", kindTitle[kind])
		for _, line := range lines {
			if shown == maxLines {
				break
			}
			fmt.Fprintf(&b, "  - %s\n", line)
			shown++
		}
	}
	if hidden := len(n.Changes) - shown; hidden > 0 {
		fmt.Fprintf(&b, "  ... and %d more; run `my changes --since %s` to see them all.\n", hidden, Short(n.From))
	}
	if len(n.Links) > 0 {
		fmt.Fprintf(&b, "Details: %s\n", strings.Join(n.Links, " "))
	}
	return b.String()
}

var kindOrder = []string{"contract", "guidance", "skill", "tool", "service", "mount", "policy", "role"}

var kindTitle = map[string]string{
	"contract": "Contract rules",
	"guidance": "Guidance",
	"skill":    "Skills",
	"tool":     "Tools",
	"service":  "Services",
	"mount":    "Content mounts",
	"policy":   "Policies",
	"role":     "Roles",
}

func renderChange(c Change) string {
	switch c.Kind {
	case "contract":
		switch c.Action {
		case "amended":
			return fmt.Sprintf("Amended: %q (was %q)", clip(c.Text), clip(c.Before))
		case "retracted":
			return fmt.Sprintf("Retracted: %q", clip(c.Text))
		default:
			return fmt.Sprintf("Added: %q", clip(c.Text))
		}
	case "guidance":
		return "Updated " + c.ID
	}
	line := capitalize(c.Action) + " " + c.ID
	if c.Text != "" {
		line += ": " + clip(c.Text)
	}
	return line
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len([]rune(s)) <= maxRuleChars {
		return s
	}
	return string([]rune(s)[:maxRuleChars]) + "…"
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// AgentPrompt is the initial prompt that asks an agent to tell the person
// about the changes before anything else.
func AgentPrompt(n Notice) string {
	return "Before anything else, tell me in plain words what changed in my organization's manifest since my last session, " +
		"including every link below so I can read the details. Keep it short, then wait for my next request.\n\n" + Render(n)
}
