package proposals

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type notes struct {
	mu  sync.Mutex
	fns []func(port.Change)
}

func (n *notes) Gen() port.Generation                          { return 0 }
func (n *notes) Since(port.Generation, port.Topic) port.Change { return port.Change{} }
func (n *notes) Subscribe(fn func(port.Change)) (cancel func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fns = append(n.fns, fn)
	return func() {}
}

func (n *notes) note(t port.Topic) {
	n.mu.Lock()
	fns := n.fns
	n.mu.Unlock()
	for _, fn := range fns {
		fn(port.Change{Topics: t})
	}
}

func TestWatchRunsAtOnceAndOnItems(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv(t)
		e.job.Log = nil
		src := &githubSource{}
		e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
		calls := func() int { src.mu.Lock(); defer src.mu.Unlock(); return src.calls }
		ch := &notes{}
		wctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		const debounce = 10 * time.Millisecond
		go func() { e.job.Watch(wctx, ch, debounce, time.Hour); close(done) }()
		defer func() { cancel(); <-done }()
		synctest.Wait()
		if n := calls(); n != 1 || len(ch.fns) != 1 {
			t.Fatalf("%d passes and %d subscriptions at start, want 1 and 1", n, len(ch.fns))
		}

		src.mu.Lock()
		src.out = []outbox.Proposal{ghNudge(pull)}
		src.mu.Unlock()
		ch.note(port.TopicItems)
		synctest.Wait()
		if n := calls(); n != 1 {
			t.Fatalf("a pass ran inside the debounce: %d passes", n)
		}
		time.Sleep(debounce)
		synctest.Wait()
		if n, all := calls(), e.drafts.all(); n != 2 || len(all) != 1 {
			t.Fatalf("after the debounce: %d passes, %d drafts", n, len(all))
		}
	})
}
