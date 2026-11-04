package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// sessionsOn is a turns service whose sessions db keeps, for a daemon to resolve a request's handle.
func sessionsOn(db *sqlite.DB) *turns.Service {
	repo := sqlite.New(db)
	return &turns.Service{Intents: turnstest.Intents(), Store: repo, Registry: repo, Sessions: turnstest.NewSessions(), Clock: agentClock{},
		Command: brand.Name}
}

// adoptSession makes handle a live session of svc and returns its id: one the user drives, or a worker the loop typed
// into last.
func adoptSession(t *testing.T, svc *turns.Service, handle string, driven bool) string {
	t.Helper()
	ctx := context.Background()
	id, err := svc.Resolve(ctx, turnstest.TestManager, handle)
	if err != nil {
		t.Fatal(err)
	}
	if !driven {
		if err := svc.Typed(ctx, id, "/review https://example.test/acme/api/pull/7"); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func TestTheGatePathTakesTheResolutionOfASessionTheUserDrives(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newTasksRig(t, nil)
	svc := sessionsOn(r.db)
	r.s.deps.Turns = svc
	worker, mine := adoptSession(t, svc, "term_1", false), adoptSession(t, svc, "term_2", true)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship it"})
	st := r.db.Gates(func() time.Time { return tasksNow })
	gs := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: fixedClock(tasksNow), Sessions: svc})
	r.s.deps.Gates, r.s.deps.GateReader = gs, st
	open := func(origin string) gate.Gate {
		g, err := gs.Open(ctx, gate.Gate{Unit: task.UnitOf("t-0000000000000001"), Kind: gate.KindQuestion, Subject: "7", Session: origin}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	for _, c := range []struct {
		name, handle, origin string
		want                 int
	}{
		{"a worker session", "term_1", "", 403},
		{"a session the user drives, on its own question", "term_2", mine, 403},
		{"a session the user drives", "term_2", worker, 200},
	} {
		g := open(c.origin)
		rec := postFrom(t, r.s, c.handle, fmt.Sprintf("/api/gates/%d/dismiss", g.ID), `{"source":"claude"}`)
		got, _ := st.Get(ctx, g.ID)
		switch {
		case rec.Code != c.want:
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		case c.want == 200 && (got.Status != gate.Dismissed || got.By != task.ActorAgent || got.BySession != mine):
			t.Errorf("%s: gate %+v, want dismissed through %s", c.name, got, mine)
		case c.want != 200 && got.Status != gate.Open:
			t.Errorf("%s: the refused resolution left the gate %s", c.name, got.Status)
		}
	}
}

func TestATaskPageOffersItsVerbsToWhoReadsIt(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	svc := sessionsOn(r.db)
	r.s.deps.Turns = svc
	adoptSession(t, svc, "term_1", false)
	adoptSession(t, svc, "term_2", true)
	r.put(t, task.Task{ID: "t-1", State: "next", Category: task.CategoryReady})
	done := func(handle string) api.VerbState {
		t.Helper()
		req := apiRequest("GET", api.TaskPath("t-1"), nil)
		req.Header.Set(api.SessionHeader, handle)
		rec := httptest.NewRecorder()
		r.s.Handler().ServeHTTP(rec, req)
		var v api.TaskView
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
			t.Fatalf("%s: %d %s", handle, rec.Code, rec.Body)
		}
		for _, st := range v.Verbs {
			if st.Verb == string(task.VerbDone) {
				return st
			}
		}
		t.Fatalf("%s: no done in %+v", handle, v.Verbs)
		return api.VerbState{}
	}
	if st := done("term_2"); !st.Enabled {
		t.Errorf("done to a session the user drives: %+v", st)
	}
	if st := done("term_1"); st.Enabled || !strings.Contains(st.Reason, "session you drive") {
		t.Errorf("done to a worker session: %+v", st)
	}
}

func TestTheTimelineNamesAChangeMadeThroughASessionTheUserDrove(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.s.deps.Timeline = timeline.Merged{Log: r.db, Holdings: r.db}
	svc := sessionsOn(r.db)
	r.s.deps.Turns = svc
	mine := adoptSession(t, svc, "term_2", true)
	r.put(t, task.Task{ID: "t-1", State: "next", Category: task.CategoryReady})
	if rec := postFrom(t, r.s, "term_2", api.TaskPath("t-1", "done"), `{"source":"claude"}`); rec.Code != 200 {
		t.Fatalf("done: %d %s", rec.Code, rec.Body)
	}
	var evs []api.TimelineEvent
	if code := get(t, r.s, api.TaskPath("t-1", "timeline")+"?kinds=verb_done", &evs); code != 200 {
		t.Fatalf("GET the timeline: %d", code)
	}
	if len(evs) != 1 || evs[0].Session != mine || evs[0].By != "you, via session "+mine || evs[0].Actor != "agent:claude" {
		t.Fatalf("the timeline %+v", evs)
	}
}
