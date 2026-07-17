package safefs

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemoveAllRequiresDeclaredTestRoot(t *testing.T) {
	t.Setenv(TestRootEnv, "")
	err := RemoveAll(filepath.Join(t.TempDir(), "target"))
	if err == nil || !strings.Contains(err.Error(), TestRootEnv) {
		t.Fatalf("RemoveAll error = %v, want missing test-root refusal", err)
	}
}

func TestRemoveAllAllowsOnlyDescendantsOfTestRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(TestRootEnv, root)
	inside := filepath.Join(root, "nested", "target")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(inside); err != nil {
		t.Fatalf("RemoveAll inside root: %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("inside target still exists: %v", err)
	}

	outside := t.TempDir()
	marker := filepath.Join(outside, "keep")
	if err := os.WriteFile(marker, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(outside); err == nil {
		t.Fatal("RemoveAll outside test root succeeded")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("outside marker was removed: %v", err)
	}
}

func TestRemoveAllRejectsSymlinkParentEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated Windows privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	t.Setenv(TestRootEnv, root)
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAll(filepath.Join(link, "target")); err == nil {
		t.Fatal("RemoveAll followed a symlink parent outside the test root")
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("escaped target was removed: %v", err)
	}
}

func TestProductionSourceUsesGuardedRecursiveRemoval(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve safefs test source path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	internalRoot := filepath.Join(repoRoot, "internal")
	allowed := map[string]bool{
		filepath.Join("internal", "safefs", "safefs.go"):   true,
		filepath.Join("internal", "testenv", "testenv.go"): true,
	}
	var violations []string
	err := filepath.WalkDir(internalRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		if allowed[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "os.RemoveAll(") {
			violations = append(violations, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("production source bypasses safefs.RemoveAll: %s", strings.Join(violations, ", "))
	}
}
