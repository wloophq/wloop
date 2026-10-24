package items

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// gatedStore blocks the first Load after arm until release is closed.
type gatedStore struct {
	*memStore
	mu      sync.Mutex
	armed   bool
	entered chan struct{}
	release chan struct{}
}

func (g *gatedStore) Load(ctx context.Context, units []unit.Unit) (map[unit.Unit][]items.Item, error) {
	g.mu.Lock()
	armed := g.armed
	g.armed = false
	g.mu.Unlock()
	got, err := g.memStore.Load(ctx, units)
	if armed {
		close(g.entered)
		<-g.release
	}
	return got, err
}

func TestConcurrentWritersKeepTheUserEdit(t *testing.T) {
	g := &gatedStore{memStore: newMem(), entered: make(chan struct{}), release: make(chan struct{})}
	l := &Ledger{Store: g, Clock: &clock{t: t0}}
	apply(t, l, items.Extracted, create(thread, "i1", "x"))
	g.armed = true
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := l.Apply(context.Background(), items.Extracted, []items.Op{update(thread, "i1", "extracted", "m:2")}); err != nil {
			t.Error(err)
		}
	}()
	<-g.entered
	userDone := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(userDone)
		if _, err := l.Apply(context.Background(), items.User, []items.Op{update(thread, "i1", "mine", "user:1")}); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-userDone:
	case <-time.After(100 * time.Millisecond):
	}
	close(g.release)
	wg.Wait()
	it := itemOf(t, l, thread, "i1")
	if it.Summary != "mine" || !it.Confirmed {
		t.Errorf("the user edit was lost: %q confirmed=%v", it.Summary, it.Confirmed)
	}
}

func TestConcurrentAppliesRace(t *testing.T) {
	l, _ := newLedger(newMem())
	apply(t, l, items.Extracted, create(thread, "i1", "x"))
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prov := items.Extracted
			if i%2 == 0 {
				prov = items.User
			}
			_, _ = l.Apply(context.Background(), prov, []items.Op{update(thread, "i1", "v"+string(rune('a'+i)), "m:x")})
			_, _ = l.Reconcile(context.Background(), pull, []items.Item{derived("gh:review", "r")}, anchor("row:1"), "")
		}()
	}
	wg.Wait()
	if it := itemOf(t, l, thread, "i1"); !it.Confirmed {
		t.Errorf("%+v", it)
	}
}
