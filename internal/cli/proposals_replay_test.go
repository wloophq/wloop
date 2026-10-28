package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// replayGolden holds the drafts the proposals job proposed, withdrew and
// had settled over the seeded fourteen days, captured while the job ran.
const replayGolden = "testdata/proposals-replay.golden"

var (
	replayStart = time.Date(2026, 10, 5, 9, 0, 0, 0, saoPaulo())
	replayDays  = 14
	replayTick  = 3 * time.Hour

	rpAna = items.Person{Key: "slack:U000ANA", Name: "Ana"}
	rpBob = items.Person{Key: "slack:U000BOB", Name: "Bob"}
	rpCid = items.Person{Key: "slack:U000CID", Name: "Cid"}
	rpDan = items.Person{Key: "slack:U000DAN", Name: "Dan"}
	rpEve = items.Person{Key: "slack:U000EVE", Name: "Eve"}
	rpFay = items.Person{Key: "slack:U000FAY", Name: "Fay"}
	// rpLegal is a waiting the user typed by hand, with no provider key.
	rpLegal = items.Person{Key: "Legal"}
)

func saoPaulo() *time.Location {
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		panic(err)
	}
	return loc
}

func rpThread(n int) unit.Unit {
	return dslack.ThreadUnit("T000TEAM", "C000CHAN", fmt.Sprintf("1700000%03d.000100", n))
}

var (
	rpA, rpB, rpC, rpD, rpE, rpF, rpH = rpThread(1), rpThread(2), rpThread(3), rpThread(4), rpThread(5), rpThread(6), rpThread(8)
	rpK                               = rpThread(9)
	rpPR                              = unit.Unit{Provider: unit.GitHub, Key: "acme/widgets#7"}
)

// proposer is one way of proposing the drafts: the old job or the default
// workflows; pass runs it until a pass changes nothing, as the daemon does
// when its own writes wake it again.
type proposer interface {
	pass(ctx context.Context) error
}

// replay runs a proposer over a real store for fourteen simulated days in
// America/Sao_Paulo, every three hours, applying the scenario's changes and
// settling each new draft by a fixed script at the next tick: sent,
// discarded, left open, in turn. It records every proposal, withdrawal and
// settlement, the text included.
type replay struct {
	t   *testing.T
	te  *testEnv
	db  *sqlite.DB
	svc *outbox.Service
	now time.Time
	// start is the first tick; script changes the store at a tick, nil
	// for a store replayed as it is.
	start  time.Time
	script func(at time.Time)
	// counts, when set, counts what the record notes by day, workflow,
	// purpose and event, for a store whose texts stay out of any output.
	counts map[string]int

	lines   []string
	known   map[draft.ID]draft.State
	settle  []draft.ID
	settled int
	byKey   map[string]int
}

func newReplay(t *testing.T) *replay {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "replay.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := newReplayOn(t, db, replayStart)
	r.script = r.scenario
	return r
}

// newReplayOn is a replay over db from start, with no scenario.
func newReplayOn(t *testing.T, db *sqlite.DB, start time.Time) *replay {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	r := &replay{t: t, te: te, db: db, now: start, start: start, known: map[draft.ID]draft.State{}, byKey: map[string]int{}}
	te.Env.Now = func() time.Time { return r.now }
	clock := clockFunc(func() time.Time { return r.now })
	ledger := te.itemsLedger(db, te.cfg, t.Logf)
	r.svc = &outbox.Service{Store: db.Drafts(), Items: ledger, Clock: clock,
		Effects: func(p unit.ProviderID) (port.Effects, error) {
			if p != dslack.Provider {
				return nil, fmt.Errorf("%s: %w", p, outbox.ErrDisabled)
			}
			return okEffects{}, nil
		}}
	return r
}

func (r *replay) item(u unit.Unit, id string, s items.State, created time.Time, waiting ...items.Person) items.Item {
	r.t.Helper()
	it := items.Item{ID: id, Unit: u, Kind: items.Request, Summary: "Item  " + id + "\nof the thread", State: s, Basis: items.NoBasis,
		Requester: items.Person{Key: "slack:U000REQ"}, Confidence: items.High, Provenance: items.Extracted, WaitingOn: waiting,
		Origin: unit.Anchor{Unit: u, Ref: "m:" + id, Permalink: "https://example.test/" + id}, CreatedAt: created, UpdatedAt: r.now}
	if s == items.Resolved {
		it.Resolution = items.Done
	}
	return r.commit(it)
}

func (r *replay) commit(it items.Item) items.Item {
	r.t.Helper()
	it.UpdatedAt = r.now
	if err := r.db.Items().Commit(context.Background(), coreitems.Commit{Items: []items.Item{it}}); err != nil {
		r.t.Fatal(err)
	}
	return it
}

func (r *replay) items(u unit.Unit) map[string]items.Item {
	r.t.Helper()
	got, err := r.db.Items().Load(context.Background(), []unit.Unit{u})
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]items.Item{}
	for _, it := range got[u] {
		out[it.ID] = it
	}
	return out
}

func (r *replay) task(e task.Task) {
	r.t.Helper()
	e.Origin, e.Created, e.Updated = task.OriginUser, r.now, r.now
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *replay) change(id task.ID, f func(*task.Task)) {
	r.t.Helper()
	e, err := r.db.Tasks().Task(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	f(&e)
	e.Updated = r.now
	if err := r.db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpUpdate, Task: e}}); err != nil {
		r.t.Fatal(err)
	}
}

func refs(its ...items.Item) []task.ItemRef {
	out := make([]task.ItemRef, len(its))
	for i, it := range its {
		out[i] = task.ItemRef{Unit: it.Unit, Item: it.ID}
	}
	return out
}

// scenario is the seeded store and its changes over the fourteen days: a
// waiting entry on two people and a hand-typed waiting, a muted waiting
// entry, one with a future resurface date, a close entry with items of two
// units, a GitHub entry and unowned items a working day old; then items
// resolve and get owners, entries end, mute, lose a person and appear.
func (r *replay) scenario(at time.Time) {
	day := func(n, hour int) time.Time {
		return replayStart.AddDate(0, 0, n).Add(time.Duration(hour-9) * time.Hour)
	}
	is := func(t time.Time) bool { return !t.After(at) && at.Before(t.Add(replayTick)) }
	before := replayStart.AddDate(0, 0, -5)
	switch {
	case at.Equal(replayStart):
		a1, a2, a3 := r.item(rpA, "a1", items.Open, before, rpAna), r.item(rpA, "a2", items.Open, before, rpBob), r.item(rpA, "a3", items.Open, before, rpAna)
		r.task(task.Task{ID: "e-a", Kind: task.KindWaiting, State: task.Waiting, Title: "a", Units: []unit.Unit{rpA}, Items: refs(a1, a2, a3),
			Waiting: &task.WaitingOn{On: []items.Person{rpAna, rpBob, rpLegal}, Since: before}})
		b1 := r.item(rpB, "b1", items.Open, before, rpCid)
		r.item(rpB, "b2", items.Open, before)
		r.task(task.Task{ID: "e-b", Kind: task.KindWaiting, State: task.Waiting, Title: "b", Units: []unit.Unit{rpB}, Items: refs(b1),
			Waiting: &task.WaitingOn{On: []items.Person{rpCid}, Since: before}, Snooze: task.Snooze{Mute: true}})
		c1 := r.item(rpC, "c1", items.Open, replayStart, rpDan)
		r.task(task.Task{ID: "e-c", Kind: task.KindWaiting, State: task.Waiting, Title: "c", Units: []unit.Unit{rpC}, Items: refs(c1),
			Waiting: &task.WaitingOn{On: []items.Person{rpDan}, Since: replayStart, Resurface: day(2, 14)}})
		d1, d2, e1 := r.item(rpD, "d1", items.Resolved, before), r.item(rpD, "d2", items.Resolved, before), r.item(rpE, "e1", items.Resolved, before)
		d2.Resolution = ""
		r.commit(d2)
		r.item(rpD, "d3", items.Open, before)
		r.task(task.Task{ID: "e-d", Kind: task.KindClose, State: task.Inbox, Title: "d", Units: []unit.Unit{rpD, rpE}, Items: refs(d1, d2, e1)})
		r.task(task.Task{ID: "e-g", Kind: task.KindWaiting, State: task.Waiting, Title: "g", Units: []unit.Unit{rpPR},
			Waiting: &task.WaitingOn{On: []items.Person{{Key: "github:octo"}}, Since: before}})
		f1 := r.item(rpF, "f1", items.Open, replayStart.AddDate(0, 0, -3).Add(9*time.Hour))
		r.item(rpF, "f2", items.Open, replayStart.Add(time.Hour))
		r.task(task.Task{ID: "e-f", Kind: task.KindAction, State: task.Next, Title: "f", Units: []unit.Unit{rpF}, Items: refs(f1)})
	case is(day(1, 9)):
		k1, k2 := r.item(rpK, "k1", items.Open, before, rpEve), r.item(rpK, "k2", items.Open, before, rpFay)
		r.task(task.Task{ID: "e-k", Kind: task.KindWaiting, State: task.Waiting, Title: "k", Units: []unit.Unit{rpK}, Items: refs(k1, k2),
			Waiting: &task.WaitingOn{On: []items.Person{rpEve}, Since: before}})
	case is(day(1, 15)):
		r.change("e-k", func(e *task.Task) { e.Waiting.On = append(e.Waiting.On, rpFay) })
	case is(day(2, 12)):
		a2 := r.items(rpA)["a2"]
		a2.State, a2.Resolution = items.Resolved, items.Done
		r.commit(a2)
	case is(day(3, 10)):
		f2 := r.items(rpF)["f2"]
		f2.Assignees = []items.Person{rpAna}
		r.commit(f2)
		r.item(rpF, "f3", items.Open, day(3, 10))
	case is(day(4, 12)):
		h1 := r.item(rpH, "h1", items.Resolved, day(4, 11))
		r.task(task.Task{ID: "e-h", Kind: task.KindClose, State: task.Next, Title: "h", Units: []unit.Unit{rpH}, Items: refs(h1)})
	case is(day(7, 9)):
		r.change("e-b", func(e *task.Task) { e.Snooze.Mute = false })
		r.change("e-c", func(e *task.Task) { e.State = task.Done })
	case is(day(8, 12)):
		r.change("e-a", func(e *task.Task) { e.Waiting.On = []items.Person{rpBob, rpLegal} })
		a4 := r.item(rpA, "a4", items.Open, day(8, 12), rpBob)
		r.change("e-a", func(e *task.Task) { e.Items = append(e.Items, refs(a4)...) })
	case is(day(9, 15)):
		r.change("e-d", func(e *task.Task) { e.State = task.Someday })
		r.change("e-h", func(e *task.Task) { e.State = task.Done })
	case is(day(10, 9)):
		f3 := r.items(rpF)["f3"]
		f3.State, f3.Resolution = items.Resolved, items.Answered
		r.commit(f3)
	case is(day(11, 9)):
		b2 := r.items(rpB)["b2"]
		b2.WaitingOn = []items.Person{rpCid}
		r.commit(b2)
	}
}

func (r *replay) stamp(format string, args ...any) {
	r.lines = append(r.lines, r.now.Format("Mon 01-02 15:04")+" "+fmt.Sprintf(format, args...))
}

// record notes the drafts the pass proposed and withdrew, in a stable order
// within the tick, and queues the new ones for settling.
func (r *replay) record() {
	r.t.Helper()
	ds, err := r.svc.List(context.Background(), outbox.Filter{})
	if err != nil {
		r.t.Fatal(err)
	}
	slices.SortFunc(ds, func(a, b draft.Draft) int {
		return strings.Compare(a.Purpose+"|"+a.Origin.Key, b.Purpose+"|"+b.Origin.Key)
	})
	var proposed, withdrawn []string
	for _, d := range ds {
		was, seen := r.known[d.ID]
		r.known[d.ID] = d.State
		switch {
		case !seen && r.counts != nil:
			r.counts[r.now.Format("01-02")+" "+workflowOf(d)+" "+d.Purpose+" proposed"]++
			r.settle = append(r.settle, d.ID)
		case was == draft.Proposed && d.State == draft.Discarded && d.Reason != "" && r.counts != nil:
			r.counts[r.now.Format("01-02")+" "+workflowOf(d)+" "+d.Purpose+" withdrawn "+d.Reason]++
		case !seen:
			proposed = append(proposed, fmt.Sprintf("propose %s %s on %s task=%q key=%s items=%s approval=%s text=%q",
				d.Purpose, d.Delivery, d.Unit, d.Task, d.Origin.Key, strings.Join(d.Items, ","), d.Approval.Mode, d.Text))
			r.settle = append(r.settle, d.ID)
		case was == draft.Proposed && d.State == draft.Discarded && d.Reason != "":
			withdrawn = append(withdrawn, fmt.Sprintf("withdraw %s key=%s reason=%s", d.Purpose, d.Origin.Key, d.Reason))
		}
	}
	for _, l := range append(proposed, withdrawn...) {
		r.stamp("%s", l)
	}
}

// settleQueued settles the drafts proposed at the last tick that are still
// proposed: the n-th is sent, discarded or left open as n mod 3, except
// entry e-k's: each person's first is discarded, the next left open.
func (r *replay) settleQueued() {
	r.t.Helper()
	ctx := context.Background()
	queued := r.settle
	r.settle = nil
	sent := false
	for _, id := range queued {
		n := r.settled
		r.settled++
		ds, err := r.svc.List(ctx, outbox.Filter{})
		if err != nil {
			r.t.Fatal(err)
		}
		i := slices.IndexFunc(ds, func(d draft.Draft) bool { return d.ID == id })
		if i < 0 || ds[i].State != draft.Proposed {
			continue
		}
		d := ds[i]
		if strings.HasPrefix(d.Origin.Key, "e-k:") {
			r.byKey[d.Origin.Key]++
			n = 2
			if r.byKey[d.Origin.Key] == 1 {
				n = 1
			}
		}
		switch n % 3 {
		case 0:
			if _, err := r.svc.Approve(ctx, id, nil); err != nil {
				r.t.Fatal(err)
			}
			sent = true
			r.stamp("send %s key=%s", d.Purpose, d.Origin.Key)
		case 1:
			if _, err := r.svc.Discard(ctx, id, ""); err != nil {
				r.t.Fatal(err)
			}
			r.known[id] = draft.Discarded
			r.stamp("discard %s key=%s", d.Purpose, d.Origin.Key)
		}
	}
	if sent {
		if _, failed, err := r.svc.Send(ctx); err != nil || failed > 0 {
			r.t.Fatalf("send: %d failed, %v", failed, err)
		}
	}
	ds, err := r.svc.List(ctx, outbox.Filter{})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, d := range ds {
		r.known[d.ID] = d.State
	}
}

// run replays the fourteen days through p and returns the record.
func (r *replay) run(p proposer) string {
	r.t.Helper()
	return r.runFrom(p, time.Time{}, nil)
}

// runFrom replays the fourteen days through p until at, then through the
// proposer then returns.
func (r *replay) runFrom(p proposer, at time.Time, then func() proposer) string {
	r.t.Helper()
	ctx := context.Background()
	for r.now = r.start; r.now.Before(r.start.AddDate(0, 0, replayDays)); r.now = r.now.Add(replayTick) {
		if then != nil && !r.now.Before(at) {
			p, then = then(), nil
		}
		if r.script != nil {
			r.script(r.now)
		}
		r.settleQueued()
		if err := p.pass(ctx); err != nil {
			r.t.Fatalf("%s: %v", r.now, err)
		}
		r.record()
	}
	return strings.Join(r.lines, "\n") + "\n"
}

// draftsState is every draft's id and state, to tell when a pass changed
// nothing.
func (r *replay) draftsState() string {
	r.t.Helper()
	ds, err := r.svc.List(context.Background(), outbox.Filter{})
	if err != nil {
		r.t.Fatal(err)
	}
	var b strings.Builder
	for _, d := range ds {
		fmt.Fprintf(&b, "%d:%s;", d.ID, d.State)
	}
	es, err := r.db.Tasks().Tasks(context.Background(), task.Query{View: task.ViewOpen})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, e := range es {
		fmt.Fprintf(&b, "%s:%s;", e.ID, e.Updated)
	}
	return b.String()
}

// untilQuiet runs pass until the drafts and entries stop changing.
func (r *replay) untilQuiet(pass func() error) error {
	for range 5 {
		before := r.draftsState()
		if err := pass(); err != nil {
			return err
		}
		if r.draftsState() == before {
			return nil
		}
	}
	return fmt.Errorf("the drafts did not settle in five passes")
}

func readGolden(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(replayGolden)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// firstDiff is the first line where got and want differ.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range max(len(g), len(w)) {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d:\n got  %s\n want %s", i+1, gl, wl)
		}
	}
	return ""
}

// workflowOf is the workflow a draft serves, the proposals job's rules
// named as the workflows that replaced them.
func workflowOf(d draft.Draft) string {
	if d.Origin.Workflow != "" {
		return d.Origin.Workflow
	}
	switch d.Origin.Rule {
	case "stalled-nudge", "closure", "ask-owner":
		return "slack-" + d.Origin.Rule
	}
	return d.Origin.Rule
}

// prime marks the drafts already stored as known, so only what the replay
// proposes is recorded and settled.
func (r *replay) prime() {
	r.t.Helper()
	ds, err := r.svc.List(context.Background(), outbox.Filter{})
	if err != nil {
		r.t.Fatal(err)
	}
	for _, d := range ds {
		r.known[d.ID] = d.State
	}
}
