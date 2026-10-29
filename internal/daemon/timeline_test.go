package daemon

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/timeline"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTaskViewServesItsBriefAndItsTimeline(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.s.deps.Timeline, r.s.deps.TaskRecords = timeline.Merged{Log: r.db, Holdings: r.db}, r.db
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	r.put(t, task.Task{ID: "t-0123456789abcdef", State: "next", Category: task.CategoryReady, Title: "Review the export", Units: []unit.Unit{pr}})
	ctx := context.Background()
	if err := r.db.PutRecord(ctx, pr, "brief", task.WriterAgent, []byte(`{"goal":"Export the ledger","risk":"high"}`), tasksNow); err != nil {
		t.Fatal(err)
	}
	var got api.TaskView
	if code := get(t, r.s, api.TaskPath("t-0123456789abcdef"), &got); code != 200 {
		t.Fatalf("GET the task: %d", code)
	}
	if got.Brief == nil || got.Brief.Kind != "brief" || string(got.Brief.Data) != `{"goal":"Export the ledger","risk":"high"}` || !got.Brief.At.Equal(tasksNow) {
		t.Fatalf("a pull request task's brief is its pull request's: %+v", got.Brief)
	}
	own := task.UnitOf("t-0123456789abcdef")
	if err := r.db.PutRecord(ctx, own, "brief", task.WriterPlugin, []byte(`{"asks":[{"ask":"Can you export it?"}]}`), tasksNow); err != nil {
		t.Fatal(err)
	}
	if get(t, r.s, api.TaskPath("t-0123456789abcdef"), &got); string(got.Brief.Data) != `{"asks":[{"ask":"Can you export it?"}]}` {
		t.Fatalf("the task's own brief: %+v", got.Brief)
	}

	var evs []api.TimelineEvent
	if code := get(t, r.s, api.TaskPath("t-0123456789abcdef", "timeline")+"?kinds=record", &evs); code != 200 {
		t.Fatalf("GET the timeline: %d", code)
	}
	if len(evs) != 2 || evs[0].Unit != "" || evs[0].Sub != "brief" || evs[0].Actor != "plugin" || evs[1].Unit != pr.String() || evs[0].Seq <= evs[1].Seq {
		t.Fatalf("the timeline %+v", evs)
	}
	var page []api.TimelineEvent
	get(t, r.s, api.TaskPath("t-0123456789abcdef", "timeline")+"?kinds=record&limit=1&before="+strconv.FormatInt(evs[0].Seq, 10), &page)
	if len(page) != 1 || page[0].Seq != evs[1].Seq {
		t.Fatalf("the page before %d: %+v", evs[0].Seq, page)
	}
	if code := get(t, r.s, api.TaskPath("t-0123456789abcdef", "timeline")+"?limit=x", nil); code != 400 {
		t.Fatalf("a bad limit: %d", code)
	}
}

// unitBriefs answers Ready by unit, recording what it was asked for.
type unitBriefs struct {
	offBriefs
	asked []unit.Unit
}

func (u *unitBriefs) Ready(_ context.Context, of unit.Unit, _ bool, _ []string) (intake.Readiness, error) {
	u.asked = append(u.asked, of)
	return intake.Readiness{Wait: brief.WaitBriefing}, nil
}

func TestAnIntakeOnATaskWaitsOnTheTasksBrief(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.s.deps.TaskRecords = r.db
	pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	r.put(t, task.Task{ID: "t-0123456789abcdef", State: "next", Category: task.CategoryReady, Title: "Review it", Units: []unit.Unit{pr}},
		task.Task{ID: "t-00000000000000a1", State: "inbox", Category: task.CategoryInbox, Title: "Capture"})
	inner := &unitBriefs{}
	b := taskBriefs{Briefs: inner, s: r.s}
	ctx := context.Background()
	rd, err := b.Ready(ctx, task.UnitOf("t-0123456789abcdef"), true, nil)
	if err != nil || rd.Wait != brief.WaitBriefing || len(inner.asked) != 1 || inner.asked[0] != pr {
		t.Fatalf("a pull request task waits on its pull request's brief: %+v %v %v", rd, inner.asked, err)
	}
	if err := r.db.PutRecord(ctx, task.UnitOf("t-0123456789abcdef"), "brief", task.WriterPerson, []byte(`{"goal":"Review it"}`), tasksNow); err != nil {
		t.Fatal(err)
	}
	if rd, err := b.Ready(ctx, task.UnitOf("t-0123456789abcdef"), true, nil); err != nil || !rd.Go || rd.Without || len(inner.asked) != 1 {
		t.Fatalf("a task with its own brief goes with it: %+v %v", rd, err)
	}
	if _, err := b.Ready(ctx, task.UnitOf("t-00000000000000a1"), true, nil); err != nil || inner.asked[1] != task.UnitOf("t-00000000000000a1") {
		t.Fatalf("a task that holds no unit waits on its own: %v %v", inner.asked, err)
	}
	if _, err := b.Ready(ctx, pr, true, nil); err != nil || inner.asked[2] != pr {
		t.Fatalf("a unit waits on its own: %v", inner.asked)
	}
}

// countedRecords counts the reads of the records it passes on.
type countedRecords struct {
	inner timeline.Records
	reads atomic.Int32
}

func (c *countedRecords) Records(ctx context.Context, kind string, units ...unit.Unit) (map[unit.Unit]port.Event, error) {
	c.reads.Add(1)
	return c.inner.Records(ctx, kind, units...)
}

func TestAListPageReadsTheBriefsOnce(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	recs := &countedRecords{inner: r.db}
	r.s.deps.TaskRecords = recs
	ctx := context.Background()
	const n = 300
	var ts []task.Task
	for i := range n {
		pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#" + strconv.Itoa(i+1)}
		ts = append(ts, task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), State: "next", Category: task.CategoryReady, Title: "Review " + pr.Key, Units: []unit.Unit{pr}})
		if i%2 == 0 {
			if err := r.db.PutRecord(ctx, pr, "brief", task.WriterAgent, []byte(`{"goal":"g`+strconv.Itoa(i+1)+`"}`), tasksNow); err != nil {
				t.Fatal(err)
			}
		}
	}
	r.put(t, ts...)
	p := r.page(t, "view=needs-me")
	if len(p.Tasks) != n {
		t.Fatalf("%d tasks on the page", len(p.Tasks))
	}
	briefed := 0
	for _, v := range p.Tasks {
		if v.Brief != nil {
			briefed++
		}
	}
	if briefed != n/2 {
		t.Fatalf("%d tasks with a brief, want %d", briefed, n/2)
	}
	if got := recs.reads.Load(); got != 1 {
		t.Fatalf("%d record reads for a page of %d tasks, want 1", got, n)
	}
}
