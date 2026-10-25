package automationtest

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/mark"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var at0 = time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC)

// RunUnits checks a facts provider holding at least one unit of p: every
// listed unit is of p, listed once and in order, and has facts in a single
// slot, of the fact types, with a fingerprint and a repo; an unknown unit's
// facts are automation.ErrUnitGone.
func RunUnits(t *testing.T, p unit.ProviderID, mk func(*testing.T) automation.Units) {
	ctx := context.Background()
	t.Run("ListedUnitsHaveFactsInOneSlot", func(t *testing.T) {
		us := mk(t)
		listed, err := us.List(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) == 0 {
			t.Fatalf("the provider under test lists no %s unit", p)
		}
		if !slices.IsSortedFunc(listed, compareUnits) || len(slices.CompactFunc(slices.Clone(listed), func(a, b unit.Unit) bool { return a == b })) != len(listed) {
			t.Errorf("List = %v, want sorted and distinct", listed)
		}
		for _, u := range listed {
			if u.Provider != p {
				t.Errorf("List(%s) holds %s", p, u)
			}
			facts, err := us.Facts(ctx, u)
			if err != nil {
				t.Fatalf("Facts(%s): %v", u, err)
			}
			checkFacts(t, u, facts)
		}
	})
	t.Run("AnUnknownUnitIsGone", func(t *testing.T) {
		if _, err := mk(t).Facts(ctx, unit.Unit{Provider: p, Key: "no-such-unit"}); !errors.Is(err, automation.ErrUnitGone) {
			t.Errorf("Facts of an unknown unit = %v, want ErrUnitGone", err)
		}
	})
}

func checkFacts(t *testing.T, u unit.Unit, facts workflow.Facts) {
	t.Helper()
	slot := ""
	for name, v := range facts {
		s, _, ok := strings.Cut(name, ".")
		if !ok {
			t.Errorf("%s: fact %q has no slot", u, name)
			continue
		}
		if slot == "" {
			slot = s
		} else if s != slot {
			t.Errorf("%s: facts in slots %q and %q", u, slot, s)
		}
		switch v.(type) {
		case bool, int, string, []string, time.Duration, time.Time:
		default:
			t.Errorf("%s: fact %q is a %T", u, name, v)
		}
	}
	if _, ok := facts[slot+".fingerprint"].(string); !ok {
		t.Errorf("%s: no %s.fingerprint string", u, slot)
	}
	if _, ok := facts[slot+".repo"].(string); !ok {
		t.Errorf("%s: no %s.repo string", u, slot)
	}
}

// EventsBackend is what RunEvents needs beyond automation.Events: a way to
// store events, which come back with their ids.
type EventsBackend interface {
	automation.Events
	Append(evs ...workflow.Event) []workflow.Event
}

// RunEvents checks an events store and its cursors.
func RunEvents(t *testing.T, mk func(*testing.T) EventsBackend) {
	ctx := context.Background()
	u := unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}
	t.Run("AnEmptyStoreHasHeadZero", func(t *testing.T) {
		if h, err := mk(t).Head(ctx); err != nil || h != 0 {
			t.Errorf("Head = %d, %v", h, err)
		}
	})
	t.Run("SinceFiltersByKindInIDOrder", func(t *testing.T) {
		es := mk(t)
		stored := es.Append(workflow.Event{Unit: u, Kind: "merged", At: at0}, workflow.Event{Unit: u, Kind: "review", Key: "r1", At: at0},
			workflow.Event{Unit: u, Kind: "merged", Key: "m2", Data: "x", At: at0.Add(time.Second)})
		if len(stored) != 3 || !(stored[0].ID < stored[1].ID && stored[1].ID < stored[2].ID) {
			t.Fatalf("Append = %+v", stored)
		}
		got, err := es.Since(ctx, stored[0].ID, []string{"merged"})
		if err != nil || len(got) != 1 || got[0].ID != stored[2].ID || got[0].Data != "x" || got[0].Unit != u {
			t.Errorf("Since(first, merged) = %+v, %v", got, err)
		}
		all, err := es.Since(ctx, 0, nil)
		if err != nil || len(all) != 3 {
			t.Errorf("Since(0, all) = %+v, %v", all, err)
		}
		if h, err := es.Head(ctx); err != nil || h != stored[2].ID {
			t.Errorf("Head = %d, %v; want %d", h, err, stored[2].ID)
		}
	})
	t.Run("ACursorRoundTrips", func(t *testing.T) {
		es := mk(t)
		if _, _, ok, err := es.Cursor(ctx, "w"); ok || err != nil {
			t.Fatalf("Cursor of a new workflow: ok %v, %v", ok, err)
		}
		fire := at0.Add(-time.Hour)
		if err := es.Advance(ctx, "w", 7, fire); err != nil {
			t.Fatal(err)
		}
		cur, last, ok, err := es.Cursor(ctx, "w")
		if err != nil || !ok || cur != 7 || !last.Equal(fire) {
			t.Errorf("Cursor = %d, %v, %v, %v", cur, last, ok, err)
		}
		if _, _, ok, _ := es.Cursor(ctx, "other"); ok {
			t.Error("a cursor leaked to another workflow")
		}
	})
}

// RunSteps checks a step registry: a step it does not know is an error in
// every method, and Check answers the same twice.
func RunSteps(t *testing.T, mk func(*testing.T) automation.Steps) {
	ctx := context.Background()
	u := unit.Unit{Provider: unit.Session, Key: "t1"}
	unknown := workflow.Step{Use: "no.such_step"}
	t.Run("AnUnknownStepIsAnError", func(t *testing.T) {
		s := mk(t)
		if _, err := s.Check(ctx, unknown, u, workflow.Why{}, automation.CheckOpts{}); err == nil {
			t.Error("Check of an unknown step succeeded")
		}
		if _, err := s.Run(ctx, unknown, u, workflow.Why{}, automation.RunOpts{}); err == nil {
			t.Error("Run of an unknown step succeeded")
		}
		if err := s.Note(ctx, unknown, u, workflow.Why{}, "r"); err == nil {
			t.Error("Note of an unknown step succeeded")
		}
		if err := s.Dismiss(ctx, unknown, u, workflow.Why{}); err == nil {
			t.Error("Dismiss of an unknown step succeeded")
		}
	})
}

// LedgerBackend is what RunLedger needs beyond automation.Ledger: the change
// log its writes note on.
type LedgerBackend interface {
	automation.Ledger
	automation.Marks
	Changes() port.Changes
}

// RunLedger checks a plan and run store: Save replaces the items of the
// workflows it names, leaving out any whose key is done or dismissed, Record stores a run and keeps a non-terminal item,
// Count counts done runs, and writes note TopicLoopEvents with events and
// TopicGate only when the pending set changes.
func RunLedger(t *testing.T, mk func(*testing.T) LedgerBackend) {
	ctx := context.Background()
	a := unit.Unit{Provider: unit.Session, Key: "t1"}
	b := unit.Unit{Provider: unit.Workspace, Key: "w1"}
	item := func(wf string, u unit.Unit, d workflow.Decision) workflow.Item {
		k := workflow.Key{Workflow: wf, Unit: u, Value: "0123456789abcdef"}
		ev := workflow.Event{ID: 3, Unit: u, Kind: "merged", Key: "k", Data: "d", At: at0}
		it := workflow.Item{ID: k.ID(), Key: k, Workflow: wf, Unit: u, Decision: d, Reason: "because",
			Steps: []workflow.Step{{Use: "some.step", Reach: workflow.ReachLocal, Args: map[string]any{"reason": "done"}}},
			Gain:  workflow.Gain{MemoryBytes: 1, DiskBytes: 2},
			Why: workflow.Why{Workflow: wf, Trigger: workflow.TriggerEvent, Event: &ev,
				Matched: []workflow.Fact{{Name: "session.state", Value: "done"}}},
			Mode: workflow.ModeApply, Marks: map[string]mark.Value{wf + "/head": {Value: "h1"}}, Attempts: 1, Error: "e", At: at0}
		if d == workflow.DecisionPending {
			it.Default, it.Deadline = workflow.OutcomeRun, at0.Add(5*time.Minute)
		}
		return it
	}
	ids := func(items []workflow.Item) []string {
		out := []string{}
		for _, it := range items {
			out = append(out, it.ID)
		}
		slices.Sort(out)
		return out
	}
	t.Run("AnItemRoundTrips", func(t *testing.T) {
		l := mk(t)
		want := item("w", a, workflow.DecisionPending)
		want.Guidance = "g"
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{want}}); err != nil {
			t.Fatal(err)
		}
		got, err := l.Items(ctx, automation.Filter{ID: want.ID})
		if err != nil || len(got) != 1 {
			t.Fatalf("Items = %v, %v", got, err)
		}
		g := got[0]
		if g.ID != want.ID || g.Key != want.Key || g.Unit != want.Unit || g.Decision != want.Decision || g.Reason != want.Reason ||
			g.Default != want.Default || !g.Deadline.Equal(want.Deadline) || g.Guidance != want.Guidance || g.Attempts != 1 ||
			g.Error != "e" || g.Mode != want.Mode || !g.At.Equal(want.At) || g.Gain != want.Gain || len(g.Steps) != 1 ||
			g.Steps[0].Use != "some.step" || g.Steps[0].Args["reason"] != "done" || g.Why.Event == nil ||
			g.Why.Event.ID != 3 || g.Why.Event.Kind != "merged" || g.Why.Trigger != workflow.TriggerEvent ||
			len(g.Why.Matched) != 1 || g.Why.Matched[0].Value != "done" || g.Marks[want.Workflow+"/head"] != (mark.Value{Value: "h1"}) {
			t.Errorf("Items = %+v\nwant %+v", g, want)
		}
	})
	t.Run("ADoneOrDismissedRunSetsTheItemsMarks", func(t *testing.T) {
		l := mk(t)
		failed, done, dismissed := item("x", a, workflow.DecisionRun), item("y", a, workflow.DecisionRun), item("z", b, workflow.DecisionRun)
		for _, c := range []struct {
			it     workflow.Item
			status workflow.RunStatus
		}{{failed, workflow.RunFailed}, {done, workflow.RunDone}, {dismissed, workflow.RunDismissed}} {
			if err := l.Record(ctx, workflow.Run{Key: c.it.Key, Status: c.status, At: at0}, c.it); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := l.Marks(ctx, a); err != nil || !maps.Equal(got, map[string]string{"y/head": "h1"}) {
			t.Errorf("marks of %s = %v, %v; want only the done run's", a, got, err)
		}
		if got, err := l.Marks(ctx, b); err != nil || got["z/head"] != "h1" {
			t.Errorf("marks of %s = %v, %v", b, got, err)
		}
		later, earlier := item("w", b, workflow.DecisionRun), item("v", b, workflow.DecisionRun)
		later.Marks = map[string]mark.Value{"w/at": {Value: "2026-10-08T11:00:00.000000000Z", Watermark: true}}
		earlier.Marks = map[string]mark.Value{"w/at": {Value: "2026-10-08T10:00:00.000000000Z", Watermark: true}}
		for _, it := range []workflow.Item{later, earlier} {
			if err := l.Record(ctx, workflow.Run{Key: it.Key, Status: workflow.RunDismissed, At: at0}, it); err != nil {
				t.Fatal(err)
			}
		}
		if got, _ := l.Marks(ctx, b); got["w/at"] != later.Marks["w/at"].Value {
			t.Errorf("a watermark moved back to %q", got["w/at"])
		}
	})
	t.Run("SaveReplacesOnlyTheNamedWorkflows", func(t *testing.T) {
		l := mk(t)
		x, y := item("x", a, workflow.DecisionRun), item("y", b, workflow.DecisionBlocked)
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"x", "y"}, Items: []workflow.Item{x, y}}); err != nil {
			t.Fatal(err)
		}
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"x"}}); err != nil {
			t.Fatal(err)
		}
		got, err := l.Items(ctx, automation.Filter{})
		if err != nil || !slices.Equal(ids(got), []string{y.ID}) {
			t.Errorf("Items = %v, %v; want only %s", ids(got), err, y.ID)
		}
	})
	t.Run("SaveSkipsAnItemWhoseKeyIsDoneOrDismissed", func(t *testing.T) {
		l := mk(t)
		done, dismissed, failed := item("x", a, workflow.DecisionPending), item("x", b, workflow.DecisionPending), item("y", a, workflow.DecisionPending)
		for _, r := range []workflow.Run{{Key: done.Key, Status: workflow.RunDone, At: at0}, {Key: dismissed.Key, Status: workflow.RunDismissed, At: at0},
			{Key: failed.Key, Status: workflow.RunFailed, Attempts: 1, At: at0}} {
			if err := l.Record(ctx, r, item(r.Key.Workflow, r.Key.Unit, workflow.DecisionRun)); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"x", "y"}, Items: []workflow.Item{done, dismissed, failed}}); err != nil {
			t.Fatal(err)
		}
		got, err := l.Items(ctx, automation.Filter{})
		if err != nil || !slices.Equal(ids(got), []string{failed.ID}) {
			t.Errorf("Items = %v, %v; a done or dismissed key came back", ids(got), err)
		}
	})
	t.Run("ItemsFilter", func(t *testing.T) {
		l := mk(t)
		x, y, z := item("x", a, workflow.DecisionRun), item("x", b, workflow.DecisionPending), item("z", a, workflow.DecisionPending)
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"x", "z"}, Items: []workflow.Item{x, y, z}}); err != nil {
			t.Fatal(err)
		}
		for name, c := range map[string]struct {
			f    automation.Filter
			want []workflow.Item
		}{
			"workflow":  {automation.Filter{Workflow: "x"}, []workflow.Item{x, y}},
			"provider":  {automation.Filter{Provider: unit.Workspace}, []workflow.Item{y}},
			"unit":      {automation.Filter{Unit: &a}, []workflow.Item{x, z}},
			"decisions": {automation.Filter{Decisions: []workflow.Decision{workflow.DecisionPending}}, []workflow.Item{y, z}},
			"id":        {automation.Filter{ID: z.ID}, []workflow.Item{z}},
			"combined":  {automation.Filter{Workflow: "x", Decisions: []workflow.Decision{workflow.DecisionPending}}, []workflow.Item{y}},
		} {
			got, err := l.Items(ctx, c.f)
			if err != nil || !slices.Equal(ids(got), ids(c.want)) {
				t.Errorf("%s: Items = %v, %v; want %v", name, ids(got), err, ids(c.want))
			}
		}
	})
	t.Run("RecordKeepsANonTerminalItemAndRemovesADoneOne", func(t *testing.T) {
		l := mk(t)
		x, y := item("x", a, workflow.DecisionRun), item("x", b, workflow.DecisionRun)
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"x"}, Items: []workflow.Item{x, y}}); err != nil {
			t.Fatal(err)
		}
		failed := y
		failed.Decision, failed.Attempts, failed.Error = workflow.DecisionSkipped, 2, "boom"
		if err := l.Record(ctx, workflow.Run{Key: x.Key, Status: workflow.RunDone, Attempts: 1, At: at0}, x); err != nil {
			t.Fatal(err)
		}
		if err := l.Record(ctx, workflow.Run{Key: y.Key, Status: workflow.RunFailed, Attempts: 2, Error: "boom", At: at0}, failed); err != nil {
			t.Fatal(err)
		}
		got, err := l.Items(ctx, automation.Filter{Workflow: "x"})
		if err != nil || len(got) != 1 || got[0].ID != y.ID || got[0].Decision != workflow.DecisionSkipped || got[0].Attempts != 2 {
			t.Errorf("Items = %+v, %v", got, err)
		}
		r, ok, err := l.Run(ctx, y.Key)
		if err != nil || !ok || r.Status != workflow.RunFailed || r.Attempts != 2 || r.Error != "boom" || !r.At.Equal(at0) {
			t.Errorf("Run(y) = %+v, %v, %v", r, ok, err)
		}
		if _, ok, _ := l.Run(ctx, workflow.Key{Workflow: "x", Unit: a, Value: "other"}); ok {
			t.Error("Run of an unknown key found one")
		}
	})
	t.Run("CountCountsDoneRunsSinceForAUnit", func(t *testing.T) {
		l := mk(t)
		rec := func(wf string, u unit.Unit, v string, s workflow.RunStatus, at time.Time) {
			it := item(wf, u, workflow.DecisionRun)
			it.Key.Value = v
			it.ID = it.Key.ID()
			if err := l.Record(ctx, workflow.Run{Key: it.Key, Status: s, At: at}, it); err != nil {
				t.Fatal(err)
			}
		}
		rec("w", a, "1", workflow.RunDone, at0)
		rec("w", a, "2", workflow.RunDone, at0.Add(-2*time.Hour))
		rec("w", b, "3", workflow.RunDone, at0)
		rec("w", a, "4", workflow.RunFailed, at0)
		rec("w", a, "5", workflow.RunDismissed, at0)
		rec("v", a, "6", workflow.RunDone, at0)
		since := at0.Add(-time.Hour)
		if n, err := l.Count(ctx, "w", since, nil); err != nil || n != 2 {
			t.Errorf("Count(w, all units) = %d, %v; want 2", n, err)
		}
		if n, err := l.Count(ctx, "w", since, &a); err != nil || n != 1 {
			t.Errorf("Count(w, a) = %d, %v; want 1", n, err)
		}
	})
	t.Run("WritesNoteGateOnlyWhenThePendingSetChanges", func(t *testing.T) {
		l := mk(t)
		log := l.Changes()
		p := item("w", a, workflow.DecisionPending)
		ev := port.Event{Unit: a, Kind: workflow.KindPlanned, Key: p.ID + ":pending", At: at0}
		g := log.Gen()
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{p}}, ev); err != nil {
			t.Fatal(err)
		}
		if c := log.Since(g, port.TopicGate|port.TopicLoopEvents); !c.Has(port.TopicGate) || !c.Has(port.TopicLoopEvents) {
			t.Errorf("a new pending item with an event noted %+v", c)
		}
		g = log.Gen()
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{p}}); err != nil {
			t.Fatal(err)
		}
		if log.Gen() != g {
			t.Errorf("an unchanged plan noted %+v", log.Since(g, ^port.Topic(0)))
		}
		later := p
		later.Deadline = p.Deadline.Add(time.Minute)
		if err := l.Save(ctx, automation.PlanWrite{Workflows: []string{"w"}, Items: []workflow.Item{later}}); err != nil {
			t.Fatal(err)
		}
		if c := log.Since(g, ^port.Topic(0)); !c.Has(port.TopicGate) || c.Has(port.TopicLoopEvents) {
			t.Errorf("a re-timed pending item noted %+v", c)
		}
		g = log.Gen()
		run := item("w", b, workflow.DecisionRun)
		ran := port.Event{Unit: b, Kind: workflow.KindStepRan, Key: run.ID + ":0", At: at0}
		if err := l.Record(ctx, workflow.Run{Key: run.Key, Status: workflow.RunDone, At: at0}, run, ran); err != nil {
			t.Fatal(err)
		}
		if c := log.Since(g, ^port.Topic(0)); c.Has(port.TopicGate) || !c.Has(port.TopicLoopEvents) {
			t.Errorf("a done run outside the pending set noted %+v", c)
		}
	})
}
