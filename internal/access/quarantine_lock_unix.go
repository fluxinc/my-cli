//go:build !windows

package access

import (
	"errors"
	"os"
	"syscall"
)

var errQuarantineLockHeld = errors.New("quarantine lock held")

func tryQuarantineFileLock(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return errQuarantineLockHeld
		}
		return err
	}
	return nil
}

func unlockQuarantineFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
