//go:build !windows

package safefs

import "os"

// SyncDirectory persists directory-entry changes after a durable file write.
func SyncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
