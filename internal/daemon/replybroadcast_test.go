package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// threadPlacer places a draft as its publisher places a thread reply: in no thread, no broadcast; in a thread, the
// broadcast its proposer asked for, else def. places counts its calls.
type threadPlacer struct {
	*fakeFx
	thread bool
	def    draft.Broadcast
	places *atomic.Int32
}

func (p threadPlacer) Place(_ context.Context, d draft.Draft) (draft.Placed, error) {
	p.places.Add(1)
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
			var v api.TaskView
			if code := get(t, r.s, api.TaskPath("t-1"), &v); code != 200 || v.ReplyBroadcast != c.want {
				t.Fatalf("%d reply_broadcast %q, want %q", code, v.ReplyBroadcast, c.want)
			}
			if len(r.drafts(t)) != 0 {
				t.Errorf("reading the task stored a draft: %+v", r.drafts(t))
			}
			before := places.Load()
			if code := get(t, r.s, api.TaskPath("t-1"), &v); code != 200 || v.ReplyBroadcast != c.want || places.Load() != before {
				t.Fatalf("read again: %d %q, the publisher asked %d more times", code, v.ReplyBroadcast, places.Load()-before)
			}
			var page api.TasksPage
			if code := get(t, r.s, api.TasksPath+"?view=inbox", &page); code != 200 {
				t.Fatalf("list: %d", code)
			}
			var listed []api.TaskView
			if code := get(t, r.s, "/api/units/github/acme%2Fapi%237/tasks", &listed); code != 200 {
				t.Fatalf("unit tasks: %d", code)
			}
			for _, e := range append(page.Tasks, listed...) {
				if e.ReplyBroadcast != "" {
					t.Errorf("a list serves reply_broadcast %q", e.ReplyBroadcast)
				}
			}
			if n := places.Load(); n != before {
				t.Errorf("the lists asked the publisher %d times", n-before)
			}
		})
	}
}
