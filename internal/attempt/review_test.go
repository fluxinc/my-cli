//go:build !windows

package attempt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReviewDirtyToCleanIsObservedChange(t *testing.T) {
	yes, no := true, false
	zero := 0
	before := &Observation{Repos: []RepoObs{{Path: "/r", Head: "same", Dirty: &yes, Ahead: &zero}}}
	after := &Observation{Repos: []RepoObs{{Path: "/r", Head: "same", Dirty: &no, Ahead: &zero}}}
	if got := LocalChange(before, after); got == nil || !*got {
		t.Fatalf("dirty changed from true to false; got %v, want observed change", got)
	}
}

func TestReviewProbeFailureIsUnknown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte("#!/bin/sh\necho probe-failed >&2\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := ProbeChild(ProcessRef{PID: 12345, StartedAt: "known"}); got != ChildUnknown {
		t.Fatalf("failed ps probe returned %q, want unknown", got)
	}
}
