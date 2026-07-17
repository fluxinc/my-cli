// Package governance validates proposed Git changes against policy loaded
// exclusively from a trusted manifest base revision.
package governance

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/access"
	"github.com/fluxinc/my-cli/internal/manifest"
)

const ManifestSurface = "@manifest"

type Runner func(name string, args ...string) ([]byte, error)

type CheckInput struct {
	Repo            string
	Repository      string
	BaseRef         string
	HeadRef         string
	ManifestRepo    string
	ManifestBaseRef string
	ManifestPath    string
	Mount           string
	ActorID         int64
	ActorLogin      string
	Runner          Runner
}

type Report struct {
	Allowed            bool        `json:"allowed"`
	Organization       string      `json:"organization"`
	Repository         string      `json:"repository"`
	Mount              string      `json:"mount"`
	BaseRef            string      `json:"base_ref"`
	HeadRef            string      `json:"head_ref"`
	ManifestBaseRef    string      `json:"manifest_base_ref"`
	ManifestCommit     string      `json:"manifest_commit"`
	ActorID            int64       `json:"actor_id"`
	ActorLogin         string      `json:"actor_login"`
	ActorPermission    string      `json:"actor_permission"`
	TrustedBasePolicy  bool        `json:"trusted_base_policy"`
	CheckedParentEdges int         `json:"checked_parent_edges"`
	CheckedProtections int         `json:"checked_protections"`
	Violations         []Violation `json:"violations,omitempty"`
}

type Violation struct {
	ReasonCode string `json:"reason_code"`
	Mode       string `json:"mode"`
	Path       string `json:"path"`
	Commit     string `json:"commit"`
	Parent     string `json:"parent"`
	Message    string `json:"message"`
}

type treeEntry struct {
	Mode string
	Type string
	OID  string
}

type actorIdentity struct {
	ID     int64  `json:"id"`
	NodeID string `json:"node_id"`
	Login  string `json:"login"`
}

type collaboratorPermission struct {
	Permission string        `json:"permission"`
	User       actorIdentity `json:"user"`
}

func Check(input CheckInput) (Report, error) {
	input.ManifestPath = strings.TrimSpace(input.ManifestPath)
	if input.ManifestPath == "" {
		input.ManifestPath = "manifest.json"
	}
	for name, value := range map[string]string{
		"repository checkout": input.Repo, "repository": input.Repository, "base ref": input.BaseRef, "head ref": input.HeadRef,
		"manifest repository path": input.ManifestRepo, "manifest base ref": input.ManifestBaseRef,
		"mount": input.Mount, "actor login": input.ActorLogin,
	} {
		if strings.TrimSpace(value) == "" {
			return Report{}, fmt.Errorf("%s is required", name)
		}
	}
	if input.ActorID <= 0 {
		return Report{}, fmt.Errorf("actor id must be a positive immutable provider id")
	}
	if !portableGitPath(input.ManifestPath) {
		return Report{}, fmt.Errorf("manifest path must be a relative Git path that stays inside the repository")
	}
	if !validGitHubLogin(input.ActorLogin) {
		return Report{}, fmt.Errorf("actor login is not a valid GitHub login")
	}
	if input.Runner == nil {
		return Report{}, fmt.Errorf("governance runner is required")
	}

	manifestCommit, err := gitText(input.Runner, input.ManifestRepo, "rev-parse", input.ManifestBaseRef+"^{commit}")
	if err != nil {
		return Report{}, fmt.Errorf("resolve trusted manifest base ref: %w", err)
	}
	manifestBytes, err := gitBytes(input.Runner, input.ManifestRepo, "show", input.ManifestBaseRef+":"+filepath.ToSlash(input.ManifestPath))
	if err != nil {
		return Report{}, fmt.Errorf("read governance from trusted manifest base ref: %w", err)
	}
	var doc manifest.Document
	if err := json.Unmarshal(manifestBytes, &doc); err != nil {
		return Report{}, fmt.Errorf("parse trusted base manifest: %w", err)
	}
	validation := manifest.ValidateDocument("", doc)
	if len(validation.Errors) != 0 {
		return Report{}, fmt.Errorf("trusted base manifest is invalid: %s", strings.Join(validation.Errors, "; "))
	}
	if !manifest.GovernanceConfigured(doc.Governance) {
		return Report{}, fmt.Errorf("trusted base manifest does not configure governance")
	}

	repository, ok := access.GitHubRepositoryName(input.Repository)
	if !ok {
		return Report{}, fmt.Errorf("repository must be a GitHub owner/repository or URL")
	}
	if err := validateSurfaceRepository(doc, input.Mount, repository); err != nil {
		return Report{}, err
	}
	identity, permission, err := resolvePRActor(input.Runner, repository, input.ActorID, input.ActorLogin)
	if err != nil {
		return Report{}, err
	}
	report := Report{
		Repository: repository, Mount: input.Mount, BaseRef: input.BaseRef, HeadRef: input.HeadRef,
		ManifestBaseRef: input.ManifestBaseRef, ManifestCommit: manifestCommit,
		Organization: doc.Organization.ID, ActorID: identity.ID, ActorLogin: identity.Login,
		ActorPermission: permission, TrustedBasePolicy: true,
	}

	if _, err := gitBytes(input.Runner, input.Repo, "merge-base", "--is-ancestor", input.BaseRef, input.HeadRef); err != nil {
		return Report{}, fmt.Errorf("head must descend from the declared base ref: %w", err)
	}
	edges, err := introducedEdges(input.Runner, input.Repo, input.BaseRef, input.HeadRef)
	if err != nil {
		return Report{}, err
	}
	report.CheckedParentEdges = len(edges)

	if input.Mount == ManifestSurface {
		if !permissionAtLeast(permission, doc.Governance.Authorization.AdminPermission) {
			violations, err := manifestSurfaceViolations(input.Runner, input.Repo, edges)
			if err != nil {
				return Report{}, err
			}
			report.Violations = violations
		}
		report.CheckedProtections = 1
		report.Allowed = len(report.Violations) == 0
		return report, nil
	}

	protections := protectionsForMount(doc.Governance.Protections, input.Mount)
	report.CheckedProtections = len(protections)
	cache := map[string]map[string]treeEntry{}
	seen := map[string]bool{}
	for _, edge := range edges {
		parentTree, err := loadTreeCached(cache, input.Runner, input.Repo, edge.Parent)
		if err != nil {
			return Report{}, err
		}
		commitTree, err := loadTreeCached(cache, input.Runner, input.Repo, edge.Commit)
		if err != nil {
			return Report{}, err
		}
		for _, protection := range protections {
			if protection.AdminOverride && permissionAtLeast(permission, doc.Governance.Authorization.AdminPermission) {
				continue
			}
			violations := compareProtection(protection, parentTree, commitTree, edge)
			for _, violation := range violations {
				key := violation.ReasonCode + "\x00" + violation.Path + "\x00" + violation.Commit + "\x00" + violation.Parent
				if !seen[key] {
					report.Violations = append(report.Violations, violation)
					seen[key] = true
				}
			}
		}
		if input.Mount == doc.Governance.Attestations.Mount {
			if err := validateAttestationAdditions(input.Runner, input.Repo, doc, manifestCommit, parentTree, commitTree, edge, input.ActorID); err != nil {
				report.Violations = append(report.Violations, Violation{
					ReasonCode: "attestation_subject_mismatch", Mode: "append-only", Path: attestationErrorPath(err),
					Commit: edge.Commit, Parent: edge.Parent, Message: err.Error(),
				})
			}
		}
	}
	sort.Slice(report.Violations, func(i, j int) bool {
		if report.Violations[i].Commit != report.Violations[j].Commit {
			return report.Violations[i].Commit < report.Violations[j].Commit
		}
		if report.Violations[i].Path != report.Violations[j].Path {
			return report.Violations[i].Path < report.Violations[j].Path
		}
		return report.Violations[i].ReasonCode < report.Violations[j].ReasonCode
	})
	report.Allowed = len(report.Violations) == 0
	return report, nil
}

type commitEdge struct{ Parent, Commit string }

func introducedEdges(runner Runner, repo, base, head string) ([]commitEdge, error) {
	out, err := gitText(runner, repo, "rev-list", "--reverse", "--topo-order", "--parents", head, "^"+base)
	if err != nil {
		return nil, fmt.Errorf("enumerate full base...head history: %w", err)
	}
	var edges []commitEdge
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, parent := range fields[1:] {
			key := parent + "\x00" + fields[0]
			if !seen[key] {
				edges = append(edges, commitEdge{Parent: parent, Commit: fields[0]})
				seen[key] = true
			}
		}
	}
	key := base + "\x00" + head
	if !seen[key] && base != head {
		edges = append(edges, commitEdge{Parent: base, Commit: head})
	}
	return edges, nil
}

func validateSurfaceRepository(doc manifest.Document, mountID, repository string) error {
	if mountID == ManifestSurface {
		expected, ok := access.GitHubRepositoryName(doc.Governance.Authorization.ManifestRepository)
		if !ok || !strings.EqualFold(expected, repository) {
			return fmt.Errorf("manifest surface repository %q does not match trusted governance authority %q", repository, doc.Governance.Authorization.ManifestRepository)
		}
		return nil
	}
	for _, mount := range manifest.EffectiveMounts(doc) {
		if mount.ID != mountID {
			continue
		}
		expected, ok := access.GitHubRepositoryName(mount.GitURL)
		if !ok || !strings.EqualFold(expected, repository) {
			return fmt.Errorf("repository %q does not match trusted mount %q repository", repository, mountID)
		}
		return nil
	}
	return fmt.Errorf("mount %q is not declared by the trusted base manifest", mountID)
}

func protectionsForMount(values []manifest.Protection, mount string) []manifest.Protection {
	var out []manifest.Protection
	for _, protection := range values {
		if protection.Mount == mount {
			out = append(out, protection)
		}
	}
	return out
}

func resolvePRActor(runner Runner, repository string, actorID int64, login string) (actorIdentity, string, error) {
	out, err := runner("gh", "api", "users/"+login)
	if err != nil {
		return actorIdentity{}, "", fmt.Errorf("resolve pull request author identity: %s", commandError(out, err))
	}
	var identity actorIdentity
	if err := json.Unmarshal(out, &identity); err != nil || identity.ID <= 0 || identity.NodeID == "" {
		return actorIdentity{}, "", fmt.Errorf("resolve pull request author identity: incomplete immutable GitHub identity")
	}
	if identity.ID != actorID {
		return actorIdentity{}, "", fmt.Errorf("pull request author login %q resolves to immutable id %d, not declared id %d", login, identity.ID, actorID)
	}
	out, err = runner("gh", "api", "repos/"+repository+"/collaborators/"+login+"/permission")
	if err != nil {
		return actorIdentity{}, "", fmt.Errorf("resolve pull request author permission on protected repository: %s", commandError(out, err))
	}
	var permission collaboratorPermission
	if err := json.Unmarshal(out, &permission); err != nil {
		return actorIdentity{}, "", fmt.Errorf("parse pull request author permission: %w", err)
	}
	if permission.User.ID != 0 && permission.User.ID != actorID {
		return actorIdentity{}, "", fmt.Errorf("repository permission response actor id does not match pull request author")
	}
	if permission.Permission == "" {
		permission.Permission = "none"
	}
	return identity, permission.Permission, nil
}

func permissionAtLeast(actual, required string) bool {
	rank := map[string]int{"none": 0, "read": 1, "triage": 1, "write": 2, "maintain": 3, "admin": 4}
	return rank[actual] >= rank[required]
}

func compareProtection(protection manifest.Protection, before, after map[string]treeEntry, edge commitEdge) []Violation {
	paths := make(map[string]bool)
	for path := range before {
		if protectedPath(path, protection.Paths) {
			paths[path] = true
		}
	}
	for path := range after {
		if protectedPath(path, protection.Paths) {
			paths[path] = true
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	var out []Violation
	for _, path := range ordered {
		old, existed := before[path]
		newEntry, exists := after[path]
		switch protection.Mode {
		case "no-delete":
			if existed && !exists {
				out = append(out, Violation{ReasonCode: "protected_path_deleted", Mode: protection.Mode, Path: path, Commit: edge.Commit, Parent: edge.Parent, Message: "path was present in the parent tree and absent from the commit tree"})
			}
		case "append-only":
			if existed && !exists {
				out = append(out, Violation{ReasonCode: "append_only_path_deleted", Mode: protection.Mode, Path: path, Commit: edge.Commit, Parent: edge.Parent, Message: "append-only path was removed from the tree"})
			} else if existed && exists && old != newEntry {
				out = append(out, Violation{ReasonCode: "append_only_path_modified", Mode: protection.Mode, Path: path, Commit: edge.Commit, Parent: edge.Parent, Message: "append-only path changed mode, type, or object id"})
			}
		}
	}
	return out
}

func manifestSurfaceViolations(runner Runner, repo string, edges []commitEdge) ([]Violation, error) {
	cache := map[string]map[string]treeEntry{}
	var out []Violation
	for _, edge := range edges {
		before, err := loadTreeCached(cache, runner, repo, edge.Parent)
		if err != nil {
			return nil, err
		}
		after, err := loadTreeCached(cache, runner, repo, edge.Commit)
		if err != nil {
			return nil, err
		}
		paths := map[string]bool{}
		for path := range before {
			paths[path] = true
		}
		for path := range after {
			paths[path] = true
		}
		for path := range paths {
			if before[path] != after[path] {
				out = append(out, Violation{ReasonCode: "manifest_admin_required", Mode: "admin-only", Path: path, Commit: edge.Commit, Parent: edge.Parent, Message: "only a repository administrator may change the manifest control plane"})
			}
		}
	}
	return out, nil
}

func protectedPath(path string, prefixes []string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
	for _, prefix := range prefixes {
		prefix = strings.TrimSuffix(filepath.ToSlash(filepath.Clean(prefix)), "/")
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func loadTreeCached(cache map[string]map[string]treeEntry, runner Runner, repo, ref string) (map[string]treeEntry, error) {
	if tree, ok := cache[ref]; ok {
		return tree, nil
	}
	out, err := gitBytes(runner, repo, "ls-tree", "-r", "-z", "--full-tree", ref)
	if err != nil {
		return nil, fmt.Errorf("read tree %s: %w", ref, err)
	}
	tree := map[string]treeEntry{}
	for _, record := range strings.Split(string(out), "\x00") {
		if record == "" {
			continue
		}
		tab := strings.IndexByte(record, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("parse git tree record")
		}
		meta := strings.Fields(record[:tab])
		if len(meta) != 3 {
			return nil, fmt.Errorf("parse git tree metadata")
		}
		tree[record[tab+1:]] = treeEntry{Mode: meta[0], Type: meta[1], OID: meta[2]}
	}
	cache[ref] = tree
	return tree, nil
}

type policyAttestation struct {
	SchemaVersion   int    `json:"schema_version"`
	Organization    string `json:"organization"`
	PolicyID        string `json:"policy_id"`
	PolicyVersion   string `json:"policy_version"`
	PolicySHA256    string `json:"policy_sha256"`
	SubjectProvider string `json:"subject_provider"`
	SubjectID       int64  `json:"subject_id"`
	SubjectLogin    string `json:"subject_login"`
	AcceptedAt      string `json:"accepted_at"`
	ManifestCommit  string `json:"manifest_commit"`
}

type pathError struct{ path, message string }

func (e pathError) Error() string { return e.message }

func validateAttestationAdditions(runner Runner, repo string, doc manifest.Document, manifestCommit string, before, after map[string]treeEntry, edge commitEdge, actorID int64) error {
	prefix := doc.Governance.Attestations.Path
	for path, entry := range after {
		if !protectedPath(path, []string{prefix}) {
			continue
		}
		if _, existed := before[path]; existed || entry.Type != "blob" {
			continue
		}
		out, err := gitBytes(runner, repo, "show", edge.Commit+":"+path)
		if err != nil {
			return pathError{path: path, message: fmt.Sprintf("read added attestation %s: %v", path, err)}
		}
		var attestation policyAttestation
		if err := json.Unmarshal(out, &attestation); err != nil {
			return pathError{path: path, message: fmt.Sprintf("added attestation %s is invalid JSON: %v", path, err)}
		}
		if attestation.SchemaVersion != 1 || attestation.Organization != doc.Organization.ID || attestation.SubjectProvider != "github" {
			return pathError{path: path, message: fmt.Sprintf("added attestation %s has invalid schema, organization, or identity provider", path)}
		}
		if attestation.SubjectID != actorID {
			return pathError{path: path, message: fmt.Sprintf("attestation subject_id %d does not match pull request author id %d", attestation.SubjectID, actorID)}
		}
		policy, ok := policyByID(doc.Governance.Policies, attestation.PolicyID)
		if !ok || policy.Version != attestation.PolicyVersion || policy.SHA256 != attestation.PolicySHA256 {
			return pathError{path: path, message: fmt.Sprintf("attestation %s does not match a policy in the trusted base manifest", path)}
		}
		if _, err := time.Parse(time.RFC3339Nano, attestation.AcceptedAt); err != nil {
			return pathError{path: path, message: fmt.Sprintf("attestation %s has invalid accepted_at", path)}
		}
		if attestation.ManifestCommit != manifestCommit {
			return pathError{path: path, message: fmt.Sprintf("attestation manifest_commit does not match trusted base manifest commit %s", manifestCommit)}
		}
		rel := strings.TrimPrefix(filepath.ToSlash(path), strings.TrimSuffix(filepath.ToSlash(prefix), "/")+"/")
		expected := filepath.ToSlash(filepath.Join(
			strconv.FormatInt(actorID, 10), policy.ID, strings.TrimPrefix(policy.SHA256, "sha256:")+".json",
		))
		if rel != expected {
			return pathError{path: path, message: fmt.Sprintf("attestation path %s must be %s/%s", path, prefix, expected)}
		}
	}
	return nil
}

func policyByID(policies []manifest.Policy, id string) (manifest.Policy, bool) {
	for _, policy := range policies {
		if policy.ID == id {
			return policy, true
		}
	}
	return manifest.Policy{}, false
}

func portableGitPath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(value))
	return clean == value && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func validGitHubLogin(value string) bool {
	if value == "" || len(value) > 39 || value[0] == '-' || value[len(value)-1] == '-' || strings.Contains(value, "--") {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return false
	}
	return true
}

func attestationErrorPath(err error) string {
	if value, ok := err.(pathError); ok {
		return value.path
	}
	return ""
}

func gitText(runner Runner, repo string, args ...string) (string, error) {
	out, err := gitBytes(runner, repo, args...)
	return strings.TrimSpace(string(out)), err
}

func gitBytes(runner Runner, repo string, args ...string) ([]byte, error) {
	command := append([]string{"-C", repo}, args...)
	out, err := runner("git", command...)
	if err != nil {
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), commandError(out, err))
	}
	return out, nil
}

func commandError(out []byte, err error) string {
	message := strings.TrimSpace(string(out))
	if message == "" && err != nil {
		message = err.Error()
	}
	return message
}
