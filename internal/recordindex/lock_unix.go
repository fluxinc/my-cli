//go:build !windows

package recordindex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockDir takes an exclusive lock on the index, waiting while another sync
// or query holds it, but no longer than ctx allows.
func lockDir(ctx context.Context, dir string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, fmt.Errorf("another my fleet index or search is using the semantic index: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func privateUmask() func() {
	old := syscall.Umask(0o077)
	return func() { syscall.Umask(old) }
}
