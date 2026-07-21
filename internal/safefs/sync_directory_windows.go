//go:build windows

package safefs

// SyncDirectory is a no-op on Windows because os.File.Sync does not support
// directory handles there. File contents are still flushed before close.
func SyncDirectory(string) error { return nil }
