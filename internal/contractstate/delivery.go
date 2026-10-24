package contractstate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const MaxContextBytes = 64 * 1024

type Instance struct {
	NeedsSnapshot  bool      `json:"needs_snapshot,omitempty"`
	ID             string    `json:"id"`
	Harness        string    `json:"harness"`
	Root           string    `json:"root"`
	ParentSession  string    `json:"parent_session,omitempty"`
	ParentInstance string    `json:"parent_instance,omitempty"`
	AliasOf        string    `json:"alias_of,omitempty"`
	NativeID       string    `json:"native_id,omitempty"`
	Started        Snapshot  `json:"started"`
	Emitted        *Snapshot `json:"emitted,omitempty"`
	StartedAt      string    `json:"started_at"`
	EmittedAt      string    `json:"emitted_at,omitempty"`
	LastHookAt     string    `json:"last_hook_at,omitempty"`
	ClosedAt       string    `json:"closed_at,omitempty"`
	Delivery       string    `json:"delivery"`
	Message        string    `json:"message,omitempty"`
}

type HookInput struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Source    string `json:"source"`
	AgentID   string `json:"agent_id"`
}

var instanceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func NewID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

// Register is the runtime registration seam, also usable by contained runtimes.
// Compilation itself never allocates an instance or mutates delivery state.
func Register(home, name, harness, root, parentSession, delivery, message string) (Instance, error) {
	id, err := NewID()
	if err != nil {
		return Instance{}, err
	}
	instance := Instance{ID: id, Harness: harness, Root: root, ParentSession: parentSession, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Delivery: delivery, Message: message}
	_, err = Update(home, name, func(s *State) error {
		if s.Active == nil {
			return fmt.Errorf("no active contract revision recorded; run my sync first")
		}
		instance.Started = *s.Active
		if s.Instances == nil {
			s.Instances = map[string]Instance{}
		}
		pruneInstances(s, time.Now().UTC())
		s.Instances[id] = instance
		return nil
	})
	return instance, err
}

// InstanceRetention bounds the registry: every launch adds an instance, so
// closed ones are dropped after this long, and a resume alias goes with its
// canonical instance. A later resume of a pruned conversation starts from a
// full authoritative snapshot.
const InstanceRetention = 30 * 24 * time.Hour

func pruneInstances(s *State, now time.Time) {
	expired := func(stamp string) bool {
		t, err := time.Parse(time.RFC3339Nano, stamp)
		return err == nil && now.Sub(t) > InstanceRetention
	}
	for id, instance := range s.Instances {
		if instance.AliasOf == "" && instance.ClosedAt != "" && expired(instance.ClosedAt) && (instance.LastHookAt == "" || expired(instance.LastHookAt)) {
			delete(s.Instances, id)
		}
	}
	for id, instance := range s.Instances {
		if instance.AliasOf == "" {
			continue
		}
		if _, ok := s.Instances[instance.AliasOf]; !ok {
			delete(s.Instances, id)
		}
	}
}

// LinkResumeParent records that launch instance id continues the harness
// conversation nativeID, so a fork of it computes retractions against what
// that conversation was last given rather than against the current contract.
// An unknown conversation leaves the instance unchanged.
func LinkResumeParent(home, name, id, nativeID string) error {
	_, err := Update(home, name, func(s *State) error {
		instance, ok := s.Instances[id]
		if !ok {
			return nil
		}
		for _, candidate := range s.Instances {
			if candidate.AliasOf != "" || candidate.ID == id || candidate.Harness != instance.Harness || candidate.NativeID != nativeID {
				continue
			}
			instance.ParentInstance = candidate.ID
			if candidate.Emitted != nil {
				instance.Started = *candidate.Emitted
			} else {
				instance.Started = candidate.Started
			}
			s.Instances[id] = instance
			return nil
		}
		return nil
	})
	return err
}

func ResolveInstance(s State, id, nativeID string) (Instance, error) {
	instance, ok := s.Instances[id]
	if !ok {
		return Instance{}, fmt.Errorf("unknown contract launch instance %q", id)
	}
	if instance.AliasOf != "" {
		instance, ok = s.Instances[instance.AliasOf]
		if !ok {
			return Instance{}, fmt.Errorf("missing resumed contract instance")
		}
	}
	if nativeID != "" && instance.NativeID != nativeID {
		for _, candidate := range s.Instances {
			if candidate.AliasOf == "" && candidate.Harness == instance.Harness && candidate.NativeID == nativeID {
				return candidate, nil
			}
		}
	}
	return instance, nil
}

// Context always includes the complete active snapshot. Retractions compare
// with this instance's own emitted text, not the manifest's last single change.
func Context(instance Instance, active Snapshot) string {
	var out strings.Builder
	fmt.Fprintf(&out, "[Organization contract update: %s / %s]\nThis is the complete organization contract. Disregard any organization contract text earlier in this conversation; where they differ, only this snapshot applies. Existing higher-priority instructions still apply.\n", active.Revision.ManifestCommit, active.Revision.Blob)
	previous := instance.Started.Text
	if instance.Emitted != nil {
		previous = instance.Emitted.Text
	}
	current := map[string]bool{}
	for _, line := range strings.Split(active.Text, "\n") {
		current[line] = true
	}
	removed := false
	for _, line := range strings.Split(previous, "\n") {
		if strings.HasPrefix(line, "- ") && !current[line] {
			if !removed {
				out.WriteString("\nRETRACTED — these earlier rules no longer apply:\n")
				removed = true
			}
			out.WriteString(line + "\n")
		}
	}
	previousLines := map[string]bool{}
	for _, line := range strings.Split(previous, "\n") {
		previousLines[line] = true
	}
	added := false
	for _, line := range strings.Split(active.Text, "\n") {
		if strings.HasPrefix(line, "- ") && !previousLines[line] {
			if !added {
				out.WriteString("\nADDED:\n")
				added = true
			}
			out.WriteString(line + "\n")
		}
	}
	out.WriteString("\nFull active organization contract:\n")
	if strings.TrimSpace(active.Text) == "" {
		out.WriteString("EMPTY — no organization contract rules remain. Earlier organization contract rules are retracted.\n")
	} else {
		out.WriteString(active.Text)
		if !strings.HasSuffix(active.Text, "\n") {
			out.WriteByte('\n')
		}
	}
	out.WriteString("[End organization contract update]\n")
	return out.String()
}

func fullSnapshotEvent(input HookInput) bool { return input.Event == "SessionStart" }

// Deliver records emission only after the writer accepts every output byte.
// The state lock serializes concurrent hooks. It does not fetch or read Git.
// A receipt proves emission into the hook path, never model acceptance.
func Deliver(home, name, id, adapter string, input HookInput, w io.Writer) error {
	if adapter != "claude" && adapter != "codex" {
		return fmt.Errorf("unsupported contract hook adapter %q", adapter)
	}
	switch input.Event {
	case "SessionStart", "UserPromptSubmit", "PostToolUse", "PreToolUse", "SessionEnd":
	default:
		return fmt.Errorf("unsupported contract hook event %q", input.Event)
	}
	if input.SessionID == "" {
		return fmt.Errorf("contract hook requires a native session_id")
	}
	// A subagent must not consume its parent conversation's delivery cursor.
	if input.AgentID != "" {
		return nil
	}
	// The common case, a tool or prompt hook with nothing new, reads state
	// without taking the lock or rewriting the file.
	if current, err := Load(home, name); err == nil && !fullSnapshotEvent(input) && input.Event != "SessionEnd" {
		if instance, err := ResolveInstance(current, id, input.SessionID); err == nil && instance.NativeID == input.SessionID && instance.ClosedAt == "" {
			if instance.Delivery == "unsupported" {
				return nil
			}
			if !instance.NeedsSnapshot && instance.Emitted != nil && current.Active != nil && instance.Emitted.Revision == current.Active.Revision {
				return nil
			}
		}
	}
	var outputErr error
	_, err := Update(home, name, func(s *State) error {
		expectedHarness := adapter
		if adapter == "claude" {
			expectedHarness = "claude-code"
		}
		instance, err := ResolveInstance(*s, id, input.SessionID)
		if err != nil {
			// A pruned or missing record must not break a live process:
			// start it over from a full authoritative snapshot.
			if s.Active == nil || !instanceIDPattern.MatchString(id) {
				return err
			}
			instance = Instance{ID: id, Harness: expectedHarness, Started: *s.Active, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Delivery: "untrusted", NeedsSnapshot: true}
		}
		if instance.Harness != expectedHarness {
			return fmt.Errorf("hook adapter does not match registered harness")
		}
		if instance.Delivery == "unsupported" {
			return nil
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if instance.NativeID != "" && instance.NativeID != input.SessionID {
			// Recover a missed/failed SessionStart without consuming the parent
			// cursor. The first later hook starts an independent full snapshot.
			parent := instance
			childID, err := NewID()
			if err != nil {
				return err
			}
			instance.NeedsSnapshot = true
			instance.ID = childID
			instance.ParentInstance = parent.ID
			instance.AliasOf = ""
			instance.StartedAt = now
			instance.EmittedAt = ""
			instance.ClosedAt = ""
			if parent.Emitted != nil {
				instance.Started = *parent.Emitted
			}
		}
		if instance.ID != id && input.Event == "SessionStart" && input.Source == "resume" {
			alias := s.Instances[id]
			alias.AliasOf = instance.ID
			s.Instances[id] = alias
		}
		instance.NativeID = input.SessionID
		instance.LastHookAt = now
		if instance.ClosedAt != "" && input.Event != "SessionEnd" {
			// Reopened without a SessionStart (missed or failed): the
			// conversation may have lost earlier context, so resend it all.
			instance.NeedsSnapshot = true
		}
		instance.ClosedAt = ""
		if input.Event == "SessionEnd" {
			instance.ClosedAt = now
			s.Instances[instance.ID] = instance
			return nil
		}
		if s.Active == nil {
			return fmt.Errorf("active contract snapshot unavailable")
		}
		if fullSnapshotEvent(input) {
			instance.NeedsSnapshot = true
		}
		if !instance.NeedsSnapshot && instance.Emitted != nil && instance.Emitted.Revision == s.Active.Revision {
			s.Instances[instance.ID] = instance
			return nil
		}
		context := Context(instance, *s.Active)
		if len(context) > MaxContextBytes {
			instance.Delivery = "incomplete"
			instance.Message = fmt.Sprintf("contract context exceeds %d bytes; manually reload the full contract", MaxContextBytes)
			s.Instances[instance.ID] = instance
			payload, _ := json.Marshal(map[string]string{"systemMessage": instance.Message})
			payload = append(payload, '\n')
			n, err := w.Write(payload)
			if err != nil {
				outputErr = err
			} else if n != len(payload) {
				outputErr = io.ErrShortWrite
			}

			return nil
		}
		payload, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{"hookEventName": input.Event, "additionalContext": context}})
		if err != nil {
			return err
		}
		payload = append(payload, '\n')
		n, err := w.Write(payload)
		if err != nil {
			outputErr = err
		} else if n != len(payload) {
			outputErr = io.ErrShortWrite
		}
		if outputErr != nil {
			// Preserve the native binding for the next retry, but never stamp
			// an emission receipt for incomplete output.
			s.Instances[instance.ID] = instance
			return nil
		}
		snapshot := *s.Active
		instance.NeedsSnapshot = false
		instance.Emitted = &snapshot
		instance.EmittedAt = now
		instance.Delivery = "emitted"
		instance.Message = "Printed into the hook path; acceptance and model behavior are not verified."
		s.Instances[instance.ID] = instance
		return nil
	})
	if err != nil {
		return err
	}
	return outputErr
}

func InstanceStatus(instance Instance, active *Snapshot) string {
	if instance.AliasOf != "" {
		return "resumed"
	}
	if instance.ClosedAt != "" {
		return "closed"
	}
	if instance.Delivery == "unsupported" || instance.Delivery == "incomplete" {
		return instance.Delivery
	}
	if instance.NativeID == "" {
		return "untrusted"
	}
	if instance.NeedsSnapshot || active == nil || instance.Emitted == nil || instance.Emitted.Revision != active.Revision {
		return "stale"
	}
	return "emitted"
}

func SortedInstances(s State) []Instance {
	instances := make([]Instance, 0, len(s.Instances))
	for _, instance := range s.Instances {
		instances = append(instances, instance)
	}
	sort.Slice(instances, func(i, j int) bool { return instances[i].ID < instances[j].ID })
	return instances
}
