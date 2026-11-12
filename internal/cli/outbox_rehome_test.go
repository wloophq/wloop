package cli

import (
	"context"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// TestARehomedDraftIsAskedAgain moves a held draft and a queued one to another unit, a new revision of each:
// both wait on one open gate at their new rev, which a person's approval queues, and neither fails.
func TestARehomedDraftIsAskedAgain(t *testing.T) {
	ctx := context.Background()
	te := newTestEnv(t, &fake.Manager{})
	te.hosted = &hostedLate{}
	db := outboxDaemon(t, te, true)
	gs := te.newGates(db, t.Logf)
	moved := unit.Unit{Provider: threadUnit.Provider, Key: "C1 0002"}
	propose := func(text string) draft.Draft {
		d, err := te.outbox.Propose(ctx, outbox.Proposal{Unit: threadUnit, Kind: draft.Kind("reply"), Text: text, Origin: draft.Origin{Kind: draft.OriginSession},
			Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	approve := func(d draft.Draft) {
		t.Helper()
		g, ok, err := te.outbox.GateOf(ctx, d)
		if err != nil || !ok || g.Status != gate.Open {
			t.Fatalf("draft %d waits on %+v %v", d.ID, g, err)
		}
		if _, err := te.outbox.Prepare(ctx, d.ID, d.Rev); err != nil {
			t.Fatal(err)
		}
		if err := gs.Resolve(ctx, g.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
			t.Logf("its consequence, which this env has no daemon to run: %v", err)
		}
	}
	held, queued := propose("held while it moves"), propose("approved before it moves")
	approve(queued)
	if got := te.draftState(t, db, queued.ID); got.State != draft.Queued {
		t.Fatalf("approved before the move: %s", got.State)
	}
	if n, err := (rehomingDrafts{db, te.Env}).RehomeDrafts(ctx, map[unit.Unit]unit.Unit{threadUnit: moved}); err != nil || n != 2 {
		t.Fatalf("moved %d %v", n, err)
	}
	for _, id := range []draft.ID{held.ID, queued.ID} {
		d := te.draftState(t, db, id)
		open, err := db.Gates(te.Now).Where(ctx, gates.Query{Kind: gate.KindDraft, Unit: moved})
		var mine []gate.Gate
		for _, g := range open {
			if g.Subject == strconv.FormatInt(int64(id), 10) {
				mine = append(mine, g)
			}
		}
		if err != nil || d.State != draft.Held || d.Unit != moved || d.Rev != 2 || len(mine) != 1 || mine[0].ApprovedRev != d.Rev {
			t.Fatalf("draft %d after the move: %s at rev %d on %s, open gates %+v %v", id, d.State, d.Rev, d.Unit, mine, err)
		}
		approve(d)
		if got := te.draftState(t, db, id); got.State != draft.Queued {
			t.Fatalf("draft %d approved again is %s", id, got.State)
		}
	}
}
