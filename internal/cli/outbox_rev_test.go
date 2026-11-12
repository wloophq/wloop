package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// TestTheRevProposeReturnsIsTheOneAnOptionPins proposes held drafts through serve's outbox and gates on the
// real store, one held by its proposer and one under a waiting policy: each stays at the rev Propose
// returns, which its gate opened for, with no write after it, so a question's option pinned at that rev
// approves it.
func TestTheRevProposeReturnsIsTheOneAnOptionPins(t *testing.T) {
	ctx := context.Background()
	for name, p := range map[string]outbox.Proposal{
		"hold":    {Unit: threadUnit, Kind: draft.Kind("reply"), Text: "Two findings.", Hold: true, Origin: draft.Origin{Kind: draft.OriginSession}},
		"waiting": {Unit: threadUnit, Kind: draft.Kind("reply"), Text: "Two findings.", Origin: draft.Origin{Kind: draft.OriginSession}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})},
	} {
		t.Run(name, func(t *testing.T) {
			te := newTestEnv(t, &fake.Manager{})
			db := outboxDaemon(t, te, true)
			d, err := te.outbox.Propose(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			stored := te.draftState(t, db, d.ID)
			dg, ok, err := te.outbox.GateOf(ctx, stored)
			if err != nil || !ok || d.Rev != 1 || stored.Rev != d.Rev || dg.ApprovedRev != d.Rev || dg.Status != gate.Open || stored.Task == "" {
				t.Fatalf("proposed at rev %d, stored %+v, gate %+v %v", d.Rev, stored, dg, err)
			}
			gs := te.newGates(db, t.Logf)
			q, err := gs.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Unit: threadUnit, Subject: "132"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			r := gates.Resolution{Intent: "answer", Answer: "Approve (option a)", By: task.ActorPerson}
			if err := gs.ApproveOption(ctx, q, r, dg, d.Rev); err != nil {
				t.Logf("the question's own consequence, which this env has no daemon to run: %v", err)
			}
			if got := te.draftState(t, db, d.ID); got.State != draft.Queued {
				t.Fatalf("the draft the option pinned at rev %d is %s", d.Rev, got.State)
			}
		})
	}
}
