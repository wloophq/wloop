package sqlite

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var (
	wfAt   = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	wfUnit = unit.Unit{Provider: unit.Session, Key: "term_1"}
)

type ledgerBackend struct {
	automation.Ledger
	log *change.Log
}

func (b ledgerBackend) Changes() port.Changes { return b.log }

type eventsBackend struct {
	automation.Events
	t  *testing.T
	db *DB
}

func (b eventsBackend) Append(evs ...workflow.Event) []workflow.Event {
	ctx := context.Background()
	head, err := b.Head(ctx)
	if err != nil {
		b.t.Fatal(err)
	}
	pevs := make([]port.Event, 0, len(evs))
	for _, ev := range evs {
		pevs = append(pevs, port.Event{Unit: ev.Unit, Kind: ev.Kind, Key: ev.Key, Data: ev.Data, At: ev.At})
	}
	if err := New(b.db).SaveFailure(ctx, pevs...); err != nil {
		b.t.Fatal(err)
	}
	out, err := b.Since(ctx, head, nil)
	if err != nil {
		b.t.Fatal(err)
	}
	return out
}

func openLogged(t *testing.T, path string) (*DB, *change.Log) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, log
}

func TestLedgerContract(t *testing.T) {
	t.Parallel()
	automationtest.RunLedger(t, func(t *testing.T) automationtest.LedgerBackend {
		db, log := openLogged(t, filepath.Join(t.TempDir(), "ledger.db"))
		return ledgerBackend{db.Ledger(), log}
	})
}

func TestEventsContract(t *testing.T) {
	t.Parallel()
	automationtest.RunEvents(t, func(t *testing.T) automationtest.EventsBackend {
		db, _ := openTemp(t)
		return eventsBackend{db.Events(), t, db}
	})
}

func pendingItem(wf string, u unit.Unit) workflow.Item {
	k := workflow.Key{Workflow: wf, Unit: u, Value: "0123456789abcdef"}
	ev := workflow.Event{ID: 9, Unit: u, Kind: "merged", Key: "m", Data: "d", At: wfAt}
	return workflow.Item{ID: k.ID(), Key: k, Workflow: wf, Unit: u, Decision: workflow.DecisionPending, Reason: "idle",
		Steps: []workflow.Step{{Use: "session.close", Reach: workflow.ReachLocal, Args: map[string]any{"reason": "idle"}}},
		Gain:  workflow.Gain{MemoryBytes: 10, DiskBytes: 20},
		Why: workflow.Why{Workflow: wf, Trigger: workflow.TriggerSchedule, Event: &ev, FiredAt: wfAt.Add(-time.Hour),
			Matched: []workflow.Fact{
				{Name: "session.closed", Value: false},
				{Name: "session.count", Value: 3},
				{Name: "session.count64", Value: int64(4)},
				{Name: "session.idle_for", Value: 2 * time.Hour},
				{Name: "session.idle_since", Value: wfAt.Add(-2 * time.Hour)},
				{Name: "session.labels", Value: []string{"a", "b"}},
				{Name: "session.missing", Value: nil},
				{Name: "session.state", Value: "done"},
			}},
		Default: workflow.OutcomeRun, Deadline: wfAt.Add(5 * time.Minute), Guidance: "careful",
		Mode: workflow.ModeApply, Attempts: 1, Error: "e", At: wfAt}
}

func TestAPlanSurvivesReopeningTheStore(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "plan.db")
	ctx := context.Background()
	want := pendingItem("session-idle", wfUnit)
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ledger().Save(ctx, automation.PlanWrite{Workflows: []string{want.Workflow}, Items: []workflow.Item{want}}); err != nil {
		t.Fatal(err)
	}
	run := workflow.Run{Key: workflow.Key{Workflow: "w", Unit: wfUnit, Value: "v"}, Status: workflow.RunFailed, Attempts: 2, Error: "boom", At: wfAt}
	if err := db.Ledger().Record(ctx, run, workflow.Item{ID: run.Key.ID(), Key: run.Key, Workflow: "w", Unit: wfUnit,
		Decision: workflow.DecisionRun, Mode: workflow.ModeApply, At: wfAt}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	again, _ := openLogged(t, path)
	got, err := again.Ledger().Items(ctx, automation.Filter{ID: want.ID})
	if err != nil || len(got) != 1 {
		t.Fatalf("Items = %v, %v", got, err)
	}
	g, w := mustJSON(t, got[0]), mustJSON(t, want)
	if g != w {
		t.Errorf("the item read back\n%s\nwant\n%s", g, w)
	}
	for i, f := range got[0].Why.Matched {
		if want, got := want.Why.Matched[i].Value, f.Value; !factEqual(got, want) {
			t.Errorf("fact %s = %#v, want %#v", f.Name, got, want)
		}
	}
	r, ok, err := again.Ledger().Run(ctx, run.Key)
	if err != nil || !ok || r != run {
		t.Errorf("Run = %+v, %v, %v; want %+v", r, ok, err, run)
	}
}

func factEqual(a, b any) bool {
	switch x := a.(type) {
	case []string:
		y, ok := b.([]string)
		return ok && slices.Equal(x, y)
	case time.Time:
		y, ok := b.(time.Time)
		return ok && x.Equal(y)
	}
	return a == b
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCursorAndLastFireSurviveReopening(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cursor.db")
	ctx := context.Background()
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	fire := wfAt.Add(-3 * time.Hour)
	if err := db.Events().Advance(ctx, "nightly", 42, fire); err != nil {
		t.Fatal(err)
	}
	if err := db.Events().Advance(ctx, "on-merge", 7, time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	again, _ := openLogged(t, path)
	cur, last, ok, err := again.Events().Cursor(ctx, "nightly")
	if err != nil || !ok || cur != 42 || !last.Equal(fire) {
		t.Errorf("Cursor(nightly) = %d, %v, %v, %v", cur, last, ok, err)
	}
	cur, last, ok, err = again.Events().Cursor(ctx, "on-merge")
	if err != nil || !ok || cur != 7 || !last.IsZero() {
		t.Errorf("Cursor(on-merge) = %d, %v, %v, %v", cur, last, ok, err)
	}
}

func TestClassificationIsCachedByFingerprint(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	c := db.Classifications()
	u := pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()
	if _, ok, err := c.Output(ctx, u, "triage", "fp1"); ok || err != nil {
		t.Fatalf("Output before any save: %v, %v", ok, err)
	}
	if err := c.Save(ctx, u, "triage", "fp1", json.RawMessage(`{"kind":"bug"}`), wfAt); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(ctx, u, "triage", "fp1", json.RawMessage(`not json`), wfAt); err == nil {
		t.Error("saved an output that is not JSON")
	}
	out, ok, err := c.Output(ctx, u, "triage", "fp1")
	if err != nil || !ok || string(out) != `{"kind":"bug"}` {
		t.Errorf("Output(fp1) = %s, %v, %v", out, ok, err)
	}
	for _, miss := range []struct{ wf, fp string }{{"triage", "fp2"}, {"other", "fp1"}} {
		if _, ok, err := c.Output(ctx, u, miss.wf, miss.fp); ok || err != nil {
			t.Errorf("Output(%s, %s) hit: %v", miss.wf, miss.fp, err)
		}
	}

	loc := time.FixedZone("BRT", -3*3600)
	lateEvening := time.Date(2026, 10, 21, 22, 0, 0, 0, loc)
	spend := func(at time.Time, limit int) bool {
		t.Helper()
		ok, err := c.Spend(ctx, at, limit)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if got := []bool{spend(lateEvening, 2), spend(lateEvening, 2), spend(lateEvening, 2)}; !slices.Equal(got, []bool{true, true, false}) {
		t.Errorf("three spends under a cap of 2 = %v", got)
	}
	if spend(lateEvening.Add(3*time.Hour), 0) {
		t.Error("a cap of 0 let a call through")
	}
	if !spend(lateEvening.Add(3*time.Hour), 2) {
		t.Error("the next local day starts full")
	}
	if n, err := c.Spent(ctx, lateEvening.Add(-20*time.Hour)); err != nil || n != 2 {
		t.Errorf("Spent on the local day = %d, %v; want 2", n, err)
	}
	if n, err := c.Spent(ctx, lateEvening.Add(3*time.Hour)); err != nil || n != 1 {
		t.Errorf("Spent on the next local day = %d, %v; want 1", n, err)
	}
}

func TestTheClassifyCapHoldsUnderConcurrentSpends(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	c := db.Classifications()
	var wg sync.WaitGroup
	var mu sync.Mutex
	passed := 0
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := c.Spend(context.Background(), wfAt, 5)
			if err != nil {
				t.Error(err)
			}
			if ok {
				mu.Lock()
				passed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if passed != 5 {
		t.Errorf("%d spends passed a cap of 5", passed)
	}
}

func TestStepArgsReadBackAsStrings(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	it := pendingItem("w", wfUnit)
	it.Steps[0].Args = map[string]any{"reason": "idle", "n": int64(5), "on": true}
	if err := db.Ledger().Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{it}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Ledger().Items(ctx, automation.Filter{ID: it.ID})
	if err != nil || len(got) != 1 {
		t.Fatalf("Items = %v, %v", got, err)
	}
	want := map[string]any{"reason": "idle", "n": "5", "on": "true"}
	if args := got[0].Steps[0].Args; !maps.Equal(args, want) {
		t.Errorf("args = %#v, want %#v", args, want)
	}
	it.Steps[0].Args = map[string]any{"schema": map[string]any{"kind": "string"}}
	if err := db.Ledger().Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{it}}); err == nil {
		t.Error("saved a step arg that is a table")
	}
}

func TestFetchesMarkTheLastFetch(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	f := db.Fetches()
	if last, err := f.Last(ctx, "ws1"); err != nil || !last.IsZero() {
		t.Fatalf("Last of a workspace never fetched = %v, %v", last, err)
	}
	for _, at := range []time.Time{wfAt, wfAt.Add(time.Hour)} {
		if err := f.Mark(ctx, "ws1", at); err != nil {
			t.Fatal(err)
		}
	}
	if last, err := f.Last(ctx, "ws1"); err != nil || !last.Equal(wfAt.Add(time.Hour)) {
		t.Errorf("Last = %v, %v", last, err)
	}
	if last, _ := f.Last(ctx, "ws2"); !last.IsZero() {
		t.Errorf("another workspace reads %v", last)
	}
}

func TestPlanWritesNoteGateOnlyWhenPendingChanges(t *testing.T) {
	t.Parallel()
	db, log := openLogged(t, filepath.Join(t.TempDir(), "gate.db"))
	ctx := context.Background()
	l := db.Ledger()
	p := pendingItem("w", wfUnit)
	blocked := pendingItem("w", unit.Unit{Provider: unit.Workspace, Key: "ws1"})
	blocked.Decision, blocked.Default, blocked.Deadline = workflow.DecisionBlocked, "", time.Time{}
	save := func(items ...workflow.Item) port.Change {
		t.Helper()
		got, n, err := noted(log, func() error { return l.Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: items}) })
		if err != nil {
			t.Fatal(err)
		}
		if n > 1 {
			t.Fatalf("one save noted %d times", n)
		}
		return got
	}
	if c := save(p); c.Topics != port.TopicGate || !slices.Equal(c.Units, []unit.Unit{wfUnit}) {
		t.Errorf("a new pending item noted %+v", c)
	}
	if c := save(p, blocked); !c.Empty() {
		t.Errorf("a new blocked item noted %+v", c)
	}
	other := p
	other.Default = workflow.OutcomeWait
	if c := save(other, blocked); c.Topics != port.TopicGate {
		t.Errorf("a pending item whose default changed noted %+v", c)
	}
	if c := save(blocked); c.Topics != port.TopicGate || !slices.Equal(c.Units, []unit.Unit{wfUnit}) {
		t.Errorf("a dropped pending item noted %+v", c)
	}
}

func TestAnUnchangedPlanNotesNothing(t *testing.T) {
	t.Parallel()
	db, log := openLogged(t, filepath.Join(t.TempDir(), "same.db"))
	ctx := context.Background()
	l := db.Ledger()
	p := pendingItem("w", wfUnit)
	w := automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{p}}
	if err := l.Save(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, n, err := noted(log, func() error { return l.Save(ctx, w) })
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("an unchanged plan noted %d times: %+v", n, got)
	}
	got, n, err = noted(log, func() error {
		return l.Record(ctx, workflow.Run{Key: p.Key, Status: workflow.RunFailed, Attempts: 1, At: wfAt}, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("a run that keeps the pending item as it was noted %d times: %+v", n, got)
	}
}

func TestEngineEventsShareTheSaveTransaction(t *testing.T) {
	t.Parallel()
	db, log := openLogged(t, filepath.Join(t.TempDir(), "tx.db"))
	ctx := context.Background()
	l := db.Ledger()
	p := pendingItem("w", wfUnit)
	planned := port.Event{Unit: wfUnit, Kind: workflow.KindPlanned, Key: p.ID + ":pending", Data: "idle", At: wfAt}
	got, n, err := noted(log, func() error {
		return l.Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{p}}, planned)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || got.Topics != port.TopicGate|port.TopicLoopEvents {
		t.Errorf("a save with its event noted %d times %+v, want once with gate and loop events", n, got)
	}

	exec(t, db, `CREATE TRIGGER refuse_boom BEFORE INSERT ON events WHEN NEW.kind = 'boom' BEGIN SELECT RAISE(ABORT, 'boom refused'); END`)
	boom := port.Event{Unit: wfUnit, Kind: "boom", Key: "b", At: wfAt}
	q := pendingItem("w", unit.Unit{Provider: unit.Session, Key: "term_2"})
	run := workflow.Run{Key: p.Key, Status: workflow.RunDone, At: wfAt}
	for name, write := range map[string]func() error{
		"Save": func() error {
			return l.Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{q}}, boom)
		},
		"Record": func() error { return l.Record(ctx, run, p, boom) },
	} {
		got, n, err := noted(log, write)
		if err == nil {
			t.Fatalf("%s: the event write did not fail", name)
		}
		if n != 0 {
			t.Errorf("%s: a failed write noted %+v", name, got)
		}
		items, err := l.Items(ctx, automation.Filter{})
		if err != nil || len(items) != 1 || items[0].ID != p.ID {
			t.Errorf("%s: items after a failed event write = %v, %v; want only %s", name, items, err, p.ID)
		}
		if _, ok, _ := l.Run(ctx, p.Key); ok {
			t.Errorf("%s: the run of a failed write was stored", name)
		}
	}
}

func savedAt(db *DB, when time.Time) automation.Ledger {
	return ledger{db: db, now: func() time.Time { return when }}
}

func runItem(wf string, u unit.Unit, value string, d workflow.Decision, since time.Time) workflow.Item {
	k := workflow.Key{Workflow: wf, Unit: u, Value: value}
	it := workflow.Item{ID: k.ID(), Key: k, Workflow: wf, Unit: u, Decision: d, Mode: workflow.ModeApply, At: since}
	if d == workflow.DecisionPending {
		it.Default, it.Deadline = workflow.OutcomeWait, since.Add(time.Hour)
	}
	return it
}

func recordRun(t *testing.T, l automation.Ledger, wf string, u unit.Unit, value string, s workflow.RunStatus, when time.Time) workflow.Key {
	t.Helper()
	it := runItem(wf, u, value, workflow.DecisionRun, when)
	if err := l.Record(context.Background(), workflow.Run{Key: it.Key, Status: s, At: when}, it); err != nil {
		t.Fatal(err)
	}
	return it.Key
}

func TestRetentionPrunesWorkflowRows(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	exec(t, db, `INSERT INTO sessions (session_id) VALUES (?)`, wfUnit.Key)
	old := retainNow.Add(-8 * day)
	if err := savedAt(db, old).Save(ctx, automation.PlanWrite{Workflows: []string{"gone"}, Items: []workflow.Item{
		runItem("gone", wfUnit, "blocked", workflow.DecisionBlocked, old),
		runItem("gone", wfUnit, "pending", workflow.DecisionPending, old),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := savedAt(db, retainNow.Add(-day)).Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{
		runItem("w", wfUnit, "blocked-since-long", workflow.DecisionBlocked, old),
	}}); err != nil {
		t.Fatal(err)
	}
	l := savedAt(db, past)
	recordRun(t, l, "r", wfUnit, "old-done", workflow.RunDone, past)
	recordRun(t, l, "r", wfUnit, "old-dismissed", workflow.RunDismissed, retainNow.Add(-40*day))
	recordRun(t, l, "r", wfUnit, "old-failed", workflow.RunFailed, past)
	recordRun(t, l, "r", wfUnit, "new-done", workflow.RunDone, recent)
	event_(t, db, "session", "term_1", workflow.KindStepRan, "a:0", past)
	event_(t, db, "session", "term_1", workflow.KindPlanned, "a:run", recent)
	event_(t, db, "github", "acme/api#7", "merged", "m", past)
	insertPR(t, db, pr.Ref{Owner: "acme", Repo: "api", Number: 7}, "OPEN", "", "")

	got := retain(t, db, DefaultRetention)
	if want := (Retained{"plan_items": 2, "workflow_runs": 1, WorkflowEventsKey: 1}); !maps.Equal(got, want) {
		t.Errorf("retained %v, want %v", got, want)
	}
	left, err := db.Ledger().Items(ctx, automation.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range left {
		ids = append(ids, it.Workflow+"/"+it.Key.Value)
	}
	if want := []string{"gone/pending", "w/blocked-since-long"}; !slices.Equal(ids, want) {
		t.Errorf("items left %v, want %v", ids, want)
	}
	if got := rowsOf(t, db, `SELECT key_value FROM workflow_runs ORDER BY key_value`); !slices.Equal(got, []string{"new-done", "old-dismissed", "old-failed"}) {
		t.Errorf("runs left %v", got)
	}
	if want := []string{"github|acme/api#7|merged|m", "session|term_1|" + workflow.KindPlanned + "|a:run"}; !slices.Equal(remainingEvents(t, db), want) {
		t.Errorf("events left %q, want %q", remainingEvents(t, db), want)
	}
}

// TestADismissedKeyIsNotPlannedAgainAfter31Days: the engine plans no item
// for a key whose run is dismissed (automation's evaluate), so the run must
// outlive every retention pass while its unit lives.
func TestADismissedKeyIsNotPlannedAgainAfter31Days(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	live, ws := unit.Unit{Provider: unit.Session, Key: "term_live"}, unit.Unit{Provider: unit.Workspace, Key: "ws1"}
	exec(t, db, `INSERT INTO sessions (session_id) VALUES (?)`, live.Key)
	dismissedAt := retainNow.Add(-31 * day)
	l := savedAt(db, dismissedAt)
	keys := []workflow.Key{
		recordRun(t, l, "workspace-review-finished", ws, "k", workflow.RunDismissed, dismissedAt),
		recordRun(t, l, "session-idle", live, "k", workflow.RunDismissed, dismissedAt),
	}
	done := recordRun(t, l, "session-idle", live, "done", workflow.RunDone, dismissedAt)
	retain(t, db, DefaultRetention)
	for _, k := range keys {
		r, ok, err := db.Ledger().Run(ctx, k)
		if err != nil || !ok || r.Status != workflow.RunDismissed {
			t.Errorf("Run(%s) = %+v, %v, %v; want it still dismissed", k.ID(), r, ok, err)
		}
	}
	if _, ok, _ := db.Ledger().Run(ctx, done); ok {
		t.Error("a done run 31 days old was kept")
	}
}

func TestRetentionPrunesTheRunsOfClosedSessions(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	exec(t, db, `INSERT INTO sessions (session_id) VALUES ('term_live')`)
	l := savedAt(db, past)
	recordRun(t, l, "session-idle", unit.Unit{Provider: unit.Session, Key: "term_live"}, "k", workflow.RunDismissed, past)
	recordRun(t, l, "session-idle", unit.Unit{Provider: unit.Session, Key: "term_gone"}, "k", workflow.RunDismissed, past)
	recordRun(t, l, "session-idle", unit.Unit{Provider: unit.Session, Key: "term_gone_lately"}, "k", workflow.RunDismissed, recent)
	got := retain(t, db, Retention{ClosedSessions: 30 * day})
	if want := (Retained{"workflow_runs": 1}); !maps.Equal(got, want) {
		t.Errorf("retained %v, want %v", got, want)
	}
	if got := rowsOf(t, db, `SELECT unit_key FROM workflow_runs ORDER BY unit_key`); !slices.Equal(got, []string{"term_gone_lately", "term_live"}) {
		t.Errorf("runs left of %v", got)
	}
}

func TestRetentionPrunesUnitsWithoutAPullRequestRow(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	probe, touched, tracked, held, overrideOnly := "acme/probe#1", "acme/probe#2", "acme/api#3", "acme/probe#4", "acme/probe#5"
	insertPR(t, db, pr.Ref{Owner: "acme", Repo: "api", Number: 3}, "OPEN", "", "")
	l := savedAt(db, past)
	for i, key := range []string{probe, touched, tracked, held} {
		seq := 200 + i
		exec(t, db, `INSERT INTO turns (seq, provider, unit_key, intent, turn_id, state, sent_at, ended_at) VALUES (?, 'github', ?, 'review', 'r', 'done', ?, ?)`, seq, key, at(past), at(past))
		exec(t, db, `INSERT INTO turn_usage (turn_seq, model, messages, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, first_at, last_at) VALUES (?, 'opus', 1, 1, 1, 0, 0, ?, ?)`, seq, at(past), at(past))
		exec(t, db, `INSERT INTO turn_records (turn_seq, provider, unit_key, hash, at, record) VALUES (?, 'github', ?, 'h', ?, '{}')`, seq, key, at(past))
		exec(t, db, `INSERT INTO turn_answers (turn_seq, provider, unit_key, at, text, source, route) VALUES (?, 'github', ?, ?, 'yes', 'cli', 'r')`, seq, key, at(past))
		exec(t, db, `INSERT INTO intakes (provider, unit_key, intent, source, request, state, accepted_at) VALUES ('github', ?, 'review', 'cli', '{}', 'started', ?)`, key, at(past))
		event_(t, db, "github", key, "round_sent", "r", past)
		u := unit.Unit{Provider: unit.GitHub, Key: key}
		recordRun(t, l, "review-follow-up", u, "dismissed", workflow.RunDismissed, past)
		recordRun(t, l, "review-follow-up", u, "failed", workflow.RunFailed, past)
	}
	for _, key := range []string{held, overrideOnly} {
		exec(t, db, `INSERT INTO overrides (provider, unit_key, hold) VALUES ('github', ?, 1)`, key)
	}
	event_(t, db, "github", touched, "turn_answered", "a", retainNow.Add(-day))
	event_(t, db, "session", "term_9", "turn_stopped", "term_9:p", past)

	got := retain(t, db, Retention{UnitlessLoopRows: 30 * day})
	if want := (Retained{UnitlessLoopRowsKey: 9}); !maps.Equal(got, want) {
		t.Errorf("retained %v, want %v (a turn, its usage, a record, an answer, an intake, an event, two runs and an item)", got, want)
	}
	for _, table := range []string{"turns", "turn_records", "turn_answers", "intakes", "events", "workflow_runs", "plan_items"} {
		keys := rowsOf(t, db, `SELECT DISTINCT unit_key FROM `+table+` WHERE provider = 'github' ORDER BY unit_key`)
		if want := []string{tracked, touched, held}; !slices.Equal(keys, want) {
			t.Errorf("%s keeps %v, want %v", table, keys, want)
		}
	}
	if keys := rowsOf(t, db, `SELECT unit_key FROM overrides ORDER BY unit_key`); !slices.Equal(keys, []string{held, overrideOnly}) {
		t.Errorf("overrides keep %v", keys)
	}
	if n := countOf(t, db, `SELECT count(*) FROM turn_usage`); n != 3 {
		t.Errorf("%d usage rows, want the 3 of the kept turns", n)
	}
	if n := countOf(t, db, `SELECT count(*) FROM events WHERE provider = 'session'`); n != 1 {
		t.Errorf("%d session events, want 1", n)
	}
	if again := retain(t, db, Retention{UnitlessLoopRows: 30 * day}); len(again) != 0 {
		t.Errorf("a second pass retained %v", again)
	}
}

func TestRetentionPrunesClassificationsAndFetches(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	ctx := context.Background()
	c, f := db.Classifications(), db.Fetches()
	u := pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()
	for _, when := range []time.Time{past, recent} {
		if err := c.Save(ctx, u, "triage", when.Format(time.RFC3339), json.RawMessage(`{}`), when); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Spend(ctx, when, 50); err != nil {
			t.Fatal(err)
		}
		if err := f.Mark(ctx, "ws-"+when.Format(time.DateOnly), when); err != nil {
			t.Fatal(err)
		}
	}
	got := retain(t, db, Retention{Classifications: 30 * day, Fetches: 30 * day})
	if want := (Retained{"classifications": 1, "classify_days": 1, "fetches": 1}); !maps.Equal(got, want) {
		t.Errorf("retained %v, want %v", got, want)
	}
	if n, _ := c.Spent(ctx, recent); n != 1 {
		t.Errorf("the recent day counts %d", n)
	}
	if _, ok, _ := c.Output(ctx, u, "triage", recent.Format(time.RFC3339)); !ok {
		t.Error("the recent classification was pruned")
	}
	if last, _ := f.Last(ctx, "ws-"+recent.Format(time.DateOnly)); !last.Equal(recent) {
		t.Errorf("the recent fetch reads %v", last)
	}
}
