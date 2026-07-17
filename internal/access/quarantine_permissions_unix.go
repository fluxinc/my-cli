//go:build !windows

package access

import (
	"fmt"
	"os"
)

func secureQuarantineDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("quarantine directory is not mode 0700: %s", path)
	}
	return nil
}
