package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// crashed writes gate id's resolution and stops there, as a daemon that died before the gate's consequence
// ran leaves it.
func crashed(t *testing.T, s interface {
	Resolve(context.Context, int64, gates.Resolution) (gate.Gate, error)
}, id int64, r gates.Resolution) gate.Gate {
	t.Helper()
	g, err := s.Resolve(context.Background(), id, r)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestAQuestionAnsweredBeforeACrashReachesItsSessionOnce(t *testing.T) {
	t.Parallel()
	q := newLiveQuestion(t)
	ctx := context.Background()
	crashed(t, q.db.Gates(q.te.Now), q.gate.ID, gates.Resolution{Status: gate.Answered, Intent: "answer", Answer: "yes", By: task.ActorPerson})
	for range 2 {
		if err := q.d.Gates.Tick(ctx, testNow); err != nil {
			t.Fatal(err)
		}
	}
	if typed := q.sessions.Typed(); len(typed) != 1 || typed[0].Handle != "s1" {
		t.Fatalf("typed %+v, want once into the asking session", typed)
	}
	books, err := q.d.Messages.Books.Books(ctx, q.open.Unit)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range books[q.open.Unit].Answers {
		if a.Gate == q.gate.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d answers of gate %d: %+v", n, q.gate.ID, books[q.open.Unit].Answers)
	}
	if got, _, _ := q.d.Messages.Store.Message(ctx, q.open.ID); got.Open() {
		t.Fatalf("the answered question is open: %+v", got)
	}
	if left, err := q.db.Gates(q.te.Now).Unapplied(ctx); err != nil || len(left) != 0 {
		t.Fatalf("unapplied %+v %v", left, err)
	}
}

func TestAQuestionWhoseAnswerRanBeforeACrashIsNotAnsweredAgain(t *testing.T) {
	t.Parallel()
	q := newLiveQuestion(t)
	ctx := context.Background()
	g := crashed(t, q.db.Gates(q.te.Now), q.gate.ID, gates.Resolution{Status: gate.Answered, Intent: "answer", Answer: "yes", By: task.ActorPerson})
	if _, err := q.d.Messages.Turns.Answer(ctx, answerOf(q.open, g)); err != nil {
		t.Fatal(err)
	}
	if err := q.d.Gates.Tick(ctx, testNow); err != nil {
		t.Fatal(err)
	}
	if typed := q.sessions.Typed(); len(typed) != 1 {
		t.Fatalf("typed %+v, want the one answer that ran before the crash", typed)
	}
	if got, _, _ := q.d.Messages.Store.Message(ctx, q.open.ID); got.Open() {
		t.Fatalf("the replay left the answered question open: %+v", got)
	}
}

func TestAProposalDecidedBeforeACrashAppliesOnce(t *testing.T) {
	for _, ran := range []bool{false, true} {
		name := map[bool]string{false: "the decision never ran", true: "the decision ran unmarked"}[ran]
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			log := change.NewLog(change.DefaultWindow, t.Logf)
			db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), log)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			te := newTestEnv(t, &fake.Manager{})
			d := servedMessages(t, te, db, log)
			te.Env.consequences = messageConsequences{d.Messages}
			ledger := te.Env.itemsLedger(db, te.cfg, t.Logf)
			ctx := context.Background()
			u := ghUnit(7)
			kind, summary, changed, later := items.Todo, "ship the fix", "ship the fix today", "ship it tomorrow"
			anchor := unit.Anchor{Unit: u, Ref: "turn:1"}
			if _, err := ledger.Apply(ctx, items.User, []items.Op{{Op: items.OpCreate, Unit: u, Item: "i1", Anchor: anchor, Fields: &items.Fields{Kind: &kind, Summary: &summary}}}); err != nil {
				t.Fatal(err)
			}
			if res, err := ledger.Apply(ctx, items.Extracted, []items.Op{{Op: items.OpUpdate, Unit: u, Item: "i1", Anchor: anchor, Fields: &items.Fields{Summary: &changed}}}); err != nil || len(res.Proposed) != 1 {
				t.Fatalf("%+v %v", res, err)
			}
			waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 1 && ms[0].TaskID != "" })
			gs, err := d.GateReader.Where(ctx, gates.Query{Kind: gate.KindProposal, Unit: u})
			if err != nil || len(gs) != 1 {
				t.Fatalf("gates %+v %v", gs, err)
			}
			g := crashed(t, db.Gates(te.Now), gs[0].ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson})
			if ran {
				if err := d.Messages.Decide(ctx, g); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ledger.Apply(ctx, items.User, []items.Op{{Op: items.OpUpdate, Unit: u, Item: "i1", Anchor: unit.Anchor{Unit: u, Ref: "user:2"}, Fields: &items.Fields{Summary: &later}}}); err != nil {
				t.Fatal(err)
			}
			want := later
			if !ran {
				want = changed
			}
			for range 2 {
				if err := d.Messages.ReconcileProposals(ctx); err != nil {
					t.Fatal(err)
				}
				if err := d.Gates.Tick(ctx, testNow); err != nil {
					t.Fatal(err)
				}
			}
			if its, err := ledger.Items(ctx, u); err != nil || len(its) != 1 || its[0].Summary != want {
				t.Fatalf("items %+v %v, want the summary %q", its, err, want)
			}
			if ps, _ := ledger.Proposals(ctx, coreitems.ProposalAccepted); len(ps) != 1 || !ps[0].Decided {
				t.Fatalf("accepted %+v", ps)
			}
			if open, _ := d.Messages.Store.Open(ctx); len(open) != 0 {
				t.Fatalf("the decided proposal is asked again: %+v", open)
			}
			if left, err := db.Gates(te.Now).Unapplied(ctx); err != nil || len(left) != 0 {
				t.Fatalf("unapplied %+v %v", left, err)
			}
		})
	}
}

// answerOf is the answer the messages give question m through its gate g.
func answerOf(m message.Message, g gate.Gate) turns.AnswerRequest {
	return turns.AnswerRequest{Unit: m.Unit, Message: m.ID, Gate: g.ID, Yes: true, Source: turn.Cockpit}
}
