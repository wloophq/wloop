package daemon

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func queuePath(kind string) string { return strings.Replace(api.QueuePath, "{kind}", kind, 1) }

func TestQueueServesTheKindsItems(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"})
	g := r.withGates(t, "t-0000000000000001")
	var q api.Queue
	if code := get(t, r.s, queuePath("answer"), &q); code != 200 {
		t.Fatalf("%d", code)
	}
	if q.Kind != "answer" || q.Title != "Agent questions" || q.View != "decide-answer" || q.Total != 1 || len(q.Items) != 1 {
		t.Fatalf("queue %+v", q)
	}
	it := q.Items[0]
	if it.Task.ID != "t-0000000000000001" || it.Gate == nil || it.Gate.ID != g.ID || it.Situation != nil {
		t.Fatalf("item %+v", it)
	}
	if it.Primary.Kind != "gate" || it.Primary.ID != g.Intents[0] || it.Primary.Label == "" || it.Primary.Label == it.Primary.ID {
		t.Fatalf("primary %+v, want the gate's first intent with its label", it.Primary)
	}
}

func TestQueueNarrowedByLens(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "call Ada"})
	r.withGates(t, "t-0000000000000001")
	r.withGates(t, "t-0000000000000002")
	var all, narrowed api.Queue
	get(t, r.s, queuePath("answer"), &all)
	if code := get(t, r.s, queuePath("answer")+"?q="+url.QueryEscape("retries"), &narrowed); code != 200 {
		t.Fatalf("%d", code)
	}
	if all.Total != 2 || narrowed.Total != 1 || narrowed.Items[0].Task.ID != "t-0000000000000001" {
		t.Fatalf("all %d, narrowed %+v", all.Total, narrowed)
	}
}

func TestQueueUnknownKind(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	rec := httptest.NewRecorder()
	r.s.Handler().ServeHTTP(rec, apiRequest("GET", queuePath("merge"), nil))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "last-step") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestEveryHomeCounterCountsWhatItsQueueLists(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "ship the retries"},
		task.Task{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "call Ada"})
	r.withGates(t, "t-0000000000000001")
	r.withGates(t, "t-0000000000000002")
	if _, err := r.s.deps.Gates.Open(context.Background(), gate.Gate{Unit: task.UnitOf("t-0000000000000001"), Kind: gate.KindQuestion, Subject: "8"}, nil); err != nil {
		t.Fatal(err)
	}
	var h api.Home
	if code := get(t, r.s, api.HomePath, &h); code != 200 {
		t.Fatalf("%d", code)
	}
	for _, c := range h.Briefing.Counters {
		if c.Kind == "" {
			continue
		}
		var q api.Queue
		if code := get(t, r.s, queuePath(string(c.Kind)), &q); code != 200 {
			t.Fatalf("%s: %d", c.Kind, code)
		}
		if c.Count != q.Total {
			t.Errorf("%s: the home counts %d, its queue lists %d", c.Kind, c.Count, q.Total)
		}
		if c.Kind == "answer" && c.Count != 3 {
			t.Errorf("answer: %d, want the 3 open questions", c.Count)
		}
	}
}

func TestAClosedTasksUnitsWaitOnNoDecision(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	thread := unit.Unit{Provider: "fake", Key: "dm-1"}
	r.withLiveRows(map[unit.Unit]liveRow{thread: {signals: map[string]bool{"waits_on_viewer": true}}})
	m := decision.Map{Signals: map[unit.ProviderID]map[string]decision.Kind{"fake": {"waits_on_viewer": decision.Answer}}}
	for _, e := range []task.Task{
		{ID: "t-00000000000000d1", State: "done", Category: task.CategoryDone, Units: []unit.Unit{thread}},
		{ID: "t-00000000000000d2", State: "trash", Category: task.CategoryDropped, Units: []unit.Unit{thread}},
	} {
		found, err := (&taskDecisions{s: r.s, m: m}).Of(context.Background(), e)
		if err != nil || len(found) != 0 {
			t.Errorf("a %s task waits on %+v (%v)", e.State, found, err)
		}
	}
	open := task.Task{ID: "t-00000000000000d3", State: "next", Category: task.CategoryReady, Units: []unit.Unit{thread}}
	if found, err := (&taskDecisions{s: r.s, m: m}).Of(context.Background(), open); err != nil || len(found) != 1 {
		t.Fatalf("an open task waits on %+v (%v), want its thread's answer", found, err)
	}
}

func TestAnOpenQuestionOnADoneTaskStaysInItsQueue(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-00000000000000d1", State: "done", Category: task.CategoryDone, Title: "asked after it ended"})
	r.withGates(t, "t-00000000000000d1")
	var q api.Queue
	if code := get(t, r.s, queuePath("answer"), &q); code != 200 || q.Total != 1 {
		t.Fatalf("%d %+v", code, q)
	}
}
