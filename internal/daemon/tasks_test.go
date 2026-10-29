package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
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
}

func newTasksRig(t *testing.T, edit func(*Deps)) *tasksRig {
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
		r.g = coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Authority: taskstest.BundledStates.Authority, Store: db.Tasks(), Items: r.ledger,
			Facts: coretasks.CardFacts{Cards: db.Cards(), Enabled: enabled, Me: func() task.Me { return meID }},
			Me:    meID, Clock: fixedClock(tasksNow), Disabled: disabledOf(enabled)})
		d.Tasks = r.g
	})
	stopOnCleanup(t, r.s)
	return r
}

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

func (r *tasksRig) put(t *testing.T, es ...task.Task) {
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
		{ID: "e-1", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}, "contexts": {List: []string{"@decide"}}}},
		{ID: "e-2", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "Hiring"}}},
		{ID: "e-3", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}, "contexts": {List: []string{"@reply"}}}, Created: tasksNow.Add(-time.Hour)},
		{ID: "e-4", State: "inbox", Category: task.CategoryInbox, Props: task.Props{"area": {Text: "Hiring"}}},
		{ID: "e-p", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}, task.PropKind: {Text: task.KindProject}}, Sequential: true, Title: "Launch"},
		{ID: "e-5", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}}, Parent: "e-p", Position: 1},
		{ID: "e-6", State: "next", Category: task.CategoryReady, Props: task.Props{"area": {Text: "OSS"}}, Parent: "e-p", Position: 2},
		{ID: "e-7", State: "someday", Category: task.CategoryDeferred, Props: task.Props{"area": {Text: "OSS"}}},
	}
}

func TestTasksPageMatchesTheQuery(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, mixedTasks()...)
	ctx := context.Background()
	for _, q := range []task.Query{{View: task.ViewNext, Area: "OSS"}, {View: task.ViewNext}, {View: task.ViewInbox, Area: "OSS"},
		{View: task.ViewNext, Area: "OSS", Contexts: []string{"@reply"}}, {View: task.ViewProjects}} {
		v := url.Values{"view": {string(q.View)}, "area": {q.Area}, "context": {strings.Join(q.Contexts, ",")}}
		p := r.page(t, v.Encode())
		want, err := r.db.Tasks().Tasks(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs := []string{}
		for _, e := range want {
			wantIDs = append(wantIDs, string(e.ID))
		}
		if !p.Full || !slices.Equal(taskIDs(p.Tasks), wantIDs) {
			t.Errorf("%+v: page %v (full %v), store %v", q, taskIDs(p.Tasks), p.Full, wantIDs)
		}
		c, err := r.g.Counts(ctx, q.Area)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := p.Counters, wireCounters(c); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%+v: counters %+v, want %+v", q, got, want)
		}
	}
	p := r.page(t, "view=next&area=OSS")
	if i := slices.Index(taskIDs(p.Tasks), "e-5"); i < 0 || p.Tasks[i].Parent != "e-p" || len(p.Tasks[i].Verbs) == 0 {
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
	next := r.page(t, "view=next")
	if rec := post(t, r.s, api.TaskPath("e-4", "next"), `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	since := func(p api.TasksPage) string { return api.Since{Epoch: p.Epoch, Rev: p.Rev}.String() }
	got := r.page(t, "view=inbox&since="+url.QueryEscape(since(inbox)))
	if got.Full || len(got.Tasks) != 0 || !slices.Equal(got.Removed, []string{"e-4"}) || got.Rev != inbox.Rev+1 {
		t.Fatalf("inbox since: %+v", got)
	}
	got = r.page(t, "view=next&since="+url.QueryEscape(since(next)))
	if got.Full || !slices.Equal(taskIDs(got.Tasks), []string{"e-4"}) || len(got.Removed) != 0 {
		t.Fatalf("next since: full %v entries %v removed %v", got.Full, taskIDs(got.Tasks), got.Removed)
	}
	if got := r.page(t, "view=next&since=other:1"); !got.Full {
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
				t.Fatal("the entries feed never published")
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
	r.put(t, task.Task{ID: "e-1", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}})
	sub := r.subscribe(t)
	rec := post(t, r.s, api.TaskPath("e-1", "next"), `{"source":"cockpit"}`)
	var res api.TaskResult
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Task.State != "next" || res.Task.ID != "e-1" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := nextChange(t, sub)
	if c.Type != "changed" || c.Counters.Views["next"] != 1 || c.Counters.Views["inbox"] != 0 || !slices.Contains(c.Units, r7u) {
		t.Fatalf("%+v %+v", c, c.Counters)
	}
}

func TestAVerbTheProfileForbidsIsForbidden(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeTasksError(rec, fmt.Errorf("%w: agent may not run done on this entry", coretasks.ErrForbidden))
	if rec.Code != http.StatusForbidden {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestVerbErrors(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "e-1", State: "inbox", Category: task.CategoryInbox})
	rec := post(t, r.s, api.TaskPath("e-1", "bogus"), `{}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "next, waiting, schedule") {
		t.Fatalf("unknown verb: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("e-none", "next"), `{}`); rec.Code != 404 {
		t.Fatalf("unknown id: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("e-1", "schedule"), `{}`); rec.Code != 400 {
		t.Fatalf("a missing argument: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("e-1", "inbox"), `{}`); rec.Code != 400 {
		t.Fatalf("a state for a verb: %d %s", rec.Code, rec.Body)
	}
	post(t, r.s, api.TaskPath("e-1", "done"), `{}`)
	if rec := post(t, r.s, api.TaskPath("e-1", "next"), `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "done") {
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
	if want := []string{"focus", "inbox", "outbox", "next", "waiting", "projects", "someday", "logbook"}; !slices.Equal(views, want) {
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
	r.g.RegisterView(task.ViewReview, "review:", reviewSource{})
	if get(t, r.s, api.ViewsPath, &cat); cat.Views[len(cat.Views)-1].ID != "review" {
		t.Fatalf("a registered review: %v", cat.Views)
	}
}

func TestTheCatalogIsWordedByTheProfile(t *testing.T) {
	t.Parallel()
	texts := intentload.ListTexts{Views: map[string]string{"next": "Now"}}
	tasks := intentload.TasksDef{Verbs: map[string]intentload.VerbText{"done": {Label: "Finish", Summary: "Close it"}, "waiting": {Label: "Waiting", Summary: "Wait", Input: "On whom?"}}}
	r := newTasksRig(t, func(d *Deps) {
		d.Intents = func() *intentload.Loaded {
			return &intentload.Loaded{Lists: texts, Tasks: tasks, Registry: dintent.NewRegistry(nil)}
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
	if labels["next"] != "Now" || labels["inbox"] != "Inbox" {
		t.Errorf("views %v", labels)
	}
}

type reviewSource struct{}

func (reviewSource) Tasks(context.Context, task.Query) ([]task.Task, error) { return nil, nil }
func (reviewSource) Task(context.Context, task.ID) (task.Task, bool, error) {
	return task.Task{}, false, nil
}
func (reviewSource) Count(context.Context, string) (int, error) { return 0, nil }

func TestCountersShipWithChanges(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, mixedTasks()...)
	sub := r.subscribe(t)
	if rec := post(t, r.s, api.TaskPath("e-4", "someday"), `{}`); rec.Code != 200 {
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
	if get(t, r.s, api.CountersPath+"?area=OSS", &oss); oss.Area != "OSS" || oss.Views["next"] != 4 || oss.Areas["Hiring"] != 1 {
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

func TestSourceActivityReturnsASnoozedTaskWhereItWasNotToTheInbox(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newTasksRig(t, nil)
		ctx := context.Background()
		r.put(t, task.Task{ID: "e-s", State: "scheduled", Category: task.CategoryDeferred, Units: []unit.Unit{r7u}, Snooze: task.Snooze{OnActivity: true, Return: task.State("next")},
			SeenAt: tasksNow.Add(-time.Hour), Created: tasksNow.Add(-2 * time.Hour), Updated: tasksNow.Add(-2 * time.Hour)})
		r.startSync(t)
		r.log.Note(port.TopicSourceEvents, r7u)
		time.Sleep(syncMaxDelay)
		synctest.Wait()
		if p := r.page(t, "view=inbox"); len(p.Tasks) != 0 {
			t.Fatalf("the source event moved the entry before its card: %+v", p.Tasks)
		}
		ana := items.Person{Key: "github:ana", Name: "ana"}
		c := card.Card{Unit: r7u, Title: "Batch export", State: "open", LastHuman: &card.Human{Who: ana, What: "commented", At: tasksNow.Add(-time.Minute)}}
		if _, err := r.db.Cards().Save(ctx, c, tasksNow); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, "the task back in Next", func() bool {
			p := r.page(t, "view=next")
			return len(p.Tasks) == 1 && p.Tasks[0].ID == "e-s" && p.Tasks[0].Changed
		})
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

// bridgeRig is the catalog rig serving the tasks of a fake store, with task e-r on acme/api#7.
func bridgeRig(t *testing.T) (catalogRig, *coretasks.Service) {
	t.Helper()
	c := newCatalogRig(t)
	store := taskstest.New()
	g := coretasks.New(coretasks.Deps{States: taskstest.BundledStates, Store: store, Facts: coretasks.CardFacts{}, Clock: fixedClock(catalogNow)})
	c.s.deps.Tasks = g
	e := task.Task{ID: "e-r", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}, Origin: task.OriginDerived, Created: catalogNow, Updated: catalogNow}
	if err := store.Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
	return c, g
}

func TestApproveRunsThePendingRound(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	c.propose(t)
	rec := post(t, c.s, api.TaskPath("e-r", "approve"), `{"source":"cockpit"}`)
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
	rec := post(t, c.s, api.TaskPath("e-r", "delegate"), `{"source":"cockpit"}`)
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
	rec := post(t, c.s, api.TaskPath("e-r", "approve"), `{"source":"cockpit"}`)
	row, _ := c.s.currentRow(context.Background(), r7u)
	want := c.s.offeredFor(row, task.VerbApprove).reason()
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), want) || want == "" {
		t.Fatalf("%d %s, want %q", rec.Code, rec.Body, want)
	}
	if len(fa.calls) != 0 || len(c.plan.acted) != 0 {
		t.Fatalf("dispatched: %v %v", fa.calls, c.plan.acted)
	}
	if e, _ := g.Task(context.Background(), "e-r"); e.State != task.State("inbox") {
		t.Fatalf("the entry moved: %+v", e)
	}
	if rec := post(t, c.s, api.TaskPath("e-r", "dismiss"), `{}`); rec.Code != 200 || len(fa.calls) != 0 {
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
		rec := post(t, c.s, api.TaskPath("e-r", verb), `{"source":"cockpit"}`)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"plugin":"github"`) {
			t.Fatalf("%s: %d %s", verb, rec.Code, rec.Body)
		}
	}
	var v api.TaskView
	if code := get(t, c.s, api.TaskPath("e-r"), &v); code != 200 || slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Enabled != (s.Verb == "dismiss") }) {
		t.Fatalf("%d %+v", code, v.Verbs)
	}
	if rec := post(t, c.s, api.TaskPath("e-r", "dismiss"), `{"source":"cockpit"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"trash"`) {
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
	rec := post(t, c.s, api.TaskPath("e-r", "approve"), `{"source":"tui"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "handled") || !slices.Equal(got, []string{"e-r tui"}) {
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
		t.Fatalf("by entry %+v\nby unit %+v", got, want)
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
			for _, view := range []string{"focus", "inbox", "next", "waiting", "projects", "someday", "logbook"} {
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
	rec := post(t, c.s, api.TaskPath("e-r", "approve"), `{"source":"<script>alert(1)</script>"}`)
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
	r.put(t, task.Task{ID: "e-c", State: "inbox", Category: task.CategoryInbox, Title: "c", Origin: task.OriginUser, Props: task.Props{task.PropKind: {Text: "capture"}}})
	var v api.TaskView
	if code := get(t, r.s, api.TaskPath("e-c"), &v); code != 200 || slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Verb == "someday" }) {
		t.Fatalf("%d %+v", code, v.Verbs)
	}
	if rec := post(t, r.s, api.TaskPath("e-c", "someday"), `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "not offered") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.TaskPath("e-c", "next"), `{}`); rec.Code != 200 {
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
