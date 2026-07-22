package syncer

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const supportedGnitRosterVersion = 1

type gnitRoster struct {
	Version int
	Mode    string
	Remote  string
	Members []gnitRosterMember
}

type gnitRosterMember struct {
	ID     string
	Path   string
	Remote string
}

// GnitWorkspaceCheck is a read-only topology diagnostic for my doctor.
type GnitWorkspaceCheck struct {
	Name    string
	Status  string
	Path    string
	Message string
}

// CheckGnitWorkspace explains how a partial Gnit topology relates to the My
// AI-managed checkout set. It never repairs or publishes anything.
func CheckGnitWorkspace(root string, entries []Entry, runner Runner) []GnitWorkspaceCheck {
	if !hasGnitWorkspace(root) {
		return nil
	}
	if runner == nil {
		runner = execCommand
	}
	roster, err := readGnitRoster(root)
	if err != nil {
		return []GnitWorkspaceCheck{{
			Name: "roster", Status: "error", Path: filepath.Join(root, ".gnit", "roster.yaml"),
			Message: "cannot prove coordinated workspace membership: " + err.Error() + "; run gnit doctor",
		}}
	}
	var checks []GnitWorkspaceCheck
	rootCheck := GnitWorkspaceCheck{Name: "root", Status: "ok", Path: root, Message: "coordinated workspace topology is readable"}
	if issue := gnitRootPublishability(root, roster, runner); issue.Code != "" {
		rootCheck.Status = "warning"
		rootCheck.Message = issue.Message
	}
	checks = append(checks, rootCheck)

	managedMembers := map[string]bool{}
	for _, entry := range entries {
		if !canonicalPathWithin(entry.LocalPath, root) {
			continue
		}
		route := classifyGnitEntry(root, roster, entry, runner)
		if route.Member == nil {
			checks = append(checks, GnitWorkspaceCheck{
				Name: entry.ID, Status: "info", Path: entry.LocalPath,
				Message: "not a coordinated workspace member; My AI uses guarded built-in publication for this checkout",
			})
			continue
		}
		managedMembers[route.Member.ID] = true
		switch route.Code {
		case "gnit_member_identity_mismatch":
			checks = append(checks, GnitWorkspaceCheck{Name: route.Member.ID, Status: "error", Path: entry.LocalPath, Message: route.Message + "; run gnit doctor"})
		case "gnit_workspace_unhealthy":
			checks = append(checks, GnitWorkspaceCheck{Name: route.Member.ID, Status: "warning", Path: entry.LocalPath, Message: route.Message + "; run gnit doctor"})
		}
	}
	for _, member := range roster.Members {
		path := filepath.Join(root, filepath.FromSlash(member.Path))
		if !isGitRepoRoot(path, runner) {
			if !managedMembers[member.ID] {
				checks = append(checks, GnitWorkspaceCheck{Name: member.ID, Status: "warning", Path: path, Message: "roster member checkout is missing or invalid; run gnit doctor"})
			}
			continue
		}
		if !managedMembers[member.ID] {
			checks = append(checks, GnitWorkspaceCheck{Name: member.ID, Status: "info", Path: path, Message: "roster member is not managed by My AI; it must be landed before a scoped My AI coordinated publish"})
		}
	}
	return checks
}

type gnitRoute struct {
	Backend string
	Member  *gnitRosterMember
	Code    string
	Message string
}

func classifyGnitEntry(root string, roster gnitRoster, entry Entry, runner Runner) gnitRoute {
	entryPath, err := canonicalPath(entry.LocalPath)
	if err != nil {
		// A missing checkout can still be matched lexically to a roster member so
		// it cannot silently fall through to the built-in publisher.
		entryPath, _ = filepath.Abs(entry.LocalPath)
		entryPath = filepath.Clean(entryPath)
	}
	for i := range roster.Members {
		member := &roster.Members[i]
		memberPath := filepath.Join(root, filepath.FromSlash(member.Path))
		canonicalMember, canonicalErr := canonicalPath(memberPath)
		if canonicalErr != nil {
			canonicalMember, _ = filepath.Abs(memberPath)
			canonicalMember = filepath.Clean(canonicalMember)
		}
		if entryPath != canonicalMember {
			continue
		}
		if canonicalErr != nil || !isGitRepoRoot(memberPath, runner) {
			return gnitRoute{Member: member, Code: "gnit_workspace_unhealthy", Message: fmt.Sprintf("coordinated workspace member %s is missing or is not a Git repository", member.ID)}
		}
		actual, _, originErr := gitTrim(runner, memberPath, "remote", "get-url", "origin")
		if originErr != nil || actual == "" {
			return gnitRoute{Member: member, Code: "gnit_workspace_unhealthy", Message: fmt.Sprintf("coordinated workspace member %s has no origin remote", member.ID)}
		}
		actualKey := normalizeGitRemote(actual)
		if normalizeGitRemote(entry.GitURL) != actualKey || (member.Remote != "" && normalizeGitRemote(member.Remote) != actualKey) {
			return gnitRoute{Member: member, Code: "gnit_member_identity_mismatch", Message: fmt.Sprintf("coordinated workspace member %s does not match the My AI repository identity", member.ID)}
		}
		return gnitRoute{Backend: "gnit", Member: member}
	}
	return gnitRoute{Backend: "builtin"}
}

func exactGnitMember(root string, roster gnitRoster, path string) *gnitRosterMember {
	candidate, err := canonicalPath(path)
	if err != nil {
		return nil
	}
	for i := range roster.Members {
		memberPath, memberErr := canonicalPath(filepath.Join(root, filepath.FromSlash(roster.Members[i].Path)))
		if memberErr == nil && memberPath == candidate {
			return &roster.Members[i]
		}
	}
	return nil
}

func gnitRootPublishability(root string, roster gnitRoster, runner Runner) gnitRoute {
	if !isGitRepoRoot(root, runner) {
		return gnitRoute{Code: "gnit_root_unpublishable", Message: "coordinated workspace root is not a Git repository; run my doctor"}
	}
	actual, _, err := gitTrim(runner, root, "remote", "get-url", "origin")
	if err != nil || actual == "" {
		return gnitRoute{Code: "gnit_root_unpublishable", Message: "coordinated workspace root has no origin remote; run my doctor"}
	}
	if roster.Remote != "" && normalizeGitRemote(roster.Remote) != normalizeGitRemote(actual) {
		return gnitRoute{Code: "gnit_root_unpublishable", Message: "coordinated workspace root origin does not match its roster identity; run my doctor"}
	}
	branch, _, err := gitTrim(runner, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" || branch == "HEAD" {
		return gnitRoute{Code: "gnit_root_unpublishable", Message: "coordinated workspace root is not on a publishable branch; run my doctor"}
	}
	return gnitRoute{}
}

func gnitScopePreflight(root string, roster gnitRoster, selected map[string]bool, runner Runner) gnitRoute {
	var extra []string
	for i := range roster.Members {
		member := &roster.Members[i]
		memberPath := filepath.Join(root, filepath.FromSlash(member.Path))
		canonicalMember, err := canonicalPath(memberPath)
		if err != nil || !isGitRepoRoot(memberPath, runner) {
			return gnitRoute{Code: "gnit_workspace_unhealthy", Message: fmt.Sprintf("coordinated workspace member %s is missing or is not a Git repository; run my doctor", member.ID)}
		}
		actual, _, err := gitTrim(runner, memberPath, "remote", "get-url", "origin")
		if err != nil || actual == "" {
			return gnitRoute{Code: "gnit_workspace_unhealthy", Message: fmt.Sprintf("coordinated workspace member %s has no origin remote; run my doctor", member.ID)}
		}
		if member.Remote != "" && normalizeGitRemote(member.Remote) != normalizeGitRemote(actual) {
			return gnitRoute{Code: "gnit_member_identity_mismatch", Message: fmt.Sprintf("coordinated workspace member %s origin does not match the roster; run my doctor", member.ID)}
		}
		branch, _, err := gitTrim(runner, memberPath, "rev-parse", "--abbrev-ref", "HEAD")
		if err != nil || branch == "" || branch == "HEAD" {
			return gnitRoute{Code: "gnit_workspace_unhealthy", Message: fmt.Sprintf("coordinated workspace member %s is not on a publishable branch; run my doctor", member.ID)}
		}
		head, _, err := gitTrim(runner, memberPath, "rev-parse", "HEAD")
		if err != nil {
			return gnitRoute{Code: "gnit_workspace_unhealthy", Message: fmt.Sprintf("cannot inspect coordinated workspace member %s; run my doctor", member.ID)}
		}
		upstream, _, upstreamErr := gitTrim(runner, memberPath, "rev-parse", "--verify", "refs/remotes/origin/"+branch)
		needsPush := upstreamErr != nil || upstream == "" || upstream != head
		if needsPush && !selected[canonicalMember] {
			extra = append(extra, member.ID)
		}
	}
	if len(extra) != 0 {
		return gnitRoute{
			Code:    "gnit_scope_exceeds_selection",
			Message: fmt.Sprintf("coordinated publish would also publish unselected workspace member(s): %s; widen the My AI sync scope or reconcile them first", strings.Join(extra, ", ")),
		}
	}
	return gnitRoute{}
}

func isGitRepoRoot(path string, runner Runner) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	top, _, err := gitTrim(runner, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	canonicalTop, err := canonicalPath(top)
	if err != nil {
		return false
	}
	canonicalCandidate, err := canonicalPath(path)
	return err == nil && canonicalTop == canonicalCandidate
}

// readGnitRoster parses the deliberately small, versioned subset emitted by
// Gnit. My AI stays dependency-free and fails closed when the file stops
// matching that contract; it never guesses membership from a partial parse.
func readGnitRoster(root string) (gnitRoster, error) {
	path := filepath.Join(root, ".gnit", "roster.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return gnitRoster{}, fmt.Errorf("read Gnit roster: %w", err)
	}
	return parseGnitRoster(data)
}

func parseGnitRoster(data []byte) (gnitRoster, error) {
	var roster gnitRoster
	var err error
	var current *gnitRosterMember
	seenVersion := false
	seenMode := false
	seenMembers := false
	membersBlock := false
	inMembers := false

	finishMember := func() error {
		if current == nil {
			return nil
		}
		if current.ID == "" || current.Path == "" {
			return fmt.Errorf("Gnit roster member requires id and path")
		}
		clean := filepath.Clean(filepath.FromSlash(current.Path))
		if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
			return fmt.Errorf("Gnit roster member %s has invalid path %q", current.ID, current.Path)
		}
		current.Path = filepath.ToSlash(clean)
		for _, existing := range roster.Members {
			if existing.ID == current.ID {
				return fmt.Errorf("Gnit roster has duplicate member id %q", current.ID)
			}
			if existing.Path == current.Path {
				return fmt.Errorf("Gnit roster has duplicate member path %q", current.Path)
			}
		}
		roster.Members = append(roster.Members, *current)
		current = nil
		return nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		raw := strings.TrimRight(scanner.Text(), " \r")
		if strings.ContainsRune(raw, '\t') {
			return gnitRoster{}, fmt.Errorf("Gnit roster line %d contains a tab", lineNo)
		}
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " "))

		if inMembers && indent == 0 && strings.HasPrefix(trimmed, "- ") {
			if err := finishMember(); err != nil {
				return gnitRoster{}, err
			}
			current = &gnitRosterMember{}
			key, value, ok := splitYAMLField(strings.TrimPrefix(trimmed, "- "))
			if !ok || key != "id" {
				return gnitRoster{}, fmt.Errorf("Gnit roster line %d must start a member with id", lineNo)
			}
			current.ID, err = parseYAMLScalar(value)
			if err != nil {
				return gnitRoster{}, fmt.Errorf("Gnit roster line %d: %w", lineNo, err)
			}
			continue
		}

		if inMembers && current != nil && indent == 2 && !strings.HasPrefix(trimmed, "- ") {
			key, value, ok := splitYAMLField(trimmed)
			if !ok {
				return gnitRoster{}, fmt.Errorf("Gnit roster line %d is not a field", lineNo)
			}
			switch key {
			case "id", "path", "remote":
				scalar, scalarErr := parseYAMLScalar(value)
				if scalarErr != nil {
					return gnitRoster{}, fmt.Errorf("Gnit roster line %d: %w", lineNo, scalarErr)
				}
				switch key {
				case "id":
					current.ID = scalar
				case "path":
					current.Path = scalar
				case "remote":
					current.Remote = scalar
				}
			}
			continue
		}

		if indent > 0 || (inMembers && strings.HasPrefix(trimmed, "- ")) {
			// Nested values belong to known or future member fields such as
			// required_excludes. They cannot establish membership.
			continue
		}
		if !inMembers && indent == 0 && strings.HasPrefix(trimmed, "- ") {
			// Sequence item for an unknown top-level field such as ignored.
			continue
		}

		if err := finishMember(); err != nil {
			return gnitRoster{}, err
		}
		inMembers = false
		key, value, ok := splitYAMLField(trimmed)
		if !ok {
			return gnitRoster{}, fmt.Errorf("Gnit roster line %d is not a top-level field", lineNo)
		}
		switch key {
		case "version":
			if seenVersion {
				return gnitRoster{}, fmt.Errorf("Gnit roster repeats version")
			}
			seenVersion = true
			versionText, scalarErr := parseYAMLScalar(value)
			if scalarErr != nil {
				return gnitRoster{}, scalarErr
			}
			roster.Version, err = strconv.Atoi(versionText)
			if err != nil {
				return gnitRoster{}, fmt.Errorf("Gnit roster version must be an integer")
			}
		case "mode":
			if seenMode {
				return gnitRoster{}, fmt.Errorf("Gnit roster repeats mode")
			}
			seenMode = true
			roster.Mode, err = parseYAMLScalar(value)
			if err != nil {
				return gnitRoster{}, err
			}
		case "remote":
			roster.Remote, err = parseYAMLScalar(value)
			if err != nil {
				return gnitRoster{}, err
			}
		case "members":
			if seenMembers {
				return gnitRoster{}, fmt.Errorf("Gnit roster repeats members")
			}
			seenMembers = true
			switch strings.TrimSpace(value) {
			case "":
				inMembers = true
				membersBlock = true
			case "[]":
				inMembers = false
			default:
				return gnitRoster{}, fmt.Errorf("Gnit roster members must be a block sequence")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return gnitRoster{}, fmt.Errorf("scan Gnit roster: %w", err)
	}
	if err := finishMember(); err != nil {
		return gnitRoster{}, err
	}
	if !seenVersion || !seenMode || !seenMembers {
		return gnitRoster{}, fmt.Errorf("Gnit roster requires version, mode, and members")
	}
	if membersBlock && len(roster.Members) == 0 {
		return gnitRoster{}, fmt.Errorf("Gnit roster members block must contain a sequence")
	}
	if roster.Version != supportedGnitRosterVersion {
		return gnitRoster{}, fmt.Errorf("unsupported Gnit roster version %d", roster.Version)
	}
	switch roster.Mode {
	case "control", "shared", "local":
	default:
		return gnitRoster{}, fmt.Errorf("unsupported Gnit roster mode %q", roster.Mode)
	}
	return roster, nil
}

func splitYAMLField(value string) (string, string, bool) {
	key, rest, ok := strings.Cut(value, ":")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(rest), true
}

func parseYAMLScalar(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("expected a scalar value")
	}
	if value == "null" || value == "~" {
		return "", nil
	}
	if strings.HasPrefix(value, "\"") {
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid quoted scalar")
		}
		return parsed, nil
	}
	if strings.HasPrefix(value, "'") {
		if len(value) < 2 || !strings.HasSuffix(value, "'") {
			return "", fmt.Errorf("invalid quoted scalar")
		}
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	if strings.ContainsAny(value, "{}[]") {
		return "", fmt.Errorf("expected a scalar value")
	}
	return value, nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func normalizeGitRemote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
			host := strings.ToLower(parsed.Hostname())
			path := strings.Trim(strings.TrimSuffix(parsed.Path, ".git"), "/")
			if host != "" && path != "" {
				return host + "/" + strings.ToLower(path)
			}
		}
	}
	if at := strings.LastIndex(value, "@"); at >= 0 {
		if colon := strings.Index(value[at+1:], ":"); colon >= 0 {
			colon += at + 1
			host := strings.ToLower(value[at+1 : colon])
			path := strings.Trim(strings.TrimSuffix(value[colon+1:], ".git"), "/")
			if host != "" && path != "" {
				return host + "/" + strings.ToLower(path)
			}
		}
	}
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimSuffix(value, "/")
	return strings.ToLower(value)
}
