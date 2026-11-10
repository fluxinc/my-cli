//go:build !windows

package cli

import (
	"testing"

	"github.com/fluxinc/my-cli/internal/attempt"
)

func TestReviewDoctorSurfacesObservationErrors(t *testing.T) {
	root := t.TempDir()
	h, err := attempt.Begin(root, attempt.Record{
		ClosureReason: attempt.ClosureExited,
		After:         &attempt.Observation{Repos: []attempt.RepoObs{{Path: "/missing", Error: "checkout unavailable"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	for _, item := range doctorAttempts(root) {
		if item.Status == "warn" || item.Status == "error" {
			return
		}
	}
	t.Fatal("doctor returned no warning for an unreadable observed checkout")
}
