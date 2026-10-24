package manifest

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// ContractFileDir is the manifest directory a contract_file must live in.
	// It is already a manifest control path, so contract edits publish with
	// the manifest.
	ContractFileDir = "guidance"
	// DefaultContractFile is where `my admin contract migrate` writes the
	// inline contract.
	DefaultContractFile = "guidance/contract.md"
	// ContractEntryBudget and ContractByteBudget bound the always-loaded
	// contract; validation warns above them.
	ContractEntryBudget = 150
	ContractByteBudget  = 16 * 1024
)

// ContractSection is one group of contract rules. The leading group of a
// contract file, and every inline contract, has no heading.
type ContractSection struct {
	Heading string   `json:"heading,omitempty"`
	Rules   []string `json:"rules"`
}

// ContractEntry is one rule with its 1-based position across the contract.
type ContractEntry struct {
	Index   int    `json:"index"`
	Heading string `json:"heading,omitempty"`
	Rule    string `json:"rule"`
}

type contractLineKind int

const (
	contractBlank contractLineKind = iota
	contractTitle
	contractHeading
	contractRule
)

// contractLine is one line of a contract file. raw is the line exactly as
// written, without its ending, so edits rewrite only the lines they touch.
type contractLine struct {
	kind contractLineKind
	text string
	raw  string
	eol  string
}

func newContractLine(kind contractLineKind, text string) contractLine {
	line := contractLine{kind: kind, text: text}
	switch kind {
	case contractTitle:
		line.raw = "# " + text
	case contractHeading:
		line.raw = "## " + text
	case contractRule:
		line.raw = "- " + text
	}
	return line
}

// Contract is a manifest's resolved organization contract: either the inline
// `contract` list or the parsed `contract_file`. A file-backed contract keeps
// its exact lines so edits change only the lines they touch.
type Contract struct {
	// File is the manifest-relative contract_file path; empty for inline.
	File    string `json:"file,omitempty"`
	Warning string `json:"warning,omitempty"`
	// Blob is the Git blob id of the contract file bytes; empty for inline.
	Blob  string `json:"blob,omitempty"`
	lines []contractLine
	// eol ends new lines: the file's first line ending, else "\n".
	eol string
}

// ContractSource names where a manifest's contract lives.
func (c Contract) Source() string {
	if c.File != "" {
		return "contract_file"
	}
	return "contract"
}

// Rules returns every rule in file order.
func (c Contract) Rules() []string {
	var rules []string
	for _, line := range c.lines {
		if line.kind == contractRule {
			rules = append(rules, line.text)
		}
	}
	return rules
}

// Entries returns every rule with its position and heading.
func (c Contract) Entries() []ContractEntry {
	var entries []ContractEntry
	heading := ""
	for _, line := range c.lines {
		switch line.kind {
		case contractHeading:
			heading = line.text
		case contractRule:
			entries = append(entries, ContractEntry{Index: len(entries) + 1, Heading: heading, Rule: line.text})
		}
	}
	return entries
}

// Sections groups rules under their headings, in file order. Headings with no
// rules are omitted.
func (c Contract) Sections() []ContractSection {
	var sections []ContractSection
	current := ContractSection{}
	flush := func() {
		if len(current.Rules) != 0 {
			sections = append(sections, current)
		}
	}
	for _, line := range c.lines {
		switch line.kind {
		case contractHeading:
			flush()
			current = ContractSection{Heading: line.text}
		case contractRule:
			current.Rules = append(current.Rules, line.text)
		}
	}
	flush()
	return sections
}

// Bytes returns the contract file content. Untouched lines keep their exact
// text and line ending; only a final line may lack one.
func (c Contract) Bytes() []byte {
	eol := c.eol
	if eol == "" {
		eol = "\n"
	}
	var out bytes.Buffer
	for i, line := range c.lines {
		out.WriteString(line.raw)
		if line.eol != "" {
			out.WriteString(line.eol)
		} else if i < len(c.lines)-1 {
			out.WriteString(eol)
		}
	}
	return out.Bytes()
}

// ContractBlob returns the Git blob id of data, matching `git hash-object`.
func ContractBlob(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// NewContractFile returns a contract file holding rules under no heading.
func NewContractFile(file string, rules []string) Contract {
	c := Contract{File: file, eol: "\n"}
	c.lines = append(c.lines, newContractLine(contractTitle, "Organization contract"), newContractLine(contractBlank, ""))
	for _, rule := range rules {
		c.lines = append(c.lines, newContractLine(contractRule, strings.TrimSpace(rule)))
	}
	for i := range c.lines {
		c.lines[i].eol = "\n"
	}
	c.Blob = ContractBlob(c.Bytes())
	return c
}

// ParseContractFile parses contract file bytes. The format is a Markdown list:
// an optional `# Title` before everything else, optional unique `## Group`
// headings, blank lines, and one single-line `- rule` per entry. Anything else
// is an error, so every change to the file is a one-line diff.
func ParseContractFile(file string, data []byte) (Contract, error) {
	c := Contract{File: file, Blob: ContractBlob(data)}
	if !utf8.Valid(data) {
		return Contract{}, fmt.Errorf("%s is not valid UTF-8", file)
	}
	if bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) {
		return Contract{}, fmt.Errorf("%s starts with a byte-order mark; save it as plain UTF-8", file)
	}
	text := string(data)
	c.eol = "\n"
	if text == "" {
		return c, nil
	}
	segments := strings.SplitAfter(text, "\n")
	if segments[len(segments)-1] == "" {
		segments = segments[:len(segments)-1]
	}
	if strings.HasSuffix(segments[0], "\r\n") {
		c.eol = "\r\n"
	}
	var problems []string
	seenRule := map[string]int{}
	seenHeading := map[string]int{}
	content := false
	for i, segment := range segments {
		n := i + 1
		raw, eol := segment, ""
		switch {
		case strings.HasSuffix(raw, "\r\n"):
			raw, eol = strings.TrimSuffix(raw, "\r\n"), "\r\n"
		case strings.HasSuffix(raw, "\n"):
			raw, eol = strings.TrimSuffix(raw, "\n"), "\n"
		}
		if strings.Contains(raw, "\r") {
			problems = append(problems, fmt.Sprintf("line %d: contains a carriage return outside a line ending", n))
			continue
		}
		add := func(kind contractLineKind, text string) {
			c.lines = append(c.lines, contractLine{kind: kind, text: text, raw: raw, eol: eol})
		}
		switch {
		case strings.TrimSpace(raw) == "":
			add(contractBlank, "")
		case strings.HasPrefix(raw, "# "):
			title := strings.TrimSpace(raw[2:])
			if content {
				problems = append(problems, fmt.Sprintf("line %d: a `# ` title may appear only once, before every heading and rule; use `## ` for groups", n))
				continue
			}
			if err := contractText(title); err != nil {
				problems = append(problems, fmt.Sprintf("line %d: title %v", n, err))
				continue
			}
			content = true
			add(contractTitle, title)
		case strings.HasPrefix(raw, "## "):
			heading := strings.TrimSpace(raw[3:])
			if err := contractText(heading); err != nil {
				problems = append(problems, fmt.Sprintf("line %d: heading %v", n, err))
				continue
			}
			if prev, ok := seenHeading[heading]; ok {
				problems = append(problems, fmt.Sprintf("line %d: heading %q duplicates line %d", n, heading, prev))
				continue
			}
			seenHeading[heading] = n
			content = true
			add(contractHeading, heading)
		case strings.HasPrefix(raw, "- "):
			rule := strings.TrimSpace(raw[2:])
			if err := contractText(rule); err != nil {
				problems = append(problems, fmt.Sprintf("line %d: rule %v", n, err))
				continue
			}
			if prev, ok := seenRule[rule]; ok {
				problems = append(problems, fmt.Sprintf("line %d: rule duplicates line %d: %q", n, prev, rule))
				continue
			}
			seenRule[rule] = n
			content = true
			add(contractRule, rule)
		case strings.TrimSpace(raw) == "-":
			problems = append(problems, fmt.Sprintf("line %d: rule must not be blank", n))
		case raw[0] == ' ' || raw[0] == '\t':
			problems = append(problems, fmt.Sprintf("line %d: indented lines are not allowed; each rule is one top-level `- ` line", n))
		case strings.HasPrefix(raw, "#"):
			problems = append(problems, fmt.Sprintf("line %d: use `## ` for group headings (one `# ` title is allowed at the top)", n))
		default:
			problems = append(problems, fmt.Sprintf("line %d: expected a `- ` rule, a `## ` heading, or a blank line", n))
		}
	}
	if len(problems) != 0 {
		return Contract{}, fmt.Errorf("%s: %s", file, strings.Join(problems, "; "))
	}
	return c, nil
}

func contractText(text string) error {
	if text == "" {
		return errors.New("must not be blank")
	}
	for _, r := range text {
		// A tab inside a rule is allowed, as inline contract rules always
		// were; leading indentation is rejected by the parser.
		if unicode.IsControl(r) && r != '\t' {
			return fmt.Errorf("contains control character %U", r)
		}
	}
	return nil
}

// ValidContractFilePath reports whether value is a clean, relative,
// slash-separated Markdown path under guidance/.
func ValidContractFilePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.Contains(value, ":") {
		return false
	}
	if path.IsAbs(value) || path.Clean(value) != value || pathEscapes(value) {
		return false
	}
	return strings.HasPrefix(value, ContractFileDir+"/") && strings.HasSuffix(value, ".md")
}

// ContractPath returns the editable source path, including the legacy file-only form.
func (doc Document) ContractPath() string {
	if doc.ContractSource != "" {
		return doc.ContractSource
	}
	return doc.ContractFile
}

// CheckContractSource checks the representation independently of file contents.
func CheckContractSource(doc Document) error {
	if doc.ContractSource != "" {
		if doc.ContractFile != "" {
			return errors.New("contract_source and contract_file cannot be combined")
		}
		if doc.Contract == nil {
			return errors.New("contract_source requires an inline contract compatibility copy (use [] for no rules)")
		}
	} else if doc.ContractFile != "" && doc.Contract != nil {
		return errors.New("manifest sets both contract and contract_file; use contract_source for an inline compatibility copy")
	}
	if file := doc.ContractPath(); doc.ContractSource == "" && file != "" && !ValidContractFilePath(file) {
		return fmt.Errorf("contract source %q must be a relative .md path under %s/", file, ContractFileDir)
	}
	return nil
}

// ResolveContractBytes resolves committed or working-tree bytes by the same rules.
// The inline copy is the compatible member view if the editable source drifts.
func ResolveContractBytes(doc Document, data []byte, readErr error) (Contract, error) {
	if err := CheckContractSource(doc); err != nil {
		return Contract{}, err
	}
	if doc.ContractPath() == "" {
		return inlineContract(doc.Contract), nil
	}
	var c Contract
	err := readErr
	if !ValidContractFilePath(doc.ContractPath()) {
		err = fmt.Errorf("unsafe contract source path; expected a relative .md path under guidance/")
	} else if doc.ContractSource != "" && doc.ContractSource != DefaultContractFile {
		// An older my's migrate refuses only because the default file exists;
		// at any other path it would drop the inline copy older members read.
		err = fmt.Errorf("contract_source must be %s", DefaultContractFile)
	}
	if err == nil {
		c, err = ParseContractFile(doc.ContractPath(), data)
	}
	if doc.ContractSource == "" {
		return c, err
	}
	if err == nil && !equalContractRules(doc.Contract, c.Rules()) {
		err = errors.New("file rules differ from the inline compatibility copy")
	}
	if err != nil {
		fallback := inlineContract(doc.Contract)
		fallback.Warning = fmt.Sprintf("contract_source %q: %v; using the inline compatibility copy. An administrator must run my admin contract migrate --manifest-dir <checkout> to regenerate the copy from the file, or add --from-inline to restore the file from the copy", doc.ContractSource, err)
		return fallback, nil
	}
	return c, nil
}

func equalContractRules(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != b[i] {
			return false
		}
	}
	return true
}

// LoadContract resolves the member-visible contract from a manifest checkout.
func LoadContract(root string, doc Document) (Contract, error) {
	if err := CheckContractSource(doc); err != nil {
		return Contract{}, err
	}
	if doc.ContractPath() == "" {
		return inlineContract(doc.Contract), nil
	}
	if root == "" {
		return Contract{}, fmt.Errorf("contract source %q needs the manifest checkout to read", doc.ContractPath())
	}
	data, err := readContractFile(root, doc.ContractPath())
	return ResolveContractBytes(doc, data, err)
}

// RequireContractSource is the strict authoring/publication check. Members can
// continue with their complete compatibility copy while an administrator repairs it.
func RequireContractSource(root string, doc Document) error {
	c, err := LoadContract(root, doc)
	if err != nil {
		return err
	}
	if c.Warning != "" {
		return errors.New(c.Warning)
	}
	return nil
}

// ContractFilePath returns the checkout path of doc's contract_file, refusing
// symlinks anywhere below root so reads and writes cannot leave the manifest.
func ContractFilePath(root, file string) (string, error) {
	if !ValidContractFilePath(file) {
		return "", fmt.Errorf("contract_file %q must be a relative .md path under %s/", file, ContractFileDir)
	}
	current := root
	parts := strings.Split(file, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return current, err
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("contract_file %q: %s is a symlink; the contract must be a regular file in the manifest repo", file, path.Join(parts[:i+1]...))
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return "", fmt.Errorf("contract_file %q is not a regular file", file)
		}
	}
	return current, nil
}

func readContractFile(root, file string) ([]byte, error) {
	full, err := ContractFilePath(root, file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("contract_file %q does not exist in the manifest checkout", file)
	}
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}

// ResolveContractTarget returns the 0-based entry a handle names: the 1-based
// position shown by `my contract list`, or the exact rule text. A number that
// is also the text of a different rule is ambiguous and refused.
func (c Contract) ResolveContractTarget(target string) (int, error) {
	target = strings.TrimSpace(target)
	rules := c.Rules()
	textIdx := -1
	for i, rule := range rules {
		if rule == target {
			textIdx = i
			break
		}
	}
	n, err := strconv.Atoi(target)
	if err != nil {
		if textIdx == -1 {
			return -1, fmt.Errorf("contract rule %q not found; run my contract list", target)
		}
		return textIdx, nil
	}
	if n < 1 || n > len(rules) {
		if textIdx != -1 {
			return textIdx, nil
		}
		return -1, fmt.Errorf("contract rule %d not found; the contract has %d rules", n, len(rules))
	}
	if textIdx != -1 && textIdx != n-1 {
		return -1, fmt.Errorf("%q is both position %d and the text of rule %d; pass the position you mean", target, n, textIdx+1)
	}
	return n - 1, nil
}

func (c Contract) ruleLine(idx int) int {
	seen := 0
	for i, line := range c.lines {
		if line.kind == contractRule {
			if seen == idx {
				return i
			}
			seen++
		}
	}
	return -1
}

func (c Contract) hasRule(text string) bool {
	for _, rule := range c.Rules() {
		if rule == text {
			return true
		}
	}
	return false
}

func (c Contract) withLines(lines []contractLine) Contract {
	out := Contract{File: c.File, lines: lines, eol: c.eol}
	if out.File != "" {
		out.Blob = ContractBlob(out.Bytes())
	}
	return out
}

// AddRule appends text to the group under heading, or to the leading group
// when heading is empty. A missing heading is created at the end. Only a
// file-backed contract has headings.
func (c Contract) AddRule(text, heading string) (Contract, error) {
	text = strings.TrimSpace(text)
	heading = strings.TrimSpace(heading)
	if err := contractText(text); err != nil {
		return Contract{}, fmt.Errorf("contract rule %v", err)
	}
	if c.hasRule(text) {
		return Contract{}, fmt.Errorf("contract rule already exists: %q", text)
	}
	if heading != "" {
		if c.File == "" {
			return Contract{}, errors.New("--under needs a contract_file; run my admin contract migrate first")
		}
		if err := contractText(heading); err != nil {
			return Contract{}, fmt.Errorf("heading %v", err)
		}
	}
	rule := c.newLine(contractRule, text)
	lines := append([]contractLine(nil), c.lines...)
	// The group starts after its heading (or at the top) and ends before the
	// next heading. Insert after its last rule, or after the heading itself.
	start, found := 0, heading == ""
	for i, line := range lines {
		if line.kind == contractHeading && line.text == heading {
			start, found = i+1, true
			break
		}
	}
	if !found {
		if len(lines) != 0 && lines[len(lines)-1].kind != contractBlank {
			lines = append(lines, c.newLine(contractBlank, ""))
		}
		lines = append(lines, c.newLine(contractHeading, heading), c.newLine(contractBlank, ""), rule)
		return c.withLines(lines), nil
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if lines[i].kind == contractHeading {
			end = i
			break
		}
	}
	insert := -1
	for i := start; i < end; i++ {
		if lines[i].kind == contractRule {
			insert = i + 1
		}
	}
	add := []contractLine{rule}
	if insert == -1 {
		// An empty group: go past its title and blank lines, and keep one
		// blank line between the rule and any heading around it.
		insert = start
		for insert < end && (lines[insert].kind == contractTitle || lines[insert].kind == contractBlank) {
			insert++
		}
		if insert > 0 && lines[insert-1].kind != contractBlank {
			add = append([]contractLine{c.newLine(contractBlank, "")}, add...)
		}
		if insert < len(lines) && lines[insert].kind == contractHeading {
			add = append(add, c.newLine(contractBlank, ""))
		}
	}
	lines = append(lines[:insert], append(add, lines[insert:]...)...)
	return c.withLines(lines), nil
}

// RemoveRule removes the 0-based entry idx.
func (c Contract) RemoveRule(idx int) (Contract, string, error) {
	line := c.ruleLine(idx)
	if line == -1 {
		return Contract{}, "", fmt.Errorf("contract rule %d not found", idx+1)
	}
	removed := c.lines[line].text
	lines := append(append([]contractLine(nil), c.lines[:line]...), c.lines[line+1:]...)
	return c.withLines(lines), removed, nil
}

// ReplaceRule rewrites the 0-based entry idx in place as one edit.
func (c Contract) ReplaceRule(idx int, text string) (Contract, string, error) {
	text = strings.TrimSpace(text)
	if err := contractText(text); err != nil {
		return Contract{}, "", fmt.Errorf("contract rule %v", err)
	}
	line := c.ruleLine(idx)
	if line == -1 {
		return Contract{}, "", fmt.Errorf("contract rule %d not found", idx+1)
	}
	old := c.lines[line].text
	if old == text {
		return Contract{}, "", fmt.Errorf("contract rule %d already reads %q", idx+1, text)
	}
	if c.hasRule(text) {
		return Contract{}, "", fmt.Errorf("contract rule already exists: %q", text)
	}
	lines := append([]contractLine(nil), c.lines...)
	replacement := newContractLine(contractRule, text)
	replacement.eol = lines[line].eol
	lines[line] = replacement
	return c.withLines(lines), old, nil
}

// validateContractSource checks contract and contract_file together. The file
// itself is read only when root is known.
func validateContractSource(root string, doc Document, result *ValidationResult) {
	if err := CheckContractSource(doc); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return
	}
	if doc.ContractPath() == "" || doc.ContractSource != "" {
		validateContract(doc.Contract, result)
	}
	if doc.ContractPath() == "" {
		warnContractBudget(inlineContract(doc.Contract), result)
		warnDetachedContractFile(root, doc, result)
		return
	}
	if root == "" {
		return
	}
	c, err := LoadContract(root, doc)
	if err != nil {
		result.Errors = append(result.Errors, err.Error())
		return
	}
	if c.Warning != "" {
		result.Warnings = append(result.Warnings, c.Warning)
	}
	warnContractBudget(c, result)
}

// warnDetachedContractFile flags a contract file that no key references. A my
// release older than contract_source drops that key when it edits the inline
// list, so later edits to the file would silently have no effect.
func warnDetachedContractFile(root string, doc Document, result *ValidationResult) {
	if warning := DetachedContractFileWarning(root, doc); warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
}

// DetachedContractFileWarning reports an unreferenced guidance/contract.md, or
// "" when there is none.
func DetachedContractFileWarning(root string, doc Document) string {
	if root == "" || doc.ContractPath() != "" {
		return ""
	}
	for _, p := range doc.AgentGuidance.Paths {
		if filepath.ToSlash(filepath.Clean(p)) == DefaultContractFile {
			return ""
		}
	}
	if _, err := ContractFilePath(root, DefaultContractFile); err != nil {
		return ""
	}
	return fmt.Sprintf("%s exists but manifest.json does not reference it, so members read only the inline contract; if an older my detached it while editing the inline list, reconnect it with my admin contract migrate --from-inline (the file is rewritten from the inline list)", DefaultContractFile)
}

func inlineContract(rules []string) Contract {
	c := Contract{}
	for _, rule := range rules {
		c.lines = append(c.lines, newContractLine(contractRule, strings.TrimSpace(rule)))
	}
	return c
}

// newLine returns a new line ending like the file's other lines.
func (c Contract) newLine(kind contractLineKind, text string) contractLine {
	line := newContractLine(kind, text)
	line.eol = c.eol
	if line.eol == "" {
		line.eol = "\n"
	}
	return line
}

func warnContractBudget(c Contract, result *ValidationResult) {
	rules := c.Rules()
	size := 0
	for _, line := range c.lines {
		size += len(line.text)
	}
	if len(rules) > ContractEntryBudget || size > ContractByteBudget {
		result.Warnings = append(result.Warnings, fmt.Sprintf("organization contract has %d rules (%d bytes); every rule loads into every session, so keep it under %d rules and %d KiB and move long material into governance.policies", len(rules), size, ContractEntryBudget, ContractByteBudget/1024))
	}
}
