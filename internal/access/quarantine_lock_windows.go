//go:build windows

package access

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
	errorLockViolation      = syscall.Errno(33)
)

var (
	errQuarantineLockHeld = errors.New("quarantine lock held")
	kernel32DLL           = syscall.NewLazyDLL("kernel32.dll")
	lockFileExProc        = kernel32DLL.NewProc("LockFileEx")
	unlockFileExProc      = kernel32DLL.NewProc("UnlockFileEx")
)

func tryQuarantineFileLock(file *os.File) error {
	var overlapped syscall.Overlapped
	result, _, callErr := lockFileExProc.Call(
		file.Fd(), lockfileExclusiveLock|lockfileFailImmediately, 0, 1, 0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if result != 0 {
		return nil
	}
	if errors.Is(callErr, errorLockViolation) {
		return errQuarantineLockHeld
	}
	return callErr
}

func unlockQuarantineFile(file *os.File) error {
	var overlapped syscall.Overlapped
	result, _, callErr := unlockFileExProc.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if result != 0 {
		return nil
	}
	return callErr
}
