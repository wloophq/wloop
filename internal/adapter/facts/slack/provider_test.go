package factslack_test

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	factslack "github.com/wloophq/wloop/internal/adapter/facts/slack"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/proposals"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

var brt = time.FixedZone("BRT", -3*60*60)

// monday is 10:00 BRT on a Monday.
var monday = time.Date(2026, 3, 2, 10, 0, 0, 0, brt)

var (
	thread = dslack.ThreadUnit("T0001", "C0001", "1700000000.000100")
	other  = dslack.ThreadUnit("T0001", "C0001", "1700000000.000200")
	dm     = dslack.Key{Team: "T0001", Conversation: "D0001", Segment: "s1"}.Unit()
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type env struct {
	t     *testing.T
	db    *sqlite.DB
	clock *clock
	p     *factslack.Provider
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "s.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &env{t: t, db: db, clock: &clock{now: monday}}
	e.p = factslack.New(db, e.clock)
	return e
}

func (e *env) entry(en gtd.Entry) {
	e.t.Helper()
	en.Origin, en.Created, en.Updated = gtd.OriginUser, monday.Add(-time.Hour), monday.Add(-time.Hour)
	if err := e.db.Entries().Apply(context.Background(), []gtd.Change{{Op: gtd.OpCreate, Entry: en}}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) item(u unit.Unit, id string, s items.State, created time.Time, owner ...items.Person) {
	e.t.Helper()
	it := items.Item{ID: id, Unit: u, Kind: items.Request, Summary: "s " + id, Origin: unit.Anchor{Unit: u, Ref: "m:" + id},
		Requester: items.Person{Key: "slack:U0009"}, Basis: items.NoBasis, State: s, Confidence: items.High,
		Provenance: items.Extracted, Assignees: owner, CreatedAt: created, UpdatedAt: created}
	if s == items.Resolved {
		it.Resolution = items.Done
	}
	if err := e.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) facts(u unit.Unit) workflow.Facts {
	e.t.Helper()
	f, err := e.p.Facts(context.Background(), u)
	if err != nil {
		e.t.Fatalf("facts of %s: %v", u, err)
	}
	return f
}

func live(id gtd.ID, s gtd.State, us ...unit.Unit) gtd.Entry {
	return gtd.Entry{ID: id, Kind: gtd.KindAction, State: s, Title: "a", Units: us}
}

func TestSlackListsTheStoredUnitsWithALiveEntry(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.entry(live("e-01", gtd.Next, thread, unit.Unit{Provider: "chat", Key: "x"}))
	e.entry(live("e-02", gtd.Waiting, dm))
	e.entry(live("e-03", gtd.Done, other))
	e.entry(live("e-04", gtd.Inbox, thread))
	got, err := e.p.List(context.Background(), dslack.Provider)
	if err != nil {
		t.Fatal(err)
	}
	if want := []unit.Unit{thread, dm}; !slices.Equal(got, want) {
		t.Fatalf("listed %v, want %v", got, want)
	}
}

func TestSlackFactsAreTheSlotsFacts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	f := e.facts(thread)
	var want, got []string
	for _, d := range builtin.Facts {
		if d.Slot == "slack" {
			want = append(want, d.Name)
		}
	}
	for k := range f {
		got = append(got, k)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("facts %v, want the slack slot's %v", got, want)
	}
}

func TestSlackFactsReadTheExtractionTheConversationAndTheItems(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	if err := e.db.Slack().PutConversations(ctx, []sqlite.ConversationRow{{ID: "C0001", Team: "T0001", Name: "general", Kind: string(dslack.KindChannel)},
		{ID: "D0001", Team: "T0001", Kind: string(dslack.KindMPIM)}}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Extraction().Save(ctx, sqlite.ExtractionSave{States: map[unit.Unit]sqlite.ExtractionState{
		thread: {StartedByViewer: true, At: monday}, other: {ClosurePosted: true, At: monday}}}); err != nil {
		t.Fatal(err)
	}
	e.item(thread, "i1", items.Open, monday.Add(-72*time.Hour))
	e.item(thread, "i2", items.Open, monday.Add(-time.Hour))
	e.item(thread, "i3", items.Open, monday.Add(-200*time.Hour), items.Person{Key: "slack:U0001"})
	e.item(thread, "i4", items.Resolved, monday.Add(-200*time.Hour))
	e.entry(live("e-01", gtd.Next, thread))
	muted := live("e-02", gtd.Scheduled, other)
	muted.Snooze = gtd.Snooze{Mute: true, Return: gtd.Next}
	e.entry(muted)
	f := e.facts(thread)
	for k, v := range map[string]any{
		"slack.channel": "C0001", "slack.conversation": "channel", "slack.started_by_me": true, "slack.closure_posted": false,
		"slack.live_entry": true, "slack.items_open": 3, "slack.items_unowned": 2, "slack.items_unowned_age_days": 1,
	} {
		if f[k] != v {
			t.Errorf("thread %s = %#v, want %#v", k, f[k], v)
		}
	}
	g := e.facts(other)
	if g["slack.closure_posted"] != true || g["slack.live_entry"] != false || g["slack.items_unowned_age_days"] != 0 {
		t.Errorf("other: closure_posted %v, live_entry %v (muted), age %v", g["slack.closure_posted"], g["slack.live_entry"], g["slack.items_unowned_age_days"])
	}
	sqlitetest.Exec(t, e.db, `UPDATE extraction_cursors SET closure_marked = 1 WHERE unit_key = ?`, thread.Key)
	if f := e.facts(thread); f["slack.closure_posted"] != true {
		t.Error("a sent draft's unit.closed is not read as closure_posted")
	}
	if f := e.facts(dm); f["slack.conversation"] != "group" || f["slack.channel"] != "D0001" {
		t.Errorf("dm segment: conversation %v, channel %v", f["slack.conversation"], f["slack.channel"])
	}
}

func TestUnownedItemsAgeInLocalWorkingDays(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	friday := time.Date(2026, 2, 27, 11, 0, 0, 0, brt)
	e.item(thread, "i1", items.Open, friday)
	e.item(other, "i2", items.Open, time.Date(2026, 2, 27, 22, 0, 0, 0, brt))
	e.entry(live("e-01", gtd.Next, thread))
	e.clock.now = monday
	if got := e.facts(other)["slack.items_unowned_age_days"]; got != 0 {
		t.Errorf("an item of Friday 22:00 BRT is not a working day old on Monday 10:00 BRT: %v", got)
	}
	for _, c := range []struct {
		now  time.Time
		want int
	}{
		{time.Date(2026, 3, 1, 23, 0, 0, 0, brt), 0},
		{time.Date(2026, 3, 2, 10, 59, 0, 0, brt), 0},
		{time.Date(2026, 3, 2, 11, 0, 0, 0, brt), 1},
		{time.Date(2026, 3, 4, 12, 0, 0, 0, brt), 3},
		{time.Date(2026, 3, 2, 13, 30, 0, 0, time.UTC), 0},
	} {
		e.clock.now = c.now
		if got := e.facts(thread)["slack.items_unowned_age_days"]; got != c.want {
			t.Errorf("at %v: %v working days, want %d", c.now, got, c.want)
		}
	}
	e.clock.now = time.Date(2026, 3, 1, 9, 0, 0, 0, brt)
	if _, err := e.p.List(context.Background(), dslack.Provider); err != nil {
		t.Fatal(err)
	}
	if got, want := e.p.Due(), time.Date(2026, 3, 2, 11, 0, 0, 0, brt); !got.Equal(want) {
		t.Errorf("due %v, want %v", got, want)
	}
}

func TestSlackSettledAtIsTheWorkflowsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	for _, d := range []struct {
		wf string
		u  unit.Unit
		s  draft.State
		at time.Time
	}{
		{"ask-a", thread, draft.Sent, monday.Add(-3 * time.Hour)},
		{"ask-a", thread, draft.Proposed, monday.Add(-time.Hour)},
		{"ask-a", other, draft.Sent, monday.Add(-time.Minute)},
		{"ask-b", thread, draft.Discarded, monday.Add(-time.Minute)},
	} {
		if _, err := e.db.Drafts().Insert(ctx, draft.Draft{Unit: d.u, Kind: draft.AskOwner, Purpose: "ask_owner", Delivery: draft.Message, Text: "x", Original: "x",
			Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: d.wf, Rule: d.wf, Key: d.u.String()}, Approval: draft.Approval{Mode: draft.Always},
			State: d.s, CreatedAt: d.at, UpdatedAt: d.at}); err != nil {
			t.Fatal(err)
		}
	}
	f, err := e.p.Scope(ctx, "ask-a", thread, e.facts(thread))
	if err != nil {
		t.Fatal(err)
	}
	if got := f["slack.settled_at"].(time.Time); !got.Equal(monday.Add(-3 * time.Hour)) {
		t.Errorf("settled_at %v", got)
	}
}

// recorder is an Outbox that keeps every proposal and holds none back.
type recorder struct{ got []outbox.Proposal }

func (r *recorder) Propose(_ context.Context, p outbox.Proposal) (draft.Draft, error) {
	r.got = append(r.got, p)
	return draft.Draft{}, nil
}

func (r *recorder) List(context.Context, outbox.Filter) ([]draft.Draft, error) { return nil, nil }

func (r *recorder) Discard(context.Context, draft.ID, string) (draft.Draft, error) {
	return draft.Draft{}, nil
}

type itemsOf struct{ db *sqlite.DB }

func (i itemsOf) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	got, err := i.db.Items().Load(ctx, []unit.Unit{u})
	return got[u], err
}

// TestSlackFactsMatchTheAskOwnerJob runs the proposals job and the slack
// facts over the same store for a week: the job asks for an owner on a unit
// exactly when it is listed with live_entry and items_unowned_age_days >= 1.
func TestSlackFactsMatchTheAskOwnerJob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	e.item(thread, "i1", items.Open, monday.Add(2*time.Hour))
	e.item(other, "i2", items.Open, monday.Add(-24*time.Hour))
	e.item(dm, "i3", items.Open, monday.Add(-72*time.Hour), items.Person{Key: "slack:U0001"})
	e.item(dm, "i4", items.Open, monday.Add(30*time.Hour))
	e.entry(live("e-01", gtd.Next, thread))
	muted := live("e-02", gtd.Scheduled, other)
	muted.Snooze = gtd.Snooze{Mute: true, Return: gtd.Next}
	e.entry(muted)
	e.entry(live("e-03", gtd.Waiting, dm))
	asked := map[string]int{}
	for h := range 7 * 24 / 3 {
		e.clock.now = monday.Add(time.Duration(h) * 3 * time.Hour)
		out := &recorder{}
		job := &proposals.Job{Outbox: out, Entries: e.db.Entries(), Items: itemsOf{e.db}, Clock: e.clock}
		if _, err := job.Run(ctx); err != nil {
			t.Fatal(err)
		}
		var byJob, byFacts []string
		for _, p := range out.got {
			if p.Origin.Rule == proposals.RuleAskOwner {
				byJob = append(byJob, p.Unit.String())
				asked[p.Unit.String()]++
			}
		}
		listed, err := e.p.List(ctx, dslack.Provider)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range listed {
			if f := e.facts(u); f["slack.live_entry"] == true && f["slack.items_unowned_age_days"].(int) >= 1 {
				byFacts = append(byFacts, u.String())
			}
		}
		slices.Sort(byJob)
		slices.Sort(byFacts)
		if !slices.Equal(byJob, byFacts) {
			t.Errorf("%s: the job asked on %v, the facts call for %v", e.clock.now.Format(time.DateTime), byJob, byFacts)
		}
	}
	if asked[thread.String()] == 0 || asked[dm.String()] == 0 || asked[other.String()] != 0 {
		t.Errorf("the scenario's asks: %v", asked)
	}
}
