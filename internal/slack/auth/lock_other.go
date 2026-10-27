//go:build !unix

package auth

import (
	"context"
	"sync"
)

var refreshMu sync.Mutex

// lockFile serializes refreshes within this process only; waiting is called
// when another refresh holds the lock.
func lockFile(ctx context.Context, _ string, waiting func()) (unlock func(), err error) {
	if !refreshMu.TryLock() {
		waiting()
		refreshMu.Lock()
	}
	return refreshMu.Unlock, nil
}
