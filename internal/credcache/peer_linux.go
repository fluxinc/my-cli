//go:build linux

package credcache

import "syscall"

func peerUID(fd uintptr) (int, error) {
	cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return -1, err
	}
	return int(cred.Uid), nil
}
