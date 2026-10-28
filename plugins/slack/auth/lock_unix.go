//go:build unix

package auth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const lockPoll = 20 * time.Millisecond

// lockFile takes an exclusive flock on path, waiting until ctx ends; waiting
// is called once when the lock is found held.
func lockFile(ctx context.Context, path string, waiting func()) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	waited := false
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, err
		}
		if errors.Is(err, syscall.EWOULDBLOCK) && !waited {
			waited = true
			waiting()
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}
