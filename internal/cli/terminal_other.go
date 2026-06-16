//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !windows

package cli

func isTerminalFD(fd uintptr) bool {
	return false
}
