package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/wake"
)

// sweepingGates are the gates service as the tasks service reads it, with what a merge supersedes.
type sweepingGates struct {
	s *gates.Service
	r gates.Reader
}

func (g sweepingGates) Open(ctx context.Context, gt gate.Gate) (gate.Gate, error) {
	return g.s.Open(ctx, gt, nil)
}
func (g sweepingGates) Get(ctx context.Context, id int64) (gate.Gate, error) { return g.r.Get(ctx, id) }
func (g sweepingGates) OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	return g.r.OfTask(ctx, id)
}
func (g sweepingGates) HoldsTask(ctx context.Context, id task.ID) (bool, error) {
	return g.s.HoldsTask(ctx, id)
}
func (g sweepingGates) ActingFor(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	return g.s.ActingFor(ctx, id)
}

func (g sweepingGates) Supersede(ctx context.Context, ids ...int64) error {
	for _, id := range ids {
		if err := g.s.Supersede(ctx, id, task.ActorPerson); err != nil && !errors.Is(err, gates.ErrResolved) {
			return err
		}
	}
	return nil
}

var (
	mergePR     = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	mergeThread = unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
)

const (
	mergeX task.ID = "t-00000000000000a1"
	mergeY task.ID = "t-00000000000000b2"
	mergeW task.ID = "t-00000000000000c3"
)

// newMergeRig is the tasks rig whose tasks service merges, over the gates service of its store.
func newMergeRig(t *testing.T) *tasksRig {
	t.Helper()
	r := newTasksRig(t, nil)
	st := r.db.Gates(func() time.Time { return tasksNow })
	svc := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: fixedClock(tasksNow), Decider: r.s})
	r.s.deps.Gates, r.s.deps.GateReader = svc, st
	r.g = coretasks.New(withLens(coretasks.Deps{States: loadedHandlers{taskstest.BundledStates, bundledLoaded}, Authority: taskstest.BundledStates.Authority,
		Store: r.db.Tasks(), Items: r.ledger, Facts: coretasks.CardFacts{Cards: r.db.Cards(), Me: func() task.Me { return meID }},
		Me: meID, Clock: fixedClock(tasksNow), Wakes: r.db.Wakes(), Gates: sweepingGates{svc, st}}, bundledLoaded, r.facts))
	r.s.deps.Tasks = r.g
	return r
}

// seedMerge stores X, a pull request's task with a wake, an item, a gate and a held draft, and Y, an older ask
// on a thread.
func (r *tasksRig) seedMerge(t *testing.T) (x, y task.Task) {
	t.Helper()
	ctx := context.Background()
	x = task.Task{ID: mergeX, Title: "Review acme/api#7", State: "next", Category: task.CategoryReady, Units: []unit.Unit{mergePR},
		Items: []task.ItemRef{{Unit: mergePR, Item: "gh:review"}}, Origin: task.OriginDerived, Created: tasksNow.Add(-time.Hour), Updated: tasksNow.Add(-time.Hour)}
	x.SetProp("area", task.Value{Text: "OSS"}, task.WriterPerson, tasksNow.Add(-time.Hour))
	y = task.Task{ID: mergeY, Title: "Finish acme/api#7", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{mergeThread},
		Items: []task.ItemRef{{Unit: mergeThread, Item: "s:ask"}}, Origin: task.OriginDerived, Created: tasksNow.Add(-2 * time.Hour), Updated: tasksNow.Add(-2 * time.Hour)}
	r.put(t, x, y)
	asks := []items.Item{
		{ID: "gh:review", Unit: mergePR, Kind: items.Request, Summary: x.Title, State: items.Open, Basis: items.NoBasis,
			Requester: items.Person{Key: "github:ana"}, WaitingOn: []items.Person{{Key: "github:me"}}, CreatedAt: x.Created, UpdatedAt: x.Created},
		{ID: "s:ask", Unit: mergeThread, Kind: items.Request, Summary: y.Title, State: items.Open, Basis: items.NoBasis,
			Requester: items.Person{Key: "slack:U0ANA"}, WaitingOn: []items.Person{{Key: "github:me"}}, CreatedAt: y.Created, UpdatedAt: y.Created},
	}
	if err := r.db.Items().Commit(ctx, coreitems.Commit{Items: asks}); err != nil {
		t.Fatal(err)
	}
	if err := r.db.Wakes().Put(ctx, wake.Wake{Task: mergeX, Until: tasksNow.Add(24 * time.Hour), Return: "next"}); err != nil {
		t.Fatal(err)
	}
	sqlitetest.Exec(t, r.db, `INSERT INTO gates (id, task_id, provider, unit_key, kind, name, status, intents, opened_at) VALUES (41, ?, 'github', 'acme/api#7', 'question', 'question', 'open', '[]', ?)`,
		string(mergeX), tasksNow.UTC().Format(event.TimeLayout))
	return r.stored(t, mergeX), r.stored(t, mergeY)
}

func (r *tasksRig) stored(t *testing.T, id task.ID) task.Task {
	t.Helper()
	e, err := r.db.Tasks().Task(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mergeBody(other task.ID) string {
	return fmt.Sprintf(`{"source":"cockpit","args":{"task":%q}}`, other)
}

func (r *tasksRig) mergeRoute(t *testing.T, on, with task.ID) {
	t.Helper()
	if rec := post(t, r.s, api.TaskPath(string(on), "merge"), mergeBody(with)); rec.Code != 200 {
		t.Fatalf("merge %s with %s: %d %s", on, with, rec.Code, rec.Body)
	}
}

func TestMergeThroughTheRouteServesTheSurvivorUnderBothIDs(t *testing.T) {
	t.Parallel()
	r := newMergeRig(t)
	r.seedMerge(t)
	r.mergeRoute(t, mergeX, mergeY)
	for _, id := range []task.ID{mergeX, mergeY} {
		v := r.view(t, string(id))
		if v.ID != string(mergeY) || v.Title != "Finish acme/api#7" {
			t.Fatalf("GET %s serves %s %q", id, v.ID, v.Title)
		}
	}
	y := r.stored(t, mergeY)
	if !slices.Equal(y.Units, []unit.Unit{mergeThread, mergePR}) || len(y.Items) != 2 || y.Prop("area").Text != "" {
		t.Fatalf("survivor %+v", y)
	}
	for range 3 {
		if err := r.g.Sync(context.Background(), []unit.Unit{mergePR, mergeThread}); err != nil {
			t.Fatal(err)
		}
	}
	if after := r.stored(t, mergeY); !after.Same(y) {
		t.Fatalf("syncs changed the survivor\n got %+v\nwant %+v", after, y)
	}
	if all, err := r.db.Tasks().Tasks(context.Background(), task.Query{All: true}); err != nil || len(all) != 1 {
		t.Fatalf("tasks after the syncs: %d, %v", len(all), err)
	}
	g, err := r.s.deps.GateReader.Get(context.Background(), 41)
	if err != nil || g.Task != mergeY || g.Status != gate.Open {
		t.Fatalf("the merged task's question: %+v, %v", g, err)
	}
}

func TestSplitThroughTheRouteRoundTripsBothTasks(t *testing.T) {
	t.Parallel()
	r := newMergeRig(t)
	x, y := r.seedMerge(t)
	r.mergeRoute(t, mergeX, mergeY)
	if rec := post(t, r.s, api.TaskPath(string(mergeY), "split"), mergeBody(mergeX)); rec.Code != 200 {
		t.Fatalf("split: %d %s", rec.Code, rec.Body)
	}
	gotX, gotY := r.stored(t, mergeX), r.stored(t, mergeY)
	if !gotX.Same(x) || !gotX.Updated.Equal(x.Updated) || !gotX.OpenChanged.Equal(x.OpenChanged) || !gotX.Asleep {
		t.Fatalf("X\n got %+v\nwant %+v", gotX, x)
	}
	gotY.Updated = y.Updated
	if !gotY.Same(y) {
		t.Fatalf("Y\n got %+v\nwant %+v", gotY, y)
	}
	if v := r.view(t, string(mergeX)); v.ID != string(mergeX) {
		t.Fatalf("GET X serves %s", v.ID)
	}
	if w, ok, err := r.db.Wakes().Of(context.Background(), mergeX); err != nil || !ok || w.Return != "next" {
		t.Fatalf("X's wake %+v %v %v", w, ok, err)
	}
	if g, _ := r.s.deps.GateReader.Get(context.Background(), 41); g.Task != mergeX {
		t.Fatalf("the question went back to %s", g.Task)
	}
}

func TestAnAgentTheUserDoesNotDriveCannotMergeOrSplit(t *testing.T) {
	t.Parallel()
	r := newMergeRig(t)
	svc := sessionsOn(r.db)
	r.s.deps.Turns = svc
	adoptSession(t, svc, "term_1", false)
	adoptSession(t, svc, "term_2", true)
	r.seedMerge(t)
	if rec := postFrom(t, r.s, "term_1", api.TaskPath(string(mergeX), "merge"), mergeBody(mergeY)); rec.Code != 403 {
		t.Fatalf("a worker session's merge: %d %s", rec.Code, rec.Body)
	}
	if e := r.stored(t, mergeX); e.ID != mergeX {
		t.Fatal("the refused merge ran")
	}
	if rec := postFrom(t, r.s, "term_2", api.TaskPath(string(mergeX), "merge"), mergeBody(mergeY)); rec.Code != 200 {
		t.Fatalf("a driven session's merge: %d %s", rec.Code, rec.Body)
	}
	if rec := postFrom(t, r.s, "term_1", api.TaskPath(string(mergeY), "split"), mergeBody(mergeX)); rec.Code != 403 {
		t.Fatalf("a worker session's split: %d %s", rec.Code, rec.Body)
	}
	if e := r.stored(t, mergeX); e.ID != mergeY {
		t.Fatal("the refused split ran")
	}
	if rec := postFrom(t, r.s, "term_2", api.TaskPath(string(mergeY), "split"), mergeBody(mergeX)); rec.Code != 200 {
		t.Fatalf("a driven session's split: %d %s", rec.Code, rec.Body)
	}
}

func TestATaskViewListsTheTasksMergedIntoIt(t *testing.T) {
	t.Parallel()
	r := newMergeRig(t)
	r.seedMerge(t)
	r.put(t, task.Task{ID: mergeW, Title: "Fix the flaky test", State: "next", Category: task.CategoryReady, Origin: task.OriginUser,
		Created: tasksNow.Add(-30 * time.Minute), Updated: tasksNow.Add(-30 * time.Minute)})
	r.mergeRoute(t, mergeX, mergeW)
	r.mergeRoute(t, mergeY, mergeX)
	want := []api.TaskRef{{ID: string(mergeX), Title: "Review acme/api#7", State: "next",
		Within: []api.TaskRef{{ID: string(mergeW), Title: "Fix the flaky test", State: "next"}}}}
	if got := r.view(t, string(mergeY)).MergedFrom; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("merged from %+v, want %+v", got, want)
	}
	if rec := post(t, r.s, api.TaskPath(string(mergeY), "split"), mergeBody(mergeW)); rec.Code == 200 {
		t.Fatal("a nested merge split from the outer task")
	}
}

func TestAMergeRemovesTheMergedTaskFromAPagedView(t *testing.T) {
	t.Parallel()
	r := newMergeRig(t)
	x, _ := r.seedMerge(t)
	x.Become("inbox", taskstest.BundledStates)
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpUpdate, Task: x}}); err != nil {
		t.Fatal(err)
	}
	before := r.page(t, "view=inbox")
	if !slices.Contains(taskIDs(before.Tasks), string(mergeX)) {
		t.Fatalf("before: %v", taskIDs(before.Tasks))
	}
	r.mergeRoute(t, mergeX, mergeY)
	since := api.Since{Epoch: before.Epoch, Rev: before.Rev}.String()
	got := r.page(t, "view=inbox&since="+url.QueryEscape(since))
	if !got.Full || slices.Contains(taskIDs(got.Tasks), string(mergeX)) || !slices.Contains(taskIDs(got.Tasks), string(mergeY)) {
		t.Fatalf("after the merge: full %v, tasks %v", got.Full, taskIDs(got.Tasks))
	}
}
