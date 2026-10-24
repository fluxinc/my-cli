package contractstate

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func deliveryFixture(t *testing.T) (string, Instance, Instance) {
	t.Helper()
	home := t.TempDir()
	_, err := Update(home, "acme", func(s *State) error {
		s.Observe(Snapshot{Configured: true, Revision: Revision{ManifestCommit: "one", Blob: "one"}, Text: "- Old rule.\n"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := Register(home, "acme", "claude-code", "/base", "", "untrusted", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Register(home, "acme", "codex", "/base", "", "untrusted", "")
	if err != nil {
		t.Fatal(err)
	}
	return home, a, b
}
func updateDelivery(t *testing.T, home, revision, text string) {
	t.Helper()
	_, err := Update(home, "acme", func(s *State) error {
		s.Observe(Snapshot{Configured: text != "", Revision: Revision{ManifestCommit: revision, Blob: revision}, Text: text})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestDeliveryIndependentReceiptsAndSkippedRevisions(t *testing.T) {
	home, a, b := deliveryFixture(t)
	var out bytes.Buffer
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", Source: "startup", SessionID: "claude-one"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Full active") {
		t.Fatal(out.String())
	}
	updateDelivery(t, home, "two", "- Intermediate.\n")
	updateDelivery(t, home, "three", "- New rule.\n")
	out.Reset()
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "PostToolUse", SessionID: "claude-one"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "RETRACTED") || !strings.Contains(out.String(), "Old rule.") || strings.Contains(out.String(), "Intermediate.") {
		t.Fatal(out.String())
	}
	state, _ := Load(home, "acme")
	if state.Instances[b.ID].Emitted != nil {
		t.Fatal("one harness consumed another cursor")
	}
	out.Reset()
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "UserPromptSubmit", SessionID: "claude-one"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatal("unchanged hook emitted noise")
	}
	out.Reset()
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", Source: "compact", SessionID: "claude-one"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Full active") {
		t.Fatal("compaction did not reinject snapshot")
	}
	updateDelivery(t, home, "four", "")
	out.Reset()
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "PostToolUse", SessionID: "claude-one"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "EMPTY") || !strings.Contains(out.String(), "RETRACTED") {
		t.Fatal("last-rule removal not emitted")
	}
}

type brokenWriter struct{ short bool }

func (w brokenWriter) Write(data []byte) (int, error) {
	if w.short {
		return len(data) - 1, nil
	}
	return 0, errors.New("closed pipe")
}
func TestDeliveryFailedOutputNeverAdvancesReceipt(t *testing.T) {
	for _, short := range []bool{false, true} {
		home, a, _ := deliveryFixture(t)
		err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", SessionID: "one"}, brokenWriter{short: short})
		if err == nil {
			t.Fatal("failed output succeeded")
		}
		s, _ := Load(home, "acme")
		if s.Instances[a.ID].Emitted != nil {
			t.Fatal("failed output advanced receipt")
		}
	}
}
func TestDeliveryForkAndResumeKeepIndependentNativeBindings(t *testing.T) {
	home, a, _ := deliveryFixture(t)
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", SessionID: "parent"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", Source: "fork", SessionID: "child"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(home, "acme")
	child, err := ResolveInstance(s, a.ID, "child")
	if err != nil || child.ID == a.ID || child.ParentInstance != a.ID {
		t.Fatal("fork did not get independent receipt")
	}
	updateDelivery(t, home, "two", "- New rule.\n")
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "PostToolUse", SessionID: "child"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(home, "acme")
	if s.Instances[a.ID].Emitted.Revision.ManifestCommit != "one" {
		t.Fatal("child advanced parent")
	}
	resumed, err := Register(home, "acme", "claude-code", "/base", "", "untrusted", "")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Deliver(home, "acme", resumed.ID, "claude", HookInput{Event: "SessionStart", Source: "resume", SessionID: "parent"}, &out); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(home, "acme")
	if s.Instances[resumed.ID].AliasOf != a.ID || s.Instances[a.ID].Emitted.Revision.ManifestCommit != "two" || out.Len() == 0 {
		t.Fatal("resume lost cursor or full snapshot")
	}
}
func TestDeliveryOversizeRemainsIncomplete(t *testing.T) {
	home, a, _ := deliveryFixture(t)
	updateDelivery(t, home, "huge", "- "+strings.Repeat("x", MaxContextBytes)+"\n")
	var out bytes.Buffer
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", SessionID: "one"}, &out); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(home, "acme")
	if s.Instances[a.ID].Delivery != "incomplete" || s.Instances[a.ID].Emitted != nil || strings.Contains(out.String(), "additionalContext") {
		t.Fatal("oversize update marked emitted")
	}
	if s.SchemaVersion != 2 {
		t.Fatal("instance state did not upgrade schema")
	}
}

func TestDeliveryForkFailedOutputCanRetryAtNextTool(t *testing.T) {
	home, a, _ := deliveryFixture(t)
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", SessionID: "parent"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	updateDelivery(t, home, "two", "- New rule.\n")
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", Source: "fork", SessionID: "child"}, brokenWriter{}); err == nil {
		t.Fatal("broken output succeeded")
	}
	var out bytes.Buffer
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "PostToolUse", SessionID: "child"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "New rule.") {
		t.Fatal("fork retry did not deliver")
	}
	s, _ := Load(home, "acme")
	if s.Instances[a.ID].Emitted.Revision.ManifestCommit != "one" {
		t.Fatal("fork retry advanced parent")
	}
}

func TestDeliveryFailedCompactionSnapshotRetriesWithoutRevisionChange(t *testing.T) {
	home, a, _ := deliveryFixture(t)
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", SessionID: "one"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", Source: "compact", SessionID: "one"}, brokenWriter{}); err == nil {
		t.Fatal("broken output succeeded")
	}
	var out bytes.Buffer
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "PostToolUse", SessionID: "one"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Full active") {
		t.Fatal("failed full snapshot was suppressed as unchanged")
	}
}

func TestDeliveryMissedForkStartRecoversAtToolHook(t *testing.T) {
	home, a, _ := deliveryFixture(t)
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "SessionStart", SessionID: "parent"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Deliver(home, "acme", a.ID, "claude", HookInput{Event: "PostToolUse", SessionID: "child"}, &out); err != nil {
		t.Fatal(err)
	}
	s, _ := Load(home, "acme")
	child, err := ResolveInstance(s, a.ID, "child")
	if err != nil || child.ID == a.ID || !strings.Contains(out.String(), "Full active") {
		t.Fatal("missed startup was not independently recovered")
	}
}
