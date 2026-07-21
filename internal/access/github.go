// Package access resolves provider identity and repository authorization for
// manifest-managed checkouts.
package access

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path"
	"strconv"
	"strings"
)

// Runner executes an external command. Tests replace it with deterministic
// GitHub API responses.
type Runner func(name string, args ...string) ([]byte, error)

type State string

const (
	StateAllowed State = "allowed"
	StateDenied  State = "denied"
	StateUnknown State = "unknown"
)

type Permission string

const (
	PermissionNone  Permission = "none"
	PermissionRead  Permission = "read"
	PermissionWrite Permission = "write"
	PermissionAdmin Permission = "admin"
)

type Actor struct {
	ID     int64  `json:"id"`
	NodeID string `json:"node_id"`
	Login  string `json:"login"`
}

type Repository struct {
	ID         int64      `json:"id"`
	NodeID     string     `json:"node_id"`
	FullName   string     `json:"full_name"`
	Private    bool       `json:"private"`
	Permission Permission `json:"permission"`
}

type Decision struct {
	State      State      `json:"state"`
	ReasonCode string     `json:"reason_code"`
	Message    string     `json:"message,omitempty"`
	Actor      Actor      `json:"actor,omitzero"`
	Repository Repository `json:"repository,omitzero"`
	HTTPStatus int        `json:"http_status,omitempty"`
}

func (d Decision) Allows(required Permission) bool {
	return d.State == StateAllowed && permissionRank(d.Repository.Permission) >= permissionRank(required)
}

// GitHubRepositoryName resolves owner/name from a manifest repository value or
// a GitHub HTTPS/SSH Git URL.
func GitHubRepositoryName(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if validOwnerRepo(value) {
		return strings.TrimSuffix(value, ".git"), true
	}
	if strings.HasPrefix(value, "git@github.com:") {
		return cleanOwnerRepo(strings.TrimPrefix(value, "git@github.com:"))
	}
	u, err := url.Parse(value)
	if err != nil {
		return "", false
	}
	if !strings.EqualFold(u.Hostname(), "github.com") {
		return "", false
	}
	return cleanOwnerRepo(strings.TrimPrefix(u.Path, "/"))
}

func cleanOwnerRepo(value string) (string, bool) {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".git")
	value = path.Clean(value)
	if !validOwnerRepo(value) {
		return "", false
	}
	return value, true
}

func validOwnerRepo(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
				continue
			}
			return false
		}
	}
	return true
}

// ResolveGitHub checks the current authenticated actor and that actor's
// permission on repository. A denial is evidence, not by itself authorization
// to quarantine local data; the access state machine applies baselines and
// confirmation rules separately.
func ResolveGitHub(repository string, runner Runner) Decision {
	actorDecision := ResolveGitHubActor(runner)
	if actorDecision.State != StateAllowed {
		return actorDecision
	}
	return ResolveGitHubForActor(repository, actorDecision.Actor, runner)
}

// ResolveGitHubActor resolves the immutable identity authenticated by gh.
// Callers checking several repositories should resolve this once and pass the
// result to ResolveGitHubForActor or ResolveGitHubKnownForActor.
func ResolveGitHubActor(runner Runner) Decision {
	if runner == nil {
		if _, err := exec.LookPath("gh"); err != nil {
			return Decision{State: StateUnknown, ReasonCode: "gh_missing", Message: "install GitHub CLI and run `gh auth login`"}
		}
		runner = execCommand
	}

	actorOut, actorErr := runner("gh", "api", "user")
	if actorErr != nil {
		return commandFailureDecision(actorOut, actorErr, "actor_lookup_failed")
	}
	var actor Actor
	if err := json.Unmarshal(actorOut, &actor); err != nil || actor.ID == 0 || actor.NodeID == "" || actor.Login == "" {
		return Decision{State: StateUnknown, ReasonCode: "invalid_actor_response", Message: "GitHub returned an incomplete immutable actor identity"}
	}
	return Decision{State: StateAllowed, ReasonCode: "positive_identity", Actor: actor}
}

// ResolveGitHubForActor checks one repository for an already-resolved actor.
// It avoids repeating the actor API request when a caller checks a target set.
func ResolveGitHubForActor(repository string, actor Actor, runner Runner) Decision {
	repoName, ok := GitHubRepositoryName(repository)
	if !ok {
		return Decision{State: StateUnknown, ReasonCode: "unsupported_repository", Message: "expected github.com owner/repository", Actor: actor}
	}
	if actor.ID == 0 || actor.NodeID == "" || actor.Login == "" {
		return Decision{State: StateUnknown, ReasonCode: "invalid_actor_response", Message: "GitHub actor identity is incomplete"}
	}
	if runner == nil {
		if _, err := exec.LookPath("gh"); err != nil {
			return Decision{State: StateUnknown, ReasonCode: "gh_missing", Message: "install GitHub CLI and run `gh auth login`", Actor: actor}
		}
		runner = execCommand
	}

	repoOut, repoErr := runner("gh", "api", "-i", "repos/"+repoName)
	status, headers, body := parseHTTPResponse(repoOut)
	if repoErr != nil {
		decision := classifyRepositoryFailure(status, headers, body, repoErr)
		decision.Actor = actor
		decision.HTTPStatus = status
		return decision
	}
	if status != 0 && status != http.StatusOK {
		decision := classifyRepositoryFailure(status, headers, body, errors.New(http.StatusText(status)))
		decision.Actor = actor
		decision.HTTPStatus = status
		return decision
	}

	return repositoryDecision(actor, status, body)
}

// ResolveGitHubKnown retries a name-based 404 through the immutable numeric
// repository id from a positive baseline. This prevents a rename or transfer
// from being misclassified as revocation merely because the old name stopped
// resolving.
func ResolveGitHubKnown(repository string, known Repository, runner Runner) Decision {
	actorDecision := ResolveGitHubActor(runner)
	if actorDecision.State != StateAllowed {
		return actorDecision
	}
	return ResolveGitHubKnownForActor(repository, known, actorDecision.Actor, runner)
}

// ResolveGitHubKnownForActor is ResolveGitHubKnown without a repeated actor
// lookup. It retains immutable repository-id fallback for rename/transfer
// ambiguity.
func ResolveGitHubKnownForActor(repository string, known Repository, actor Actor, runner Runner) Decision {
	decision := ResolveGitHubForActor(repository, actor, runner)
	if decision.State != StateDenied || decision.ReasonCode != "repository_not_found" || known.ID == 0 || known.NodeID == "" {
		return decision
	}
	if runner == nil {
		runner = execCommand
	}
	out, callErr := runner("gh", "api", "-i", "repositories/"+strconv.FormatInt(known.ID, 10))
	status, headers, body := parseHTTPResponse(out)
	if callErr != nil || (status != 0 && status != http.StatusOK) {
		resolved := classifyRepositoryFailure(status, headers, body, callErr)
		resolved.Actor = decision.Actor
		resolved.HTTPStatus = status
		if resolved.State == StateDenied {
			resolved.ReasonCode = "known_repository_not_found"
			resolved.Message = "the immutable repository id is no longer visible to the authenticated actor"
		}
		return resolved
	}
	resolved := repositoryDecision(decision.Actor, status, body)
	if resolved.Repository.NodeID != "" && resolved.Repository.NodeID != known.NodeID {
		return Decision{
			State: StateUnknown, ReasonCode: "repository_identity_mismatch",
			Message: "GitHub returned a different immutable repository identity for the cached numeric id",
			Actor:   decision.Actor, HTTPStatus: status,
		}
	}
	return resolved
}

func repositoryDecision(actor Actor, status int, body []byte) Decision {
	var payload struct {
		ID          int64  `json:"id"`
		NodeID      string `json:"node_id"`
		FullName    string `json:"full_name"`
		Private     bool   `json:"private"`
		Permissions struct {
			Admin    bool `json:"admin"`
			Maintain bool `json:"maintain"`
			Push     bool `json:"push"`
			Triage   bool `json:"triage"`
			Pull     bool `json:"pull"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.ID == 0 || payload.NodeID == "" || payload.FullName == "" {
		return Decision{State: StateUnknown, ReasonCode: "invalid_repository_response", Message: "GitHub returned an incomplete immutable repository identity", Actor: actor, HTTPStatus: status}
	}
	permission := PermissionNone
	switch {
	case payload.Permissions.Admin:
		permission = PermissionAdmin
	case payload.Permissions.Maintain || payload.Permissions.Push:
		permission = PermissionWrite
	case payload.Permissions.Triage || payload.Permissions.Pull || !payload.Private:
		permission = PermissionRead
	}
	repositoryResult := Repository{
		ID: payload.ID, NodeID: payload.NodeID, FullName: payload.FullName,
		Private: payload.Private, Permission: permission,
	}
	if permission == PermissionNone {
		return Decision{State: StateDenied, ReasonCode: "permission_denied", Message: "authenticated actor has no repository permission", Actor: actor, Repository: repositoryResult, HTTPStatus: status}
	}
	return Decision{State: StateAllowed, ReasonCode: "positive_permission", Actor: actor, Repository: repositoryResult, HTTPStatus: status}
}

func Require(decision Decision, permission Permission) error {
	if decision.Allows(permission) {
		return nil
	}
	actor := decision.Actor.Login
	if actor == "" {
		actor = "current GitHub actor"
	}
	if decision.State == StateAllowed {
		return fmt.Errorf("%s has %s permission on %s; %s permission is required", actor, decision.Repository.Permission, decision.Repository.FullName, permission)
	}
	message := decision.Message
	if message == "" {
		message = decision.ReasonCode
	}
	return fmt.Errorf("cannot establish %s GitHub permission for %s: %s (%s)", permission, actor, message, decision.ReasonCode)
}

// RequireGitHubPermission checks a GitHub URL or owner/name and is a no-op for
// non-GitHub remotes such as local fixture repositories.
func RequireGitHubPermission(repository string, permission Permission, runner Runner) (Decision, bool, error) {
	if _, ok := GitHubRepositoryName(repository); !ok {
		return Decision{}, false, nil
	}
	decision := ResolveGitHub(repository, runner)
	return decision, true, Require(decision, permission)
}

func permissionRank(permission Permission) int {
	switch permission {
	case PermissionRead:
		return 1
	case PermissionWrite:
		return 2
	case PermissionAdmin:
		return 3
	default:
		return 0
	}
}

func classifyRepositoryFailure(status int, headers http.Header, body []byte, commandErr error) Decision {
	message := strings.TrimSpace(string(body))
	if message == "" && commandErr != nil {
		message = commandErr.Error()
	}
	if headers.Get("X-GitHub-SSO") != "" || strings.Contains(strings.ToLower(message), "saml sso") {
		return Decision{State: StateUnknown, ReasonCode: "sso_authorization_required", Message: message}
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "resource not accessible by personal access token") || strings.Contains(lower, "insufficient scope") || strings.Contains(lower, "oauth scope") {
		return Decision{State: StateUnknown, ReasonCode: "credential_scope_insufficient", Message: message}
	}
	if status == http.StatusNotFound {
		if scopes, present := headers["X-Oauth-Scopes"]; present && !scopeListContains(scopes, "repo") && !scopeListContains(scopes, "public_repo") {
			return Decision{State: StateUnknown, ReasonCode: "credential_scope_insufficient", Message: "GitHub returned 404 with a token that does not advertise repository scope"}
		}
	}
	switch status {
	case http.StatusNotFound:
		return Decision{State: StateDenied, ReasonCode: "repository_not_found", Message: "repository is not visible to the authenticated actor; this is ambiguous until confirmed against a positive baseline"}
	case http.StatusForbidden:
		return Decision{State: StateUnknown, ReasonCode: "forbidden_unknown", Message: message}
	case http.StatusUnauthorized:
		return Decision{State: StateUnknown, ReasonCode: "authentication_failed", Message: message}
	case http.StatusTooManyRequests:
		return Decision{State: StateUnknown, ReasonCode: "rate_limited", Message: message}
	}
	if status >= 500 {
		return Decision{State: StateUnknown, ReasonCode: "provider_unavailable", Message: message}
	}
	return commandFailureDecision(body, commandErr, "provider_check_failed")
}

func scopeListContains(values []string, want string) bool {
	for _, value := range values {
		for _, scope := range strings.Split(value, ",") {
			if strings.TrimSpace(scope) == want {
				return true
			}
		}
	}
	return false
}

func commandFailureDecision(out []byte, err error, fallback string) Decision {
	message := strings.TrimSpace(string(out))
	if message == "" && err != nil {
		message = err.Error()
	}
	lower := strings.ToLower(message)
	reason := fallback
	switch {
	case strings.Contains(lower, "not logged") || strings.Contains(lower, "authentication"):
		reason = "authentication_failed"
	case strings.Contains(lower, "network") || strings.Contains(lower, "connection") || strings.Contains(lower, "timeout"):
		reason = "network_unavailable"
	}
	return Decision{State: StateUnknown, ReasonCode: reason, Message: message}
}

func parseHTTPResponse(out []byte) (int, http.Header, []byte) {
	headers := make(http.Header)
	normalized := bytes.ReplaceAll(out, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("HTTP/")) {
		return 0, headers, out
	}
	parts := bytes.SplitN(normalized, []byte("\n\n"), 2)
	if len(parts) != 2 {
		return 0, headers, out
	}
	scanner := bufio.NewScanner(bytes.NewReader(parts[0]))
	status := 0
	if scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 {
			status, _ = strconv.Atoi(fields[1])
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		name, value, ok := strings.Cut(line, ":")
		if ok {
			headers.Add(strings.TrimSpace(name), strings.TrimSpace(value))
		}
	}
	return status, headers, parts[1]
}

func execCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
