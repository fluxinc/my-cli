package access

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRevocationRequiresPositiveBaselineAndSeparatedPersistentDenials(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "umbrella")
	target := filepath.Join(root, "repos", "sample")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	allowed := positiveDecision("R_repo")
	if _, err := RecordPositive(RecordInput{
		Home: home, Path: target, AllowedRoot: root, Organization: "acme", Manifest: "acme",
		SourceRef: "manifest:acme:repo:sample", Kind: "repo", Decision: allowed,
		CheckedAt: time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	denied := Decision{State: StateDenied, ReasonCode: "repository_not_found", Actor: allowed.Actor}
	firstAt := time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC)
	first, err := RecordObservation(ObservationInput{
		Home: home, Path: target, Decision: denied, CheckedAt: firstAt,
		RequiredConfirmations: 2, ConfirmationInterval: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.CleanupEligible || !first.AdvancedDenialCount || first.Progress.ConsecutiveDenials != 1 {
		t.Fatalf("first = %#v", first)
	}
	tooSoon, err := RecordObservation(ObservationInput{
		Home: home, Path: target, Decision: denied, CheckedAt: firstAt.Add(14 * time.Minute),
		RequiredConfirmations: 2, ConfirmationInterval: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tooSoon.CleanupEligible || tooSoon.AdvancedDenialCount || tooSoon.Progress.ConsecutiveDenials != 1 {
		t.Fatalf("tooSoon = %#v", tooSoon)
	}
	// Reloading inside every call proves evidence persists across processes.
	confirmed, err := RecordObservation(ObservationInput{
		Home: home, Path: target, Decision: denied, CheckedAt: firstAt.Add(15 * time.Minute),
		RequiredConfirmations: 2, ConfirmationInterval: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed.CleanupEligible || !confirmed.AdvancedDenialCount || confirmed.Progress.ConsecutiveDenials != 2 || confirmed.Progress.ConfirmedAt == "" {
		t.Fatalf("confirmed = %#v", confirmed)
	}
}

func TestUnknownAndAllowedResetDenialSequence(t *testing.T) {
	home, root, target, allowed := setupObservedRepository(t)
	base := time.Date(2026, 7, 16, 21, 0, 0, 0, time.UTC)
	denied := Decision{State: StateDenied, ReasonCode: "repository_not_found", Actor: allowed.Actor}
	recordObservationForTest(t, home, target, denied, base)
	unknown := Decision{State: StateUnknown, ReasonCode: "sso_authorization_required", Actor: allowed.Actor}
	result := recordObservationForTest(t, home, target, unknown, base.Add(15*time.Minute))
	if result.CleanupEligible || result.Progress.ConsecutiveDenials != 0 || result.Progress.LastState != StateUnknown {
		t.Fatalf("unknown = %#v", result)
	}
	result = recordObservationForTest(t, home, target, denied, base.Add(30*time.Minute))
	if result.Progress.ConsecutiveDenials != 1 {
		t.Fatalf("denial after unknown = %#v", result)
	}
	result = recordObservationForTest(t, home, target, allowed, base.Add(45*time.Minute))
	if result.CleanupEligible || result.Progress.ConsecutiveDenials != 0 || result.Progress.LastState != StateAllowed {
		t.Fatalf("allowed = %#v", result)
	}
	inventory, err := LoadInventory(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := inventory.Repositories[0].Baselines[0].CheckedAt; got != base.Add(45*time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("positive baseline timestamp = %s", got)
	}
	_ = root
}

func TestFalsePositiveUnknownsNeverAdvanceRevocation(t *testing.T) {
	home, _, target, allowed := setupObservedRepository(t)
	reasons := []string{
		"sso_authorization_required", "credential_scope_insufficient", "authentication_failed",
		"network_unavailable", "rate_limited", "provider_unavailable", "forbidden_unknown",
	}
	for i, reason := range reasons {
		result := recordObservationForTest(t, home, target, Decision{
			State: StateUnknown, ReasonCode: reason, Actor: allowed.Actor,
		}, time.Date(2026, 7, 16, 21, i, 0, 0, time.UTC))
		if result.CleanupEligible || result.Progress.ConsecutiveDenials != 0 {
			t.Fatalf("reason %s advanced revocation: %#v", reason, result)
		}
	}
	identityUnknown, err := RecordObservation(ObservationInput{
		Home: home, Path: target, Decision: Decision{State: StateUnknown, ReasonCode: "network_unavailable"},
		CheckedAt: time.Now(), RequiredConfirmations: 2, ConfirmationInterval: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if identityUnknown.Persisted || identityUnknown.CleanupEligible {
		t.Fatalf("identity-unknown result = %#v", identityUnknown)
	}
}

func TestDenialWithoutActorBaselineNeverBecomesCleanupEligible(t *testing.T) {
	home, _, target, allowed := setupObservedRepository(t)
	otherActor := Actor{ID: 99, NodeID: "U_other", Login: "other"}
	denied := Decision{State: StateDenied, ReasonCode: "repository_not_found", Actor: otherActor}
	for i := 0; i < 4; i++ {
		result := recordObservationForTest(t, home, target, denied, time.Date(2026, 7, 16, 21, i*15, 0, 0, time.UTC))
		if result.HasPositiveBaseline || result.CleanupEligible || result.Progress.ConsecutiveDenials != 0 {
			t.Fatalf("unbaselined denial %d = %#v", i, result)
		}
	}
	_ = allowed
}

func setupObservedRepository(t *testing.T) (string, string, string, Decision) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "umbrella")
	target := filepath.Join(root, "repos", "sample")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	allowed := positiveDecision("R_repo")
	if _, err := RecordPositive(RecordInput{
		Home: home, Path: target, AllowedRoot: root, Organization: "acme", Manifest: "acme",
		SourceRef: "manifest:acme:repo:sample", Kind: "repo", Decision: allowed, CheckedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	return home, root, target, allowed
}

func recordObservationForTest(t *testing.T, home, target string, decision Decision, checkedAt time.Time) ObservationResult {
	t.Helper()
	result, err := RecordObservation(ObservationInput{
		Home: home, Path: target, Decision: decision, CheckedAt: checkedAt,
		RequiredConfirmations: 2, ConfirmationInterval: 15 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
