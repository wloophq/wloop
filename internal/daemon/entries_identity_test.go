package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
)

func TestSyncLogsAnIdentityErrorOnce(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var lines []string
	var calls atomic.Int32
	r := newEntriesRig(t, func(d *Deps) {
		d.Log = func(format string, args ...any) {
			mu.Lock()
			defer mu.Unlock()
			lines = append(lines, fmt.Sprintf(format, args...))
		}
		d.GTDMe = func(context.Context) (bool, error) {
			calls.Add(1)
			return false, errors.New("slack: viewer unknown")
		}
	})
	r.startSync(t)
	for i := range 3 {
		waitUntil(t, "another identity read", func() bool { return calls.Load() > int32(i) })
		r.log.Note(port.TopicItems, r7u)
		time.Sleep(syncMaxDelay + 100*time.Millisecond)
	}
	if calls.Load() < 3 {
		t.Fatalf("identity read %d times; an unsettled identity is retried", calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for _, l := range lines {
		if strings.Contains(l, "entries: identity:") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d identity lines:\n%s", n, strings.Join(lines, "\n"))
	}
}
