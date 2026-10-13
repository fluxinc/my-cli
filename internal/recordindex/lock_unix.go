//go:build !windows

package recordindex

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockDir takes an exclusive lock on the index, waiting for any other sync
// or query on it to finish.
func lockDir(dir string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

func privateUmask() func() {
	old := syscall.Umask(0o077)
	return func() { syscall.Umask(old) }
}
