package daemon

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// pageOf is a page's context over r's tasks, with its pass.
func pageOf(t *testing.T, r *tasksRig) context.Context {
	t.Helper()
	ctx := context.Background()
	n, err := r.g.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return coretasks.WithPage(n.Within(ctx))
}

// TestAMatchViewMatchesAsTheFullView holds what a lens query reads of a task, its texts and its facets, equal in
// its match view and its full view, for tasks with properties, units and their rows' facets, a brief, an open
// question and an owner.
func TestAMatchViewMatchesAsTheFullView(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.withFacetRows(facetRows{
		generalThread: {"where": {"general"}, "waits-on": {"me"}},
		apiPR:         {"where": {"acme/api"}, "ci": {"failure"}, "kind": {"bug"}},
	})
	if err := r.db.PutRecord(context.Background(), apiPR, "brief", task.WriterAgent, []byte(`{"goal":"Fix the widget","risk":"low","kind":"bugfix"}`), tasksNow); err != nil {
		t.Fatal(err)
	}
	es := []task.Task{
		{ID: "t-0000000000000001", State: "inbox", Category: task.CategoryInbox, Title: "Answer in general", Notes: "about the gears", Units: []unit.Unit{generalThread}},
		{ID: "t-0000000000000002", State: "next", Category: task.CategoryReady, Title: "Review the widget fix", Units: []unit.Unit{apiPR, generalThread},
			Props: task.Props{"focus": {Text: "on"}}},
		{ID: "t-0000000000000003", State: "waiting", Category: task.CategoryWaiting, Title: "Widget review from Ada",
			Props: task.Props{"owner": {Actors: []items.Person{{Key: "chat:ADA", Name: "Ada"}}}}},
	}
	r.put(t, es...)
	r.withGates(t, "t-0000000000000002")
	ctx := pageOf(t, r)
	vs := r.s.taskViews(ctx, r.g, false)
	for _, e := range es {
		match, err := vs.matchView(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		full, err := r.s.taskViews(ctx, r.g, false).view(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(match.Texts(), full.Texts()) || !reflect.DeepEqual(match.Facets, full.Facets) {
			t.Errorf("%s: match view texts %q facets %v, full view texts %q facets %v", e.ID, match.Texts(), match.Facets, full.Texts(), full.Facets)
		}
	}
}

// countedViews are a task viewer's views, counting the full ones built.
type countedViews struct {
	*taskViewer
	full int
}

func (c *countedViews) view(ctx context.Context, e task.Task) (api.TaskView, error) {
	c.full++
	return c.taskViewer.view(ctx, e)
}

// TestASearchBuildsTheFullViewOfItsResultsOnly matches every task on its match view and builds the full view of
// the one it returns.
func TestASearchBuildsTheFullViewOfItsResultsOnly(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	var es []task.Task
	for i := range 200 {
		es = append(es, task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), State: "next", Category: task.CategoryReady, Title: fmt.Sprintf("widget %d", i)})
	}
	es[42].Title = "the gear review"
	r.put(t, es...)
	ctx := pageOf(t, r)
	vs := &countedViews{taskViewer: r.s.taskViews(ctx, r.g, false)}
	out, err := r.s.search(ctx, r.g, vs, "gear")
	if err != nil {
		t.Fatal(err)
	}
	if out.Total != 1 || len(out.Groups) != 1 || out.Groups[0].Results[0].Task.ID != string(es[42].ID) {
		t.Fatalf("search = %+v, want the gear task", out)
	}
	if got := out.Groups[0].Results[0].Task; got.Verbs == nil {
		t.Errorf("the result's view has no verbs: %+v", got)
	}
	if vs.full != 1 {
		t.Errorf("%d full views built for one result among 200 tasks, want 1", vs.full)
	}
}

// TestATaskDetailsAsksCarryTheirBriefsQuotes gives each ask of a task's detail the quotes its brief record keeps
// of the item.
func TestATaskDetailsAsksCarryTheirBriefsQuotes(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.withFacetRows(facetRows{generalThread: {"where": {"general"}}})
	ctx := context.Background()
	it := items.Item{ID: "i-1", Unit: generalThread, Kind: items.Request, Summary: "send the widget specs", Origin: unit.Anchor{Unit: generalThread, Ref: "m:1"},
		Requester: items.Person{Key: "chat:BEA", Name: "Bea"}, Basis: items.NoBasis, State: items.Open, Confidence: items.High,
		Provenance: items.Extracted, CreatedAt: tasksNow, UpdatedAt: tasksNow}
	if err := r.db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{it}}); err != nil {
		t.Fatal(err)
	}
	if err := r.db.PutRecord(ctx, generalThread, "brief", task.WriterAgent,
		[]byte(`{"goal":"Send the specs","asks":[{"item":"i-1","quotes":[{"text":"can you send the widget specs?"}]}]}`), tasksNow); err != nil {
		t.Fatal(err)
	}
	r.put(t, task.Task{ID: "t-0000000000000001", State: "next", Category: task.CategoryReady, Title: "Send the specs", Units: []unit.Unit{generalThread},
		Items: []task.ItemRef{{Unit: generalThread, Item: "i-1"}}})
	v := r.view(t, "t-0000000000000001")
	if len(v.Asks) != 1 || !reflect.DeepEqual(v.Asks[0].Quotes, []string{"can you send the widget specs?"}) {
		t.Fatalf("asks %+v, want the one ask with its brief's quote", v.Asks)
	}
}
