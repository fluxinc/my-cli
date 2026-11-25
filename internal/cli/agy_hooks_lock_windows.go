//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var agyHooksKernel32 = syscall.NewLazyDLL("kernel32.dll")
var agyHooksLockFile = agyHooksKernel32.NewProc("LockFileEx")
var agyHooksUnlockFile = agyHooksKernel32.NewProc("UnlockFileEx")

func lockAntigravityHooks(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var overlapped syscall.Overlapped
		result, _, callErr := agyHooksLockFile.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
		if result != 0 {
			return func() {
				var overlapped syscall.Overlapped
				agyHooksUnlockFile.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
				_ = file.Close()
			}, nil
		}
		if !errors.Is(callErr, syscall.Errno(33)) {
			_ = file.Close()
			return nil, callErr
		}
		if time.Now().After(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("antigravity hooks.json is busy; retry")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
