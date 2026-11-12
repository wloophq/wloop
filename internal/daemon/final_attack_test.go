package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
)

func finalRig(t *testing.T) (*outboxRig, string, string, *gates.Service) {
	t.Helper()
	r := newOutboxRig(t)
	svc := sessionsOn(r.db)
	svc.Clock = r.clock
	r.s.deps.Turns = svc
	worker, mine := adoptSession(t, svc, "term_1", false), adoptSession(t, svc, "term_2", true)
	st := r.db.Gates(r.clock.Now)
	gs := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: r.clock, Sessions: svc,
		Publisher: publishTo(func(ctx context.Context, g gate.Gate) error { return r.o.Publish(ctx, g) })})
	r.s.deps.Gates, r.o.Gates = gs, rigGates{gs, st}
	return r, worker, mine, gs
}

// FINAL-A1: a session the user drives revises a worker's draft through the person's edit verb, then approves it:
// either the edit is refused and the draft is untouched, or the session cannot approve the rev it wrote.
func TestFinalAttackDrivenReviserApprovesItsOwnRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, worker, mine, _ := finalRig(t)
	d, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Reply, Text: "the worker's text",
		Origin: draft.Origin{Kind: draft.OriginSession, Session: worker}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
	if err != nil {
		t.Fatal(err)
	}
	rec := postFrom(t, r.s, "term_2", fmt.Sprintf("/api/tasks/draft:%d/edit", d.ID), `{"source":"claude","args":{"text":"the driven session's text","rev":"1"}}`)
	if rec.Code != 200 {
		if got, _ := r.o.Store.Get(ctx, d.ID); got.Text != "the worker's text" || got.State != draft.Held {
			t.Fatalf("a refused edit changed the draft: %d %s, draft %s %q", rec.Code, rec.Body, got.State, got.Text)
		}
		return
	}
	got, _ := r.o.Store.Get(ctx, d.ID)
	g, _, _ := r.o.GateOf(ctx, got)
	t.Logf("after the edit: by %+v, gate %d stamps session=%q sessions=%v", got.By, g.ID, g.Session, g.Sessions)
	rec = postFrom(t, r.s, "term_2", fmt.Sprintf("/api/gates/%d/approve", g.ID), `{"source":"claude","args":{"rev":"2"}}`)
	got, _ = r.o.Store.Get(ctx, d.ID)
	if rec.Code == 200 || got.State != draft.Held {
		t.Errorf("session %s approved the rev it wrote: %d %s, draft %s %q", mine, rec.Code, rec.Body, got.State, got.Text)
	}
}

// FINAL-A1b: the same in one call, approve with text: the draft must not be queued with the session's text.
func TestFinalAttackDrivenSessionApprovesWithItsOwnText(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, worker, mine, _ := finalRig(t)
	d, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Reply, Text: "the worker's text",
		Origin: draft.Origin{Kind: draft.OriginSession, Session: worker}, Policy: draft.Fixed(draft.Approval{Mode: draft.Wait})})
	if err != nil {
		t.Fatal(err)
	}
	g, _, _ := r.o.GateOf(ctx, d)
	rec := postFrom(t, r.s, "term_2", fmt.Sprintf("/api/gates/%d/approve", g.ID), `{"source":"claude","args":{"rev":"1","text":"written by the approving session"}}`)
	got, _ := r.o.Store.Get(ctx, d.ID)
	if rec.Code == 200 && got.State == draft.Queued {
		t.Errorf("session %s wrote and approved %q in one call, stamped by %+v", mine, got.Text, got.By)
	}
}
