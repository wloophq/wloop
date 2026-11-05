package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unsafe"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/wake"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/change"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/wakes"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/readmodel"
)

// tasksNow is newServerOn's now.
var (
	tasksNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	meID     = task.Me{Keys: []string{"github:me"}}
	r7u      = pr.Ref(r7).Unit()
)

type tasksRig struct {
	s      *Server
	db     *sqlite.DB
	log    *change.Log
	g      *coretasks.Service
	ledger *coreitems.Ledger
	wakes  *wakes.Service
	// facts are the tasks' facts the views read, factReads how many times they were computed.
	facts     func(context.Context, task.Task) (workflow.Facts, error)
	factReads atomic.Int32
	// linked reads the read model's link state of a unit for the facts when set.
	linked bool
}

func newTasksRig(t testing.TB, edit func(*Deps)) *tasksRig {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &tasksRig{db: db, log: log, ledger: &coreitems.Ledger{Store: db.Items(), Clock: fixedClock(tasksNow)}}
	r.s = newServerOn(t, db, log, func(d *Deps) {
		d.Items, d.Cards = r.ledger, db.Cards()
		d.Cfg.Tasks.Areas, d.Cfg.Tasks.Contexts = []string{"OSS", "Hiring"}, []string{"@decide", "@reply"}
		if edit != nil {
			edit(d)
		}
		enabled := d.PluginEnabled
		r.ledger.Enabled = enabled
		r.wakes = wakes.New(wakes.Deps{Store: db.Wakes(), Tasks: db.Tasks(), Clock: fixedClock(tasksNow)})
		loaded := bundledLoaded
		if d.Intents != nil {
			loaded = func() *intentload.Loaded { return cmp.Or(d.Intents(), bundledLoaded()) }
		}
		r.facts = countedLensFacts(db, loaded, tasksNow, &r.factReads, func(ctx context.Context, u unit.Unit) (facttask.Link, error) {
			if !r.linked {
				return facttask.Link{}, nil
			}
			signals, moved, _, err := r.s.LinkState(ctx, u)
			return facttask.Link{Signals: signals, Moved: moved}, err
		})
		r.g = coretasks.New(withLens(coretasks.Deps{States: loadedHandlers{taskstest.BundledStates, loaded}, Authority: taskstest.BundledStates.Authority, Store: db.Tasks(), Items: r.ledger,
			Facts: coretasks.CardFacts{Cards: db.Cards(), Enabled: enabled, Me: func() task.Me { return meID }},
			Me:    meID, Clock: fixedClock(tasksNow), Disabled: disabledOf(enabled), Wakes: db.Wakes(),
			Activity: func(ctx context.Context, id task.ID) error { return r.wakes.Event(ctx, id, wake.OnActivity, "") }}, loaded, r.facts))
		d.Tasks, d.Wakes, d.TaskUnits = r.g, r.wakes, SyncUnits(db.Tasks(), r.ledger)
	})
	stopOnCleanup(t, r.s)
	return r
}

// loadedHandlers are the bundled states with the handlers of the loaded profile, which a test's tasks.toml may
// give another verb.
type loadedHandlers struct {
	coretasks.States
	loaded func() *intentload.Loaded
}

func (l loadedHandlers) Handler(verb string) string { return l.loaded().Tasks.Handler(verb) }

func disabledOf(enabled func(unit.ProviderID) bool) func() []unit.ProviderID {
	return func() []unit.ProviderID {
		var out []unit.ProviderID
		for _, p := range []unit.ProviderID{unit.GitHub, "slack"} {
			if enabled != nil && !enabled(p) {
				out = append(out, p)
			}
		}
		return out
	}
}

func (r *tasksRig) put(t testing.TB, es ...task.Task) {
	t.Helper()
	var cs []task.Change
	for _, e := range es {
		if e.Created.IsZero() {
			e.Created, e.Updated = tasksNow, tasksNow
		}
		if e.Origin == "" {
			e.Origin = task.OriginUser
		}
		cs = append(cs, task.Change{Op: task.OpCreate, Task: e})
	}
	if err := r.db.Tasks().Apply(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
}

// sleep puts wake w on its task.
func (r *tasksRig) sleep(t *testing.T, w wake.Wake) {
	t.Helper()
	if err := r.db.Wakes().Put(context.Background(), w); err != nil {
		t.Fatal(err)
	}
}

func (r *tasksRig) page(t *testing.T, query string) api.TasksPage {
	t.Helper()
	var p api.TasksPage
	if code := get(t, r.s, api.TasksPath+"?"+query, &p); code != 200 {
		t.Fatalf("GET %s: %d", query, code)
	}
	return p
}

func taskIDs(vs []api.TaskView) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mixedTasks() []task.Task {
	return []task.Task{
		{ID: "t-1", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}, "contexts": {List: []string{"@decide"}}}},
		{ID: "t-2", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "Hiring"}}},
		{ID: "t-3", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}, "contexts": {List: []string{"@reply"}}}, Created: tasksNow.Add(-time.Hour)},
		{ID: "t-4", State: "inbox", Category: task.CategoryInbox, Props: task.Props{"area": {Text: "Hiring"}}},
		{ID: "t-p", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}, task.PropKind: {Text: task.KindProject}}, Sequential: true, Title: "Launch"},
		{ID: "t-5", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}}, Parent: "t-p", Position: 1},
		{ID: "t-6", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}}, Parent: "t-p", Position: 2},
		{ID: "t-7", State: "someday", Category: task.CategoryDeferred, Props: task.Props{"area": {Text: "OSS"}}},
	}
}

func TestTasksPageMatchesTheQuery(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, mixedTasks()...)
	ctx := context.Background()
	for _, q := range []task.Query{{View: "needs-me", Area: "OSS"}, {View: "needs-me"}, {View: "inbox", Area: "Hiring"},
		{View: "needs-me", Area: "OSS", Contexts: []string{"@reply"}}, {View: "projects"}, {View: "someday", Area: "OSS"}} {
		v := url.Values{"view": {string(q.View)}, "area": {q.Area}, "context": {strings.Join(q.Contexts, ",")}}
		p := r.page(t, v.Encode())
		want, err := r.g.Tasks(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs := []string{}
		for _, e := range want {
			wantIDs = append(wantIDs, string(e.ID))
		}
		if !p.Full || !slices.Equal(taskIDs(p.Tasks), wantIDs) || len(wantIDs) == 0 {
			t.Errorf("%+v: page %v (full %v), the service's %v", q, taskIDs(p.Tasks), p.Full, wantIDs)
		}
		c, err := r.g.Counts(ctx, q.Area)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := p.Counters, wireCounters(c); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%+v: counters %+v, want %+v", q, got, want)
		}
	}
	p := r.page(t, "view=needs-me&area=OSS")
	if i := slices.Index(taskIDs(p.Tasks), "t-5"); i < 0 || p.Tasks[i].Parent != "t-p" || len(p.Tasks[i].Verbs) == 0 {
		t.Fatalf("project action %+v", p.Tasks)
	}
	if code := get(t, r.s, api.TasksPath+"?view=bogus", nil); code != 400 {
		t.Fatalf("an unknown view: %d", code)
	}
}

func TestTasksSinceIsIncremental(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, mixedTasks()...)
	inbox := r.page(t, "view=inbox")
	needs := r.page(t, "view=needs-me")
	if rec := post(t, r.s, api.TaskPath("t-4", "next"), `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	since := func(p api.TasksPage) string { return api.Since{Epoch: p.Epoch, Rev: p.Rev}.String() }
	got := r.page(t, "view=inbox&since="+url.QueryEscape(since(inbox)))
	if got.Full || len(got.Tasks) != 0 || !slices.Equal(got.Removed, []string{"t-4"}) || got.Rev != inbox.Rev+1 {
		t.Fatalf("inbox since: %+v", got)
	}
	got = r.page(t, "view=needs-me&since="+url.QueryEscape(since(needs)))
	if !got.Full || !slices.Contains(taskIDs(got.Tasks), "t-4") || len(got.Tasks) != len(needs.Tasks)+1 {
		t.Fatalf("needs me since, a view over who owes a task: full %v tasks %v", got.Full, taskIDs(got.Tasks))
	}
	if got := r.page(t, "view=inbox&since=other:1"); !got.Full {
		t.Fatal("another epoch is not Full")
	}
}

// subscribe follows the hub and the tasks feed until the test ends.
func (r *tasksRig) subscribe(t *testing.T) *subscriber {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { r.s.feedTasks(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	sub := r.s.hub.subscribe("test")
	t.Cleanup(func() { r.s.hub.unsubscribe(sub) })
	waitFeedListening(t, r.log, sub)
	return sub
}

// waitFeedListening notes changes until the feed publishes one, so a change
// the test makes next cannot land before the feed subscribed to the log.
func waitFeedListening(t *testing.T, log *change.Log, sub *subscriber) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		log.NoteAll(taskInputs)
		retry := time.After(4 * syncQuiet)
		for waiting := true; waiting; {
			select {
			case <-sub.signal:
				if c, ok := sub.take(); ok && c.Counters != nil {
					return
				}
			case <-retry:
				waiting = false
			case <-deadline:
				t.Fatal("the tasks feed never published")
			}
		}
	}
}

// nextChange is the first change of sub that carries counters.
func nextChange(t *testing.T, sub *subscriber) api.Change {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case <-sub.signal:
			if c, ok := sub.take(); ok && c.Counters != nil {
				return c
			}
		case <-timeout:
			t.Fatal("no change with counters")
		}
	}
}

func TestVerbPostMovesAndStreams(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-1", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}})
	sub := r.subscribe(t)
	rec := post(t, r.s, api.TaskPath("t-1", "next"), `{"source":"cockpit"}`)
	var res api.TaskResult
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Task.State != "next" || res.Task.ID != "t-1" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := nextChange(t, sub)
	if c.Type != "changed" || c.Counters.Views["needs-me"] != 1 || c.Counters.Views["inbox"] != 0 || !slices.Contains(c.Units, r7u) {
		t.Fatalf("%+v %+v", c, c.Counters)
	}
}

func TestAVerbTheProfileForbidsIsForbidden(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeTasksError(rec, fmt.Errorf("%w: agent may not run done on this task", coretasks.ErrForbidden))
	if rec.Code != http.StatusForbidden {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestVerbErrors(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-1", State: "inbox", Category: task.CategoryInbox})
	rec := post(t, r.s, api.TaskPath("t-1", "bogus"), `{}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "next, waiting, schedule") {
		t.Fatalf("unknown verb: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("t-none", "next"), `{}`); rec.Code != 404 {
		t.Fatalf("unknown id: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("t-1", "schedule"), `{}`); rec.Code != 400 {
		t.Fatalf("a missing argument: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("t-1", "inbox"), `{}`); rec.Code != 400 {
		t.Fatalf("a state for a verb: %d %s", rec.Code, rec.Body)
	}
	post(t, r.s, api.TaskPath("t-1", "done"), `{}`)
	if rec := post(t, r.s, api.TaskPath("t-1", "next"), `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "done") {
		t.Fatalf("a refused state: %d %s", rec.Code, rec.Body)
	}
}

func TestCaptureLandsInInbox(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	rec := post(t, r.s, api.TasksPath, `{"title":"call the bank"}`)
	var v api.TaskView
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if v.State != "inbox" || v.Title != "call the bank" {
		t.Fatalf("%+v", v)
	}
	if p := r.page(t, "view=inbox"); !slices.Equal(taskIDs(p.Tasks), []string{v.ID}) || p.Counters.Badge != 1 {
		t.Fatalf("%+v", p)
	}
	var detail api.TaskView
	if code := get(t, r.s, api.TaskPath(v.ID), &detail); code != 200 || detail.ID != v.ID {
		t.Fatalf("detail %d %+v", code, detail)
	}
	if rec := post(t, r.s, api.TasksPath, `{"title":"  "}`); rec.Code != 400 {
		t.Fatalf("an empty title: %d %s", rec.Code, rec.Body)
	}
}

func TestCatalogServesViewsAndVerbs(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	var cat api.ViewsCatalog
	if code := get(t, r.s, api.ViewsPath, &cat); code != 200 {
		t.Fatalf("%d", code)
	}
	var views []string
	for _, v := range cat.Views {
		views = append(views, v.ID)
	}
	if want := bundledLoaded().Views.IDs(); !slices.Equal(views, want) {
		t.Fatalf("views %v", views)
	}
	if !slices.Equal(cat.Areas, []string{"OSS", "Hiring"}) || !slices.Equal(cat.Contexts, []string{"@decide", "@reply"}) {
		t.Fatalf("areas %v contexts %v", cat.Areas, cat.Contexts)
	}
	keyOf := map[string]string{}
	for _, v := range cat.Verbs {
		keyOf[v.Verb] = v.Key
	}
	for verb, key := range map[string]string{"next": "x", "waiting": "w", "schedule": "s", "someday": "y", "reference": "l", "focus": "f",
		"project": "p", "approve": "a", "delegate": "d", "done": "e", "dismiss": "Backspace", "seen": ""} {
		if k, ok := keyOf[verb]; !ok || k != key {
			t.Errorf("%s: key %q (%v), want %q", verb, k, ok, key)
		}
	}
}

func TestTheCatalogIsWordedByTheProfile(t *testing.T) {
	t.Parallel()
	views := intentload.ViewsDef{Views: []intentload.ViewDef{{ID: "needs-me", Label: "Now", Over: task.OverTasks}, {ID: "inbox", Label: "Inbox", Over: task.OverTasks}}}
	tasks := intentload.TasksDef{Verbs: map[string]intentload.VerbText{"done": {Label: "Finish", Summary: "Close it"}, "waiting": {Label: "Waiting", Summary: "Wait", Input: "On whom?"}}}
	r := newTasksRig(t, func(d *Deps) {
		d.Intents = func() *intentload.Loaded {
			return &intentload.Loaded{Views: views, Tasks: tasks, Registry: dintent.NewRegistry(nil)}
		}
	})
	var cat api.ViewsCatalog
	if code := get(t, r.s, api.ViewsPath, &cat); code != 200 {
		t.Fatalf("%d", code)
	}
	verbs := map[string]api.VerbSpec{}
	for _, v := range cat.Verbs {
		verbs[v.Verb] = v
	}
	if got := verbs["done"]; got.Label != "Finish" || got.Summary != "Close it" {
		t.Errorf("done: %+v", got)
	}
	if got := verbs["waiting"]; got.Input != "On whom?" {
		t.Errorf("waiting: %+v", got)
	}
	if got := verbs["someday"]; got.Label != "Someday" || got.Summary == "" {
		t.Errorf("a verb the profile leaves out takes the bundled words: %+v", got)
	}
	labels := map[string]string{}
	for _, v := range cat.Views {
		labels[v.ID] = v.Label
	}
	if labels["needs-me"] != "Now" || labels["inbox"] != "Inbox" {
		t.Errorf("views %v", labels)
	}
}

func TestCountersShipWithChanges(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, mixedTasks()...)
	sub := r.subscribe(t)
	if rec := post(t, r.s, api.TaskPath("t-4", "someday"), `{}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := nextChange(t, sub)
	var counters api.Counters
	if code := get(t, r.s, api.CountersPath, &counters); code != 200 {
		t.Fatalf("%d", code)
	}
	if c.Counters.Views["inbox"] != 0 || c.Counters.Views["someday"] != 2 || fmt.Sprint(*c.Counters) != fmt.Sprint(counters) {
		t.Fatalf("change %+v, counters %+v", *c.Counters, counters)
	}
	var page api.RowsPage
	if code := get(t, r.s, api.Rows+"?view=all", &page); code != 200 || page.Counters == nil || fmt.Sprint(*page.Counters) != fmt.Sprint(counters) {
		t.Fatalf("rows page counters %+v, want %+v", page.Counters, counters)
	}
	var oss api.Counters
	if get(t, r.s, api.CountersPath+"?area=OSS", &oss); oss.Area != "OSS" || oss.Views["needs-me"] != 5 || oss.Areas["Hiring"] != 2 {
		t.Fatalf("OSS counters %+v", oss)
	}
}

// startSync runs the Sync worker until the test ends.
func (r *tasksRig) startSync(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { r.s.syncTasks(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
}

func owedToMe(u unit.Unit, id string) items.Op {
	return items.Op{Op: items.OpCreate, Unit: u, Item: id, Anchor: unit.Anchor{Ref: "user:" + id}, Fields: &items.Fields{Kind: ptrOf(items.Todo),
		Summary: ptrOf("review " + id), WaitingOn: &[]items.Person{{Key: "github:me", Name: "me"}}}}
}

func TestItemsNoteSyncsTasks(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	sub := r.subscribe(t)
	r.startSync(t)
	if _, err := r.ledger.Apply(context.Background(), items.User, []items.Op{owedToMe(r7u, "i1")}); err != nil {
		t.Fatal(err)
	}
	c := nextChange(t, sub)
	for !slices.Contains(c.Units, r7u) {
		c = nextChange(t, sub)
	}
	if c.Counters.Views["inbox"] != 1 {
		t.Fatalf("%+v %+v", c, c.Counters)
	}
	es, err := r.db.Tasks().TasksOf(context.Background(), r7u)
	if err != nil || len(es) != 1 || es[0].State != task.State("inbox") || es[0].Origin != task.OriginDerived {
		t.Fatalf("%+v %v", es, err)
	}
}

func TestItemsNotedBeforeTheSyncStartsSyncTasks(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	sub := r.subscribe(t)
	if _, err := r.ledger.Apply(context.Background(), items.User, []items.Op{owedToMe(r7u, "i1")}); err != nil {
		t.Fatal(err)
	}
	r.startSync(t)
	c := nextChange(t, sub)
	for !slices.Contains(c.Units, r7u) {
		c = nextChange(t, sub)
	}
	if es, err := r.db.Tasks().TasksOf(context.Background(), r7u); err != nil || len(es) != 1 {
		t.Fatalf("%+v %v", es, err)
	}
}

func TestSourceActivityReturnsASnoozedTaskWhereItWasNotToTheInbox(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newTasksRig(t, nil)
		ctx := context.Background()
		r.put(t, task.Task{ID: "t-s", State: "scheduled", Category: task.CategoryDeferred, Units: []unit.Unit{r7u},
			SeenAt: tasksNow.Add(-time.Hour), Created: tasksNow.Add(-2 * time.Hour), Updated: tasksNow.Add(-2 * time.Hour)})
		r.sleep(t, wake.Wake{Task: "t-s", On: []string{wake.OnActivity}, Return: "next"})
		r.startSync(t)
		r.log.Note(port.TopicSourceEvents, r7u)
		time.Sleep(syncMaxDelay)
		synctest.Wait()
		if p := r.page(t, "view=inbox"); len(p.Tasks) != 0 {
			t.Fatalf("the source event moved the task before its card: %+v", p.Tasks)
		}
		ana := items.Person{Key: "github:ana", Name: "ana"}
		c := card.Card{Unit: r7u, Title: "Batch export", State: "open", LastHuman: &card.Human{Who: ana, What: "commented", At: tasksNow.Add(-time.Minute)}}
		if _, err := r.db.Cards().Save(ctx, c, tasksNow); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "the task's wake matched", func() bool {
			w, ok, err := r.db.Wakes().Of(ctx, "t-s")
			return err == nil && ok && len(w.On) == 0 && w.Return == "next"
		})
		if _, err := r.g.Do(ctx, "t-s", coretasks.Request{Actor: task.ActorWorkflow, Source: "task-wake", Verb: task.VerbWake}); err != nil {
			t.Fatal(err)
		}
		if p := r.page(t, "view=needs-me"); len(p.Tasks) != 1 || p.Tasks[0].ID != "t-s" || !p.Tasks[0].Changed {
			t.Fatalf("the woken task is not back in Next: %+v", p.Tasks)
		}
		if p := r.page(t, "view=inbox"); len(p.Tasks) != 0 {
			t.Fatalf("activity brought a clarified task to the Inbox: %+v", p.Tasks)
		}
	})
}

// blockingItems holds every read until release is closed.
type blockingItems struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingItems) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil, nil
}

func TestSyncRunsOffTheSubscriber(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	b := &blockingItems{entered: make(chan struct{}), release: make(chan struct{})}
	r.s.deps.Tasks = coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: taskstest.New(), Items: b, Facts: coretasks.CardFacts{}, Clock: fixedClock(tasksNow)})
	r.startSync(t)
	defer close(b.release)
	noted := make(chan struct{})
	go func() {
		r.log.Note(port.TopicItems, r7u)
		close(noted)
	}()
	select {
	case <-b.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("Sync never read the noted unit's items")
	}
	select {
	case <-noted:
	case <-time.After(5 * time.Second):
		t.Fatal("the note waits on Sync")
	}
}

// bridgeRig is the catalog rig serving the tasks of a fake store, with task t-r on acme/api#7.
func bridgeRig(t *testing.T) (catalogRig, *coretasks.Service) {
	t.Helper()
	c := newCatalogRig(t)
	store := taskstest.New()
	g := coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: store, Facts: coretasks.CardFacts{}, Clock: fixedClock(catalogNow)})
	c.s.deps.Tasks = g
	e := task.Task{ID: "t-r", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}, Origin: task.OriginDerived, Created: catalogNow, Updated: catalogNow}
	if err := store.Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	return c, g
}

func TestApproveRunsThePendingRound(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	c.propose(t)
	rec := post(t, c.s, api.TaskPath("t-r", "approve"), `{"source":"cockpit"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "sent the review-follow-up round") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(c.plan.acted, []workflow.Action{workflow.ActionApply}) || c.plan.isPending(r7u) {
		t.Fatalf("acted %v", c.plan.acted)
	}
	direct := newCatalogRig(t)
	direct.propose(t)
	row, _ := direct.s.currentRow(context.Background(), r7u)
	st, _ := offerOf(row, "run_now")
	if rec := post(t, direct.s, "/api/units/github/acme%2Fapi%237/run_now", fmt.Sprintf(`{"source":"cockpit","basis":%q}`, st.Basis)); rec.Code != 200 ||
		!slices.Equal(direct.plan.acted, c.plan.acted) {
		t.Fatalf("the unit route: %d %s, acted %v", rec.Code, rec.Body, direct.plan.acted)
	}
}

func TestDelegateRunsTheIntent(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	rec := post(t, c.s, api.TaskPath("t-r", "delegate"), `{"source":"cockpit"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "review accepted as intake") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	open, err := c.s.deps.Intakes.Open(context.Background())
	if err != nil || len(open) != 1 || open[0].Unit != r7u || string(open[0].Intent) != "review" {
		t.Fatalf("intakes %+v %v", open, err)
	}
}

func TestBridgeRefusesWhatTheRowDoesNotOffer(t *testing.T) {
	t.Parallel()
	c, g := bridgeRig(t)
	fa := &fakeActions{}
	c.s.deps.Actions = fa
	rec := post(t, c.s, api.TaskPath("t-r", "approve"), `{"source":"cockpit"}`)
	row, _ := c.s.currentRow(context.Background(), r7u)
	want := c.s.offeredFor(row, task.VerbApprove).reason()
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), want) || want == "" {
		t.Fatalf("%d %s, want %q", rec.Code, rec.Body, want)
	}
	if len(fa.calls) != 0 || len(c.plan.acted) != 0 {
		t.Fatalf("dispatched: %v %v", fa.calls, c.plan.acted)
	}
	if e, _ := g.Task(context.Background(), "t-r"); e.State != task.State("inbox") {
		t.Fatalf("the task moved: %+v", e)
	}
	if rec := post(t, c.s, api.TaskPath("t-r", "dismiss"), `{}`); rec.Code != 200 || len(fa.calls) != 0 {
		t.Fatalf("dismiss with no question gate to dismiss: %d %s %v", rec.Code, rec.Body, fa.calls)
	}
}

func TestBridgeRunsOnlyForActionTasks(t *testing.T) {
	t.Parallel()
	c, g := bridgeRig(t)
	c.propose(t)
	capture, err := g.Capture(context.Background(), "ask ana about the export", "", &r7u)
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(t, c.s, api.TaskPath(string(capture.ID), "approve"), `{"source":"cockpit"}`); rec.Code != 409 {
		t.Fatalf("approve on a capture: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, c.s, api.TaskPath(string(capture.ID), "dismiss"), `{}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"trash"`) {
		t.Fatalf("dismiss on a capture: %d %s", rec.Code, rec.Body)
	}
	if len(c.plan.acted) != 0 || !c.plan.isPending(r7u) {
		t.Fatalf("a capture acted on the round: %v", c.plan.acted)
	}
}

func TestBridgeRefusesADisabledPlugin(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	c.propose(t)
	c.s.deps.PluginEnabled = func(p unit.ProviderID) bool { return p != unit.GitHub }
	for _, verb := range []string{"approve", "next"} {
		rec := post(t, c.s, api.TaskPath("t-r", verb), `{"source":"cockpit"}`)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"plugin":"github"`) {
			t.Fatalf("%s: %d %s", verb, rec.Code, rec.Body)
		}
	}
	var v api.TaskView
	if code := get(t, c.s, api.TaskPath("t-r"), &v); code != 200 || slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Enabled != (s.Verb == "dismiss") }) {
		t.Fatalf("%d %+v", code, v.Verbs)
	}
	if rec := post(t, c.s, api.TaskPath("t-r", "dismiss"), `{"source":"cockpit"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"trash"`) {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	if len(c.plan.acted) != 0 {
		t.Fatalf("dispatched %v", c.plan.acted)
	}
}

func TestRegisteredHandlerWins(t *testing.T) {
	t.Parallel()
	c, g := bridgeRig(t)
	c.propose(t)
	var got []string
	g.Register(coretasks.EveryKind, string(task.VerbApprove), func(_ context.Context, e task.Task, req coretasks.Request) (coretasks.Result, error) {
		got = append(got, string(e.ID)+" "+req.Source)
		return coretasks.Result{Task: e, Lines: []string{"handled"}}, nil
	})
	rec := post(t, c.s, api.TaskPath("t-r", "approve"), `{"source":"tui"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "handled") || !slices.Equal(got, []string{"t-r tui"}) {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, got)
	}
	if len(c.plan.acted) != 0 || !c.plan.isPending(r7u) {
		t.Fatalf("the bridge ran too: %v", c.plan.acted)
	}
}

func TestBridgePicksOneActionPerVerb(t *testing.T) {
	t.Parallel()
	user := func(id, verb string) api.ActionSpec {
		return api.ActionSpec{ID: id, Verb: verb, Source: "/home/u/intents/" + id + "/intent.toml", Plugin: "github"}
	}
	bundled := func(id, verb, plugin string) api.ActionSpec {
		return api.ActionSpec{ID: id, Verb: verb, Source: "intents/" + id + "/intent.toml", Plugin: plugin}
	}
	specs := []api.ActionSpec{{ID: "run_now", Verb: "approve", Source: "builtin"}, user("fast-approve", "approve"),
		user("deep-check", "delegate"), bundled("act", "delegate", ""), bundled("address", "delegate", "github"), bundled("review", "delegate", "github")}
	on := func(id string) api.ActionState { return api.ActionState{ID: id, Enabled: true} }
	for _, c := range []struct {
		name    string
		verb    task.Verb
		actions []api.ActionState
		runs    string
		reason  string
	}{
		{"built-in over user", task.VerbApprove, []api.ActionState{on("fast-approve"), on("run_now")}, "run_now", ""},
		{"user alone", task.VerbApprove, []api.ActionState{on("fast-approve")}, "fast-approve", ""},
		{"user over a bundled intent of the same provider", task.VerbDelegate, []api.ActionState{on("review"), on("deep-check")}, "deep-check", ""},
		{"own provider over any provider", task.VerbDelegate, []api.ActionState{on("act"), on("review")}, "review", ""},
		{"two bundled enabled", task.VerbDelegate, []api.ActionState{on("review"), on("address")}, "", "offers review and address to delegate"},
		{"a disabled user intent hides the bundled one", task.VerbDelegate, []api.ActionState{{ID: "deep-check", Reason: "not now"}, on("review")}, "", "not now"},
		{"the first disabled reason", task.VerbDelegate, []api.ActionState{{ID: "address", Reason: "nothing to address"}, {ID: "review", Reason: "a review runs"}}, "", "nothing to address"},
		{"none offered", task.VerbDelegate, []api.ActionState{on("run_now")}, "", "offers no action to delegate"},
	} {
		b := rankOffered(specs, api.Row{Unit: r7u, Actions: c.actions}, c.verb)
		st, ok := b.runs()
		if ok != (c.runs != "") || st.ID != c.runs || (c.reason != "" && !strings.Contains(b.reason(), c.reason)) {
			t.Errorf("%s: runs %q (%v), reason %q", c.name, st.ID, ok, b.reason())
		}
	}
}

func TestBridgeRanksAProviderIntentOverBundledAct(t *testing.T) {
	t.Parallel()
	slackUnit := unit.Unit{Provider: "slack", Key: "T1/C1/1.1"}
	specs := []api.ActionSpec{
		{ID: "act", Verb: "delegate", Source: "intents/act/intent.toml"},
		{ID: "review", Verb: "delegate", Source: "intents/review/intent.toml", Plugin: "github"},
		{ID: "triage", Verb: "delegate", Source: "/home/u/intents/triage/intent.toml", Plugin: "slack"},
		{ID: "bundled-slack", Verb: "delegate", Source: "intents/bundled-slack/intent.toml", Plugin: "slack"},
		{ID: "mine-any", Verb: "delegate", Source: "/home/u/intents/mine-any/intent.toml"},
	}
	on := func(id string) api.ActionState { return api.ActionState{ID: id, Enabled: true} }
	for _, c := range []struct {
		name    string
		actions []api.ActionState
		runs    string
	}{
		{"act alone", []api.ActionState{on("act")}, "act"},
		{"a user slack intent over act", []api.ActionState{on("act"), on("triage")}, "triage"},
		{"a bundled slack intent over act", []api.ActionState{on("act"), on("bundled-slack")}, "bundled-slack"},
		{"a user slack intent over a bundled slack one", []api.ActionState{on("bundled-slack"), on("triage")}, "triage"},
		{"a user any intent over act", []api.ActionState{on("act"), on("mine-any")}, "mine-any"},
		{"a bundled slack intent over a user any one", []api.ActionState{on("mine-any"), on("bundled-slack")}, "bundled-slack"},
	} {
		st, ok := rankOffered(specs, api.Row{Unit: slackUnit, Actions: c.actions}, task.VerbDelegate).runs()
		if !ok || st.ID != c.runs {
			t.Errorf("%s: runs %q (%v), want %q", c.name, st.ID, ok, c.runs)
		}
	}
}

func TestTaskAnswerRouteMatchesTheUnitAnswer(t *testing.T) {
	t.Parallel()
	body := `{"option_id":"publish","guidance":"say thanks","source":"cockpit"}`
	unitRig := newMessageRig(t)
	unitRig.question(t)
	unitRig.idle()
	rec := post(t, unitRig.s, "/api/units/github/acme%2Fapi%238/answer", body)
	want := unitRig.outcome(rec.Code, actionLines(t, rec.Body.Bytes()))
	r := newMessageRig(t)
	m := r.question(t)
	r.idle()
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath(m.TaskID), &v); code != 200 || !messageIs(v, m.ID) ||
		!slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Verb == "answer" && s.Enabled }) {
		t.Fatalf("%d %+v", code, v)
	}
	rec = post(t, r.s, api.TaskPath(m.TaskID, "answer"), `{"args":{"option_id":"publish","guidance":"say thanks"},"source":"cockpit"}`)
	var res api.TaskResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if got := r.outcome(rec.Code, res.Lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("by task %+v\nby unit %+v", got, want)
	}
	if evs := r.tasks.Events; len(evs) != 1 || evs[0].Kind != "verb_answer" || evs[0].Actor != "person:cockpit" {
		t.Fatalf("events %+v", evs)
	}
}

func TestCountersWithTermsCountWhatTheFilteredListsShow(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, mixedTasks()...)
	for _, area := range []string{"", "OSS"} {
		for _, terms := range [][]string{{"launch"}, {"oss"}, {"OSS", "decide"}, {"nothing-holds-this"}} {
			q := url.Values{api.CountersTerm: terms}
			if area != "" {
				q.Set("area", area)
			}
			var c api.Counters
			if code := get(t, r.s, api.CountersPath+"?"+q.Encode(), &c); code != 200 {
				t.Fatalf("%v: %d", terms, code)
			}
			for _, view := range storedViews() {
				v := url.Values{"view": {view}, "area": {area}}
				n := 0
				for _, e := range r.page(t, v.Encode()).Tasks {
					if api.TaskMatches(e, terms) {
						n++
					}
				}
				if c.Views[view] != n {
					t.Errorf("area %q terms %v: %s counts %d, the filtered list shows %d", area, terms, view, c.Views[view], n)
				}
			}
		}
	}
	var c api.Counters
	if get(t, r.s, api.CountersPath+"?"+url.Values{api.CountersTerm: {"launch"}}.Encode(), &c); c.Areas["OSS"] != 1 || c.Areas["Hiring"] != 0 {
		t.Errorf("areas under a filter: %v", c.Areas)
	}
}

func TestBridgeRelaysARefusalAsJSONOnly(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	c.propose(t)
	rec := post(t, c.s, api.TaskPath("t-r", "approve"), `{"source":"<script>alert(1)</script>"}`)
	if rec.Code != 400 || strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if h := rec.Header(); h.Get("Content-Type") != "application/json" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", h)
	}
}

func TestAVerbTheProfileDoesNotOfferIsNeitherListedNorRun(t *testing.T) {
	t.Parallel()
	offer, err := expr.CompileWorkflow(`task.prop.kind != "capture"`, builtin.OfferEnv())
	if err != nil {
		t.Fatal(err)
	}
	tasks := intentload.TasksDef{Verbs: map[string]intentload.VerbText{"someday": {Offer: offer}}}
	r := newTasksRig(t, func(d *Deps) {
		d.Intents = func() *intentload.Loaded { return &intentload.Loaded{Tasks: tasks, Registry: dintent.NewRegistry(nil)} }
	})
	r.put(t, task.Task{ID: "t-c", State: "inbox", Category: task.CategoryInbox, Title: "c", Origin: task.OriginUser, Props: task.Props{task.PropKind: {Text: "capture"}}})
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("t-c"), &v); code != 200 || slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Verb == "someday" }) {
		t.Fatalf("%d %+v", code, v.Verbs)
	}
	if rec := post(t, r.s, api.TaskPath("t-c", "someday"), `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "not offered") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("t-c", "next"), `{}`); rec.Code != 200 {
		t.Fatalf("a verb the profile leaves alone: %d %s", rec.Code, rec.Body)
	}
}

func TestAnAnsweredGateShowsItsMessagesAnswersAndWhetherTheAgentReadThem(t *testing.T) {
	t.Parallel()
	r := newMessageRig(t)
	m := r.question(t)
	r.idle()
	if rec := post(t, r.s, api.TaskPath(m.TaskID, "answer"), `{"args":{"option_id":"publish"},"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	answers := func() []api.TurnAnswer {
		t.Helper()
		var gs []api.GateView
		if code := get(t, r.s, api.GatesPath+"?status=answered", &gs); code != 200 || len(gs) != 1 {
			t.Fatalf("%d %+v", code, gs)
		}
		var msg api.Message
		if err := json.Unmarshal(gs[0].Subject, &msg); err != nil || msg.ID != m.ID {
			t.Fatalf("%v %s", err, gs[0].Subject)
		}
		return msg.Answers
	}
	if a := answers(); len(a) != 1 || a[0].OptionID != "publish" || a[0].Message != m.ID || a[0].ReadAt != nil {
		t.Fatalf("%+v", a)
	}
	if _, err := r.s.deps.Messages.Read(context.Background(), messages.ReadRequest{Session: "term-1", Unit: r8u.Unit()}); err != nil {
		t.Fatal(err)
	}
	if a := answers(); a[0].ReadAt == nil {
		t.Fatalf("%+v", a)
	}
}

func TestAVerbAWorkflowRunsOnAMessageRecordsItsActor(t *testing.T) {
	t.Parallel()
	r := newMessageRig(t)
	m := r.question(t)
	req := coretasks.Request{Verb: task.VerbDismiss, Source: "stale", Actor: task.ActorWorkflow, Chain: workflow.Chain{}.Then("stale")}
	if _, err := r.s.deps.Tasks.Do(context.Background(), task.ID(m.TaskID), req); err != nil {
		t.Fatal(err)
	}
	if evs := r.tasks.Events; len(evs) != 1 || evs[0].Kind != "verb_dismiss" || evs[0].Actor != "workflow:stale" {
		t.Fatalf("events %+v", evs)
	}
}

// messageIs says whether v serves message id on an open gate.
func messageIs(v api.TaskView, id int64) bool {
	m, _, ok := v.MessageOf()
	return ok && m.ID == id
}

func TestTaskViewServesTheWake(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "t-s", State: "scheduled", Category: task.CategoryDeferred, Units: []unit.Unit{r7u}},
		task.Task{ID: "t-n", State: "next", Category: task.CategoryReady})
	until := tasksNow.Add(48 * time.Hour)
	r.sleep(t, wake.Wake{Task: "t-s", Until: until, When: `size(task.link_owes) != 0`, Return: "next"})
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("t-s"), &v); code != 200 || v.Wake == nil || !v.Wake.Until.Equal(until) ||
		v.Wake.When != `size(task.link_owes) != 0` || v.Wake.Return != "next" {
		t.Fatalf("%d: wake %+v", code, v.Wake)
	}
	var none api.TaskView
	if code := get(t, r.s, api.TaskPath("t-n"), &none); code != 200 || none.Wake != nil {
		t.Fatalf("%d: a task with no wake serves %+v", code, none.Wake)
	}
}

func TestUnitTasksAreTheOpenTasksHoldingTheUnit(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	ref8, ref9 := r7, r7
	ref8.Number, ref9.Number = 8, 9
	other := pr.Ref(ref8).Unit()
	r.put(t,
		task.Task{ID: "t-open", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}},
		task.Task{ID: "t-next", State: "next", Category: task.CategoryReady, Units: []unit.Unit{r7u, other}},
		task.Task{ID: "t-done", State: "done", Category: task.CategoryDone, Units: []unit.Unit{r7u}},
		task.Task{ID: "t-other", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{other}},
		task.Task{ID: "t-item", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{other}, Items: []task.ItemRef{{Unit: r7u, Item: "i1"}}},
	)
	var got []api.TaskView
	if code := get(t, r.s, api.UnitPath(r7u, "tasks"), &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	if ids := taskIDs(got); !slices.Equal(slices.Sorted(slices.Values(ids)), []string{"t-item", "t-next", "t-open"}) {
		t.Fatalf("tasks %v, want t-item, t-next and t-open", ids)
	}
	if i := slices.Index(taskIDs(got), "t-open"); len(got[i].Verbs) == 0 {
		t.Fatalf("a unit's task carries its verbs as a view lists them: %+v", got[i])
	}
	var none []api.TaskView
	if code := get(t, r.s, api.UnitPath(pr.Ref(ref9).Unit(), "tasks"), &none); code != 200 || len(none) != 0 {
		t.Fatalf("a unit no task holds: %d %v", code, none)
	}
}

func TestUnitTasksWithoutATasksServiceAreNone(t *testing.T) {
	t.Parallel()
	s := newNotedServer(t, func(d *Deps) { d.Tasks = nil })
	var got []api.TaskView
	if code := get(t, s, api.UnitPath(r7u, "tasks"), &got); code != 200 || got == nil || len(got) != 0 {
		t.Fatalf("%d %v", code, got)
	}
}

func TestUnitTasksLeaveOutADisabledPluginsTasks(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, func(d *Deps) { d.PluginEnabled = func(p unit.ProviderID) bool { return p != unit.GitHub } })
	r.put(t, task.Task{ID: "t-open", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}})
	var got []api.TaskView
	if code := get(t, r.s, api.UnitPath(r7u, "tasks"), &got); code != 200 || len(got) != 0 {
		t.Fatalf("%d: a disabled plugin's task shows: %v", code, taskIDs(got))
	}
}

func TestUnitTasksListChildrenInTheirSiblingOrder(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t,
		task.Task{ID: "t-second", State: "inbox", Category: task.CategoryInbox, Parent: "t-p", Position: 2, Units: []unit.Unit{r7u},
			Created: tasksNow.Add(-time.Hour), Updated: tasksNow},
		task.Task{ID: "t-first", State: "inbox", Category: task.CategoryInbox, Parent: "t-p", Position: 1, Units: []unit.Unit{r7u}},
	)
	var got []api.TaskView
	if code := get(t, r.s, api.UnitPath(r7u, "tasks"), &got); code != 200 || !slices.Equal(taskIDs(got), []string{"t-first", "t-second"}) {
		t.Fatalf("%d: tasks %v, want t-first then t-second", code, taskIDs(got))
	}
}

func TestTheViewsCatalogServesFacetsAndAVersion(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	var cat api.ViewsCatalog
	if code := get(t, r.s, api.ViewsPath, &cat); code != 200 {
		t.Fatalf("%d", code)
	}
	if len(cat.Facets) != len(bundledLoaded().Views.Facets) || cat.Version == "" {
		t.Fatalf("facets %d, version %q", len(cat.Facets), cat.Version)
	}
	if i := slices.IndexFunc(cat.Facets, func(f api.FacetSpec) bool { return f.ID == "waits-on" }); i < 0 || cat.Facets[i].Match != "team" || cat.Facets[i].Provider != "" {
		t.Errorf("waits-on: %+v", cat.Facets)
	}
	if cat.DefaultSort != "View order" {
		t.Errorf("default sort %q", cat.DefaultSort)
	}

	l := *bundledLoaded()
	l.Views.Facets = slices.Clone(l.Views.Facets)
	l.Views.Facets[0].Label = "Origin"
	other := newTasksRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return &l } })
	var changed api.ViewsCatalog
	get(t, other.s, api.ViewsPath, &changed)
	if changed.Version == "" || changed.Version == cat.Version || changed.Facets[0].Label != "Origin" {
		t.Errorf("version %q after a label change, was %q", changed.Version, cat.Version)
	}
	var again api.ViewsCatalog
	get(t, r.s, api.ViewsPath, &again)
	if again.Version != cat.Version {
		t.Errorf("version %q then %q for the same profile", cat.Version, again.Version)
	}
}

func TestAFacetThatDisagreesWithAPluginIsAHealthWarning(t *testing.T) {
	t.Parallel()
	l := *bundledLoaded()
	l.Views.Facets = slices.Clone(l.Views.Facets)
	i := slices.IndexFunc(l.Views.Facets, func(f intentload.FacetDef) bool { return f.ID == "risk" })
	l.Views.Facets[i].Sort = "text"
	r := newTasksRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return &l } })
	var h api.Health
	if code := get(t, r.s, "/api/health", &h); code != 200 {
		t.Fatalf("%d", code)
	}
	if !slices.ContainsFunc(h.Warnings, func(w string) bool { return strings.HasPrefix(w, "profile: ") && strings.Contains(w, "facets.risk") }) {
		t.Fatalf("warnings %v", h.Warnings)
	}
	var clean api.Health
	get(t, newTasksRig(t, nil).s, "/api/health", &clean)
	if slices.ContainsFunc(clean.Warnings, func(w string) bool { return strings.HasPrefix(w, "profile: ") }) {
		t.Errorf("the bundled profile warns: %v", clean.Warnings)
	}
}

func TestFacetProblemsAreComputedOncePerProfileAndCatalog(t *testing.T) {
	t.Parallel()
	l := *bundledLoaded()
	l.Views.Facets = slices.Clone(l.Views.Facets)
	i := slices.IndexFunc(l.Views.Facets, func(f intentload.FacetDef) bool { return f.ID == "risk" })
	l.Views.Facets[i].Sort = "text"
	r := newTasksRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return &l } })
	first := r.s.facetProblems(&l)
	if len(first) == 0 {
		t.Fatal("no problem")
	}
	l.Views.Facets[i].Sort = "rank"
	if again := r.s.facetProblems(&l); len(again) != len(first) {
		t.Errorf("recomputed for the same profile and catalog: %d then %d", len(first), len(again))
	}
}

// liveRows serves a row with each unit's facets and link signals, which a test moves with set.
type liveRows struct {
	mu   sync.Mutex
	rows map[unit.Unit]liveRow
}

type liveRow struct {
	facets  map[string][]string
	signals map[string]bool
}

func (*liveRows) Provider() unit.ProviderID { return "fake" }

func (p *liveRows) Listed(context.Context) ([]unit.Unit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.SortedFunc(maps.Keys(p.rows), api.CompareUnits), nil
}

func (p *liveRows) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	in := map[unit.Unit]any{}
	for _, u := range units {
		in[u] = p.rows[u]
	}
	return in, nil
}

func (*liveRows) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	r := in.(liveRow)
	return api.Row{Title: "row", Facets: r.facets, Views: []string{}, Summary: api.Summary{Stalled: r.signals["stalled"]}}, nil
}

func (*liveRows) Signals(_ readmodel.Base, _ api.Row, in any, _ time.Time) map[string]bool {
	return in.(liveRow).signals
}

// set gives u's row facets and signals and notes it, as a source's write does.
func (p *liveRows) set(log *change.Log, u unit.Unit, facets map[string][]string, signals map[string]bool) {
	p.mu.Lock()
	p.rows[u] = liveRow{facets: facets, signals: signals}
	p.mu.Unlock()
	log.Note(port.TopicPRs, u)
}

// withLiveRows serves rows over r's store, gives its tasks' views their facts, briefs and link state, and
// returns the rows to move.
func (r *tasksRig) withLiveRows(rows map[unit.Unit]liveRow) *liveRows {
	p := &liveRows{rows: rows}
	m := readmodel.New(r.log, nil, nil, nil, nil, p)
	m.UseClock(r.s.deps.Now)
	r.s.modelOnce.Do(func() { r.s.model = m })
	r.s.deps.Row = catalog.RowOf(bundledLoaded, r.facts)
	r.s.deps.TaskRecords = r.db
	r.linked = true
	return p
}

// follow runs the rows and tasks feeds until the test ends, in a synctest bubble, and returns a subscriber
// to the hub once both wait for changes.
func (r *tasksRig) follow(t *testing.T) *subscriber {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() { cancel(); wg.Wait() })
	wg.Go(func() { r.s.feedRows(ctx) })
	wg.Go(func() { r.s.feedTasks(ctx) })
	synctest.Wait()
	sub := r.s.hub.subscribe("test")
	t.Cleanup(func() { r.s.hub.unsubscribe(sub) })
	return sub
}

// published is what sub was published after d, merged; ok is false when nothing was.
func published(sub *subscriber, d time.Duration) (api.Change, bool) {
	time.Sleep(d)
	synctest.Wait()
	return sub.take()
}

func tasksSince(p api.TasksPage) string { return api.Since{Epoch: p.Epoch, Rev: p.Rev}.String() }

func TestARowChangeResendsTheTasksHoldingItsUnit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newTasksRig(t, nil)
		lone := unit.Unit{Provider: unit.GitHub, Key: "acme/api#99"}
		rows := r.withLiveRows(map[unit.Unit]liveRow{
			apiPR: {facets: map[string][]string{"where": {"acme/api"}, "ci": {"failure"}}},
			lone:  {facets: map[string][]string{"where": {"acme/api"}}},
		})
		r.put(t, task.Task{ID: "t-pr", State: "inbox", Category: task.CategoryInbox, Title: "Review the fix", Units: []unit.Unit{apiPR}},
			task.Task{ID: "t-own", State: "inbox", Category: task.CategoryInbox, Title: "Think it over"})
		sub := r.follow(t)
		first := r.page(t, "view=inbox")
		if got := facetsOf(t, first, "t-pr")["ci"]; !slices.Equal(got, []string{"failure"}) {
			t.Fatalf("ci %v before the move", got)
		}
		rows.set(r.log, lone, map[string][]string{"where": {"acme/web"}}, nil)
		if c, _ := published(sub, readmodel.PokeQuiet+syncQuiet); c.Counters != nil {
			t.Fatalf("a row no task holds moved the counters: %+v", c)
		}
		rows.set(r.log, apiPR, map[string][]string{"where": {"acme/api"}, "ci": {"success"}}, nil)
		c, ok := published(sub, readmodel.PokeQuiet+syncQuiet)
		if !ok || c.Counters == nil || !slices.Contains(c.Units, apiPR) {
			t.Fatalf("the row's move published %+v (%v), want its unit with the counters", c, ok)
		}
		q := url.Values{"view": {"inbox"}, "since": {tasksSince(first)}, "units": {apiPR.String()}}
		got := r.page(t, q.Encode())
		if got.Full || !slices.Equal(taskIDs(got.Tasks), []string{"t-pr"}) {
			t.Fatalf("the page since the move: full %v, tasks %v", got.Full, taskIDs(got.Tasks))
		}
		if ci := got.Tasks[0].Facets["ci"]; !slices.Equal(ci, []string{"success"}) {
			t.Fatalf("ci %v after the move, want success", ci)
		}
	})
}

func TestABriefWriteResendsItsTask(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newTasksRig(t, nil)
		r.withLiveRows(map[unit.Unit]liveRow{generalThread: {facets: map[string][]string{"where": {"general"}}}})
		r.put(t, task.Task{ID: "t-slack", State: "inbox", Category: task.CategoryInbox, Title: "Answer in general", Units: []unit.Unit{generalThread}})
		sub := r.follow(t)
		first := r.page(t, "view=inbox")
		if err := r.db.PutRecord(context.Background(), generalThread, "brief", task.WriterAgent, []byte(`{"goal":"Answer","kind":"question"}`), tasksNow); err != nil {
			t.Fatal(err)
		}
		c, ok := published(sub, readmodel.PokeQuiet+syncQuiet)
		if !ok || c.Counters == nil || !slices.Contains(c.Units, generalThread) {
			t.Fatalf("the brief write published %+v (%v), want its unit with the counters", c, ok)
		}
		q := url.Values{"view": {"inbox"}, "since": {tasksSince(first)}, "units": {generalThread.String()}}
		got := r.page(t, q.Encode())
		if got.Full || !slices.Equal(taskIDs(got.Tasks), []string{"t-slack"}) || !slices.Equal(got.Tasks[0].Facets["type"], []string{"question"}) {
			t.Fatalf("the page since the brief: full %v, tasks %v %+v", got.Full, taskIDs(got.Tasks), got.Tasks)
		}
	})
}

func TestStalledGainsATaskWhenItsPRStalls(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newTasksRig(t, nil)
		rows := r.withLiveRows(map[unit.Unit]liveRow{apiPR: {facets: map[string][]string{"where": {"acme/api"}}}})
		r.put(t, task.Task{ID: "t-pr", State: "next", Category: task.CategoryReady, Title: "Land the fix", Units: []unit.Unit{apiPR}})
		sub := r.follow(t)
		if n := r.page(t, "view=stalled").Counters.Views["stalled"]; n != 0 {
			t.Fatalf("%d stalled before the pull request stalls", n)
		}
		rows.set(r.log, apiPR, map[string][]string{"where": {"acme/api"}}, map[string]bool{"stalled": true})
		c, ok := published(sub, readmodel.PokeQuiet+syncQuiet)
		if !ok || c.Counters == nil || c.Counters.Views["stalled"] != 1 {
			t.Fatalf("the stall published %+v (%v), want Stalled at 1", c, ok)
		}
	})
}

func TestPagesNameTheVersionsOfTheirSpecs(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.withFacetRows(facetRows{apiPR: {"where": {"acme/api"}}})
	var views api.ViewsCatalog
	if code := get(t, r.s, api.ViewsPath, &views); code != 200 || views.Version == "" {
		t.Fatalf("views %d %q", code, views.Version)
	}
	if p := r.page(t, "view=inbox"); p.Specs != views.Version {
		t.Errorf("the tasks page names specs %q, the views are at %q", p.Specs, views.Version)
	}
	var cat api.CatalogResponse
	if code := get(t, r.s, api.Catalog, &cat); code != 200 || cat.Version == "" {
		t.Fatalf("catalog %d %q", code, cat.Version)
	}
	var rows api.RowsPage
	if code := get(t, r.s, api.Rows, &rows); code != 200 || rows.Catalog != cat.Version {
		t.Errorf("the rows page names catalog %q, the catalog is at %q", rows.Catalog, cat.Version)
	}
}

func (*liveRows) Views() []api.ViewSpec { return nil }

func (*liveRows) Facets() []api.FacetSpec {
	return []api.FacetSpec{{ID: "ci", Label: "CI", Provider: "fake"}}
}

func (*liveRows) Situations() []api.SituationSpec { return nil }

func TestATaskLogTooLargeToListPublishesAll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newTasksRig(t, nil)
		r.withLiveRows(map[unit.Unit]liveRow{})
		sub := r.follow(t)
		r.log.NoteAll(port.TopicTasks)
		c, ok := published(sub, syncQuiet)
		if !ok || !c.All || c.Counters == nil || len(c.Units) != 0 {
			t.Fatalf("an overflowed log published %+v (%v), want All with the counters", c, ok)
		}
	})
}

func TestRowMovesWaitOnlyForATasksFeed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		u := agentRef.Unit()
		p := &fakeRows{units: []unit.Unit{u}}
		s, log, _ := fakeServer(t, p)
		runFeeds(t, s)
		p.set([]unit.Unit{u}, 1)
		log.Note(port.TopicPRs, u)
		time.Sleep(readmodel.PokeQuiet)
		synctest.Wait()
		if units, all := s.moved.take(); len(units) != 0 || all {
			t.Fatalf("with no tasks feed the row moves kept %v (all %v)", units, all)
		}
	})
}

// failingTasks is a task store whose lists fail.
type failingTasks struct{ *taskstest.Store }

func (failingTasks) Tasks(context.Context, task.Query) ([]task.Task, error) {
	return nil, errors.New("the store is gone")
}

func TestARowMoveNamesItsUnitWhenTheTasksCannotBeRead(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.deps.Tasks = coretasks.New(coretasks.Deps{Store: failingTasks{taskstest.New()}})
	sub := s.hub.subscribe("test")
	t.Cleanup(func() { s.hub.unsubscribe(sub) })
	s.moved.attach()
	s.moved.add(api.Change{Units: []unit.Unit{apiPR}})
	s.publishTasks(context.Background(), port.Change{})
	if c, ok := sub.take(); !ok || !slices.Equal(c.Units, []unit.Unit{apiPR}) {
		t.Fatalf("published %+v (%v), want the moved unit", c, ok)
	}
}

func TestPagesReuseOneBuildOfTheirSpecs(t *testing.T) {
	t.Parallel()
	loaded := bundledLoaded()
	r := newTasksRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return loaded } })
	r.withLiveRows(map[unit.Unit]liveRow{})
	first, again := r.s.viewsCatalog(), r.s.viewsCatalog()
	if unsafe.SliceData(first.Views) != unsafe.SliceData(again.Views) {
		t.Error("two tasks pages built the views catalog twice")
	}
	rows, rowsAgain := r.s.rowsCatalog(), r.s.rowsCatalog()
	if len(rows.Facets) == 0 || unsafe.SliceData(rows.Facets) != unsafe.SliceData(rowsAgain.Facets) {
		t.Error("two rows pages built the catalog twice")
	}
	loaded = draftReplies(t)()
	if next := r.s.viewsCatalog(); unsafe.SliceData(next.Views) == unsafe.SliceData(first.Views) {
		t.Error("a reloaded profile kept the views catalog built before it")
	}
	if next := r.s.rowsCatalog(); unsafe.SliceData(next.Facets) == unsafe.SliceData(rows.Facets) {
		t.Error("a reloaded profile kept the catalog built before it")
	}
}

func TestARowsChangeAndATasksChangeMergeForASubscriber(t *testing.T) {
	t.Parallel()
	h := newHub()
	sub := h.subscribe("test")
	other := unit.Unit{Provider: unit.GitHub, Key: "acme/api#13"}
	h.publish(api.Change{Type: "changed", Epoch: "e", Rev: 2, Units: []unit.Unit{other}})
	h.publish(api.Change{Type: "changed", Epoch: "e", Rev: 2, Units: []unit.Unit{apiPR}, Counters: &api.Counters{Badge: 1}})
	h.publish(api.Change{Type: "changed", Epoch: "e", Rev: 3, Units: []unit.Unit{apiPR}})
	c, ok := sub.take()
	if !ok || !slices.Equal(c.Units, []unit.Unit{apiPR, other}) || c.Counters == nil || c.Counters.Badge != 1 || c.Rev != 3 {
		t.Fatalf("merged %+v (%v), want both units, the counters and rev 3", c, ok)
	}
}
