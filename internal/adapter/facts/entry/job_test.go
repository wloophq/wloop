package factentry_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/proposals"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

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

type itemsOf struct{ e *env }

func (i itemsOf) Items(ctx context.Context, u unit.Unit) ([]items.Item, error) {
	got, err := i.e.db.Items().Load(ctx, []unit.Unit{u})
	return got[u], err
}

// TestEntryFactsMatchTheProposalsJob runs the proposals job and the entry
// facts over the same store for two weeks: the job nudges an entry exactly
// when nudge_due_at is set and items_held > 0, and closes one exactly when
// it is a close entry in inbox or next with items_resolved > 0.
func TestEntryFactsMatchTheProposalsJob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e := newEnv(t)
	owed := func(u unit.Unit, id string, s items.State) items.Item {
		return e.item(u, id, s, monday.Add(-24*time.Hour), items.Person{Key: "chat:U1"})
	}
	i1, i2, i3 := owed(chatA, "i1", items.Open), owed(chatB, "i2", items.Open), owed(chatA, "i3", items.Resolved)
	i4 := owed(mailX, "i4", items.Open)
	e.item(chatB, "i5", items.Resolved, monday.Add(-24*time.Hour))
	e.entry(waiting("e-w1", chatA, monday.Add(-24*time.Hour), i1))
	resurfaces := waiting("e-w2", chatB, monday, i2)
	resurfaces.Waiting.Resurface = monday.Add(3*24*time.Hour + 2*time.Hour)
	e.entry(resurfaces)
	e.entry(waiting("e-w3", chatA, monday.Add(-5*24*time.Hour), i3))
	nobody := waiting("e-w4", chatA, monday.Add(-5*24*time.Hour), i1)
	nobody.Waiting.On = nil
	e.entry(nobody)
	recent := waiting("e-w5", chatB, monday.Add(-9*24*time.Hour), i2)
	recent.Waiting.LastNudge = monday.Add(-24 * time.Hour)
	e.entry(recent)
	e.entry(waiting("e-w6", mailX, monday.Add(-9*24*time.Hour), i4))
	e.entry(gtd.Entry{ID: "e-c1", Kind: gtd.KindClose, State: gtd.Inbox, Title: "c", Units: []unit.Unit{chatB}})
	e.entry(gtd.Entry{ID: "e-c2", Kind: gtd.KindClose, State: gtd.Someday, Title: "c", Units: []unit.Unit{chatB}})
	e.entry(gtd.Entry{ID: "e-c3", Kind: gtd.KindClose, State: gtd.Next, Title: "c", Units: []unit.Unit{mailX}})
	e.entry(gtd.Entry{ID: "e-c4", Kind: gtd.KindClose, State: gtd.Next, Title: "c", Units: []unit.Unit{chatA}})
	moved := false
	nudged := map[string]int{}
	for h := range 14 * 24 / 6 {
		e.clock.now = monday.Add(time.Duration(h) * 6 * time.Hour)
		if !moved && e.clock.now.Sub(monday) > 5*24*time.Hour {
			moved = true
			w1, err := e.db.Entries().Entry(ctx, "e-w1")
			if err != nil {
				t.Fatal(err)
			}
			w1.Waiting.LastNudge = e.clock.now
			e.entry(w1)
		}
		out := &recorder{}
		job := &proposals.Job{Outbox: out, Entries: e.db.Entries(), Items: itemsOf{e}, StaleDays: e.s.StaleDays, RenudgeAfter: e.s.RenudgeAfter,
			Enabled: func(p unit.ProviderID) bool { return p != mailX.Provider }, Clock: e.clock}
		if _, err := job.Run(ctx); err != nil {
			t.Fatal(err)
		}
		var byJob []string
		for _, p := range out.got {
			if p.Origin.Rule == proposals.RuleStalledNudge || p.Origin.Rule == proposals.RuleClosure {
				byJob = append(byJob, p.Origin.Rule+" "+p.Entry)
			}
		}
		listed, err := e.p.List(ctx, unit.Entry)
		if err != nil {
			t.Fatal(err)
		}
		var byFacts []string
		for _, u := range listed {
			f := e.facts(gtd.ID(u.Key))
			if f["entry.unit"] == "" {
				continue
			}
			if !f["entry.nudge_due_at"].(time.Time).IsZero() && f["entry.items_held"].(int) > 0 {
				byFacts = append(byFacts, proposals.RuleStalledNudge+" "+u.Key)
			}
			if st := f["entry.state"]; f["entry.kind"] == "close" && (st == "inbox" || st == "next") && f["entry.items_resolved"].(int) > 0 {
				byFacts = append(byFacts, proposals.RuleClosure+" "+u.Key)
			}
		}
		slices.Sort(byJob)
		slices.Sort(byFacts)
		if !slices.Equal(byJob, byFacts) {
			t.Errorf("%s: the job proposed %v, the facts call for %v", e.clock.now.Format(time.DateTime), byJob, byFacts)
		}
		if h == 0 && fmt.Sprint(byJob) != "[closure e-c1 closure e-c4]" {
			t.Fatalf("the scenario starts with only the closures due: %v", byJob)
		}
		for _, p := range byJob {
			nudged[p]++
		}
	}
	for _, p := range []string{"stalled-nudge e-w1", "stalled-nudge e-w2", "stalled-nudge e-w5"} {
		if nudged[p] == 0 {
			t.Errorf("%s never came due: %v", p, nudged)
		}
	}
}
