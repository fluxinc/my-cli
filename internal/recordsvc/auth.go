// Package recordsvc reads and writes records in a service bound to a data
// type (records-http/v1). The service owns the records, their revisions and
// their workflow; this package only invokes it and never keeps a copy.
package recordsvc

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CommandRunner runs an external command and returns its standard output.
// Tests replace it; the default runs the real command.
type CommandRunner func(name string, args ...string) ([]byte, error)

func defaultRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// ResolveAuth turns a manifest auth_ref into a bearer credential at
// invocation. The credential is never logged, stored or put in an error:
// failures say which reference failed and how to fix it, nothing more.
func ResolveAuth(ref string, run CommandRunner) (string, error) {
	if run == nil {
		run = defaultRunner
	}
	switch {
	case ref == "none":
		return "", nil
	case strings.HasPrefix(ref, "env://"):
		name := strings.TrimPrefix(ref, "env://")
		value, ok := os.LookupEnv(name)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("auth_ref %s: environment variable %s is not set", ref, name)
		}
		return strings.TrimSpace(value), nil
	case strings.HasPrefix(ref, "op://"):
		out, err := run("op", "read", "--no-newline", ref)
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return "", fmt.Errorf("auth_ref %s: op read failed (exit %d); sign in with op signin and check the reference", ref, exitErr.ExitCode())
			}
			if errors.Is(err, exec.ErrNotFound) {
				return "", fmt.Errorf("auth_ref %s needs the 1Password CLI (op), which is not on PATH", ref)
			}
			return "", fmt.Errorf("auth_ref %s: op read failed", ref)
		}
		value := strings.TrimSpace(string(out))
		if value == "" {
			return "", fmt.Errorf("auth_ref %s: op read returned nothing", ref)
		}
		return value, nil
	case strings.HasPrefix(ref, "broker://"):
		return "", fmt.Errorf("auth_ref %s: broker references are not supported for service-backed records yet; use op:// or env://", ref)
	default:
		return "", fmt.Errorf("auth_ref %q is not a supported reference", ref)
	}
}
