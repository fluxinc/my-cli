//go:build !windows

package attempt

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const lockSupported = true

// tryLock takes an exclusive, non-blocking lock. It reports false, with no
// error, when another process holds it.
func tryLock(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func unlock(file *os.File) {
	syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

// ProcessStart returns an opaque start-time string for pid from ps. It
// reports found=false when no such process exists, and an error when the
// answer could not be established.
func ProcessStart(pid int) (start string, found bool, err error) {
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := cmd.Output()
	text := strings.Join(strings.Fields(string(out)), " ")
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && text == "" {
			return "", false, nil
		}
		return "", false, err
	}
	if text == "" {
		return "", false, nil
	}
	return text, true, nil
}

// ProbeChild compares a recorded child with the local process table. A pid
// alone proves nothing: only a matching start time means the same process.
func ProbeChild(ref ProcessRef) string {
	if ref.PID <= 0 || ref.StartedAt == "" {
		return ChildUnknown
	}
	start, found, err := ProcessStart(ref.PID)
	switch {
	case err != nil:
		return ChildUnknown
	case !found, start != ref.StartedAt:
		return ChildNotFound
	default:
		return ChildPossiblyRunning
	}
}
