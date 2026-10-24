//go:build !unix

package auth

import (
	"context"
	"sync"
)

var refreshMu sync.Mutex

// lockFile serializes refreshes within this process only.
func lockFile(ctx context.Context, _ string) (unlock func(), err error) {
	refreshMu.Lock()
	return refreshMu.Unlock, nil
}
