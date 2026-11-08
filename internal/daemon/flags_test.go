package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// flagRig is a server whose tasks feed runs over the log its store notes, with a subscriber on its hub.
type flagRig struct {
	s   *Server
	db  *sqlite.DB
	log *change.Log
	sub *subscriber
}

func newFlagRig(t *testing.T) *flagRig {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "flags.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := newServerOn(t, db, log, func(*Deps) {})
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { s.feedTasks(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	sub := s.hub.subscribe("test")
	t.Cleanup(func() { s.hub.unsubscribe(sub) })
	synctest.Wait()
	return &flagRig{s: s, db: db, log: log, sub: sub}
}

// published is what the hub sent sub once the feed's quiet period passed; ok false when nothing.
func (r *flagRig) published() (api.Change, bool) {
	time.Sleep(2 * syncQuiet)
	synctest.Wait()
	return r.sub.take()
}

var flagUnit = unit.Unit{Provider: unit.Session, Key: "term_flags"}

func flagItem(d workflow.Decision, reason string) workflow.Item {
	k := workflow.Key{Workflow: "tidy", Unit: flagUnit, Value: "v1"}
	it := workflow.Item{ID: k.ID(), Key: k, Workflow: "tidy", Unit: flagUnit, Decision: d, Reason: reason,
		At: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)}
	if d == workflow.DecisionPending {
		it.Default, it.Deadline = workflow.OutcomeWait, it.At.Add(time.Hour)
	}
	return it
}

func (r *flagRig) save(t *testing.T, items ...workflow.Item) {
	t.Helper()
	if err := r.db.Ledger().Save(context.Background(), automation.PlanWrite{Workflows: []string{"tidy"}, Items: items}); err != nil {
		t.Fatal(err)
	}
}

func TestASweepPassRewritingTheSameItemsPublishesNoPlan(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFlagRig(t)
		it := flagItem(workflow.DecisionBlocked, "a workspace holds it")
		r.save(t, it)
		if c, ok := r.published(); !ok || !c.Plan {
			t.Fatalf("a new item published %+v (%v), want the plan", c, ok)
		}
		r.save(t, it)
		if c, ok := r.published(); ok {
			t.Fatalf("the same items saved again published %+v", c)
		}
	})
}

func TestAPlanWriteThatKeepsThePendingSetPublishesThePlanAndTheGates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFlagRig(t)
		r.save(t, flagItem(workflow.DecisionPending, "idle"))
		r.published()
		r.save(t, flagItem(workflow.DecisionPending, "idle for a day"))
		c, ok := r.published()
		if !ok || !c.Plan || !c.Gates {
			t.Fatalf("a pending item whose reason changed published %+v (%v), want the plan and the gates, whose plan gate shows it", c, ok)
		}
		if c.All || len(c.Units) > 0 || c.Counters != nil {
			t.Fatalf("a plan write published a row or task change: %+v", c)
		}
	})
}

func TestAGateNotePublishesTheGatesAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFlagRig(t)
		r.log.Note(port.TopicGates)
		c, ok := r.published()
		if !ok || !c.Gates || c.Plan {
			t.Fatalf("a gate opened or resolved published %+v (%v), want the gates alone", c, ok)
		}
	})
}

func TestARowsOnlyChangePublishesNeitherPlanNorGates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		r := newFlagRig(t)
		r.log.Note(port.TopicPRs|port.TopicSourceEvents, apiPR)
		r.s.moved.add(api.Change{Units: []unit.Unit{apiPR}})
		if c, ok := r.published(); ok && (c.Plan || c.Gates) {
			t.Fatalf("a rows-only change published %+v", c)
		}
	})
}

func TestAFlagOnlyPublishDoesNotMoveTheRowsPosition(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	at := api.Since{Epoch: "e", Rev: 9}
	s.hub.moveTo(at)
	s.hub.publish(api.Change{Type: "changed", Epoch: "e", Rev: 4, Plan: true, Gates: true})
	s.hub.publish(api.Change{Type: "changed", Epoch: "e", Rev: 4, Counters: &api.Counters{}})
	if got := s.hub.position(); got != at {
		t.Fatalf("a publish of flags and counters moved the rows to %v from %v", got, at)
	}
}
