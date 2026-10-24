package cli

import (
	"context"
	"os"
	"time"
)

// upgradePoll paces the checks for a replaced binary.
var upgradePoll = 10 * time.Second

// watchBinary closes the returned channel once path names another file than
// it did at the call, unchanged over two checks so a copy still being
// written does not count. A path that cannot be read now is never watched.
func watchBinary(ctx context.Context, path string) <-chan struct{} {
	changed := make(chan struct{})
	start, err := os.Stat(path)
	if err != nil {
		return changed
	}
	go func() {
		t := time.NewTicker(upgradePoll)
		defer t.Stop()
		var pending os.FileInfo
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			now, err := os.Stat(path)
			if err != nil || sameBinary(start, now) {
				pending = nil
				continue
			}
			if pending != nil && sameBinary(pending, now) {
				close(changed)
				return
			}
			pending = now
		}
	}()
	return changed
}

func sameBinary(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.ModTime().Equal(b.ModTime()) && a.Size() == b.Size()
}
