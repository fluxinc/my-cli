//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package cli

import (
	"syscall"
	"unsafe"
)

func isTerminalFD(fd uintptr) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGETA), uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}
