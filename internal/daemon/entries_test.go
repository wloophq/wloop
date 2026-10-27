package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/card"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// entriesNow is newServerOn's now.
var (
	entriesNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	meID       = gtd.Me{Keys: []string{"github:me"}}
	r7u        = pr.Ref(r7).Unit()
)

type entriesRig struct {
	s      *Server
	db     *sqlite.DB
	log    *change.Log
	g      *coregtd.Service
	ledger *coreitems.Ledger
}

func newEntriesRig(t *testing.T, edit func(*Deps)) *entriesRig {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &entriesRig{db: db, log: log, ledger: &coreitems.Ledger{Store: db.Items(), Clock: fixedClock(entriesNow)}}
	r.s = newServerOn(t, db, log, func(d *Deps) {
		d.Items, d.Cards = r.ledger, db.Cards()
		d.Cfg.GTD.Areas, d.Cfg.GTD.Contexts = []string{"OSS", "Hiring"}, []string{"@decide", "@reply"}
		if edit != nil {
			edit(d)
		}
		enabled := d.PluginEnabled
		r.ledger.Enabled = enabled
		r.g = coregtd.New(coregtd.Deps{Store: db.Entries(), Items: r.ledger,
			Facts: coregtd.CardFacts{Cards: db.Cards(), Enabled: enabled, Me: func() gtd.Me { return meID }},
			Me:    meID, Clock: fixedClock(entriesNow), StaleWaitingDays: 2, Disabled: disabledOf(enabled)})
		d.GTD = r.g
	})
	t.Cleanup(r.s.stopLife)
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

func (r *entriesRig) put(t *testing.T, es ...gtd.Entry) {
	t.Helper()
	var cs []gtd.Change
	for _, e := range es {
		if e.Created.IsZero() {
			e.Created, e.Updated = entriesNow, entriesNow
		}
		if e.Origin == "" {
			e.Origin = gtd.OriginUser
		}
		if e.Kind == "" {
			e.Kind = gtd.KindCapture
		}
		cs = append(cs, gtd.Change{Op: gtd.OpCreate, Entry: e})
	}
	if err := r.db.Entries().Apply(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
}

func (r *entriesRig) page(t *testing.T, query string) api.EntriesPage {
	t.Helper()
	var p api.EntriesPage
	if code := get(t, r.s, api.EntriesPath+"?"+query, &p); code != 200 {
		t.Fatalf("GET %s: %d", query, code)
	}
	return p
}

func entryIDs(vs []api.EntryView) []string {
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

func mixedEntries() []gtd.Entry {
	return []gtd.Entry{
		{ID: "e-1", State: gtd.Next, Area: "OSS", Contexts: []string{"@decide"}},
		{ID: "e-2", State: gtd.Next, Area: "Hiring"},
		{ID: "e-3", State: gtd.Next, Area: "OSS", Contexts: []string{"@reply"}, Created: entriesNow.Add(-time.Hour)},
		{ID: "e-4", State: gtd.Inbox, Area: "Hiring"},
		{ID: "e-p", Kind: gtd.KindProject, State: gtd.Next, Area: "OSS", Sequential: true, Title: "Launch"},
		{ID: "e-5", State: gtd.Next, Area: "OSS", Project: "e-p", Position: 1},
		{ID: "e-6", State: gtd.Next, Area: "OSS", Project: "e-p", Position: 2},
		{ID: "e-7", State: gtd.Someday, Area: "OSS"},
	}
}

func TestEntriesPageMatchesTheQuery(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	r.put(t, mixedEntries()...)
	ctx := context.Background()
	for _, q := range []gtd.Query{{View: gtd.ViewNext, Area: "OSS"}, {View: gtd.ViewNext}, {View: gtd.ViewInbox, Area: "OSS"},
		{View: gtd.ViewNext, Area: "OSS", Contexts: []string{"@reply"}}, {View: gtd.ViewProjects}} {
		v := url.Values{"view": {string(q.View)}, "area": {q.Area}, "context": {strings.Join(q.Contexts, ",")}}
		p := r.page(t, v.Encode())
		want, err := r.db.Entries().Entries(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs := []string{}
		for _, e := range want {
			wantIDs = append(wantIDs, string(e.ID))
		}
		if !p.Full || !slices.Equal(entryIDs(p.Entries), wantIDs) {
			t.Errorf("%+v: page %v (full %v), store %v", q, entryIDs(p.Entries), p.Full, wantIDs)
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
	if i := slices.Index(entryIDs(p.Entries), "e-5"); i < 0 || p.Entries[i].ProjectTitle != "Launch" || len(p.Entries[i].Verbs) == 0 {
		t.Fatalf("project action %+v", p.Entries)
	}
	if code := get(t, r.s, api.EntriesPath+"?view=bogus", nil); code != 400 {
		t.Fatalf("an unknown view: %d", code)
	}
}

func TestEntriesSinceIsIncremental(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	r.put(t, mixedEntries()...)
	inbox := r.page(t, "view=inbox")
	next := r.page(t, "view=next")
	if rec := post(t, r.s, api.EntryPath("e-4", "next"), `{"source":"cockpit"}`); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	since := func(p api.EntriesPage) string { return api.Since{Epoch: p.Epoch, Rev: p.Rev}.String() }
	got := r.page(t, "view=inbox&since="+url.QueryEscape(since(inbox)))
	if got.Full || len(got.Entries) != 0 || !slices.Equal(got.Removed, []string{"e-4"}) || got.Rev != inbox.Rev+1 {
		t.Fatalf("inbox since: %+v", got)
	}
	got = r.page(t, "view=next&since="+url.QueryEscape(since(next)))
	if got.Full || !slices.Equal(entryIDs(got.Entries), []string{"e-4"}) || len(got.Removed) != 0 {
		t.Fatalf("next since: full %v entries %v removed %v", got.Full, entryIDs(got.Entries), got.Removed)
	}
	if got := r.page(t, "view=next&since=other:1"); !got.Full {
		t.Fatal("another epoch is not Full")
	}
}

// subscribe follows the hub and the entries feed until the test ends.
func (r *entriesRig) subscribe(t *testing.T) *subscriber {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { r.s.feedEntries(ctx) })
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
		log.NoteAll(entryInputs)
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
	r := newEntriesRig(t, nil)
	r.put(t, gtd.Entry{ID: "e-1", State: gtd.Inbox, Units: []unit.Unit{r7u}})
	sub := r.subscribe(t)
	rec := post(t, r.s, api.EntryPath("e-1", "next"), `{"source":"cockpit"}`)
	var res api.EntryResult
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &res) != nil || res.Entry.State != "next" || res.Entry.ID != "e-1" {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	c := nextChange(t, sub)
	if c.Type != "changed" || c.Counters.Views["next"] != 1 || c.Counters.Views["inbox"] != 0 || !slices.Contains(c.Units, r7u) {
		t.Fatalf("%+v %+v", c, c.Counters)
	}
}

func TestVerbErrors(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	r.put(t, gtd.Entry{ID: "e-1", State: gtd.Inbox})
	rec := post(t, r.s, api.EntryPath("e-1", "bogus"), `{}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "next, waiting, schedule") {
		t.Fatalf("unknown verb: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.EntryPath("e-none", "next"), `{}`); rec.Code != 404 {
		t.Fatalf("unknown id: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.EntryPath("e-1", "schedule"), `{}`); rec.Code != 400 {
		t.Fatalf("a missing argument: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, r.s, api.EntryPath("e-1", "inbox"), `{}`); rec.Code != 400 {
		t.Fatalf("a state for a verb: %d %s", rec.Code, rec.Body)
	}
	post(t, r.s, api.EntryPath("e-1", "done"), `{}`)
	if rec := post(t, r.s, api.EntryPath("e-1", "next"), `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "done") {
		t.Fatalf("a refused state: %d %s", rec.Code, rec.Body)
	}
}

func TestCaptureLandsInInbox(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	rec := post(t, r.s, api.EntriesPath, `{"title":"call the bank","notes":"before 5"}`)
	var v api.EntryView
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &v) != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if v.Kind != "capture" || v.State != "inbox" || v.Title != "call the bank" || v.Notes != "before 5" {
		t.Fatalf("%+v", v)
	}
	if p := r.page(t, "view=inbox"); !slices.Equal(entryIDs(p.Entries), []string{v.ID}) || p.Counters.Badge != 1 {
		t.Fatalf("%+v", p)
	}
	var detail api.EntryView
	if code := get(t, r.s, api.EntryPath(v.ID), &detail); code != 200 || detail.ID != v.ID {
		t.Fatalf("detail %d %+v", code, detail)
	}
	if rec := post(t, r.s, api.EntriesPath, `{"title":"  "}`); rec.Code != 400 {
		t.Fatalf("an empty title: %d %s", rec.Code, rec.Body)
	}
}

func TestCatalogServesViewsAndVerbs(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	var cat api.GTDCatalog
	if code := get(t, r.s, api.GTDPath, &cat); code != 200 {
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
	r.g.RegisterView(gtd.ViewReview, "review:", reviewSource{})
	if get(t, r.s, api.GTDPath, &cat); cat.Views[len(cat.Views)-1].ID != "review" {
		t.Fatalf("a registered review: %v", cat.Views)
	}
}

func TestTheCatalogIsWordedByTheProfile(t *testing.T) {
	t.Parallel()
	texts := intentload.GTDTexts{
		Verbs: map[string]intentload.VerbText{"done": {Label: "Finish", Summary: "Close it"}},
		Views: map[string]string{"next": "Now"},
	}
	r := newEntriesRig(t, func(d *Deps) { d.Intents = func() *intentload.Loaded { return &intentload.Loaded{GTD: texts} } })
	var cat api.GTDCatalog
	if code := get(t, r.s, api.GTDPath, &cat); code != 200 {
		t.Fatalf("%d", code)
	}
	verbs := map[string]api.GTDVerb{}
	for _, v := range cat.Verbs {
		verbs[v.Verb] = v
	}
	if got := verbs["done"]; got.Label != "Finish" || got.Summary != "Close it" {
		t.Errorf("done: %+v", got)
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

func (reviewSource) Entries(context.Context, gtd.Query) ([]gtd.Entry, error) { return nil, nil }
func (reviewSource) Entry(context.Context, gtd.ID) (gtd.Entry, bool, error) {
	return gtd.Entry{}, false, nil
}
func (reviewSource) Count(context.Context, string) (int, error) { return 0, nil }

func TestCountersShipWithChanges(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	r.put(t, mixedEntries()...)
	sub := r.subscribe(t)
	if rec := post(t, r.s, api.EntryPath("e-4", "someday"), `{}`); rec.Code != 200 {
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
func (r *entriesRig) startSync(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { r.s.syncEntries(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
}

func owedToMe(u unit.Unit, id string) items.Op {
	return items.Op{Op: items.OpCreate, Unit: u, Item: id, Anchor: unit.Anchor{Ref: "user:" + id}, Fields: &items.Fields{Kind: ptrOf(items.Todo),
		Summary: ptrOf("review " + id), WaitingOn: &[]items.Person{{Key: "github:me", Name: "me"}}}}
}

func TestItemsNoteSyncsEntries(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	sub := r.subscribe(t)
	r.startSync(t)
	if _, err := r.ledger.Apply(context.Background(), items.User, []items.Op{owedToMe(r7u, "i1")}); err != nil {
		t.Fatal(err)
	}
	c := nextChange(t, sub)
	if !slices.Contains(c.Units, r7u) || c.Counters.Views["inbox"] != 1 {
		t.Fatalf("%+v %+v", c, c.Counters)
	}
	es, err := r.db.Entries().EntriesOf(context.Background(), r7u)
	if err != nil || len(es) != 1 || es[0].State != gtd.Inbox || es[0].Kind != gtd.KindAction {
		t.Fatalf("%+v %v", es, err)
	}
}

func TestSourceActivityReinboxes(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	ctx := context.Background()
	r.put(t, gtd.Entry{ID: "e-s", State: gtd.Scheduled, Units: []unit.Unit{r7u}, Snooze: gtd.Snooze{OnActivity: true, Return: gtd.Next},
		SeenAt: entriesNow.Add(-time.Hour), Created: entriesNow.Add(-2 * time.Hour), Updated: entriesNow.Add(-2 * time.Hour)})
	r.startSync(t)
	r.log.Note(port.TopicSourceEvents, r7u)
	time.Sleep(syncMaxDelay + 500*time.Millisecond)
	if p := r.page(t, "view=inbox"); len(p.Entries) != 0 {
		t.Fatalf("the source event moved the entry before its card: %+v", p.Entries)
	}
	ana := items.Person{Key: "github:ana", Name: "ana"}
	c := card.Card{Unit: r7u, Title: "Batch export", State: "open", LastHuman: &card.Human{Who: ana, What: "commented", At: entriesNow.Add(-time.Minute)}}
	if _, err := r.db.Cards().Save(ctx, c, entriesNow); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the entry back in the Inbox", func() bool {
		p := r.page(t, "view=inbox")
		return len(p.Entries) == 1 && p.Entries[0].ID == "e-s" && p.Entries[0].Changed
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
	r := newEntriesRig(t, nil)
	b := &blockingItems{entered: make(chan struct{}), release: make(chan struct{})}
	r.s.deps.GTD = coregtd.New(coregtd.Deps{Store: gtdtest.New(), Items: b, Facts: coregtd.CardFacts{}, Clock: fixedClock(entriesNow)})
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

// bridgeRig is the catalog rig serving the entries of a fake store, with entry e-r on acme/api#7.
func bridgeRig(t *testing.T) (catalogRig, *coregtd.Service) {
	t.Helper()
	c := newCatalogRig(t)
	store := gtdtest.New()
	g := coregtd.New(coregtd.Deps{Store: store, Facts: coregtd.CardFacts{}, Clock: fixedClock(catalogNow)})
	c.s.deps.GTD = g
	e := gtd.Entry{ID: "e-r", Kind: gtd.KindAction, State: gtd.Inbox, Units: []unit.Unit{r7u}, Origin: gtd.OriginDerived, Created: catalogNow, Updated: catalogNow}
	if err := store.Apply(context.Background(), []gtd.Change{{Op: gtd.OpCreate, Entry: e}}); err != nil {
		t.Fatal(err)
	}
	return c, g
}

func TestApproveRunsThePendingRound(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	c.propose(t)
	rec := post(t, c.s, api.EntryPath("e-r", "approve"), `{"source":"cockpit"}`)
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
	rec := post(t, c.s, api.EntryPath("e-r", "delegate"), `{"source":"cockpit"}`)
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
	rec := post(t, c.s, api.EntryPath("e-r", "approve"), `{"source":"cockpit"}`)
	row, _ := c.s.currentRow(context.Background(), r7u)
	want := c.s.offeredFor(row, gtd.VerbApprove).reason()
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), want) || want == "" {
		t.Fatalf("%d %s, want %q", rec.Code, rec.Body, want)
	}
	if len(fa.calls) != 0 || len(c.plan.acted) != 0 {
		t.Fatalf("dispatched: %v %v", fa.calls, c.plan.acted)
	}
	if e, _ := g.Entry(context.Background(), "e-r"); e.State != gtd.Inbox {
		t.Fatalf("the entry moved: %+v", e)
	}
	if rec := post(t, c.s, api.EntryPath("e-r", "dismiss"), `{}`); rec.Code != 409 || !strings.Contains(rec.Body.String(), "no open question") {
		t.Fatalf("dismiss with the turn's dismiss offered disabled: %d %s", rec.Code, rec.Body)
	}
}

func TestBridgeRunsOnlyForActionEntries(t *testing.T) {
	t.Parallel()
	c, g := bridgeRig(t)
	c.propose(t)
	capture, err := g.Capture(context.Background(), "ask ana about the export", "", &r7u)
	if err != nil {
		t.Fatal(err)
	}
	if rec := post(t, c.s, api.EntryPath(string(capture.ID), "approve"), `{"source":"cockpit"}`); rec.Code != 409 {
		t.Fatalf("approve on a capture: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, c.s, api.EntryPath(string(capture.ID), "dismiss"), `{}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"trash"`) {
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
		rec := post(t, c.s, api.EntryPath("e-r", verb), `{"source":"cockpit"}`)
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), `"plugin":"github"`) {
			t.Fatalf("%s: %d %s", verb, rec.Code, rec.Body)
		}
	}
	var v api.EntryView
	if code := get(t, c.s, api.EntryPath("e-r"), &v); code != 200 || slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Enabled != (s.Verb == "dismiss") }) {
		t.Fatalf("%d %+v", code, v.Verbs)
	}
	if rec := post(t, c.s, api.EntryPath("e-r", "dismiss"), `{"source":"cockpit"}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"state":"trash"`) {
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
	g.Register(gtd.KindAction, gtd.VerbApprove, func(_ context.Context, e gtd.Entry, req coregtd.Request) (coregtd.Result, error) {
		got = append(got, string(e.ID)+" "+req.Source)
		return coregtd.Result{Entry: e, Lines: []string{"handled"}}, nil
	})
	rec := post(t, c.s, api.EntryPath("e-r", "approve"), `{"source":"tui"}`)
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
		verb    gtd.Verb
		actions []api.ActionState
		runs    string
		reason  string
	}{
		{"built-in over user", gtd.VerbApprove, []api.ActionState{on("fast-approve"), on("run_now")}, "run_now", ""},
		{"user alone", gtd.VerbApprove, []api.ActionState{on("fast-approve")}, "fast-approve", ""},
		{"user over a bundled intent of the same provider", gtd.VerbDelegate, []api.ActionState{on("review"), on("deep-check")}, "deep-check", ""},
		{"own provider over any provider", gtd.VerbDelegate, []api.ActionState{on("act"), on("review")}, "review", ""},
		{"two bundled enabled", gtd.VerbDelegate, []api.ActionState{on("review"), on("address")}, "", "offers review and address to delegate"},
		{"a disabled user intent hides the bundled one", gtd.VerbDelegate, []api.ActionState{{ID: "deep-check", Reason: "not now"}, on("review")}, "", "not now"},
		{"the first disabled reason", gtd.VerbDelegate, []api.ActionState{{ID: "address", Reason: "nothing to address"}, {ID: "review", Reason: "a review runs"}}, "", "nothing to address"},
		{"none offered", gtd.VerbDelegate, []api.ActionState{on("run_now")}, "", "offers no action to delegate"},
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
		st, ok := rankOffered(specs, api.Row{Unit: slackUnit, Actions: c.actions}, gtd.VerbDelegate).runs()
		if !ok || st.ID != c.runs {
			t.Errorf("%s: runs %q (%v), want %q", c.name, st.ID, ok, c.runs)
		}
	}
}

func TestEntryAnswerRouteMatchesTheUnitAnswer(t *testing.T) {
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
	var v api.EntryView
	if code := get(t, r.s, api.EntryPath(m.EntryID), &v); code != 200 || v.Message == nil || v.Message.ID != m.ID ||
		!slices.ContainsFunc(v.Verbs, func(s api.VerbState) bool { return s.Verb == "answer" && s.Enabled }) {
		t.Fatalf("%d %+v", code, v)
	}
	rec = post(t, r.s, api.EntryPath(m.EntryID, "answer"), `{"args":{"option_id":"publish","guidance":"say thanks"},"source":"cockpit"}`)
	var res api.EntryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if got := r.outcome(rec.Code, res.Lines); !reflect.DeepEqual(got, want) {
		t.Fatalf("by entry %+v\nby unit %+v", got, want)
	}
}

func TestCountersWithTermsCountWhatTheFilteredListsShow(t *testing.T) {
	t.Parallel()
	r := newEntriesRig(t, nil)
	r.put(t, mixedEntries()...)
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
				for _, e := range r.page(t, v.Encode()).Entries {
					if api.EntryMatches(e, terms) {
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
	if get(t, r.s, api.CountersPath+"?"+url.Values{api.CountersTerm: {"launch"}}.Encode(), &c); c.Areas["OSS"] != 2 || c.Areas["Hiring"] != 0 {
		t.Errorf("areas under a filter: %v", c.Areas)
	}
}

func TestBridgeRelaysARefusalAsJSONOnly(t *testing.T) {
	t.Parallel()
	c, _ := bridgeRig(t)
	c.propose(t)
	rec := post(t, c.s, api.EntryPath("e-r", "approve"), `{"source":"<script>alert(1)</script>"}`)
	if rec.Code != 400 || strings.Contains(rec.Body.String(), "<script>") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if h := rec.Header(); h.Get("Content-Type") != "application/json" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", h)
	}
}
