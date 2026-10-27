package views_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/adapter/view/testsink"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/view"
)

type countedBooks struct {
	next  views.Books
	fail  error
	mu    sync.Mutex
	calls [][]unit.Unit
}

func (c *countedBooks) Books(ctx context.Context, refs ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	c.mu.Lock()
	c.calls = append(c.calls, refs)
	fail := c.fail
	c.fail = nil
	c.mu.Unlock()
	if fail != nil {
		return nil, fail
	}
	return c.next.Books(ctx, refs...)
}

func (c *countedBooks) take() [][]unit.Unit {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.calls
	c.calls = nil
	return out
}

type reconcileFixture struct {
	*fixture
	sink    *testsink.Sink
	log     *change.Log
	counted *countedBooks
}

func newReconcileFixture(t *testing.T) *reconcileFixture {
	t.Helper()
	sink := testsink.New("test")
	f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
	rf := &reconcileFixture{fixture: f, sink: sink, log: change.NewLog(change.DefaultWindow, t.Logf)}
	rf.counted = &countedBooks{next: f.books}
	f.svc.Books = rf.counted
	f.svc.Changes = rf.log
	f.run(t)
	return rf
}

func (f *reconcileFixture) reconcile(t *testing.T) int {
	t.Helper()
	n, err := f.svc.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *reconcileFixture) settle(t *testing.T, ref unit.Unit) {
	t.Helper()
	eventually(t, "the card applied", func() bool { _, ok := f.applied(t, "test", ref); return ok })
}

func TestFirstReconcileReadsAll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReconcileFixture(t)
		f.books.Put(asked(r7, "keep both?"))
		f.books.Put(asked(r8, "and this?"))
		if n := f.reconcile(t); n != 2 {
			t.Fatalf("first reconcile = %d, want 2", n)
		}
		calls := f.counted.take()
		if len(calls) != 1 || len(calls[0]) != 0 {
			t.Fatalf("books calls = %v, want one read of every book", calls)
		}
	})
}

func TestQuietReconcileReadsNoBook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReconcileFixture(t)
		f.books.Put(asked(r7, "keep both?"))
		f.reconcile(t)
		f.settle(t, r7)
		f.counted.take()

		if n := f.reconcile(t); n != 0 {
			t.Fatalf("quiet reconcile queued %d", n)
		}
		if calls := f.counted.take(); len(calls) != 0 {
			t.Fatalf("quiet reconcile read books %v", calls)
		}
	})
}

func TestReconcileReadsTheTurnedRefs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReconcileFixture(t)
		f.books.Put(asked(r7, "keep both?"))
		f.books.Put(asked(r8, "and this?"))
		f.reconcile(t)
		f.settle(t, r7)
		f.settle(t, r8)
		f.counted.take()

		f.books.Put(asked(r7, "changed question"))
		f.log.Note(port.TopicTurns, r7)
		if n := f.reconcile(t); n != 1 {
			t.Fatalf("reconcile after a turn = %d, want 1", n)
		}
		calls := f.counted.take()
		if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != r7 {
			t.Fatalf("books calls = %v, want a read of %v only", calls, r7)
		}

		f.log.Note(port.TopicConfig)
		f.reconcile(t)
		if calls := f.counted.take(); len(calls) != 1 || len(calls[0]) != 0 {
			t.Fatalf("books calls after a note for all = %v, want one read of every book", calls)
		}
	})
}

func TestReconcileReadsADriftedCard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReconcileFixture(t)
		f.books.Put(asked(r7, "keep both?"))
		f.books.Put(asked(r8, "and this?"))
		f.reconcile(t)
		f.settle(t, r7)
		f.settle(t, r8)
		f.counted.take()

		f.sink.Place(r7, "parked")
		if n := f.reconcile(t); n != 1 {
			t.Fatalf("reconcile with a drifted card = %d, want 1", n)
		}
		calls := f.counted.take()
		if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != r7 {
			t.Fatalf("books calls = %v, want a read of %v only", calls, r7)
		}
		eventually(t, "the override", func() bool { a, _ := f.applied(t, "test", r7); return a.Override == "parked" })

		f.counted.take()
		f.log.Note(port.TopicViews, r7)
		if n := f.reconcile(t); n != 0 {
			t.Fatalf("reconcile with the override stored = %d", n)
		}
		if calls := f.counted.take(); len(calls) != 0 {
			t.Fatalf("books calls = %v, want none", calls)
		}
	})
}

type skippingSink struct {
	*testsink.Sink
	mu   sync.Mutex
	skip map[unit.Unit]bool
}

func (s *skippingSink) Apply(ctx context.Context, items []view.Item, force map[unit.Unit]bool) ([]view.Applied, error) {
	s.mu.Lock()
	var keep []view.Item
	for _, it := range items {
		if !s.skip[it.Unit] {
			keep = append(keep, it)
		}
	}
	s.mu.Unlock()
	return s.Sink.Apply(ctx, keep, force)
}

func (s *skippingSink) allow(u unit.Unit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.skip, u)
}

func TestReconcileRetriesASkippedItemWhenItsLinkAppears(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inner := testsink.New("test")
		sink := &skippingSink{Sink: inner, skip: map[unit.Unit]bool{r7: true}}
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: inner})
		log := change.NewLog(change.DefaultWindow, t.Logf)
		counted := &countedBooks{next: f.books}
		f.svc.Books, f.svc.Changes = counted, log
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))
		f.books.Put(asked(r8, "and this?"))
		if _, err := f.svc.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		eventually(t, "r8 applied", func() bool { _, ok := f.applied(t, "test", r8); return ok })
		if _, ok := f.applied(t, "test", r7); ok {
			t.Fatal("r7 applied while skipped")
		}
		counted.take()

		sink.allow(r7)
		log.Note(port.TopicFollow, r7)
		if n, err := f.svc.Reconcile(context.Background()); err != nil || n != 1 {
			t.Fatalf("reconcile after the link = %d, %v", n, err)
		}
		if calls := counted.take(); len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != r7 {
			t.Fatalf("books calls = %v, want a read of %v only", calls, r7)
		}
		eventually(t, "r7 applied", func() bool { _, ok := f.applied(t, "test", r7); return ok })
	})
}

func TestAFailedReconcileIsRedone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newReconcileFixture(t)
		f.books.Put(asked(r7, "keep both?"))
		f.reconcile(t)
		f.settle(t, r7)
		f.counted.take()

		f.books.Put(asked(r7, "changed question"))
		f.log.Note(port.TopicTurns, r7)
		f.counted.fail = errors.New("books down")
		if _, err := f.svc.Reconcile(context.Background()); err == nil {
			t.Fatal("reconcile hid the books failure")
		}
		f.counted.take()
		if n := f.reconcile(t); n != 1 {
			t.Fatalf("reconcile after the failure = %d, want 1", n)
		}
		if calls := f.counted.take(); len(calls) != 1 || len(calls[0]) != 1 || calls[0][0] != r7 {
			t.Fatalf("books calls = %v, want a read of %v", calls, r7)
		}
	})
}

// heldState holds the flush that saves hold's item until release closes, so
// a reconcile runs while that flush is still in flight.
type heldState struct {
	views.State
	hold    unit.Unit
	saved   chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *heldState) SaveApplied(ctx context.Context, sink string, a view.Applied, evs ...port.Event) error {
	err := s.State.SaveApplied(ctx, sink, a, evs...)
	if a.Unit == s.hold {
		s.once.Do(func() {
			close(s.saved)
			<-s.release
		})
	}
	return err
}

func TestReconcileDuringAFlushKeepsTheDriftedCard(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		held := &heldState{State: f.state, hold: r7, saved: make(chan struct{}), release: make(chan struct{})}
		f.svc.State, f.svc.Changes = held, change.NewLog(change.DefaultWindow, t.Logf)
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))
		if _, err := f.svc.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-held.saved

		sink.Place(r7, "parked")
		if n, err := f.svc.Reconcile(context.Background()); err != nil || n != 1 {
			t.Fatalf("reconcile with a drifted card during a flush = %d, %v, want 1", n, err)
		}
		close(held.release)
		eventually(t, "the override", func() bool { a, _ := f.applied(t, "test", r7); return a.Override == "parked" })
	})
}

func TestAForcedFlushDoesNotForceTheDriftedCardAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		held := &heldState{State: f.state, hold: r7, saved: make(chan struct{}), release: make(chan struct{})}
		f.svc.State, f.svc.Changes = held, change.NewLog(change.DefaultWindow, t.Logf)
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))
		f.svc.RoundStarted(context.Background(), r7)
		<-held.saved

		sink.Place(r7, "parked")
		if n, err := f.svc.Reconcile(context.Background()); err != nil || n != 1 {
			t.Fatalf("reconcile with a drifted card during a forced flush = %d, %v, want 1", n, err)
		}
		close(held.release)
		eventually(t, "the override", func() bool { a, _ := f.applied(t, "test", r7); return a.Override == "parked" })
		if column, _, _ := sink.Card(r7); column != "parked" {
			t.Fatalf("the card the user parked moved to %q", column)
		}
	})
}

func TestARoundStartedDuringAForcedFlushKeepsItsForce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := testsink.New("test")
		f := newFixture(t, views.Sink{ViewSink: sink, Placements: sink})
		held := &heldState{State: f.state, hold: r7, saved: make(chan struct{}), release: make(chan struct{})}
		f.svc.State = held
		f.run(t)
		f.books.Put(asked(r7, "keep both?"))
		f.svc.RoundStarted(context.Background(), r7)
		<-held.saved

		sink.Place(r7, "parked")
		f.svc.RoundStarted(context.Background(), r7)
		close(held.release)
		eventually(t, "the second write saved", func() bool {
			return len(slices.DeleteFunc(f.pub.kinds(r7), func(k string) bool { return k != views.KindViewApplied })) == 2
		})
		if column, _, _ := sink.Card(r7); column == "parked" {
			t.Fatal("a round start left the card where the user parked it")
		}
		if a, _ := f.applied(t, "test", r7); a.Override != "" {
			t.Fatalf("a round start recorded the override %q", a.Override)
		}
	})
}
