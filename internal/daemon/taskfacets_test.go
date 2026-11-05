package daemon

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/readmodel"
)

// facetRows serves a row with the given facets for each unit it holds.
type facetRows map[unit.Unit]map[string][]string

func (facetRows) Provider() unit.ProviderID { return "fake" }

func (facetRows) Views() []api.ViewSpec           { return nil }
func (facetRows) Situations() []api.SituationSpec { return nil }
func (facetRows) Facets() []api.FacetSpec {
	return []api.FacetSpec{{ID: "where", Label: "Where", Match: "suffix"}}
}

func (p facetRows) Listed(context.Context) ([]unit.Unit, error) {
	out := []unit.Unit{}
	for u := range p {
		out = append(out, u)
	}
	return out, nil
}

func (p facetRows) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	in := map[unit.Unit]any{}
	for _, u := range units {
		in[u] = p[u]
	}
	return in, nil
}

func (facetRows) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	return api.Row{Title: "row", Facets: in.(map[string][]string), Views: []string{}}, nil
}

// withFacetRows serves rows over r's store and gives its tasks' views their facts and briefs.
func (r *tasksRig) withFacetRows(rows facetRows) {
	m := readmodel.New(r.log, nil, nil, nil, nil, rows)
	m.UseClock(r.s.deps.Now)
	r.s.modelOnce.Do(func() { r.s.model = m })
	r.s.deps.Row = catalog.RowOf(bundledLoaded, r.facts)
	r.s.deps.TaskRecords = r.db
}

var (
	generalThread = unit.Unit{Provider: "slack", Key: "T1/C1/1700000000.000100"}
	apiPR         = unit.Unit{Provider: unit.GitHub, Key: "acme/api#12"}
)

func facetsOf(t *testing.T, p api.TasksPage, id string) map[string][]string {
	t.Helper()
	for _, v := range p.Tasks {
		if v.ID == id {
			return v.Facets
		}
	}
	t.Fatalf("no task %s on the page %v", id, taskIDs(p.Tasks))
	return nil
}

func TestTaskViewsCarryTheirFacets(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.withFacetRows(facetRows{
		generalThread: {"where": {"general"}, "waits-on": {"me"}},
		apiPR:         {"where": {"acme/api"}, "ci": {"failure"}, "kind": {"bug"}, "risk": {"high"}},
	})
	if err := r.db.PutRecord(context.Background(), apiPR, "brief", task.WriterAgent, []byte(`{"goal":"Fix it","risk":"low","kind":"bugfix"}`), tasksNow); err != nil {
		t.Fatal(err)
	}
	r.put(t, task.Task{ID: "t-slack", State: "inbox", Category: task.CategoryInbox, Title: "Answer in general", Units: []unit.Unit{generalThread}},
		task.Task{ID: "t-pr", State: "inbox", Category: task.CategoryInbox, Title: "Review the fix", Units: []unit.Unit{apiPR}})
	p := r.page(t, "view=inbox")
	slack := facetsOf(t, p, "t-slack")
	for id, want := range map[string][]string{"source": {"slack"}, "where": {"general"}, "waits-on": {"me"}, "category": {"inbox"}} {
		if got := slack[id]; !reflect.DeepEqual(got, want) {
			t.Errorf("the Slack task's %s is %v, want %v (facets %v)", id, got, want, slack)
		}
	}
	gh := facetsOf(t, p, "t-pr")
	for id, want := range map[string][]string{"source": {"github"}, "where": {"acme/api"}, "ci": {"failure"}, "kind": {"bug"}, "risk": {"high"}, "type": {"bugfix"}} {
		if got := gh[id]; !reflect.DeepEqual(got, want) {
			t.Errorf("the pull request task's %s is %v, want %v (facets %v)", id, got, want, gh)
		}
	}
}

func TestATaskWithNoUnitHasOnlyItsOwnFacets(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.withFacetRows(facetRows{generalThread: {"where": {"general"}}})
	e := task.Task{ID: "t-own", State: "inbox", Category: task.CategoryInbox, Title: "Think it over", Created: tasksNow, Updated: tasksNow, Origin: task.OriginUser}
	r.put(t, e)
	f, err := r.facts(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	defs := bundledLoaded().Views.Facets
	want := catalog.MergeFacets(defs, catalog.FactFacets(defs, f), nil)
	got := facetsOf(t, r.page(t, "view=inbox"), "t-own")
	if len(got) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("facets %v, want its facts' %v", got, want)
	}
	if _, ok := got["where"]; ok {
		t.Fatalf("a task with no unit has a where: %v", got)
	}
}

func TestATaskWithTwoUnitsUnionsTheirRows(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.withFacetRows(facetRows{generalThread: {"where": {"general"}}, apiPR: {"where": {"acme/api"}, "ci": {"failure"}}})
	r.put(t, task.Task{ID: "t-both", State: "inbox", Category: task.CategoryInbox, Title: "Ship it", Units: []unit.Unit{generalThread, apiPR}})
	got := facetsOf(t, r.page(t, "view=inbox"), "t-both")
	if want := []string{"general", "acme/api"}; !reflect.DeepEqual(got["where"], want) {
		t.Errorf("where %v, want %v", got["where"], want)
	}
	if want := []string{"failure"}; !reflect.DeepEqual(got["ci"], want) {
		t.Errorf("ci %v, want the linked pull request's %v", got["ci"], want)
	}
}

func manyTasks(t testing.TB, r *tasksRig, n int) facetRows {
	rows := facetRows{}
	var ts []task.Task
	for i := range n {
		pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#" + strconv.Itoa(i+1)}
		rows[pr] = map[string][]string{"where": {"acme/api"}}
		ts = append(ts, task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), State: "next", Category: task.CategoryReady, Title: "Review " + pr.Key, Units: []unit.Unit{pr}})
	}
	r.put(t, ts...)
	return rows
}

func TestAListPageReadsTheRowsOnce(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	const n = 300
	r.withFacetRows(manyTasks(t, r, n))
	var reads atomic.Int32
	r.s.readRows = func(ctx context.Context, units []unit.Unit) (map[unit.Unit]readmodel.Read, error) {
		reads.Add(1)
		got, _, _, err := r.s.readModel().ReadMany(ctx, units)
		return got, err
	}
	p := r.page(t, "view=needs-me")
	if len(p.Tasks) != n {
		t.Fatalf("%d tasks on the page", len(p.Tasks))
	}
	for _, v := range p.Tasks {
		if !reflect.DeepEqual(v.Facets["where"], []string{"acme/api"}) {
			t.Fatalf("%s: where %v", v.ID, v.Facets["where"])
		}
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("%d row reads for a page of %d tasks, want 1", got, n)
	}
}

func TestAListPageComputesEachTasksFactsOnce(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	const n = 40
	r.withFacetRows(manyTasks(t, r, n))
	r.factReads.Store(0)
	p := r.page(t, "view=needs-me")
	if len(p.Tasks) != n {
		t.Fatalf("%d tasks on the page", len(p.Tasks))
	}
	if got := r.factReads.Load(); got != n {
		t.Fatalf("%d facts reads for a page of %d tasks, want %d", got, n, n)
	}
}

// profileOnce is the bundled profile loaded once, so a benchmark times what serves a page, not the load.
func profileOnce() func() *intentload.Loaded {
	l := bundledLoaded()
	return func() *intentload.Loaded { return l }
}

func BenchmarkTasksPage(b *testing.B) {
	loaded := profileOnce()
	r := newTasksRig(b, func(d *Deps) { d.Intents = loaded })
	r.s.deps.Row = catalog.RowOf(loaded, lensFacts(r.db, loaded, tasksNow))
	ctx := context.Background()
	const n = 300
	var ts []task.Task
	for i := range n {
		pr := unit.Unit{Provider: unit.GitHub, Key: "acme/api#" + strconv.Itoa(i+1)}
		ts = append(ts, task.Task{ID: task.ID(fmt.Sprintf("t-%016x", i+1)), State: "next", Category: task.CategoryReady, Title: "Review " + pr.Key, Units: []unit.Unit{pr}})
		if i%2 == 0 {
			if err := r.db.PutRecord(ctx, pr, "brief", task.WriterAgent, []byte(`{"goal":"g`+strconv.Itoa(i+1)+`","kind":"feature"}`), tasksNow); err != nil {
				b.Fatal(err)
			}
		}
	}
	r.put(b, ts...)
	h := r.s.Handler()
	for b.Loop() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, apiRequest("GET", api.TasksPath+"?view=needs-me", nil))
		if rec.Code != 200 {
			b.Fatalf("%d: %s", rec.Code, rec.Body)
		}
	}
}

// BenchmarkPublishTasks times the tasks feed's publish of a burst of row changes over n stored tasks, half
// of the burst's units held by a task.
func BenchmarkPublishTasks(b *testing.B) {
	loaded := profileOnce()
	r := newTasksRig(b, func(d *Deps) { d.Intents = loaded })
	ctx := context.Background()
	const n, burst = 300, 40
	r.withFacetRows(manyTasks(b, r, n))
	var units []unit.Unit
	for i := range burst {
		units = append(units, unit.Unit{Provider: unit.GitHub, Key: "acme/api#" + strconv.Itoa(i+1)})
		units = append(units, unit.Unit{Provider: unit.GitHub, Key: "acme/web#" + strconv.Itoa(i+1)})
	}
	r.s.moved.attach()
	for b.Loop() {
		r.s.moved.add(api.Change{Units: units})
		r.s.publishTasks(ctx, port.Change{})
	}
}

func TestCountersHonorQualifiers(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	rows := facetRows{}
	var ts []task.Task
	for i, where := range []string{"general", "general", "random"} {
		u := unit.Unit{Provider: "slack", Key: fmt.Sprintf("T1/C%d/1700000000.000100", i)}
		rows[u] = map[string][]string{"where": {where}}
		ts = append(ts, task.Task{ID: task.ID(fmt.Sprintf("t-%d", i+1)), State: "inbox", Category: task.CategoryInbox, Title: "Answer", Units: []unit.Unit{u}})
	}
	r.withFacetRows(rows)
	r.put(t, ts...)
	for q, want := range map[string]int{"": 3, "where:general": 2, "-where:general": 1, "where:general where:random": 3, "where:nowhere": 0, "answer where:random": 1} {
		var c api.Counters
		if code := get(t, r.s, api.CountersPath+"?"+url.Values{api.CountersQuery: {q}}.Encode(), &c); code != 200 {
			t.Fatalf("%q: %d", q, code)
		}
		if got := c.Views["inbox"]; got != want {
			t.Errorf("q=%q counts %d in the inbox, want %d", q, got, want)
		}
	}
}

func TestAFilteredCountReadsEachTasksFactsAndRowsOnce(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	const n = 40
	r.withFacetRows(manyTasks(t, r, n))
	var reads atomic.Int32
	r.s.readRows = func(ctx context.Context, units []unit.Unit) (map[unit.Unit]readmodel.Read, error) {
		reads.Add(1)
		got, _, _, err := r.s.readModel().ReadMany(ctx, units)
		return got, err
	}
	r.factReads.Store(0)
	var c api.Counters
	if code := get(t, r.s, api.CountersPath+"?"+url.Values{api.CountersQuery: {"where:acme/api"}}.Encode(), &c); code != 200 {
		t.Fatalf("%d", code)
	}
	if c.Views["needs-me"] != n {
		t.Fatalf("counts %v, want %d in needs-me", c.Views, n)
	}
	if got := r.factReads.Load(); got != n {
		t.Errorf("%d facts reads for %d tasks, want %d", got, n, n)
	}
	if got := reads.Load(); got != 1 {
		t.Errorf("%d row reads for %d tasks, want 1", got, n)
	}
}
