package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fluxinc/my-cli/internal/contractstate"
	"github.com/fluxinc/my-cli/internal/harness"
)

const contractInstanceEnv = "MYCLI_CONTRACT_INSTANCE"
const contractManifestEnv = "MYCLI_CONTRACT_MANIFEST"
const contractHomeEnv = "MYCLI_CONTRACT_HOME"

func (a app) runContractPending(args []string) error {
	home, name, id := os.Getenv(contractHomeEnv), os.Getenv(contractManifestEnv), os.Getenv(contractInstanceEnv)
	var adapter string
	var jsonOut bool
	fs := newFlagSet("my contract pending", a.stderr)
	fs.StringVar(&home, "home", home, "override home directory")
	fs.StringVar(&name, "manifest", name, "registered manifest")
	fs.StringVar(&id, "instance", id, "launch instance id (defaults to launch environment)")
	fs.StringVar(&adapter, "hook", "", "native hook adapter: claude or codex")
	fs.BoolVar(&jsonOut, "json", false, "print pending context as JSON without a receipt")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "manifest": true, "instance": true, "hook": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 || name == "" || id == "" {
		return fmt.Errorf("contract pending requires --manifest and --instance, or a my ai launch environment")
	}
	if adapter != "" {
		if jsonOut {
			return fmt.Errorf("--hook owns the native JSON output; omit --json")
		}
		var input contractstate.HookInput
		reader := a.stdin
		if reader == nil {
			reader = os.Stdin
		}
		decoder := json.NewDecoder(io.LimitReader(reader, 1024*1024))
		if err := decoder.Decode(&input); err != nil {
			return fmt.Errorf("read contract hook input: %w", err)
		}
		return contractstate.Deliver(home, name, id, adapter, input, a.stdout)
	}
	state, err := contractstate.Load(home, name)
	if err != nil {
		return err
	}
	instance, err := contractstate.ResolveInstance(state, id, "")
	if err != nil {
		return err
	}
	context := ""
	if state.Active != nil && (instance.NeedsSnapshot || instance.Emitted == nil || instance.Emitted.Revision != state.Active.Revision) {
		context = contractstate.Context(instance, *state.Active)
	}
	if jsonOut {
		return printJSON(a.stdout, map[string]any{"instance": instance.ID, "status": contractstate.InstanceStatus(instance, state.Active), "context": context, "receipt_recorded": false})
	}
	_, err = io.WriteString(a.stdout, context)
	return err
}

// External contained runtimes register only at launch, never at compile time.
func (a app) runContractRegister(args []string) error {
	var home, name, root, harnessName, parent string
	fs := newFlagSet("my contract register", a.stderr)
	fs.StringVar(&home, "home", "", "override home directory")
	fs.StringVar(&name, "manifest", "", "registered manifest")
	fs.StringVar(&root, "root", "", "actual runtime launch root")
	fs.StringVar(&harnessName, "harness", "", "harness name")
	fs.StringVar(&parent, "parent-session", "", "optional workspace session id")
	rest, err := parseInterspersed(fs, args, map[string]bool{"home": true, "manifest": true, "root": true, "harness": true, "parent-session": true})
	if err != nil {
		return err
	}
	if len(rest) != 0 || name == "" || root == "" || harnessName == "" {
		return fmt.Errorf("usage: my contract register --manifest NAME --harness NAME --root DIR [--home DIR]")
	}
	h, err := harness.Parse(harnessName)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	delivery, message := "unsupported", "External runtime: restart or manually load the contract; no verified adapter is installed."
	// Registration is not evidence of hook installation or trust. A runtime
	// attaching the documented adapters must still prove emission with a hook.
	if h == harness.ClaudeCode || h == harness.Codex {
		delivery = "untrusted"
		message = "External runtime must install and trust the native adapter; registration alone proves no delivery."
	}
	instance, err := contractstate.Register(home, name, string(h), root, parent, delivery, message)
	if err != nil {
		return err
	}
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	return printJSON(a.stdout, map[string]any{"instance": instance, "environment": map[string]string{contractHomeEnv: home, contractManifestEnv: name, contractInstanceEnv: instance.ID}, "receipt_recorded": false})
}

func contractHookInvocation(args []string) bool {
	return len(args) >= 3 && args[1] == "contract" && args[2] == "pending"
}

func (a app) printContractFallback(args []string) {
	if contractHookInvocation(args) {
		return
	}
	home, name, id := os.Getenv(contractHomeEnv), os.Getenv(contractManifestEnv), os.Getenv(contractInstanceEnv)
	if name == "" || id == "" {
		return
	}
	state, err := contractstate.Load(home, name)
	if err != nil {
		return
	}
	native := os.Getenv("CODEX_THREAD_ID")
	instance, err := contractstate.ResolveInstance(state, id, native)
	if err != nil || state.Active == nil {
		return
	}
	baseline := instance.Started
	if instance.Emitted != nil {
		baseline = *instance.Emitted
	}
	if baseline.Revision == state.Active.Revision {
		return
	}
	context := contractstate.Context(instance, *state.Active)
	if len(context) > contractstate.MaxContextBytes {
		fmt.Fprintln(a.stderr, "notice: organization contract update is too large for automatic context; run my contract show and manually reload it (no emission receipt recorded)")
		return
	}
	fmt.Fprintln(a.stderr, "notice: pending organization contract update (stderr fallback; no emission receipt recorded)")
	fmt.Fprint(a.stderr, strings.TrimSpace(context)+"\n")
}
