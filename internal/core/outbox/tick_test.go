package outbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
)

func TestWindowExpiresWithoutAWrite(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(10*time.Minute))
	d := e.propose(reply("x"))
	if d.State != draft.Proposed || !d.Deadline.Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("%+v", d)
	}
	e.clock.now = t0.Add(10*time.Minute - time.Second)
	if n, err := e.svc.Tick(ctx); n != 0 || err != nil || e.get(d.ID).State != draft.Proposed {
		t.Fatalf("tick before the deadline %d %v", n, err)
	}
	e.clock.now = t0.Add(10 * time.Minute)
	if n, err := e.svc.Tick(ctx); n != 1 || err != nil {
		t.Fatalf("tick %d %v", n, err)
	}
	if got := e.get(d.ID); got.State != draft.Approved || got.Would.Outcome != draft.OutcomeAuto {
		t.Fatalf("%+v", got)
	}
	if n, _ := e.svc.Tick(ctx); n != 0 {
		t.Fatalf("a second tick approved %d", n)
	}
	for range 2 {
		e.svc.Send(ctx)
	}
	if !slices.Equal(e.fx.published, []draft.ID{d.ID}) {
		t.Fatalf("published %v", e.fx.published)
	}
}

func TestDraftPolicyWindowDiscardWins(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(10*time.Minute))
	d := e.propose(reply("x"))
	e.clock.now = t0.Add(time.Minute)
	if _, err := e.svc.Discard(ctx, d.ID, ""); err != nil {
		t.Fatal(err)
	}
	e.clock.now = t0.Add(time.Hour)
	if n, _ := e.svc.Tick(ctx); n != 0 {
		t.Fatalf("tick approved %d", n)
	}
	e.svc.Send(ctx)
	if len(e.fx.published) != 0 || e.get(d.ID).State != draft.Discarded {
		t.Fatalf("published %v, %s", e.fx.published, e.get(d.ID).State)
	}

	raced := e.propose(reply("y"))
	e.clock.now = raced.Deadline
	e.store.beforeUpdate = func(u draft.Draft) {
		if u.ID == raced.ID && u.State == draft.Approved {
			e.store.beforeUpdate = nil
			if _, err := e.svc.Discard(ctx, raced.ID, ""); err != nil {
				t.Errorf("discard: %v", err)
			}
		}
	}
	if n, _ := e.svc.Tick(ctx); n != 0 {
		t.Fatalf("tick approved a draft discarded under it: %d", n)
	}
	if got := e.get(raced.ID); got.State != draft.Discarded {
		t.Fatalf("%+v", got)
	}
}

func TestEditStopsTheWindow(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(10*time.Minute))
	d := e.propose(reply("x"))
	e.clock.now = t0.Add(time.Minute)
	got, err := e.svc.Edit(ctx, d.ID, "better")
	if err != nil {
		t.Fatal(err)
	}
	if got.Approval != always || !got.Deadline.IsZero() {
		t.Fatalf("after the edit %+v", got)
	}
	e.clock.now = t0.Add(time.Hour)
	if n, _ := e.svc.Tick(ctx); n != 0 || e.get(d.ID).State != draft.Proposed {
		t.Fatalf("tick approved %d", n)
	}
	if _, ok, _ := e.svc.NextDeadline(ctx); ok {
		t.Fatal("an edited draft still has a deadline")
	}

	raced := e.propose(reply("y"))
	e.clock.now = raced.Deadline
	e.store.beforeUpdate = func(u draft.Draft) {
		if u.ID == raced.ID && u.State == draft.Approved {
			e.store.beforeUpdate = nil
			if _, err := e.svc.Edit(ctx, raced.ID, "edited under the tick"); err != nil {
				t.Errorf("edit: %v", err)
			}
		}
	}
	if n, _ := e.svc.Tick(ctx); n != 0 {
		t.Fatalf("tick approved a draft edited under it: %d", n)
	}
	if got := e.get(raced.ID); got.State != draft.Proposed || got.Approval != always {
		t.Fatalf("%+v", got)
	}
}

func TestNextDeadlineIsTheEarliest(t *testing.T) {
	e := newEnv()
	if _, ok, err := e.svc.NextDeadline(ctx); ok || err != nil {
		t.Fatalf("empty outbox: %v %v", ok, err)
	}
	withPolicy(e, window(30*time.Minute))
	late := e.propose(reply("late"))
	e.clock.now = t0.Add(time.Minute)
	withPolicy(e, window(5*time.Minute))
	early := e.propose(reply("early"))
	withPolicy(e, none)
	e.propose(reply("now"))
	at, ok, err := e.svc.NextDeadline(ctx)
	if !ok || err != nil || !at.Equal(early.Deadline) {
		t.Fatalf("next %v %v %v, want %v", at, ok, err, early.Deadline)
	}
	e.svc.Discard(ctx, early.ID, "")
	if at, _, _ := e.svc.NextDeadline(ctx); !at.Equal(late.Deadline) {
		t.Fatalf("after the discard %v, want %v", at, late.Deadline)
	}
}

func TestTickSurfacesStoreErrors(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(time.Minute))
	e.propose(reply("x"))
	e.clock.now = t0.Add(time.Hour)
	e.svc.Store = failingList{e.store}
	if _, err := e.svc.Tick(ctx); !errors.Is(err, errBoom) {
		t.Fatalf("tick: %v", err)
	}
	if _, _, err := e.svc.NextDeadline(ctx); !errors.Is(err, errBoom) {
		t.Fatalf("next deadline: %v", err)
	}
}

type failingList struct{ *memStore }

func (failingList) List(ctx context.Context, f Filter) ([]draft.Draft, error) { return nil, errBoom }

func TestConcurrentTicksApproveOnce(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(time.Minute))
	var ids []draft.ID
	for i := range 40 {
		ids = append(ids, e.propose(reply(fmt.Sprint("t", i))).ID)
	}
	e.clock.now = t0.Add(2 * time.Minute)
	var total atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			n, _ := e.svc.Tick(ctx)
			total.Add(int64(n))
		})
	}
	for i := 0; i < len(ids); i += 2 {
		wg.Go(func() { e.svc.Edit(ctx, ids[i], "edited") })
	}
	wg.Wait()
	for range 4 {
		wg.Go(func() { e.svc.Send(ctx) })
	}
	wg.Wait()
	e.svc.Send(ctx)
	seen := map[draft.ID]int{}
	for _, id := range e.fx.published {
		seen[id]++
	}
	auto := 0
	for _, id := range ids {
		d := e.get(id)
		if seen[id] > 1 {
			t.Errorf("draft %d published %d times", id, seen[id])
		}
		if d.Would.Outcome == draft.OutcomeAuto {
			auto++
			if d.Edited {
				t.Errorf("draft %d edited and auto-approved", id)
			}
		}
		if d.Edited && d.State != draft.Proposed {
			t.Errorf("edited draft %d is %s", id, d.State)
		}
	}
	if int(total.Load()) != auto || len(e.fx.published) != auto {
		t.Errorf("tick total %d, auto %d, published %d", total.Load(), auto, len(e.fx.published))
	}
}
