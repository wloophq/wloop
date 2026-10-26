package proposals

import (
	"context"
	"sync"
	"testing"
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

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWatchRunsAtOnceAndOnItems(t *testing.T) {
	e := newEnv(t)
	e.job.Log = nil
	src := &githubSource{}
	e.job.Sources = []Source{{Provider: unit.GitHub, Proposals: src.Proposals}}
	calls := func() int { src.mu.Lock(); defer src.mu.Unlock(); return src.calls }
	ch := &notes{}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.job.Watch(wctx, ch, 10*time.Millisecond, time.Hour); close(done) }()
	defer func() { cancel(); <-done }()
	eventually(t, "the first pass", func() bool { return calls() == 1 })
	eventually(t, "the subscription", func() bool { ch.mu.Lock(); defer ch.mu.Unlock(); return len(ch.fns) == 1 })

	src.mu.Lock()
	src.out = []outbox.Proposal{ghNudge(pull)}
	src.mu.Unlock()
	ch.note(port.TopicItems)
	eventually(t, "the nudge", func() bool { return len(e.drafts.all()) == 1 })
}
