package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/core/port"
)

func TestSyncLogsAnIdentityErrorOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
		}
		waitUntil(t, "the last note's identity read", func() bool { return calls.Load() > 3 })
		synctest.Wait()
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
	})
}
