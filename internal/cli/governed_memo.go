package cli

import (
	"strings"
	"sync"

	"github.com/fluxinc/my-cli/internal/access"
)

// governedMemo caches positive governance lookups for one CLI invocation so
// repeated gates do not re-pay provider round trips. Only successes are
// cached: denials and unknown provider states are always re-resolved, and
// nothing is persisted, so a cached result cannot outlive a single command.
// A nil memo (the zero value in tests) disables caching entirely.
type governedMemo struct {
	mu        sync.Mutex
	actor     access.Actor
	decisions map[string]access.Decision
	inflight  map[string]*governedDecisionCall
}

func newGovernedMemo() *governedMemo {
	return &governedMemo{
		decisions: map[string]access.Decision{},
		inflight:  map[string]*governedDecisionCall{},
	}
}

type governedDecisionCall struct {
	done     chan struct{}
	decision access.Decision
}

func (m *governedMemo) loadActor() (access.Actor, bool) {
	if m == nil {
		return access.Actor{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.actor, m.actor.ID != 0 && m.actor.NodeID != "" && m.actor.Login != ""
}

func (m *governedMemo) storeActor(actor access.Actor) {
	if m == nil || actor.ID == 0 || actor.NodeID == "" || actor.Login == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actor = actor
}

// resolveDecision coalesces simultaneous checks of the same repository. A
// positive result remains available for the rest of the invocation; a denial
// or unknown result is only shared with callers already waiting on that exact
// provider request, then immediately forgotten.
func (m *governedMemo) resolveDecision(key string, resolve func() access.Decision) access.Decision {
	if m == nil || key == "" {
		return resolve()
	}
	m.mu.Lock()
	if decision, ok := m.decisions[key]; ok {
		m.mu.Unlock()
		return decision
	}
	if call, ok := m.inflight[key]; ok {
		m.mu.Unlock()
		<-call.done
		return call.decision
	}
	call := &governedDecisionCall{done: make(chan struct{})}
	m.inflight[key] = call
	m.mu.Unlock()

	decision := resolve()

	m.mu.Lock()
	if decision.Allows(access.PermissionRead) {
		m.decisions[key] = decision
	}
	call.decision = decision
	delete(m.inflight, key)
	close(call.done)
	m.mu.Unlock()
	return decision
}

func governedRepositoryKey(repository string) string {
	name, ok := access.GitHubRepositoryName(repository)
	if !ok {
		return ""
	}
	return strings.ToLower(name)
}

func (a app) governedActorDecision() access.Decision {
	if actor, ok := a.memo.loadActor(); ok {
		return access.Decision{State: access.StateAllowed, ReasonCode: "positive_identity", Actor: actor}
	}
	decision := access.ResolveGitHubActor(a.accessRunner)
	if decision.State == access.StateAllowed {
		a.memo.storeActor(decision.Actor)
	}
	return decision
}

func (a app) governedRepositoryDecision(repository string, known access.Repository) access.Decision {
	actorDecision := a.governedActorDecision()
	if actorDecision.State != access.StateAllowed {
		return actorDecision
	}
	return a.governedRepositoryDecisionForActor(repository, known, actorDecision.Actor)
}

func (a app) governedRepositoryDecisionForActor(repository string, known access.Repository, actor access.Actor) access.Decision {
	key := governedRepositoryKey(repository)
	return a.memo.resolveDecision(key, func() access.Decision {
		if known.ID != 0 && known.NodeID != "" {
			return access.ResolveGitHubKnownForActor(repository, known, actor, a.accessRunner)
		}
		return access.ResolveGitHubForActor(repository, actor, a.accessRunner)
	})
}
