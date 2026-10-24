package contractstate

import (
	"os"
	"sync"
	"testing"
)

func TestNeverConfiguredCreatesNoState(t *testing.T) {
	home := t.TempDir()
	_, err := Update(home, "acme", func(s *State) error { s.Observe(Snapshot{Revision: Revision{ManifestCommit: "one"}}); return nil })
	if err != nil {
		t.Fatal(err)
	}
	p, _ := Path(home, "acme")
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("unexpected state: %v", err)
	}
}

func TestPendingRootsSurviveReloadAndPartialFailure(t *testing.T) {
	home := t.TempDir()
	old := Snapshot{Configured: true, Revision: Revision{ManifestCommit: "one", Blob: "a"}, Text: "- old\n"}
	next := Snapshot{Configured: true, Revision: Revision{ManifestCommit: "two", Blob: "b"}, Text: "- new\n"}
	_, err := Update(home, "acme", func(s *State) error {
		s.Observe(old)
		s.Register("base", "umbrella", "")
		s.Register("session", "session", "s1")
		s.Installed("base", old.Revision, "")
		s.Installed("session", old.Revision, "")
		s.Observe(next)
		s.Installed("base", next.Revision, "")
		s.Installed("session", next.Revision, "write failed")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Load(home, "acme")
	if err != nil {
		t.Fatal(err)
	}
	if s.Pending("base") || !s.Pending("session") {
		t.Fatalf("pending roots: %#v", s.Roots)
	}
	if s.Previous == nil || s.Previous.Revision != old.Revision {
		t.Fatal("previous revision lost")
	}
	_, err = Update(home, "acme", func(s *State) error { s.Installed("session", next.Revision, ""); return nil })
	if err != nil {
		t.Fatal(err)
	}
	s, _ = Load(home, "acme")
	if s.Pending("session") {
		t.Fatal("successful retry still pending")
	}
}

func TestRetractionAndRollbackAreNewRevisions(t *testing.T) {
	var s State
	first := Snapshot{Configured: true, Revision: Revision{ManifestCommit: "one", Blob: "a"}, Text: "- rule\n"}
	s.Observe(first)
	if !s.Observe(Snapshot{Revision: Revision{ManifestCommit: "two", Blob: "empty"}}) {
		t.Fatal("last-rule removal not observed")
	}
	if s.Active == nil || s.Active.Configured || s.Previous.Text != first.Text {
		t.Fatal("removal lost history")
	}
	rollback := first
	rollback.Revision.ManifestCommit = "three"
	if !s.Observe(rollback) || s.Active.Revision.ManifestCommit != "three" {
		t.Fatal("rollback ignored")
	}
	if s.Observe(rollback) {
		t.Fatal("unchanged revision reported twice")
	}
}

func TestConcurrentRootReceiptsDoNotLoseUpdates(t *testing.T) {
	home := t.TempDir()
	snap := Snapshot{Configured: true, Revision: Revision{ManifestCommit: "one", Blob: "a"}}
	if _, err := Update(home, "acme", func(s *State) error { s.Observe(snap); return nil }); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, root := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func(root string) {
			defer wg.Done()
			_, err := Update(home, "acme", func(s *State) error {
				s.Register(root, "session", root)
				s.Installed(root, snap.Revision, "")
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}(root)
	}
	wg.Wait()
	s, err := Load(home, "acme")
	if err != nil || len(s.Roots) != 3 {
		t.Fatalf("state=%#v err=%v", s, err)
	}
}

func TestUnrelatedCommitPreservesLastTextChange(t *testing.T) {
	var s State
	old := Snapshot{Configured: true, Text: "- old\n", Revision: Revision{ManifestCommit: "one", Blob: "a"}}
	new := Snapshot{Configured: true, Text: "- new\n", Revision: Revision{ManifestCommit: "two", Blob: "b"}}
	s.Observe(old)
	s.Observe(new)
	new.Revision.ManifestCommit = "three"
	s.Observe(new)
	if s.Active.Revision.ManifestCommit != "three" || s.Previous == nil || s.Previous.Text != old.Text {
		t.Fatalf("state=%#v", s)
	}
}
