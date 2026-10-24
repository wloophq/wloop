package proposals

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
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

func TestWatchRunsOnItemsAndStampsOnDrafts(t *testing.T) {
	e := newEnv(t)
	e.svc.OnSent = nil
	e.job.Log = nil
	ch := &notes{}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.job.Watch(wctx, ch, 10*time.Millisecond, time.Hour); close(done) }()
	defer func() { cancel(); <-done }()
	eventually(t, "the subscription", func() bool { ch.mu.Lock(); defer ch.mu.Unlock(); return len(ch.fns) == 1 })

	stalledThread(e)
	ch.note(port.TopicItems)
	eventually(t, "two nudges", func() bool { return len(e.drafts.all()) == 2 })

	for _, d := range e.drafts.all() {
		if _, err := e.svc.Approve(ctx, d.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.Send(ctx)
	ch.note(port.TopicDrafts)
	eventually(t, "the stamps", func() bool {
		all, _ := e.entries.Entries(ctx, gtd.Query{View: gtd.ViewWaiting})
		for _, en := range all {
			if en.Waiting != nil && en.Waiting.LastNudge.IsZero() {
				return false
			}
		}
		return true
	})
	if got := e.list(RuleStalledNudge, draft.Sent); len(got) != 2 {
		t.Fatalf("%d sent", len(got))
	}
}
