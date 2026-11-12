package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
)

// TestASenderPostsOnlyTheRevItsGateApproved is FINAL-A3: a rule released rev 1, its send failed, a person
// edited the failed draft and retried it. The edit takes the draft back to held under a waiting gate, so the
// retry is refused and nothing posts that no gate approved at that rev.
func TestASenderPostsOnlyTheRevItsGateApproved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newOutboxRig(t)
	st := r.db.Gates(r.clock.Now)
	gs := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: r.clock,
		Publisher: publishTo(func(ctx context.Context, g gate.Gate) error { return r.o.Publish(ctx, g) })})
	r.s.deps.Gates, r.o.Gates = gs, rigGates{gs, st}
	r.fx.fail = fmt.Errorf("422 Unprocessable Entity")
	d, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Reply, Text: "rule-approved rev 1",
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "test", Key: "k"}, Policy: draft.Fixed(draft.Approval{Mode: draft.Send})})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.o.Send(ctx); err != nil {
		t.Logf("send: %v", err)
	}
	failed, _ := r.o.Store.Get(ctx, d.ID)
	if failed.State != draft.Failed {
		t.Fatalf("want failed, got %s", failed.State)
	}
	if code, body := r.verb(t, d.ID, "edit", fmt.Sprintf(`{"text":"rev 2 nobody approved","rev":"%d"}`, failed.Rev)); code != 200 {
		t.Fatalf("edit: %d %s", code, body)
	}
	edited, _ := r.o.Store.Get(ctx, d.ID)
	g, _, _ := r.o.GateOf(ctx, edited)
	if edited.State != draft.Held || g.Status != gate.Open || g.ApprovedRev != edited.Rev {
		t.Fatalf("the edited failed draft %s waits on %+v", edited.State, g)
	}
	r.fx.fail = nil
	if code, body := r.verb(t, d.ID, "retry", fmt.Sprintf(`{"rev":"%d"}`, edited.Rev)); code == 200 {
		t.Fatalf("a retry released an edit no gate approved: %s", body)
	}
	r.clock.set(tasksNow.Add(time.Minute))
	if _, _, err := r.o.Send(ctx); err != nil {
		t.Logf("send: %v", err)
	}
	if sent, _ := r.o.Store.Get(ctx, d.ID); sent.State == draft.Sent {
		t.Fatalf("posted rev %d %q, which no gate approved", sent.Rev, sent.Text)
	}
}
