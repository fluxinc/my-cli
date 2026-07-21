package access

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestGitHubRepositoryNameSupportsManifestHTTPSAndSSH(t *testing.T) {
	for input, want := range map[string]string{
		"example/control":                          "example/control",
		"https://github.com/example/control.git":   "example/control",
		"ssh://git@github.com/example/control.git": "example/control",
		"git@github.com:example/control.git":       "example/control",
	} {
		got, ok := GitHubRepositoryName(input)
		if !ok || got != want {
			t.Errorf("GitHubRepositoryName(%q) = %q, %v; want %q, true", input, got, ok, want)
		}
	}
	for _, input := range []string{"", "example", "https://gitlab.com/example/control", "../example/control"} {
		if got, ok := GitHubRepositoryName(input); ok {
			t.Errorf("GitHubRepositoryName(%q) = %q, true; want rejected", input, got)
		}
	}
}

func TestResolveGitHubReturnsImmutableActorAndPermission(t *testing.T) {
	var calls []string
	decision := ResolveGitHub("git@github.com:example/control.git", func(name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "api user":
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		case "api -i repos/example/control":
			return githubResponse(200, "", `{"id":29,"node_id":"R_repo","full_name":"example/control","private":true,"permissions":{"admin":true,"push":true,"pull":true}}`), nil
		default:
			return nil, fmt.Errorf("unexpected call")
		}
	})
	if !decision.Allows(PermissionAdmin) || decision.Actor.ID != 17 || decision.Actor.NodeID != "U_actor" || decision.Repository.NodeID != "R_repo" {
		t.Fatalf("decision = %#v", decision)
	}
	if strings.Join(calls, "|") != "gh api user|gh api -i repos/example/control" {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestResolveGitHubActorCanBeSharedAcrossRepositoryChecks(t *testing.T) {
	var calls []string
	runner := func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		if joined == "api user" {
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		}
		repo := strings.TrimPrefix(joined, "api -i repos/")
		return githubResponse(200, "", fmt.Sprintf(`{"id":29,"node_id":"R_%s","full_name":%q,"private":true,"permissions":{"pull":true}}`, strings.ReplaceAll(repo, "/", "_"), repo)), nil
	}
	actorDecision := ResolveGitHubActor(runner)
	if actorDecision.State != StateAllowed {
		t.Fatalf("actor decision = %#v", actorDecision)
	}
	for _, repo := range []string{"example/one", "example/two"} {
		if decision := ResolveGitHubForActor(repo, actorDecision.Actor, runner); !decision.Allows(PermissionRead) {
			t.Fatalf("%s decision = %#v", repo, decision)
		}
	}
	if got := strings.Join(calls, "|"); got != "api user|api -i repos/example/one|api -i repos/example/two" {
		t.Fatalf("calls = %s", got)
	}
}

func TestResolveGitHubClassifiesAmbiguousAndCredentialFailures(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		headers    string
		body       string
		wantState  State
		wantReason string
	}{
		{"not found", 404, "", `{"message":"Not Found"}`, StateDenied, "repository_not_found"},
		{"not found missing scope", 404, "X-OAuth-Scopes: read:user\n", `{"message":"Not Found"}`, StateUnknown, "credential_scope_insufficient"},
		{"sso", 403, "X-GitHub-SSO: required; url=https://example.invalid\n", `{"message":"Forbidden"}`, StateUnknown, "sso_authorization_required"},
		{"scope", 403, "", `{"message":"Resource not accessible by personal access token"}`, StateUnknown, "credential_scope_insufficient"},
		{"rate limit", 429, "", `{"message":"rate limited"}`, StateUnknown, "rate_limited"},
		{"provider", 503, "", `{"message":"unavailable"}`, StateUnknown, "provider_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := ResolveGitHub("example/control", func(name string, args ...string) ([]byte, error) {
				if strings.Join(args, " ") == "api user" {
					return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
				}
				return githubResponse(tt.status, tt.headers, tt.body), errors.New("exit 1")
			})
			if decision.State != tt.wantState || decision.ReasonCode != tt.wantReason || decision.Actor.ID != 17 {
				t.Fatalf("decision = %#v, want state=%s reason=%s", decision, tt.wantState, tt.wantReason)
			}
		})
	}
}

func TestResolveGitHubKnownTreatsRenameAsAllowedByImmutableID(t *testing.T) {
	var calls []string
	decision := ResolveGitHubKnown("example/old-name", Repository{ID: 29, NodeID: "R_repo", FullName: "example/old-name"}, func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		switch joined {
		case "api user":
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		case "api -i repos/example/old-name":
			return githubResponse(404, "X-OAuth-Scopes: repo\n", `{"message":"Not Found"}`), errors.New("exit 1")
		case "api -i repositories/29":
			return githubResponse(200, "", `{"id":29,"node_id":"R_repo","full_name":"example/new-name","private":true,"permissions":{"pull":true}}`), nil
		default:
			return nil, fmt.Errorf("unexpected call %s", joined)
		}
	})
	if !decision.Allows(PermissionRead) || decision.Repository.FullName != "example/new-name" || decision.Repository.NodeID != "R_repo" {
		t.Fatalf("decision = %#v", decision)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestResolveGitHubKnownRequiresRepeatedEvidenceOnlyAfterImmutableIDAlsoDisappears(t *testing.T) {
	decision := ResolveGitHubKnown("example/old-name", Repository{ID: 29, NodeID: "R_repo"}, func(name string, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "api user":
			return []byte(`{"id":17,"node_id":"U_actor","login":"operator"}`), nil
		case "api -i repos/example/old-name", "api -i repositories/29":
			return githubResponse(404, "X-OAuth-Scopes: repo\n", `{"message":"Not Found"}`), errors.New("exit 1")
		default:
			return nil, fmt.Errorf("unexpected call")
		}
	})
	if decision.State != StateDenied || decision.ReasonCode != "known_repository_not_found" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRequireReportsInsufficientPermission(t *testing.T) {
	decision := Decision{
		State:      StateAllowed,
		Actor:      Actor{ID: 17, Login: "operator"},
		Repository: Repository{FullName: "example/control", Permission: PermissionWrite},
	}
	err := Require(decision, PermissionAdmin)
	if err == nil || !strings.Contains(err.Error(), "operator has write permission") || !strings.Contains(err.Error(), "admin permission is required") {
		t.Fatalf("err = %v", err)
	}
}

func githubResponse(status int, headers, body string) []byte {
	return []byte(fmt.Sprintf("HTTP/2.0 %d Status\n%s\n%s", status, headers, body))
}
