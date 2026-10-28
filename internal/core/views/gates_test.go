package views_test

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/view"
)

func TestOrcaCardFollowsOpenGates(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		board := newOrcaBoard()
		f := newFixture(t, views.Sink{ViewSink: board, Placements: board})
		st := gatestest.NewStore(clock{}.Now)
		f.svc.Gates = st
		f.run(t)
		board.place(r7, "reviewing")
		board.place(r8, "reviewing")
		for _, u := range []turn.Book{
			{Unit: r7, Turns: []turn.Turn{reviewTurn(1, turn.Done)}, Records: []turn.Record{rec(1, 1, record.Done, "commented")}},
			{Unit: r8, Turns: []turn.Turn{reviewTurn(1, turn.Done)}, Records: []turn.Record{rec(1, 1, record.Done, "commented")}},
		} {
			f.books.Put(u)
		}
		f.svc.Changed(ctx, r7, r8)
		eventually(t, "the reviewed cards", func() bool {
			return board.card(r7).column == "reviewed" && board.card(r8).column == "reviewed"
		})

		g, err := st.Open(ctx, gate.Gate{Kind: gate.KindPlan, Name: "plan", Unit: r7, Subject: "review/acme/api#7/k", Status: gate.Open, Intents: []string{"run_now", "skip"}})
		if err != nil {
			t.Fatal(err)
		}
		f.svc.Changed(ctx, r7, r8)
		eventually(t, "the card of the open gate", func() bool { return board.card(r7).column == "needs-me" })
		if c := board.card(r7); !c.unread {
			t.Fatalf("the card of an open gate = %+v, want it unread in needs-me", c)
		}
		if c := board.card(r8); c.column != "reviewed" {
			t.Fatalf("a card with no gate open moved to %+v", c)
		}
		if a, _ := f.applied(t, "orca", r7); a.Status != view.NeedsMe {
			t.Fatalf("applied %+v, want needs-me", a)
		}

		if _, err := st.Resolve(ctx, g.ID, gates.Resolution{Status: gate.Rejected, Intent: "skip", By: task.ActorPerson}); err != nil {
			t.Fatal(err)
		}
		f.svc.Changed(ctx, r7)
		eventually(t, "the card back once the gate closed", func() bool { return board.card(r7).column == "reviewed" })
	})
}
