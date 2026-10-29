package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/storetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	dmessage "github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// notifyRig is the catalog rig with the tasks, the items ledger and the Inbox messages over its store, so a
// fixture's row derives its items, card, message and tasks as the daemon would.
type notifyRig struct {
	catalogRig
	g      *coretasks.Service
	ledger *coreitems.Ledger
}

func newNotifyRig(t *testing.T) *notifyRig {
	t.Helper()
	c := newCatalogRig(t)
	r := &notifyRig{catalogRig: c, ledger: &coreitems.Ledger{Store: c.db.Items(), Clock: fixedClock(catalogNow)}}
	enabled := func(p unit.ProviderID) bool { return c.s.pluginEnabled(p) }
	r.ledger.Enabled = enabled
	r.g = coretasks.New(withLens(coretasks.Deps{States: taskstest.BundledStates, Store: c.db.Tasks(), Items: r.ledger,
		Facts: coretasks.CardFacts{Cards: c.db.Cards(), Enabled: enabled, Me: func() task.Me { return meID }},
		Me:    meID, Clock: fixedClock(catalogNow)}, nil, nil))
	c.s.deps.Tasks, c.s.deps.Items, c.s.deps.Cards = r.g, r.ledger, c.db.Cards()
	c.s.deps.Messages = &messages.Service{Store: inventory.NewMessages(c.db), Books: c.repo, Turns: c.s.deps.Turns, Tasks: r.g,
		Intents: turnstest.Intents(), Clock: fixedClock(catalogNow)}
	serveMessageGates(c.s, c.db.Tasks(), r.g)
	c.s.registerMessages()
	return r
}

// settle derives u's card and items from its row as the cards job does, reconciles its messages and syncs its
// tasks; it returns the row.
func (r *notifyRig) settle(t *testing.T, u unit.Unit) api.Row {
	t.Helper()
	ctx := context.Background()
	row, err := r.s.currentRow(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	its, err := r.s.cards().Items(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if c, err := r.s.cards().Card(ctx, row); err == nil {
		c.Unit = u
		if _, err := r.db.Cards().Save(ctx, c, catalogNow); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.ledger.Reconcile(ctx, u, its, unit.Anchor{Unit: u, Ref: "row:1"}, items.Superseded); err != nil {
		t.Fatal(err)
	}
	if err := r.s.deps.Messages.Reconcile(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := r.g.Sync(ctx, []unit.Unit{u}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.deps.Messages.Reconcile(ctx, u); err != nil {
		t.Fatal(err)
	}
	return row
}

// notified are the attentions of every task of every view, by task id.
func (r *notifyRig) notified(t *testing.T) map[string]api.TaskView {
	t.Helper()
	out := map[string]api.TaskView{}
	for _, v := range storedViews() {
		var p api.TasksPage
		if code := get(t, r.s, api.TasksPath+"?view="+v, &p); code != 200 {
			t.Fatalf("view %s: %d", v, code)
		}
		for _, e := range p.Tasks {
			if e.Attention == nil {
				continue
			}
			if v != "inbox" && v != "outbox" && v != "focus" && len(e.Gates) == 0 && !pullRequestTask(e) {
				t.Errorf("%s entry %s (%s) with no open gate notifies %+v", v, e.ID, e.State, e.Attention)
			}
			out[e.ID] = e
		}
	}
	return out
}

// pullRequestTask says whether e is a pull request's task, which notifies its row's notice whatever its state.
func pullRequestTask(e api.TaskView) bool {
	return len(e.Units) > 0 && e.Units[0].Provider == unit.GitHub
}

// newCategory is the category a row attention's category takes on a task.
func newCategory(old string) api.Category {
	switch old {
	case "HELD":
		return api.CategoryHeld
	case "ROUND", "BLOCKED", "WAIT":
		return api.CategoryInbox
	}
	return api.Category(old)
}

// taskActions are a row attention's action ids as a task offers them.
func taskActions(acts []api.AttentionAction) []string {
	out := []string{}
	for _, a := range acts {
		switch a.ID {
		case "run_now":
			a.ID = "task:approve"
		case "skip":
			a.ID = "task:dismiss"
		}
		out = append(out, a.ID+" "+a.Title)
	}
	return out
}

// waitFixtures add what the catalog fixtures lack: a started turn whose session waits on a permission prompt.
var waitFixtures = []catalogFixture{
	{"permission", r7, func(t *testing.T, c catalogRig) {
		ctx := context.Background()
		sessions := c.s.deps.Sessions.(*turnstest.Sessions)
		sessions.SetAgents(turns.AgentObservation{WorkspaceID: "w7", Pane: "t:1", State: turn.AgentWaiting})
		c.s.deps.Agents = snapshotOf(sessions)
		at := catalogNow.Add(-30 * time.Minute)
		b := turn.Book{Unit: r7u, Turns: []turn.Turn{{Intent: turn.Intent(dintent.Address), Role: "pr.address", ID: "r7", Trigger: turn.Automated, Head: "h2", Session: "term-7",
			State: turn.Started, SentAt: at, StartedAt: at}}}
		if _, err := c.repo.Save(ctx, b, port.Event{Unit: r7u, Kind: turns.KindTurnSent, Key: "r", At: at}); err != nil {
			t.Fatal(err)
		}
		if err := c.repo.SaveSession(ctx, turn.Session{ID: "term-7", Pane: "t:1", WorkspaceID: "w7"}, port.Event{Kind: turns.KindSessionLaunched, Key: "term-7", At: catalogNow}); err != nil {
			t.Fatal(err)
		}
	}},
}

func rawActions(acts []api.AttentionAction) []string {
	out := []string{}
	for _, a := range acts {
		out = append(out, a.ID+" "+a.Title)
	}
	return out
}

func TestTaskAttentionMatchesRowAttention(t *testing.T) {
	t.Parallel()
	var compared atomic.Int32
	t.Cleanup(func() {
		if n := compared.Load(); n < 6 {
			t.Errorf("only %d fixtures notify", n)
		}
	})
	for _, f := range slices.Concat(catalogFixtures, waitFixtures) {
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			r := newNotifyRig(t)
			f.seed(t, r.catalogRig)
			row := r.settle(t, f.ref.Unit())
			if f.name == "pending_muted" {
				// A mute holds the tasks the pull request has when it is set: mute again once its items made theirs.
				if err := muteBy(r.st, f.ref, catalogNow.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
				row = r.settle(t, f.ref.Unit())
			}
			got := r.notified(t)
			old := rowAttention(t, r.s, row)
			if f.name == "pending_muted" {
				es, err := r.db.Tasks().TasksOf(context.Background(), f.ref.Unit())
				if err != nil || len(es) == 0 || slices.ContainsFunc(es, func(e task.Task) bool { return e.State != task.State("scheduled") }) {
					t.Fatalf("the muted unit's entries %+v %v", es, err)
				}
			}
			if old == nil {
				for id, e := range got {
					t.Errorf("entry %s (%s) notifies %+v where the row did not", id, e.State, e.Attention)
				}
				return
			}
			compared.Add(1)
			t.Logf("%s -> %v", old.ID, func() (ids []string) {
				for _, e := range got {
					ids = append(ids, e.Attention.ID)
				}
				return
			}())
			if len(got) != 1 {
				t.Fatalf("the row notifies %s, the entries %d times: %+v", old.ID, len(got), got)
			}
			for id, e := range got {
				a := e.Attention
				cat, task, ok := api.ParseNotificationID(a.ID)
				if !ok || task != id || cat != newCategory(old.Category) || a.Category != string(cat) {
					t.Errorf("id %s category %s, old %s %s", a.ID, a.Category, old.ID, old.Category)
				}
				if a.Title != old.Title || a.Body != old.Body || a.Subtitle != old.Subtitle || a.Image != old.Image {
					t.Errorf("entry %+v\nrow   %+v", a, old)
				}
				if want, have := taskActions(old.Actions), rawActions(a.Actions); !slices.Equal(want, have) {
					t.Errorf("actions %q, row %q", have, want)
				}
			}
		})
	}
}

// fakeOutbox is the outbox as the daemon serves it: its open drafts listed, and the proposed or failed ones as
// tasks "draft:<id>".
type fakeOutbox struct{ drafts []draft.Draft }

func (f *fakeOutbox) List(_ context.Context, fl outbox.Filter) ([]draft.Draft, error) {
	var out []draft.Draft
	for _, d := range f.drafts {
		if len(fl.States) == 0 || slices.Contains(fl.States, d.State) {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeOutbox) task(d draft.Draft) task.Task {
	return task.Task{ID: task.ID("draft:" + strconv.FormatInt(int64(d.ID), 10)), State: "inbox", Category: task.CategoryInbox, Title: d.Text,
		Units: []unit.Unit{d.Unit}, Origin: task.OriginDraft, Created: d.CreatedAt, Updated: d.UpdatedAt}
}

func (f *fakeOutbox) Tasks(context.Context, task.Query) ([]task.Task, error) {
	var out []task.Task
	for _, d := range f.drafts {
		if d.State == draft.Held || d.State == draft.Failed {
			out = append(out, f.task(d))
		}
	}
	return out, nil
}

func (f *fakeOutbox) Task(_ context.Context, id task.ID) (task.Task, bool, error) {
	for _, d := range f.drafts {
		if e := f.task(d); e.ID == id {
			return e, true, nil
		}
	}
	return task.Task{}, false, nil
}

func withOutbox(r *tasksRig, drafts ...draft.Draft) *fakeOutbox {
	f := &fakeOutbox{drafts: drafts}
	r.g.RegisterView(task.OverGates, "draft:", f)
	r.s.deps.Drafts = f
	return f
}

func proposed(id int64, task string) draft.Draft {
	return draft.Draft{ID: draft.ID(id), Unit: r7u, Task: task, Kind: draft.Comment, Purpose: "comment", Delivery: draft.CommentDelivery, Text: "Thanks, merging.\nmore", State: draft.Held,
		CreatedAt: tasksNow.Add(-time.Hour), UpdatedAt: tasksNow.Add(-time.Duration(id) * time.Minute)}
}

// attentions are the attentions of every view's tasks, by task id.
func attentions(t *testing.T, r *tasksRig) map[string]*api.Attention {
	t.Helper()
	out := map[string]*api.Attention{}
	for _, v := range storedViews() {
		for _, e := range r.page(t, "view="+v).Tasks {
			if e.Attention != nil {
				if v != "inbox" && v != "outbox" && v != "focus" && len(e.Gates) == 0 {
					t.Errorf("%s entry %s with no open gate notifies %+v", v, e.ID, e.Attention)
				}
				out[e.ID] = e.Attention
			}
		}
	}
	return out
}

func actionIDs(a *api.Attention) []string {
	var out []string
	for _, x := range a.Actions {
		out = append(out, x.ID)
	}
	return out
}

func TestInboxTasksCarryAttention(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "e-in", State: "inbox", Category: task.CategoryInbox, Title: "ask ana about the export"})
	got := attentions(t, r)
	a := got["e-in"]
	if len(got) != 1 || a == nil {
		t.Fatalf("%+v", got)
	}
	cat, tk, ok := api.ParseNotificationID(a.ID)
	if !ok || cat != api.CategoryInbox || tk != "e-in" || a.Category != "INBOX" || a.Body != "ask ana about the export" {
		t.Fatalf("%+v", a)
	}
	r.put(t, task.Task{ID: "e-renamed", State: "inbox", Category: task.CategoryInbox, Title: "another"})
	if b := attentions(t, r)["e-renamed"]; b == nil || strings.Split(b.ID, "|")[2] == strings.Split(a.ID, "|")[2] {
		t.Fatalf("another title kept the token: %+v", b)
	}
}

func TestOnlyInboxAndOutboxNotify(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t,
		task.Task{ID: "e-in", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}},
		task.Task{ID: "e-next", State: "next", Category: task.CategoryReady, Units: []unit.Unit{r7u}},
		task.Task{ID: "e-wait", State: "waiting", Category: task.CategoryWaiting, Units: []unit.Unit{r7u}},
		task.Task{ID: "e-muted", State: "scheduled", Category: task.CategoryDeferred, Units: []unit.Unit{r7u}},
		task.Task{ID: "e-someday", State: "someday", Category: task.CategoryDeferred},
		task.Task{ID: "e-done", State: "done", Category: task.CategoryDone},
	)
	got := attentions(t, r)
	if len(got) != 1 || got["e-in"] == nil {
		t.Fatalf("%+v", got)
	}
}

func TestOutboxDraftNotifiesOnce(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "e-next", State: "next", Category: task.CategoryReady, Units: []unit.Unit{r7u}})
	failed := proposed(3, "")
	failed.State = draft.Failed
	sent := proposed(4, "")
	sent.State = draft.Sent
	withOutbox(r, proposed(1, ""), proposed(2, "e-next"), failed, sent)
	got := attentions(t, r)
	if len(got) != 3 {
		t.Fatalf("%+v", got)
	}
	for _, id := range []string{"draft:1", "draft:2"} {
		a := got[id]
		d := proposed(int64(id[len(id)-1]-'0'), "")
		if a == nil || a.ID != api.NotificationID(api.CategoryOutbox, id, d.UpdatedAt.UTC().Format(stampToken)) || a.Category != "OUTBOX" ||
			!slices.Equal(actionIDs(a), []string{"open", "task:dismiss"}) || a.Body != "comment: Thanks, merging." {
			t.Errorf("%s: %+v", id, a)
		}
	}
	if a := got["draft:3"]; a == nil || !slices.Equal(actionIDs(a), []string{"open", "task:dismiss"}) {
		t.Errorf("failed %+v", a)
	}
}

func TestProposalAndDraftNotifyOnce(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, nil)
	r.put(t, task.Task{ID: "e-prop", State: "inbox", Category: task.CategoryInbox, Title: "nudge ana?", Units: []unit.Unit{r7u}, Origin: task.OriginMessage})
	before := attentions(t, r)["e-prop"]
	withOutbox(r, proposed(1, "e-prop"))
	got := attentions(t, r)
	a := got["e-prop"]
	if len(got) != 1 || a == nil || !slices.Equal(actionIDs(a), actionIDs(before)) || slices.Contains(actionIDs(a), "task:approve") {
		t.Fatalf("%+v", got)
	}
	if before == nil || before.ID == a.ID {
		t.Fatalf("the draft did not key the entry's notification: %+v then %+v", before, a)
	}
}

func TestNoNotificationForADisabledPlugin(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, func(d *Deps) { d.PluginEnabled = func(p unit.ProviderID) bool { return p != unit.GitHub } })
	r.put(t,
		task.Task{ID: "e-gh", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u}},
		task.Task{ID: "e-mixed", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{r7u, {Provider: "slack", Key: "T000/C000/1.2"}}},
		task.Task{ID: "e-capture", State: "inbox", Category: task.CategoryInbox},
	)
	withOutbox(r, proposed(1, ""))
	got := attentions(t, r)
	if len(got) != 2 || got["e-gh"] != nil || got["draft:1"] != nil || got["e-mixed"] == nil || got["e-capture"] == nil {
		t.Fatalf("%+v", got)
	}
}

func TestAskedTurnNotifiesOnce(t *testing.T) {
	t.Parallel()
	r := newNotifyRig(t)
	r.settle(t, r7u)
	if es, _ := r.db.Tasks().TasksOf(context.Background(), r7u); len(es) != 1 || es[0].State != task.State("next") {
		t.Fatalf("the review request's entry %+v", es)
	}
	r.asks(t, turn.Asked, draftQuestion("publish?"))
	row := r.settle(t, r7u)
	if row.Question == nil {
		t.Fatalf("no question on %+v", row)
	}
	got := r.notified(t)
	if len(got) != 1 {
		t.Fatalf("%d notifications: %+v", len(got), got)
	}
	for _, e := range got {
		if _, _, asks := e.MessageOf(); !asks || e.Attention.Category != string(api.QuestionCategory(row.Question.RecordID)) {
			t.Fatalf("%+v", e)
		}
	}
}

func TestHeldTaskIdStableAcrossRestart(t *testing.T) {
	t.Parallel()
	r := newNotifyRig(t)
	r.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundHeld, Key: "cap", At: catalogNow, Data: "daily cap"})
	r.settle(t, r7u)
	first := r.notified(t)
	if len(first) != 1 {
		t.Fatalf("%+v", first)
	}
	d := r.s.deps
	d.Now = func() time.Time { return catalogNow.Add(time.Hour) }
	again := New(d)
	again.token = ""
	stopOnCleanup(t, again)
	r.s = again
	second := r.notified(t)
	for id, e := range first {
		cat, _, _ := api.ParseNotificationID(e.Attention.ID)
		if cat != api.CategoryHeld || second[id].Attention == nil || second[id].Attention.ID != e.Attention.ID {
			t.Fatalf("%s then %+v", e.Attention.ID, second[id].Attention)
		}
	}
}

// TestLiveTasksNotifyAsTheRows checks over the seeded live-size store that every row notification
// is one task notification of the same unit, and no task notifies where no row did.
func TestLiveTasksNotifyAsTheRows(t *testing.T) {
	t.Parallel()
	s, f := newLiveServer(t)
	ctx := context.Background()
	ledger := &coreitems.Ledger{Store: f.DB.Items(), Clock: fixedClock(f.Now)}
	me := task.Me{Keys: []string{"github:user-01"}}
	g := coretasks.New(withLens(coretasks.Deps{States: taskstest.BundledStates, Store: f.DB.Tasks(), Items: ledger, Facts: coretasks.CardFacts{Cards: f.DB.Cards(), Me: func() task.Me { return me }},
		Me: me, Clock: fixedClock(f.Now)}, nil, nil))
	s.deps.Tasks, s.deps.Items = g, ledger
	s.deps.Messages = &messages.Service{Store: inventory.NewMessages(f.DB), Books: f.Turns, Turns: s.deps.Turns, Tasks: g,
		Intents: turnstest.Intents(), Clock: fixedClock(f.Now)}
	serveMessageGates(s, f.DB.Tasks(), g)
	s.registerMessages()
	var rows []api.Row
	if code := get(t, s, api.Rows, &rows); code != 200 {
		t.Fatalf("%d", code)
	}
	var units []unit.Unit
	byUnit := map[unit.Unit]*api.Attention{}
	for _, row := range rows {
		units = append(units, row.Unit)
		byUnit[row.Unit] = rowAttention(t, s, row)
		its, err := s.cards().Items(ctx, row)
		if err != nil {
			t.Fatal(err)
		}
		if c, err := s.cards().Card(ctx, row); err == nil {
			c.Unit = row.Unit
			if _, err := f.DB.Cards().Save(ctx, c, f.Now); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := ledger.Reconcile(ctx, row.Unit, its, unit.Anchor{Unit: row.Unit, Ref: "row:1"}, items.Superseded); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.deps.Messages.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.Sync(ctx, units); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got := map[unit.Unit][]*api.Attention{}
	tasks := 0
	for _, v := range storedViews() {
		var p api.TasksPage
		if code := get(t, s, api.TasksPath+"?view="+v, &p); code != 200 {
			t.Fatalf("%s: %d", v, code)
		}
		tasks += len(p.Tasks)
		for _, e := range p.Tasks {
			if e.Attention == nil || v == "focus" {
				continue
			}
			if len(e.Units) == 0 {
				t.Errorf("%s notifies with no unit: %+v", e.ID, e.Attention)
				continue
			}
			got[e.Units[0]] = append(got[e.Units[0]], e.Attention)
		}
	}
	t.Logf("%d rows, %d entries over every view served in %s", len(rows), tasks, time.Since(start))
	notified, answered := 0, 0
	for u, old := range byUnit {
		news := got[u]
		switch {
		case old == nil && len(news) > 0:
			t.Errorf("%s notifies %s, its row nothing", u.Label(), news[0].ID)
		case old != nil && len(news) == 0 && answerWaits(ctx, t, f, u):
			answered++
		case old != nil && len(news) != 1:
			t.Errorf("%s: the row notifies %s, the entries %d times", u.Label(), old.ID, len(news))
		case old != nil:
			notified++
			if news[0].Title != old.Title || news[0].Body != old.Body {
				t.Errorf("%s: entry %q %q, row %q %q", u.Label(), news[0].Title, news[0].Body, old.Title, old.Body)
			}
		}
	}
	if notified == 0 {
		t.Fatal("no seeded row notifies")
	}
	t.Logf("%d notifications compared, %d questions answered and waiting for their session", notified, answered)
}

// answerWaits says whether u's asked turn holds an answer, pending or queued, that its session has not taken
// yet: the row still asks, the Inbox message counts it answered.
func answerWaits(ctx context.Context, t *testing.T, f *storetest.Fixture, u unit.Unit) bool {
	t.Helper()
	books, err := f.Turns.Books(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	b := books[u]
	for _, tu := range b.Turns {
		if tu.State != turn.Asked {
			continue
		}
		last, _ := b.LastRecord(tu.Seq)
		for _, a := range b.Answers {
			if a.TurnSeq == tu.Seq && (a.Route == turn.Pending || a.Route == turn.Queued) && (last == nil || a.At.After(last.At)) {
				return true
			}
		}
	}
	return false
}

func TestADraftOfASilentInboxTaskNotifiesItself(t *testing.T) {
	t.Parallel()
	r := newTasksRig(t, func(d *Deps) { d.PluginEnabled = func(p unit.ProviderID) bool { return p != "slack" } })
	slack := unit.Unit{Provider: "slack", Key: "T000/C000/1.2"}
	r.put(t, task.Task{ID: "e-off", State: "inbox", Category: task.CategoryInbox, Units: []unit.Unit{slack}}, task.Task{ID: "e-in", State: "inbox", Category: task.CategoryInbox})
	failed := proposed(2, "e-in")
	failed.State = draft.Failed
	withOutbox(r, proposed(1, "e-off"), failed)
	if got := attentions(t, r); len(got) != 3 || got["draft:1"] == nil || got["draft:2"] == nil || slices.Contains(actionIDs(got["e-in"]), "task:approve") {
		t.Fatalf("%+v", got)
	}
}

func TestAStaleQuestionMessageDoesNotNotifyTwice(t *testing.T) {
	t.Parallel()
	r := newNotifyRig(t)
	r.asks(t, turn.Asked, draftQuestion("publish?"))
	r.settle(t, r7u)
	ctx := context.Background()
	e, err := r.g.Open(ctx, coretasks.OpenRequest{Title: "an older question", Unit: &r7u, Origin: task.OriginMessage})
	if err != nil {
		t.Fatal(err)
	}
	stale := dmessage.Message{TaskID: string(e.ID), Unit: r7u, Kind: dmessage.Question, Text: "an older question",
		Origin: dmessage.Origin{Kind: dmessage.OriginTurn, TurnSeq: 99}, CreatedAt: catalogNow}
	m, err := r.s.deps.Messages.Store.Upsert(ctx, stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.deps.Gates.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Task: e.ID, Unit: r7u, Subject: strconv.FormatInt(m.ID, 10)}, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.notified(t); len(got) != 1 || got[string(e.ID)].Attention != nil {
		t.Fatalf("%d notifications: %+v", len(got), got)
	}
}

func TestAnotherDerivedTaskOfANotifyingRowStaysSilent(t *testing.T) {
	t.Parallel()
	r := newNotifyRig(t)
	r.events(t, dinv.Event{PR: r7, Kind: dinv.KindRoundHeld, Key: "cap", At: catalogNow, Data: "daily cap"})
	r.settle(t, r7u)
	other := task.Task{ID: "e-other", State: "inbox", Category: task.CategoryInbox, Title: "look at it", Units: []unit.Unit{r7u},
		Items: []task.ItemRef{{Unit: r7u, Item: "gh:attention"}}, Origin: task.OriginDerived, Created: catalogNow.Add(time.Minute), Updated: catalogNow}
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: other}}); err != nil {
		t.Fatal(err)
	}
	got := r.notified(t)
	if len(got) != 1 || got["e-other"].Attention != nil {
		t.Fatalf("%d notifications: %+v", len(got), got)
	}
	for _, e := range got {
		if cat, _, _ := api.ParseNotificationID(e.Attention.ID); cat != api.CategoryHeld {
			t.Fatalf("%+v", e.Attention)
		}
	}
}

// storedViews are the views the tasks store serves; review needs its source.
// storedViews are the bundled views that list something.
func storedViews() []string {
	var out []string
	for _, v := range bundledLoaded().Views.Views {
		if catalog.Lists(bundledLoaded().Views, v.ID) {
			out = append(out, v.ID)
		}
	}
	return out
}

func TestANotificationOffersOnlyTheVerbsTheProfileOffers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tasks.toml"), []byte("[verbs.dismiss]\noffer = '!task.id.startsWith(\"draft:\")'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := cacheOver(t, dir)
	r := newTasksRig(t, func(d *Deps) { d.Intents = cache.Get })
	withOutbox(r, proposed(1, ""))
	if a := attentions(t, r)["draft:1"]; a == nil || !slices.Equal(actionIDs(a), []string{"open"}) {
		t.Errorf("%+v", a)
	}
}
