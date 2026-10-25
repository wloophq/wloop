//go:build unix

package sqlite

import (
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// LockStore takes, without waiting, the lock of the store at path that its
// writer holds: the daemon while it serves, and a command that migrates or
// restores the store. A held lock is ErrStoreBusy.
func LockStore(path string) (unlock func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrStoreBusy
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func freeBytes(dir string) int64 {
	for ; ; dir = filepath.Dir(dir) {
		var st unix.Statfs_t
		if err := unix.Statfs(dir, &st); err == nil {
			return int64(st.Bavail) * int64(st.Bsize) //nolint:gosec // block counts and sizes fit in int64
		}
		if dir == filepath.Dir(dir) {
			return -1
		}
	}
}
