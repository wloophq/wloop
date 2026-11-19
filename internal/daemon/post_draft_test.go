package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// postRig is an Outbox rig whose gates read the sessions of svc, with a worker session the loop typed into last
// that proposed a held reply on r7u.
func postRig(t *testing.T) (*outboxRig, *AgentDrafts, string, draft.Draft) {
	t.Helper()
	r := newOutboxRig(t)
	svc := sessionsOn(r.db)
	svc.Clock = r.clock
	r.s.deps.Turns = svc
	worker := adoptSession(t, svc, "term_1", false)
	st := r.db.Gates(r.clock.Now)
	gs := gates.New(gates.Deps{Store: st, Reader: st, Profile: profileGates{r.s.loaded().Gates}, Clock: r.clock, Sessions: svc,
		Publisher: publishTo(func(ctx context.Context, g gate.Gate) error { return r.o.Publish(ctx, g) })})
	r.s.deps.Gates, r.s.deps.GateReader, r.o.Gates = gs, st, rigGates{gs, st}
	d, err := r.o.Propose(context.Background(), outbox.Proposal{Unit: r7u, Kind: draft.Kind("reply"), Text: "Ship it",
		Origin: draft.Origin{Kind: draft.OriginSession, Session: worker}})
	if err != nil {
		t.Fatal(err)
	}
	a := &AgentDrafts{Outbox: r.o, Unit: func(context.Context, string) (unit.Unit, bool, error) { return r7u, true, nil },
		Post: r.s.PostDraft, Link: r.s.DraftLink}
	return r, a, worker, d
}

// A write with draft false from a session the loop typed into last stays a draft; once the user prompts that
// session, the same write posts the draft it proposed, approved through it.
func TestDraftFalsePostsOnlyFromASessionTheUserDrives(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, a, worker, d := postRig(t)
	no := false
	got, err := a.Update(ctx, worker, api.AgentDraftUpdate{ID: int64(d.ID), Rev: d.Rev, Draft: &no})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != string(draft.Held) {
		t.Fatalf("an unattended session's draft false: %s, want the draft kept for the user", got.State)
	}
	r.clock.set(tasksNow.Add(time.Minute))
	if err := r.s.deps.Turns.PromptSubmitted(ctx, worker, "post it as a comment"); err != nil {
		t.Fatal(err)
	}
	text := "Ship it, as a comment"
	got, err = a.Update(ctx, worker, api.AgentDraftUpdate{ID: int64(d.ID), Rev: d.Rev, Text: &text, Draft: &no})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != string(draft.Queued) || got.Text != text {
		t.Fatalf("the draft after draft false from a session the user drives: %+v, want the revision queued", got)
	}
	stored, _ := r.o.Store.Get(ctx, d.ID)
	g, _, _ := r.o.GateOf(ctx, stored)
	if g.Status != gate.Approved || g.By != task.ActorAgent || g.BySession != worker || !g.ByUser() {
		t.Fatalf("the draft's gate %+v, want approved through the session that proposed it", g)
	}
}

func TestADraftLinkOpensItsCardOnceTheCockpitListens(t *testing.T) {
	t.Parallel()
	s := New(Deps{})
	stopOnCleanup(t, s)
	if got := s.DraftLink(7); got != "" {
		t.Fatalf("a link before the cockpit listens: %q", got)
	}
	s.cockpitPort.Store(8123)
	if got, want := s.DraftLink(7), "http://127.0.0.1:8123/#task=draft%3A7"; got != want {
		t.Fatalf("DraftLink(7) = %q, want %q", got, want)
	}
}
