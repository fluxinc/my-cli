// Package testenv provides process-wide filesystem isolation for packages whose
// tests exercise umbrella discovery or recursive removal.
package testenv

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fluxinc/my-cli/internal/safefs"
)

// Runner is implemented by *testing.M without importing testing into this
// helper package.
type Runner interface {
	Run() int
}

// Run executes a package test suite with an isolated home, working directory,
// temporary directory, Git configuration, and destructive-operation root.
func Run(m Runner) int {
	root, err := os.MkdirTemp("", "my-cli-test-process-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create isolated test root:", err)
		return 2
	}
	oldwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "read test working directory:", err)
		_ = os.RemoveAll(root)
		return 2
	}
	home := filepath.Join(root, "home")
	work := filepath.Join(root, "cwd")
	tmp := filepath.Join(root, "tmp")
	for _, dir := range []string{home, work, tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			fmt.Fprintln(os.Stderr, "create isolated test directory:", err)
			_ = os.RemoveAll(root)
			return 2
		}
	}
	gitConfig := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(gitConfig, nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "create isolated Git config:", err)
		_ = os.RemoveAll(root)
		return 2
	}
	for key, value := range map[string]string{
		"HOME":                home,
		"USERPROFILE":         home,
		"XDG_CONFIG_HOME":     filepath.Join(home, ".config"),
		"XDG_DATA_HOME":       filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":      filepath.Join(home, ".local", "state"),
		"TMPDIR":              tmp,
		"GIT_CONFIG_GLOBAL":   gitConfig,
		"GIT_CONFIG_NOSYSTEM": "1",
		safefs.TestRootEnv:    root,
	} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "set isolated test environment %s: %v\n", key, err)
			_ = os.RemoveAll(root)
			return 2
		}
	}
	if err := os.Chdir(work); err != nil {
		fmt.Fprintln(os.Stderr, "enter isolated test working directory:", err)
		_ = os.RemoveAll(root)
		return 2
	}

	code := m.Run()
	if err := os.Chdir(oldwd); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, "restore test working directory:", err)
		code = 2
	}
	if err := os.RemoveAll(root); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, "remove isolated test root:", err)
		code = 2
	}
	return code
}
