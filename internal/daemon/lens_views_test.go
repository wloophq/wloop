package daemon

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
	intentload "github.com/wloophq/wloop/internal/intent"
)

func TestBadgeCountsTheProfilesViews(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, mixedTasks()...)
	r.put(t, task.Task{ID: "t-9", State: "inbox", Category: task.CategoryInbox})
	p := r.page(t, "view=inbox")
	if c := p.Counters; c.Badge != c.Views["inbox"]+c.Views["needs-me"] || c.Badge == 0 {
		t.Fatalf("the bundled badge %d, views %v", c.Badge, c.Views)
	}
	var cat api.ViewsCatalog
	get(t, r.s, api.ViewsPath, &cat)
	var badged []string
	for _, v := range cat.Views {
		if v.Badge {
			badged = append(badged, v.ID)
		}
	}
	if !slices.Equal(badged, []string{"inbox", "needs-me"}) {
		t.Fatalf("the catalog's badge views %v", badged)
	}

	l := *bundledLoaded()
	l.Views.Badge = []string{"someday", "logbook"}
	mine := newTasksRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return &l } })
	mine.put(t, mixedTasks()...)
	mine.put(t, task.Task{ID: "t-done", State: "done", Category: task.CategoryDone})
	var c api.Counters
	get(t, mine.s, api.CountersPath, &c)
	if c.Badge != 2 || c.Badge != c.Views["someday"]+c.Views["logbook"] {
		t.Fatalf("a profile's badge counts %d, views %v", c.Badge, c.Views)
	}
}

func TestCountersRepushOnGatesAndWakes(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-done", State: "done", Category: task.CategoryDone}, task.Task{ID: "t-next", State: "next", Category: task.CategoryReady})
	sub := r.subscribe(t)
	if _, err := r.db.Gates(func() time.Time { return tasksNow }).Open(context.Background(), gate.Gate{Task: "t-done", Kind: gate.KindQuestion, Name: "question", Subject: "q1"}); err != nil {
		t.Fatal(err)
	}
	c := nextChange(t, sub)
	if c.Counters.Views["needs-me"] != 2 {
		t.Fatalf("after a gate opened on a done task: %+v", c.Counters)
	}
	if err := r.db.Wakes().Put(context.Background(), wake.Wake{Task: "t-next", Until: tasksNow.Add(time.Hour), Return: "next"}); err != nil {
		t.Fatal(err)
	}
	if c = nextChange(t, sub); c.Counters.Views["snoozed"] != 1 {
		t.Fatalf("after a wake: %+v", c.Counters)
	}
}

func TestNeedsMeOrdersASequentialProjectsNextActionFirst(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.serveProjects()
	at := func(m int) time.Time { return tasksNow.Add(time.Duration(m) * time.Minute) }
	r.put(t,
		task.Task{ID: "t-x", State: "next", Category: task.CategoryReady, Created: at(-60), Updated: at(-60)},
		task.Task{ID: "t-p", Props: task.Props{task.PropKind: {Text: task.KindProject}}, State: "next", Category: task.CategoryReady, Sequential: true, Created: at(-50), Updated: at(-50)},
		task.Task{ID: "t-a2", State: "next", Category: task.CategoryReady, Parent: "t-p", Position: 2, Created: at(-40), Updated: at(-40)},
		task.Task{ID: "t-a1", State: "next", Category: task.CategoryReady, Parent: "t-p", Position: 1, Created: at(-30), Updated: at(-30)},
	)
	if got := taskIDs(r.page(t, "view=needs-me").Tasks); !slices.Equal(got, []string{"t-a1", "t-x", "t-p", "t-a2"}) {
		t.Fatalf("needs me %v", got)
	}
	if rec := post(t, r.s, api.TaskPath("t-a1", "done"), `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if got := taskIDs(r.page(t, "view=needs-me").Tasks); !slices.Equal(got, []string{"t-a2", "t-x", "t-p"}) {
		t.Fatalf("after the first action: %v", got)
	}
}

// TestAFailedApprovedDraftShowsInTheOutboxWithRetry holds review focus 5: an approved draft whose delivery
// failed is in the Outbox, as its gate and its draft's task, with Retry offered.
func TestAFailedApprovedDraftShowsInTheOutboxWithRetry(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	failed := r.stored(t, draft.Draft{Unit: r7u, Text: "failed once", State: draft.Failed, Error: "refused"})
	r.stored(t, draft.Draft{Unit: r7u, Text: "out", State: draft.Sent})
	p := r.page(t, "view=outbox")
	if got := taskIDs(p.Tasks); !slices.Equal(got, []string{fmt.Sprintf("draft:%d", failed.ID)}) || p.Counters.Views["outbox"] != 1 {
		t.Fatalf("outbox %v, counters %v", got, p.Counters.Views)
	}
	if len(p.Gates) != 1 || p.Gates[0].Status != string(gate.Approved) || p.Gates[0].Delivery != string(draft.Failed) {
		t.Fatalf("gates %+v", p.Gates)
	}
	retry := slices.IndexFunc(p.Tasks[0].Verbs, func(v api.VerbState) bool { return v.Verb == "retry" })
	if retry < 0 || !p.Tasks[0].Verbs[retry].Enabled {
		t.Fatalf("verbs %+v", p.Tasks[0].Verbs)
	}
}

// openTasks gives a gate the task of its unit, or a new capture task, as serve wires the gates.
type openTasks struct {
	store coretasks.Store
	g     *coretasks.Service
}

func (o openTasks) TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error) {
	return o.store.TasksOf(ctx, u)
}

func (o openTasks) Open(ctx context.Context, r coretasks.OpenRequest) (task.Task, error) {
	return o.g.Open(ctx, r)
}

// TestADraftOnAnUnheldUnitCountsOnlyInTheOutbox checks that a draft on a unit no task holds, whose gate opens a
// capture task, counts once toward the badge: its task in the Inbox and not also in Needs me, its gate in the
// Outbox, which the badge leaves out.
func TestADraftOnAnUnheldUnitCountsOnlyInTheOutbox(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	st := r.db.Gates(r.clock.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: r.clock, Tasks: openTasks{r.db.Tasks(), r.g}})
	r.o.Gates, r.s.deps.Gates = rigGates{svc, st}, svc
	r.propose(t, r7u, "on a unit no task holds", draft.Wait)
	c := r.page(t, "view=outbox").Counters
	if c.Views["outbox"] != 1 || c.Views["inbox"] != 1 || c.Views["needs-me"] != 0 || c.Badge != 1 {
		t.Fatalf("counters %+v", c)
	}
}

// TestTheOutboxPageAndCountAgreeUnderAFilter checks that the area and the contexts narrow the Outbox's gates as
// they narrow its tasks, and the area its count, which the contexts leave as the chips count them apart: a
// draft stands as a task that takes the properties of the task its gate is on.
func TestTheOutboxPageAndCountAgreeUnderAFilter(t *testing.T) {
	t.Parallel()
	r := newOutboxRig(t)
	st := r.db.Gates(r.clock.Now)
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: r.clock, Tasks: openTasks{r.db.Tasks(), r.g}})
	r.o.Gates, r.s.deps.Gates = rigGates{svc, st}, svc
	r8u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	r.put(t, task.Task{ID: "t-oss", State: "next", Category: task.CategoryReady, Units: []unit.Unit{r7u},
		Props: task.Props{"area": {Text: "OSS"}, "contexts": {List: []string{"@reply"}}}},
		task.Task{ID: "t-hiring", State: "next", Category: task.CategoryReady, Units: []unit.Unit{r8u}, Props: task.Props{"area": {Text: "Hiring"}}})
	r.propose(t, r7u, "on the OSS task", draft.Wait)
	r.propose(t, r8u, "on the Hiring task", draft.Wait)
	for _, c := range []struct {
		query        string
		listed, area int
	}{
		{"view=outbox", 2, 2}, {"view=outbox&area=OSS", 1, 1}, {"view=outbox&area=Hiring", 1, 1},
		{"view=outbox&context=@reply", 1, 2}, {"view=outbox&area=Hiring&context=@reply", 0, 1},
	} {
		p := r.page(t, c.query)
		if len(p.Gates) != c.listed || len(p.Tasks) != c.listed || p.Counters.Views["outbox"] != c.area {
			t.Errorf("%s: %d gates, %d tasks, count %d; want %d listed, %d counted", c.query, len(p.Gates), len(p.Tasks), p.Counters.Views["outbox"], c.listed, c.area)
		}
	}
}
