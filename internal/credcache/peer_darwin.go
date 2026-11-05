//go:build darwin

package credcache

import (
	"syscall"
	"unsafe"
)

// xucred mirrors <sys/ucred.h>; the standard library has no wrapper for it.
type xucred struct {
	Version uint32
	UID     uint32
	NGroups int16
	Groups  [16]uint32
}

const (
	solLocal      = 0
	localPeerCred = 1
)

func peerUID(fd uintptr) (int, error) {
	var cred xucred
	size := uint32(unsafe.Sizeof(cred))
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, solLocal, localPeerCred, uintptr(unsafe.Pointer(&cred)), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return -1, errno
	}
	return int(cred.UID), nil
}
