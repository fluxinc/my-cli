// Package safefs centralizes destructive filesystem operations and their
// test-process containment guard.
package safefs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TestRootEnv declares the only filesystem tree a test process may remove
// recursively. Production processes normally leave it unset.
const TestRootEnv = "MYCLI_TEST_ROOT"

// RemoveAll removes path recursively. Test binaries must declare TestRootEnv,
// and the target must resolve strictly beneath that root.
func RemoveAll(path string) error {
	if err := checkTestRemoval(path); err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func checkTestRemoval(path string) error {
	root := strings.TrimSpace(os.Getenv(TestRootEnv))
	if root == "" {
		if isTestBinary() {
			return fmt.Errorf("refusing recursive removal in test process without %s", TestRootEnv)
		}
		return nil
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("refusing recursive removal of an empty path")
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve test root: %w", err)
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve test root symlinks: %w", err)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve removal path: %w", err)
	}

	parentReal, err := existingAncestorReal(filepath.Dir(pathAbs))
	if err != nil {
		return fmt.Errorf("resolve removal parent: %w", err)
	}
	if !withinOrEqual(rootReal, parentReal) {
		return fmt.Errorf("refusing recursive removal through path outside test root %s: %s", rootReal, pathAbs)
	}
	if targetReal, err := filepath.EvalSymlinks(pathAbs); err == nil {
		if !strictlyWithin(rootReal, targetReal) {
			return fmt.Errorf("refusing recursive removal outside test root %s: %s", rootReal, targetReal)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("resolve removal target: %w", err)
	}
	return nil
}

func existingAncestorReal(path string) (string, error) {
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		path = parent
	}
}

func strictlyWithin(root, path string) bool {
	if filepath.Clean(root) == filepath.Clean(path) {
		return false
	}
	return withinOrEqual(root, path)
}

func withinOrEqual(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func isTestBinary() bool {
	// This is a secondary fail-closed signal for tests that forgot to install
	// testenv. The primary containment boundary is TestRootEnv, which also
	// follows spawned or renamed binaries through their inherited environment.
	name := strings.ToLower(filepath.Base(os.Args[0]))
	return strings.HasSuffix(name, ".test") || strings.HasSuffix(name, ".test.exe")
}
