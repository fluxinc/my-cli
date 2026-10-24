package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fluxinc/my-cli/internal/contractstate"
	"github.com/fluxinc/my-cli/internal/harness"
	"github.com/fluxinc/my-cli/internal/worksession"
)

func (a app) runContractHarness(h harness.Harness, binary string, args []string, target, home, name, root string) error {
	a.harnessEnvironment = contractLaunchEnvironment(os.Environ(), "", "", "")
	state, err := contractstate.Load(home, name)
	if err != nil || state.Active == nil || !state.Active.Configured && state.Active.Text == "" {
		return a.runHarness(binary, args, target)
	}
	delivery, message := "untrusted", "Hook has not emitted yet."
	if h == harness.Codex {
		message = "Hook has not emitted yet; Codex runs it only after you trust its exact definition (/hooks)."
	}
	adapterArgs := []string{}
	if runtime.GOOS == "windows" {
		delivery = "unsupported"
		message = "Native Windows live adapters are not verified; restart or manually reload the contract."
	} else if h != harness.ClaudeCode && h != harness.Codex {
		delivery = "unsupported"
		message = "This harness has no verified live contract adapter; restart or manually reload the contract."
	} else {
		version, probeErr := a.probeContractHarness(binary)
		minimum := [3]int{0, 163, 0}
		if h == harness.ClaudeCode {
			minimum = [3]int{2, 1, 310}
		}
		if probeErr != nil || !contractVersionAtLeast(version, minimum) {
			delivery = "unsupported"
			message = "Harness version does not meet the verified live adapter minimum; update the harness or manually reload."
		} else if conflictingContractHookArgs(h, args) {
			delivery = "unsupported"
			message = "Explicit launch hook/settings options retained; automatic contract adapter is disabled for this launch."
		} else {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			adapterArgs, err = contractAdapterArgs(home, h, executable)
			if err != nil {
				return err
			}
		}
	}
	// An unpublished local checkout must not inject the older active snapshot
	// over the local guidance the user intentionally launched with.
	if doc, err := loadSingleRegisteredDoc(home, name); err == nil {
		if snapshot, err := currentContractSnapshot(doc.ref); err == nil && snapshot.Provenance == "unpublished" {
			delivery = "unsupported"
			message = "Local contract is unpublished; publish or reconcile it and relaunch for live delivery."
			adapterArgs = nil
		}
	}
	parent := ""
	if sessions, err := worksession.List(root); err == nil {
		for _, session := range sessions {
			if session.Path == target && session.Status == worksession.StatusActive {
				parent = session.ID
				break
			}
		}
	}
	instance, err := contractstate.Register(home, name, string(h), target, parent, delivery, message)
	if err != nil {
		return err
	}
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	if native := contractResumeTarget(h, args); native != "" {
		if err := contractstate.LinkResumeParent(home, name, instance.ID, native); err != nil {
			return err
		}
	}
	a.harnessEnvironment = contractLaunchEnvironment(os.Environ(), home, name, instance.ID)
	fmt.Fprintf(a.stderr, "contract\t%s\t%s\t%s\n", instance.ID, delivery, message)
	defer func() {
		_, _ = contractstate.Update(home, name, func(s *contractstate.State) error {
			current, err := contractstate.ResolveInstance(*s, instance.ID, "")
			if err != nil {
				return nil
			}
			current.ClosedAt = time.Now().UTC().Format(time.RFC3339Nano)
			s.Instances[current.ID] = current
			return nil
		})
	}()
	return a.runHarness(binary, append(adapterArgs, args...), target)
}

func (a app) probeContractHarness(binary string) (string, error) {
	if a.harnessProbe != nil {
		data, err := a.harnessProbe(binary, "--version")
		return string(data), err
	}
	return probeContractVersion(binary)
}

var contractVersionPattern = regexp.MustCompile(`([0-9]+)\.([0-9]+)\.([0-9]+)`)

func contractVersionAtLeast(value string, min [3]int) bool {
	fields := contractVersionPattern.FindStringSubmatch(value)
	if len(fields) != 4 {
		return false
	}
	for i := range min {
		n, err := strconv.Atoi(fields[i+1])
		if err != nil {
			return false
		}
		if n != min[i] {
			return n > min[i]
		}
	}
	return true
}

var conversationIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// contractResumeTarget returns the conversation a launch resumes or forks:
// claude --resume/-r ID, or codex resume|fork ... ID. Only a UUID counts, so
// option values, names and search terms are never mistaken for one; those
// launches, like --continue and pickers, start from the full snapshot.
func contractResumeTarget(h harness.Harness, args []string) string {
	for i, arg := range args {
		switch {
		case h == harness.ClaudeCode && strings.HasPrefix(arg, "--resume="):
			if value := strings.TrimPrefix(arg, "--resume="); conversationIDPattern.MatchString(value) {
				return value
			}
			return ""
		case h == harness.ClaudeCode && (arg == "--resume" || arg == "-r"):
			if i+1 < len(args) && conversationIDPattern.MatchString(args[i+1]) {
				return args[i+1]
			}
			return ""
		case h == harness.Codex && (arg == "resume" || arg == "fork"):
			for _, next := range args[i+1:] {
				if conversationIDPattern.MatchString(next) {
					return next
				}
			}
			return ""
		}
	}
	return ""
}

func conflictingContractHookArgs(h harness.Harness, args []string) bool {
	for i, arg := range args {
		if h == harness.ClaudeCode && (arg == "--settings" || strings.HasPrefix(arg, "--settings=")) {
			return true
		}
		if h == harness.Codex {
			value := arg
			if (arg == "-c" || arg == "--config") && i+1 < len(args) {
				value = args[i+1]
			}
			value = strings.TrimPrefix(strings.TrimPrefix(value, "--config="), "-c")
			if strings.HasPrefix(value, "hooks.") || strings.HasPrefix(value, "hooks=") {
				return true
			}
		}
	}
	return false
}
func contractLaunchEnvironment(inherited []string, home, name, id string) []string {
	env := make([]string, 0, len(inherited)+3)
	for _, entry := range inherited {
		if strings.HasPrefix(entry, contractHomeEnv+"=") || strings.HasPrefix(entry, contractManifestEnv+"=") || strings.HasPrefix(entry, contractInstanceEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, contractHomeEnv+"="+home, contractManifestEnv+"="+name, contractInstanceEnv+"="+id)
}

func contractAdapterArgs(home string, h harness.Harness, executable string) ([]string, error) {
	adapter := "codex"
	if h == harness.ClaudeCode {
		adapter = "claude"
	}
	command := shellQuote(executable) + " contract pending --hook " + adapter
	events := []string{"SessionStart", "UserPromptSubmit", "PostToolUse", "SessionEnd"}
	if h == harness.Codex {
		events = append(events, "PreToolUse")
	}
	if h == harness.Codex {
		args := []string{}
		encoded, _ := json.Marshal(command)
		for _, event := range events {
			timeout := 10
			if event == "SessionEnd" {
				timeout = 1
			}
			handler := fmt.Sprintf(`{type="command",command=%s,timeout=%d`, encoded, timeout)
			if event != "SessionEnd" {
				handler += `,additionalContextLimit=0`
			}
			handler += "}"
			args = append(args, "-c", fmt.Sprintf("hooks.%s=[{hooks=[%s]}]", event, handler))
		}
		return args, nil
	}
	hooks := map[string]any{}
	for _, event := range events {
		hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 10}}}}
	}
	data, err := json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
	if err != nil {
		return nil, err
	}
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	hash := contractstate.ContentHash(data)
	dir := filepath.Join(home, ".local", "share", "my-cli", "state", "contract", "hooks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "claude-"+hash+".json")
	if old, err := os.ReadFile(path); err == nil && string(old) == string(data) {
		return []string{"--settings", path}, nil
	}
	file, err := os.CreateTemp(dir, ".hooks-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return nil, err
	}
	return []string{"--settings", path}, nil
}
