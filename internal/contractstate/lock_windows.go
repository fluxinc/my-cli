//go:build windows

package contractstate

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var contractKernel32 = syscall.NewLazyDLL("kernel32.dll")
var contractLockFile = contractKernel32.NewProc("LockFileEx")
var contractUnlockFile = contractKernel32.NewProc("UnlockFileEx")

func lock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var overlapped syscall.Overlapped
		result, _, callErr := contractLockFile.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
		if result != 0 {
			return func() {
				var overlapped syscall.Overlapped
				contractUnlockFile.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
				file.Close()
			}, nil
		}
		if !errors.Is(callErr, syscall.Errno(33)) {
			file.Close()
			return nil, callErr
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("contract state is busy; retry")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
