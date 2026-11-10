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

// ProcessStart returns an opaque start-time string for pid from ps. The
// string has one-second resolution, so equal strings mean "plausibly the same
// process", never proof of identity.
//
// It reports found=false with no error only when ps gives its no-such-process
// result and the kernel confirms it. Any other failure of the probe is an
// error: the answer is unknown, not "gone".
func ProcessStart(pid int) (start string, found bool, err error) {
	if pid <= 0 {
		return "", false, errors.New("invalid pid")
	}
	cmd := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, psErr := cmd.Output()
	if text := strings.Join(strings.Fields(string(out)), " "); psErr == nil && text != "" {
		return text, true, nil
	}
	// "Gone" needs two independent answers: ps's own no-such-process result
	// (status 1, nothing printed) and the kernel's. A probe that failed any
	// other way says nothing about the process.
	var exit *exec.ExitError
	if errors.As(psErr, &exit) && exit.ExitCode() == 1 && len(strings.TrimSpace(string(out))) == 0 && len(strings.TrimSpace(string(exit.Stderr))) == 0 &&
		errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		return "", false, nil
	}
	if psErr == nil {
		psErr = errors.New("ps printed no start time for a process that exists")
	}
	return "", false, psErr
}

// ExitSignal names the signal that ended a child, or "" if it exited.
func ExitSignal(exit *exec.ExitError) string {
	if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return status.Signal().String()
	}
	return ""
}

// ProbeChild compares a recorded child with the local process table. A pid
// alone proves nothing, and a matching start time is only second-resolution
// evidence, which is why the best answer is "possibly running". Nothing
// should derive authority from it.
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
