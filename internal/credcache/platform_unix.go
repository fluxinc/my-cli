//go:build darwin || linux

package credcache

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Supported reports verified per-user IPC on this platform.
const Supported = true

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return verifyPrivateDir(dir)
}

// verifyPrivateDir refuses anything but a real directory this user owns with
// no group or other access. The directory, not the socket, is the boundary.
func verifyPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || int(stat.Uid) != os.Getuid() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s must be a directory owned by this user with mode 0700", dir)
	}
	return verifyAncestors(dir)
}

// verifyAncestors walks the real path, as OpenSSH's StrictModes does: nobody
// else may be able to rename the private directory away. A symlinked ~/.config
// is fine when the place it leads is safe.
func verifyAncestors(dir string) error {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	for parent := filepath.Dir(real); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if err != nil {
			return err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || (int(stat.Uid) != os.Getuid() && stat.Uid != 0) {
			return fmt.Errorf("%s is owned by another user, so it cannot protect the credential cache", parent)
		}
		if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("%s is writable by other users, so it cannot protect the credential cache", parent)
		}
		if parent == filepath.Dir(parent) {
			return nil
		}
	}
}

// tryLockFile takes the broker lock without waiting; the OS releases it when
// the holder dies, so a crash never leaves it stuck.
func tryLockFile(path string) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		f.Close()
		return nil, false, nil
	}
	if err != nil {
		f.Close()
		return nil, false, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, true, nil
}

// hardenBrokerProcess keeps keys out of core dumps.
func hardenBrokerProcess() {
	_ = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{})
}

// Detach makes a spawned broker outlive its command and terminal.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func isNotRunning(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}

func peerIsCurrentUser(conn net.Conn) bool {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return false
	}
	allowed := false
	if err = raw.Control(func(fd uintptr) {
		uid, peerErr := peerUID(fd)
		allowed = peerErr == nil && uid == os.Getuid()
	}); err != nil {
		return false
	}
	return allowed
}
