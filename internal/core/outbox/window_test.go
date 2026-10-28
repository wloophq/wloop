package outbox

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
)

func TestAWindowDraftIsQueuedWhenItsGateWindowPasses(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(10*time.Minute))
	d := e.propose(reply("x"))
	if g := e.gate(d.ID); d.State != draft.Held || g.Status != gate.Open || !g.Deadline.Equal(t0.Add(10*time.Minute)) || g.Default != "approve" {
		t.Fatalf("%+v %+v", d, g)
	}
	e.clock.now = t0.Add(10*time.Minute - time.Second)
	if err := e.tick(); err != nil || e.get(d.ID).State != draft.Held {
		t.Fatalf("tick before the deadline: %v %s", err, e.get(d.ID).State)
	}
	e.clock.now = t0.Add(10 * time.Minute)
	if err := e.tick(); err != nil {
		t.Fatal(err)
	}
	if got, g := e.get(d.ID), e.gate(d.ID); got.State != draft.Queued || g.Status != gate.Approved || g.Rule != "window" {
		t.Fatalf("%+v %+v", got, g)
	}
	for range 2 {
		e.svc.Send(ctx)
	}
	if !slices.Equal(e.fx.published, []draft.ID{d.ID}) {
		t.Fatalf("published %v", e.fx.published)
	}
}

func TestDiscardingAWindowDraftBeatsItsWindow(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(10*time.Minute))
	d := e.propose(reply("x"))
	e.clock.now = t0.Add(time.Minute)
	if err := e.decide(d.ID, "discard"); err != nil {
		t.Fatal(err)
	}
	e.clock.now = t0.Add(time.Hour)
	if err := e.tick(); err != nil {
		t.Fatal(err)
	}
	e.svc.Send(ctx)
	if got := e.get(d.ID); len(e.fx.published) != 0 || got.State != draft.Dropped || got.Reason != "user" {
		t.Fatalf("published %v, %+v", e.fx.published, got)
	}
}

func TestEditStopsTheWindow(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(10*time.Minute))
	d := e.propose(reply("x"))
	first := e.gate(d.ID)
	e.clock.now = t0.Add(time.Minute)
	got, err := e.svc.Edit(ctx, d.ID, "better")
	if err != nil {
		t.Fatal(err)
	}
	g := e.gate(got.ID)
	if e.approval(got.ID) != waiting || g.ID == first.ID || !g.Deadline.IsZero() {
		t.Fatalf("after the edit %+v", g)
	}
	if old, _ := e.gates.Get(ctx, first.ID); old.Status != gate.Superseded {
		t.Fatalf("the windowed gate %+v", old)
	}
	e.clock.now = t0.Add(time.Hour)
	if err := e.tick(); err != nil || e.get(d.ID).State != draft.Held {
		t.Fatalf("tick: %v %s", err, e.get(d.ID).State)
	}
}

func TestAnEditRacingItsWindowNeverPostsUnapprovedText(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(time.Minute))
	var ids []draft.ID
	for i := range 40 {
		ids = append(ids, e.propose(reply(fmt.Sprint("t", i))).ID)
	}
	e.clock.now = t0.Add(2 * time.Minute)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { e.tick() })
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
	for _, id := range ids {
		d := e.get(id)
		if seen[id] > 1 {
			t.Errorf("draft %d published %d times", id, seen[id])
		}
		if d.Edited && (seen[id] > 0 || d.State != draft.Held) {
			t.Errorf("edited draft %d is %s, published %d", id, d.State, seen[id])
		}
		if !d.Edited && d.State != draft.Sent {
			t.Errorf("draft %d its window released is %s", id, d.State)
		}
	}
}
