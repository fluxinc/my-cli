//go:build windows

package changenotice

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

var noticeKernel32 = syscall.NewLazyDLL("kernel32.dll")
var noticeLockFile = noticeKernel32.NewProc("LockFileEx")
var noticeUnlockFile = noticeKernel32.NewProc("UnlockFileEx")

func lock(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var overlapped syscall.Overlapped
		result, _, callErr := noticeLockFile.Call(file.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
		if result != 0 {
			return func() {
				var overlapped syscall.Overlapped
				noticeUnlockFile.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
				file.Close()
			}, nil
		}
		if !errors.Is(callErr, syscall.Errno(33)) {
			file.Close()
			return nil, callErr
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, fmt.Errorf("change notice state is busy; retry")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
