//go:build windows

package access

import "fmt"

func secureQuarantineDirectory(path string) error {
	return fmt.Errorf("Windows quarantine is blocked until an explicit per-user DACL can be verified for %s; the checkout remains blocked in place", path)
}
