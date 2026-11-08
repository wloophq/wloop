package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// threadPlacer places a draft as its publisher places a thread reply: in no thread, no broadcast; in a thread, the
// broadcast its proposer asked for, else def. places counts its calls; hangs makes it wait for its deadline.
type threadPlacer struct {
	*fakeFx
	thread bool
	def    draft.Broadcast
	places *atomic.Int32
	hangs  bool
}

func (p threadPlacer) Place(ctx context.Context, d draft.Draft) (draft.Placed, error) {
	p.places.Add(1)
	if p.hangs {
		<-ctx.Done()
		return draft.Placed{}, ctx.Err()
	}
	switch {
	case !p.thread:
		return draft.Placed{Broadcast: draft.NoBroadcast}, nil
	case d.Broadcast == draft.BroadcastOn || d.Broadcast == draft.BroadcastOff:
		return draft.Placed{Broadcast: d.Broadcast}, nil
	}
	return draft.Placed{Broadcast: p.def}, nil
}

func (r *outboxRig) placeAs(thread bool, def draft.Broadcast) *atomic.Int32 {
	n := &atomic.Int32{}
	pl := threadPlacer{fakeFx: r.fx, thread: thread, def: def, places: n}
	r.o.Effects = func(unit.ProviderID) (port.Effects, error) { return pl, nil }
	return n
}

func TestAReplyAsksForItsBroadcast(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.placeAs(true, draft.BroadcastOff)
	r.put(t, unitTask("t-1", task.OriginDerived, r7u))
	for i, c := range []struct {
		args string
		want draft.Broadcast
	}{
		{`"broadcast":"true","send":"draft"`, draft.BroadcastOn},
		{`"broadcast":"false","send":"now"`, draft.BroadcastOff},
		{`"send":"draft"`, draft.BroadcastOff},
	} {
		rec := post(t, r.s, api.TaskPath("t-1", "reply"), `{"source":"cockpit","args":{"text":"Shipped",`+c.args+`}}`)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", c.args, rec.Code, rec.Body)
		}
		ds := r.drafts(t)
		if len(ds) != i+1 {
			t.Fatalf("%s: drafts %+v", c.args, ds)
		}
		newest := slices.MaxFunc(ds, func(a, b draft.Draft) int { return cmp.Compare(a.ID, b.ID) })
		if newest.Broadcast != c.want {
			t.Errorf("%s: broadcast %q, want %q", c.args, newest.Broadcast, c.want)
		}
	}
	if rec := post(t, r.s, api.TaskPath("t-1", "reply"), `{"source":"cockpit","args":{"text":"Shipped","broadcast":"maybe"}}`); rec.Code != 400 {
		t.Errorf("a broadcast neither true nor false: %d %s", rec.Code, rec.Body)
	}
}

func TestADraftTheUserWritesAsksForItsBroadcast(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.placeAs(true, draft.BroadcastOff)
	rec := post(t, r.s, api.Drafts, `{"unit":{"provider":"github","key":"acme/api#7"},"text":"Shipped","broadcast":"on","source":"cockpit"}`)
	var d api.Draft
	if err := json.Unmarshal(rec.Body.Bytes(), &d); rec.Code != 200 || err != nil || d.Broadcast != "on" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.Drafts, `{"unit":{"provider":"github","key":"acme/api#7"},"text":"Shipped","broadcast":"loud","source":"cockpit"}`); rec.Code != 400 {
		t.Errorf("a broadcast neither on nor off: %d %s", rec.Code, rec.Body)
	}
}

func TestAReplyInNoThreadIgnoresItsBroadcast(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	r.placeAs(false, draft.NoBroadcast)
	r.put(t, unitTask("t-1", task.OriginDerived, r7u))
	rec := post(t, r.s, api.TaskPath("t-1", "reply"), `{"source":"cockpit","args":{"text":"Shipped","broadcast":"true","send":"draft"}}`)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if d := r.drafts(t)[0]; d.Broadcast != draft.NoBroadcast {
		t.Errorf("a reply in no thread broadcasts %q", d.Broadcast)
	}
}

func TestATasksDetailServesItsReplysBroadcast(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		thread bool
		def    draft.Broadcast
		want   string
	}{
		{"thread, on", true, draft.BroadcastOn, "on"},
		{"thread, off", true, draft.BroadcastOff, "off"},
		{"no thread", false, draft.NoBroadcast, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newOutboxRig(t)
			places := r.placeAs(c.thread, c.def)
			r.put(t, unitTask("t-1", task.OriginDerived, r7u))
			var page api.TasksPage
			if code := get(t, r.s, api.TasksPath+"?view=inbox", &page); code != 200 || len(page.Tasks) == 0 {
				t.Fatalf("list: %d %+v", code, page.Tasks)
			}
			var listed []api.TaskView
			if code := get(t, r.s, "/api/units/github/acme%2Fapi%237/tasks", &listed); code != 200 || len(listed) == 0 {
				t.Fatalf("unit tasks: %d %+v", code, listed)
			}
			for _, e := range append(page.Tasks, listed...) {
				if e.ReplyBroadcast != "" {
					t.Errorf("a list serves reply_broadcast %q", e.ReplyBroadcast)
				}
			}
			if n := places.Load(); n != 0 {
				t.Errorf("the lists asked the publisher %d times", n)
			}
			var v api.TaskView
			if code := get(t, r.s, api.TaskPath("t-1"), &v); code != 200 || v.ReplyBroadcast != c.want || places.Load() != 1 {
				t.Fatalf("%d reply_broadcast %q, want %q, asked %d times", code, v.ReplyBroadcast, c.want, places.Load())
			}
			if len(r.drafts(t)) != 0 {
				t.Errorf("reading the task stored a draft: %+v", r.drafts(t))
			}
			if code := get(t, r.s, api.TaskPath("t-1"), &v); code != 200 || v.ReplyBroadcast != c.want || places.Load() != 1 {
				t.Fatalf("read again: %d %q, the publisher asked %d times", code, v.ReplyBroadcast, places.Load())
			}
		})
	}
}

// A lookup the page ran out of time for says nothing about the publisher and is asked again on the next read; one that
// ran out of its own time is a slow publisher, kept for draftWhereRetry.
func TestAReplysBroadcastIsKeptOnlyWhenItsOwnLookupTimedOut(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newOutboxRig(t)
		n := &atomic.Int32{}
		pl := threadPlacer{fakeFx: r.fx, thread: true, places: n, hangs: true}
		r.o.Effects = func(unit.ProviderID) (port.Effects, error) { return pl, nil }
		r.put(t, unitTask("t-1", task.OriginDerived, r7u))
		r.s.whereLookup, r.s.wherePage = time.Second, 200*time.Millisecond
		for range 2 {
			if code := get(t, r.s, api.TaskPath("t-1"), nil); code != 200 {
				t.Fatalf("read: %d", code)
			}
		}
		if got := n.Load(); got != 2 {
			t.Fatalf("under the page's deadline the publisher was asked %d times, want each read", got)
		}
		r.s.whereLookup, r.s.wherePage = 200*time.Millisecond, time.Second
		for range 2 {
			if code := get(t, r.s, api.TaskPath("t-1"), nil); code != 200 {
				t.Fatalf("read: %d", code)
			}
		}
		if got := n.Load(); got != 3 {
			t.Fatalf("under its own deadline the publisher was asked %d more times, want once", got-2)
		}
	})
}

func TestReplyBroadcastsAndDraftWheresForgetOldLookups(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newOutboxRig(t)
		r.placeAs(true, draft.BroadcastOn)
		r.put(t, unitTask("t-1", task.OriginDerived, r7u))
		r.put(t, unitTask("t-2", task.OriginDerived, unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}))
		d := r.propose(t, r7u, "hello", draft.Wait)
		get(t, r.s, api.TaskPath("t-1"), nil)
		r.s.draftWhere(context.Background(), d)
		time.Sleep(draftWhereRetry + time.Second)
		get(t, r.s, api.TaskPath("t-2"), nil)
		count := func(m *sync.Map) int {
			n := 0
			m.Range(func(any, any) bool { n++; return true })
			return n
		}
		if b, w := count(&r.s.replyBroadcasts), count(&r.s.draftWheres); b != 1 || w != 0 {
			t.Fatalf("kept %d reply broadcasts and %d destinations, want only the newest broadcast", b, w)
		}
	})
}
