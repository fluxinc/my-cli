// Package changenotice summarizes what changed in an organization manifest
// between two revisions, so a person is told about new or amended contract
// rules, skills, tools and guidance instead of having them arrive silently.
//
// The diff is pure: callers read both revisions from Git and pass the
// documents, contract rules and changed paths in.
package changenotice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/fluxinc/my-cli/internal/manifest"
)

// Change is one line of a notice.
type Change struct {
	// Kind is contract, guidance, skill, tool, service, mount, policy, role
	// or manifest.
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
	// Incomplete means part of the range could not be read. An incomplete
	// notice is shown but never recorded as seen, so it is retried.
	Incomplete bool `json:"incomplete,omitempty"`
}

// Empty reports whether there is nothing to tell the person.
func (n Notice) Empty() bool {
	return len(n.Changes) == 0 && n.Note == ""
}

// Revision is what a notice compares at one manifest commit.
type Revision struct {
	Doc   manifest.Document
	Rules []string
	// Raw is manifest.json as committed, used to notice settings that have
	// no item of their own.
	Raw []byte
}

// Diff compares two revisions. changedPaths are the manifest repository paths
// that differ between them (git diff --name-only), used to spot edited
// guidance files and skill contents that manifest.json does not record.
func Diff(old, new Revision, changedPaths []string) []Change {
	var out []Change
	out = append(out, DiffRules(old.Rules, new.Rules)...)
	out = append(out, diffGuidance(old.Doc, new.Doc, changedPaths)...)
	out = append(out, diffItems("skill", itemsOf(old.Doc.Skills, skillKey, noSummary), itemsOf(new.Doc.Skills, skillKey, noSummary), skillUpdates(old.Doc, new.Doc, changedPaths))...)
	out = append(out, diffItems("tool", itemsOf(old.Doc.Tools, toolKey, toolSummary), itemsOf(new.Doc.Tools, toolKey, toolSummary), nil)...)
	out = append(out, diffItems("service", itemsOf(old.Doc.Services, serviceKey, serviceSummary), itemsOf(new.Doc.Services, serviceKey, serviceSummary), nil)...)
	out = append(out, diffItems("mount", itemsOf(old.Doc.Mounts, mountKey, noSummaryMount), itemsOf(new.Doc.Mounts, mountKey, noSummaryMount), nil)...)
	out = append(out, diffItems("policy", itemsOf(old.Doc.Governance.Policies, policyKey, policySummary), itemsOf(new.Doc.Governance.Policies, policyKey, policySummary), nil)...)
	out = append(out, diffItems("role", itemsOf(old.Doc.Roles, roleKey, roleSummary), itemsOf(new.Doc.Roles, roleKey, roleSummary), nil)...)
	out = append(out, otherSettings(old.Raw, new.Raw)...)
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

// item is one declared manifest entry: a one-line summary for people and its
// full declaration, field by field, for noticing any change.
type item struct {
	summary string
	fields  map[string]string
}

func itemsOf[T any](list []T, key func(T) string, summary func(T) string) map[string]item {
	out := map[string]item{}
	for _, v := range list {
		out[key(v)] = item{summary: summary(v), fields: jsonFields(v)}
	}
	return out
}

// jsonFields is a value's top-level JSON fields in compact form.
func jsonFields(v any) map[string]string {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, value := range raw {
		var compact bytes.Buffer
		if json.Compact(&compact, value) == nil {
			out[k] = compact.String()
		}
	}
	return out
}

// changedFields names the fields whose value differs, sorted.
func changedFields(old, new map[string]string) []string {
	var out []string
	for k, v := range new {
		if old[k] != v {
			out = append(out, k)
		}
	}
	for k := range old {
		if _, ok := new[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// diffItems compares declarations by id. extra names ids whose files changed
// even though their declaration did not.
func diffItems(kind string, old, new map[string]item, extra map[string]string) []Change {
	var out []Change
	for _, id := range sortedKeys(new) {
		before, existed := old[id]
		after := new[id]
		switch {
		case !existed:
			out = append(out, Change{Kind: kind, Action: "added", ID: id, Text: after.summary})
		case before.summary != after.summary:
			out = append(out, Change{Kind: kind, Action: "updated", ID: id, Text: describeUpdate(before.summary, after.summary)})
		default:
			fields := changedFields(before.fields, after.fields)
			switch {
			case len(fields) > 0:
				out = append(out, Change{Kind: kind, Action: "updated", ID: id, Text: "changed " + strings.Join(fields, ", ")})
			case extra[id] != "":
				out = append(out, Change{Kind: kind, Action: "updated", ID: id, Text: extra[id]})
			}
		}
	}
	for _, id := range sortedKeys(old) {
		if _, kept := new[id]; !kept {
			out = append(out, Change{Kind: kind, Action: "removed", ID: id, Text: old[id].summary})
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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func skillKey(s manifest.Skill) string     { return s.ID }
func toolKey(t manifest.Tool) string       { return t.ID }
func serviceKey(s manifest.Service) string { return s.ID }
func mountKey(m manifest.Mount) string     { return m.ID }
func policyKey(p manifest.Policy) string   { return p.ID }
func roleKey(r manifest.Role) string       { return r.ID }

func noSummary(manifest.Skill) string          { return "" }
func noSummaryMount(manifest.Mount) string     { return "" }
func toolSummary(t manifest.Tool) string       { return t.Purpose }
func serviceSummary(s manifest.Service) string { return s.Purpose }
func roleSummary(r manifest.Role) string       { return r.Purpose }
func policySummary(p manifest.Policy) string {
	title := p.Title
	if title == "" {
		title = p.ID
	}
	return title + " (version " + p.Version + ")"
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

func guidancePaths(doc manifest.Document) map[string]bool {
	out := map[string]bool{}
	for _, p := range doc.AgentGuidance.Paths {
		out[path.Clean(p)] = true
	}
	for _, r := range doc.Roles {
		for _, p := range r.GuidancePaths {
			out[path.Clean(p)] = true
		}
	}
	return out
}

// diffGuidance reports guidance files that were declared, dropped or edited.
func diffGuidance(old, new manifest.Document, changedPaths []string) []Change {
	before, after := guidancePaths(old), guidancePaths(new)
	var out []Change
	for _, p := range sortedKeys(after) {
		switch {
		case !before[p]:
			out = append(out, Change{Kind: "guidance", Action: "added", ID: p})
		case anyUnder(changedPaths, p):
			out = append(out, Change{Kind: "guidance", Action: "updated", ID: p})
		}
	}
	for _, p := range sortedKeys(before) {
		if !after[p] {
			out = append(out, Change{Kind: "guidance", Action: "removed", ID: p})
		}
	}
	return out
}

// itemized are the manifest keys whose changes the other kinds already
// report.
var itemized = map[string]bool{
	"contract": true, "contract_file": true, "contract_source": true,
	"agent_guidance": true, "skills": true, "tools": true, "services": true,
	"mounts": true, "roles": true,
}

// otherSettings reports top-level manifest settings that changed and have no
// item of their own, so no change goes unmentioned.
func otherSettings(oldRaw, newRaw []byte) []Change {
	old, new := topLevel(oldRaw), topLevel(newRaw)
	if old == nil || new == nil {
		return nil
	}
	var names []string
	for _, k := range changedFields(old, new) {
		if !itemized[k] {
			names = append(names, k)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []Change{{Kind: "manifest", Action: "updated", Text: "settings changed: " + strings.Join(names, ", ")}}
}

// topLevel is manifest.json's top-level fields, with governance policies
// set aside because policies are reported as items.
func topLevel(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range fields {
		if k == "governance" {
			var gov map[string]json.RawMessage
			if json.Unmarshal(v, &gov) == nil {
				delete(gov, "policies")
				if len(gov) == 0 {
					continue
				}
				v, _ = json.Marshal(gov)
			}
		}
		var compact bytes.Buffer
		if json.Compact(&compact, v) == nil {
			out[k] = compact.String()
		}
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

// maxRuleChars bounds one rule in a capped notice.
const maxRuleChars = 400

// maxLines bounds a capped notice so the terminal at launch stays readable.
const maxLines = 40

// Render is the capped plain-text notice shown on the terminal at launch.
// The cap line names the command that lists everything.
func Render(n Notice) string {
	return render(n, true)
}

// RenderAll is the complete listing, with no cap and no clipping.
func RenderAll(n Notice) string {
	return render(n, false)
}

func render(n Notice, capped bool) string {
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
				lines = append(lines, renderChange(c, capped))
			}
		}
		if len(lines) == 0 || capped && shown == maxLines {
			continue
		}
		fmt.Fprintf(&b, "%s:\n", kindTitle[kind])
		for _, line := range lines {
			if capped && shown == maxLines {
				break
			}
			fmt.Fprintf(&b, "  - %s\n", line)
			shown++
		}
	}
	if hidden := len(n.Changes) - shown; capped && hidden > 0 {
		fmt.Fprintf(&b, "  ... and %d more; run %s to list them all.\n", hidden, "`my changes --manifest "+n.Manifest+" --since "+Short(n.From)+" --to "+Short(n.To)+" --all`")
	}
	if len(n.Links) > 0 {
		fmt.Fprintf(&b, "Details: %s\n", strings.Join(n.Links, " "))
	}
	return b.String()
}

var kindOrder = []string{"contract", "guidance", "skill", "tool", "service", "mount", "policy", "role", "manifest"}

var kindTitle = map[string]string{
	"contract": "Contract rules",
	"guidance": "Guidance",
	"skill":    "Skills",
	"tool":     "Tools",
	"service":  "Services",
	"mount":    "Content mounts",
	"policy":   "Policies",
	"role":     "Roles",
	"manifest": "Other settings",
}

func renderChange(c Change, capped bool) string {
	text, before := c.Text, c.Before
	if capped {
		text, before = clip(text), clip(before)
	}
	switch c.Kind {
	case "contract":
		switch c.Action {
		case "amended":
			return fmt.Sprintf("Amended: %q (was %q)", text, before)
		case "retracted":
			return fmt.Sprintf("Retracted: %q", text)
		default:
			return fmt.Sprintf("Added: %q", text)
		}
	case "guidance":
		return capitalize(c.Action) + " " + c.ID
	case "manifest":
		return capitalize(text)
	}
	line := capitalize(c.Action) + " " + c.ID
	if text != "" {
		line += ": " + text
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
