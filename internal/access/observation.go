package access

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ObservationInput struct {
	Home                  string
	Path                  string
	Decision              Decision
	CheckedAt             time.Time
	RequiredConfirmations int
	ConfirmationInterval  time.Duration
}

type ObservationResult struct {
	Persisted           bool               `json:"persisted"`
	HasPositiveBaseline bool               `json:"has_positive_baseline"`
	CleanupEligible     bool               `json:"cleanup_eligible"`
	AdvancedDenialCount bool               `json:"advanced_denial_count"`
	Progress            RevocationProgress `json:"progress,omitzero"`
}

// RecordObservation advances revocation evidence for an already activated
// managed path. It never creates a positive baseline. Unknown results break a
// denial sequence, and a denial without a prior positive baseline can never
// become cleanup eligible.
func RecordObservation(input ObservationInput) (ObservationResult, error) {
	if input.RequiredConfirmations < 2 {
		return ObservationResult{}, fmt.Errorf("revocation confirmations must be at least 2")
	}
	if input.ConfirmationInterval <= 0 {
		return ObservationResult{}, fmt.Errorf("revocation confirmation interval must be positive")
	}
	if input.Decision.State != StateAllowed && input.Decision.State != StateDenied && input.Decision.State != StateUnknown {
		return ObservationResult{}, fmt.Errorf("invalid access decision state %q", input.Decision.State)
	}
	checkedAt := input.CheckedAt.UTC()
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}
	if input.Decision.Actor.ID == 0 || input.Decision.Actor.NodeID == "" {
		// Credential and network failures can prevent immutable actor lookup.
		// They are unknown and must not mutate another actor's evidence.
		if input.Decision.State == StateUnknown {
			return ObservationResult{}, nil
		}
		return ObservationResult{}, fmt.Errorf("access observation requires an immutable actor identity")
	}

	canonicalPath, err := canonicalExistingPath(input.Path)
	if err != nil {
		return ObservationResult{}, err
	}
	inventory, err := LoadInventory(input.Home)
	if err != nil {
		return ObservationResult{}, err
	}
	entryIndex := -1
	for i := range inventory.Repositories {
		if inventory.Repositories[i].CanonicalPath == canonicalPath {
			entryIndex = i
			break
		}
	}
	if entryIndex < 0 {
		return ObservationResult{}, fmt.Errorf("managed path has no activated inventory entry: %s", canonicalPath)
	}
	entry := &inventory.Repositories[entryIndex]
	if input.Decision.State == StateAllowed {
		if !input.Decision.Allows(PermissionRead) || input.Decision.Repository.NodeID == "" {
			return ObservationResult{}, fmt.Errorf("allowed observation requires positive repository read permission")
		}
		if entry.Repository.NodeID != input.Decision.Repository.NodeID {
			return ObservationResult{}, fmt.Errorf("managed repository identity changed from %s to %s", entry.Repository.NodeID, input.Decision.Repository.NodeID)
		}
		entry.Repository = input.Decision.Repository
	}

	baselineIndex := -1
	for i := range entry.Baselines {
		if entry.Baselines[i].Actor.ID == input.Decision.Actor.ID {
			if entry.Baselines[i].Actor.NodeID != input.Decision.Actor.NodeID {
				return ObservationResult{}, fmt.Errorf("actor identity node changed for immutable actor id %d", input.Decision.Actor.ID)
			}
			baselineIndex = i
			break
		}
	}
	hasBaseline := baselineIndex >= 0
	progressIndex := -1
	for i := range entry.Revocations {
		if entry.Revocations[i].Actor.ID == input.Decision.Actor.ID {
			progressIndex = i
			break
		}
	}
	if progressIndex < 0 {
		entry.Revocations = append(entry.Revocations, RevocationProgress{Actor: input.Decision.Actor})
		progressIndex = len(entry.Revocations) - 1
	}
	progress := &entry.Revocations[progressIndex]
	if progress.Actor.NodeID != "" && progress.Actor.NodeID != input.Decision.Actor.NodeID {
		return ObservationResult{}, fmt.Errorf("revocation actor identity changed for immutable actor id %d", input.Decision.Actor.ID)
	}
	progress.Actor = input.Decision.Actor
	progress.LastCheckedAt = checkedAt.Format(time.RFC3339Nano)
	progress.ReasonCode = input.Decision.ReasonCode
	advanced := false

	switch input.Decision.State {
	case StateAllowed:
		resetDenialProgress(progress, StateAllowed)
		if hasBaseline {
			entry.Baselines[baselineIndex] = PositiveBaseline{
				Actor: input.Decision.Actor, Permission: input.Decision.Repository.Permission,
				CheckedAt: checkedAt.Format(time.RFC3339Nano),
			}
		}
	case StateUnknown:
		resetDenialProgress(progress, StateUnknown)
	case StateDenied:
		progress.LastState = StateDenied
		if !hasBaseline {
			progress.ConsecutiveDenials = 0
			progress.FirstDeniedAt = ""
			progress.LastQualifyingAt = ""
			progress.ConfirmedAt = ""
			break
		}
		lastQualifying, parseErr := parseOptionalTimestamp(progress.LastQualifyingAt)
		if parseErr != nil {
			return ObservationResult{}, fmt.Errorf("read last qualifying denial: %w", parseErr)
		}
		if progress.ConsecutiveDenials == 0 || lastQualifying.IsZero() {
			progress.ConsecutiveDenials = 1
			progress.FirstDeniedAt = checkedAt.Format(time.RFC3339Nano)
			progress.LastQualifyingAt = checkedAt.Format(time.RFC3339Nano)
			advanced = true
		} else if !checkedAt.Before(lastQualifying.Add(input.ConfirmationInterval)) {
			progress.ConsecutiveDenials++
			progress.LastQualifyingAt = checkedAt.Format(time.RFC3339Nano)
			advanced = true
		}
		if progress.ConsecutiveDenials >= input.RequiredConfirmations && progress.ConfirmedAt == "" {
			progress.ConfirmedAt = checkedAt.Format(time.RFC3339Nano)
		}
	}

	resultProgress := *progress
	sortInventory(&inventory)
	if err := saveInventory(input.Home, inventory); err != nil {
		return ObservationResult{}, err
	}
	return ObservationResult{
		Persisted: true, HasPositiveBaseline: hasBaseline,
		CleanupEligible:     hasBaseline && resultProgress.LastState == StateDenied && resultProgress.ConsecutiveDenials >= input.RequiredConfirmations,
		AdvancedDenialCount: advanced, Progress: resultProgress,
	}, nil
}

func resetDenialProgress(progress *RevocationProgress, state State) {
	progress.LastState = state
	progress.ConsecutiveDenials = 0
	progress.FirstDeniedAt = ""
	progress.LastQualifyingAt = ""
	progress.ConfirmedAt = ""
}

func parseOptionalTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("managed path must be a real directory: %s", abs)
	}
	return filepath.EvalSymlinks(abs)
}
