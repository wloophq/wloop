package timeline_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// log is an event log over rows numbered in the order they were added.
type log struct{ rows []timeline.Event }

func (l *log) add(u unit.Unit, kind string) {
	l.rows = append(l.rows, timeline.Event{Seq: int64(len(l.rows) + 1), Event: port.Event{Unit: u, Kind: kind, At: time.Unix(0, 0)}})
}

func (l *log) EventsOf(_ context.Context, units []unit.Unit, f timeline.Filter) ([]timeline.Event, error) {
	var out []timeline.Event
	for _, ev := range slices.Backward(l.rows) {
		if !slices.Contains(units, ev.Unit) || len(f.Kinds) > 0 && !slices.Contains(f.Kinds, ev.Kind) || f.Before > 0 && ev.Seq >= f.Before {
			continue
		}
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
		out = append(out, ev)
	}
	return out, nil
}

type holdings map[task.ID][]unit.Unit

func (h holdings) UnitsOf(_ context.Context, id task.ID) ([]unit.Unit, error) { return h[id], nil }

func seqs(evs []timeline.Event) []int64 {
	out := []int64{}
	for _, ev := range evs {
		out = append(out, ev.Seq)
	}
	return out
}

func TestTimelineMergesTheTasksEventsAndItsUnits(t *testing.T) {
	id := task.ID("t-0123456789abcdef")
	other := task.ID("t-fedcba9876543210")
	pr := unit.Unit{Provider: "github", Key: "acme/api#7"}
	thread := unit.Unit{Provider: "chat", Key: "C1/1.1"}
	l := &log{}
	l.add(task.UnitOf(id), "task_created")    // 1
	l.add(pr, "pushed")                       // 2
	l.add(task.UnitOf(other), "task_created") // 3
	l.add(thread, "message")                  // 4
	l.add(pr, timeline.KindRecord)            // 5
	l.add(unit.Unit{Provider: "github", Key: "acme/api#8"}, "pushed")
	l.add(task.UnitOf(id), "chat") // 7
	r := timeline.Merged{Log: l, Holdings: holdings{id: {pr, thread, pr}}}
	ctx := context.Background()

	got, err := r.Of(ctx, id, timeline.Filter{})
	if err != nil || !slices.Equal(seqs(got), []int64{7, 5, 4, 2, 1}) {
		t.Fatalf("the merged timeline: %v %v", seqs(got), err)
	}
	if got[1].Unit != pr || got[0].Unit != task.UnitOf(id) {
		t.Fatalf("each event keeps its unit: %+v", got[:2])
	}
	page, _ := r.Of(ctx, id, timeline.Filter{Limit: 2})
	next, _ := r.Of(ctx, id, timeline.Filter{Limit: 2, Before: page[len(page)-1].Seq})
	if !slices.Equal(seqs(page), []int64{7, 5}) || !slices.Equal(seqs(next), []int64{4, 2}) {
		t.Fatalf("pages by Seq: %v then %v", seqs(page), seqs(next))
	}
	if kinds, _ := r.Of(ctx, id, timeline.Filter{Kinds: []string{timeline.KindRecord, "chat"}}); !slices.Equal(seqs(kinds), []int64{7, 5}) {
		t.Fatalf("by kind: %v", seqs(kinds))
	}
	if none, err := r.Of(ctx, task.ID("t-1111111111111111"), timeline.Filter{}); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("a task with no events: %v %v", none, err)
	}
}

type records map[unit.Unit]port.Event

func (r records) Records(_ context.Context, kind string, units ...unit.Unit) (map[unit.Unit]port.Event, error) {
	out := map[unit.Unit]port.Event{}
	for _, u := range units {
		if ev, ok := r[u]; ok && ev.Key == kind {
			out[u] = ev
		}
	}
	return out, nil
}

func TestAPullRequestTasksBriefIsItsPullRequests(t *testing.T) {
	id := task.ID("t-0123456789abcdef")
	pr := unit.Unit{Provider: "github", Key: "acme/api#7"}
	r := records{pr: {Unit: pr, Kind: timeline.KindRecord, Key: "brief", Data: `{"goal":"pr"}`}}
	ctx := context.Background()
	if ev, ok, err := timeline.RecordOf(ctx, r, "brief", id, []unit.Unit{pr}); err != nil || !ok || ev.Data != `{"goal":"pr"}` {
		t.Fatalf("the pull request's: %+v %v %v", ev, ok, err)
	}
	r[task.UnitOf(id)] = port.Event{Unit: task.UnitOf(id), Kind: timeline.KindRecord, Key: "brief", Data: `{"goal":"own"}`}
	if ev, _, _ := timeline.RecordOf(ctx, r, "brief", id, []unit.Unit{pr}); ev.Data != `{"goal":"own"}` {
		t.Fatalf("the task's own comes first: %+v", ev)
	}
	if _, ok, _ := timeline.RecordOf(ctx, r, "report", id, []unit.Unit{pr}); ok {
		t.Fatal("a kind none keeps")
	}
}
