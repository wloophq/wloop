package outbox

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

func TestOutboxSendsOnlyApprovedDrafts(t *testing.T) {
	e := newEnv()
	held := e.propose(reply("held"))
	approved := e.propose(reply("approved"))
	rejected := e.propose(reply("rejected"))
	withdrawn := e.propose(reply("withdrawn"))
	if _, err := e.approve(approved.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.decide(rejected.ID, "discard"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Withdraw(ctx, withdrawn.ID, draft.WithdrawMoot); err != nil {
		t.Fatal(err)
	}
	openQueued := e.propose(reply("queued while its gate is open"))
	rejectedQueued := e.propose(reply("queued after its gate refused it"))
	if err := e.decide(rejectedQueued.ID, "discard"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []draft.Draft{e.get(openQueued.ID), e.get(rejectedQueued.ID)} {
		from := d.State
		d.State = draft.Queued
		if _, err := e.store.Update(ctx, d, from); err != nil {
			t.Fatal(err)
		}
	}
	withPolicy(e, observe)
	dry := e.propose(reply("dry run"))
	forged, err := e.store.Insert(ctx, draft.Draft{Unit: thread, Purpose: "reply", Delivery: draft.Message, Text: "queued with no gate",
		State: draft.Queued, CreatedAt: t0, UpdatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		e.svc.Send(ctx)
	}
	for _, id := range []draft.ID{openQueued.ID, rejectedQueued.ID} {
		if got := e.get(id); got.State != draft.Failed {
			t.Errorf("a queued draft whose gate is %s is %s", e.gate(id).Status, got.State)
		}
	}
	if !slices.Equal(e.fx.published, []draft.ID{approved.ID}) {
		t.Fatalf("published %v, want only the approved draft %d", e.fx.published, approved.ID)
	}
	for id, want := range map[draft.ID]draft.State{held.ID: draft.Held, approved.ID: draft.Sent, rejected.ID: draft.Dropped,
		withdrawn.ID: draft.Dropped, dry.ID: draft.Dropped, forged.ID: draft.Failed} {
		if got := e.get(id).State; got != want {
			t.Errorf("draft %d is %s, want %s", id, got, want)
		}
	}
	for id, want := range map[draft.ID]gate.Status{held.ID: gate.Open, approved.ID: gate.Approved, rejected.ID: gate.Rejected,
		withdrawn.ID: gate.Superseded, dry.ID: gate.Rejected} {
		if got := e.gate(id).Status; got != want {
			t.Errorf("the gate of draft %d is %s, want %s", id, got, want)
		}
	}
	if g := e.gate(withdrawn.ID); g.By != task.ActorWorkflow {
		t.Errorf("withdrawn by %s", g.By)
	}
	if _, err := e.svc.Discard(ctx, held.ID, ""); !errors.Is(err, ErrConflict) {
		t.Errorf("a held draft is discarded through its gate, not the outbox: %v", err)
	}
	if err := e.decider.Supersede(ctx, e.gate(held.ID).ID, task.ActorAgent); err == nil {
		t.Error("an agent superseded a draft's gate")
	}
}

func TestAFailedApprovedDraftCanBeRetried(t *testing.T) {
	e := newEnv()
	held := e.propose(reply("held"))
	failed := e.propose(reply("fails"))
	sent := e.propose(reply("sent"))
	e.approve(sent.ID, nil)
	e.svc.Send(ctx)
	e.approve(failed.ID, nil)
	e.fx.failWith = errBoom
	e.svc.Send(ctx)
	e.fx.failWith = nil

	out, err := e.svc.Outbox(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []draft.ID
	for _, p := range out {
		ids = append(ids, p.Draft.ID)
		if p.Gate.Subject != strconv.FormatInt(int64(p.Draft.ID), 10) {
			t.Errorf("draft %d listed with the gate of %s", p.Draft.ID, p.Gate.Subject)
		}
	}
	if !slices.Equal(ids, []draft.ID{failed.ID, held.ID}) {
		t.Fatalf("the Outbox lists %v, want the failed approved draft and the held one", ids)
	}
	if out[0].Gate.Status != gate.Approved || out[0].Draft.State != draft.Failed || out[1].Gate.Status != gate.Open {
		t.Fatalf("%+v", out)
	}
	got, err := e.svc.Retry(ctx, failed.ID, false)
	if err != nil || got.State != draft.Queued {
		t.Fatalf("retry: %+v %v", got, err)
	}
	if n, _, err := e.svc.Send(ctx); n != 1 || err != nil || e.get(failed.ID).State != draft.Sent {
		t.Fatalf("sent %d %v, %s", n, err, e.get(failed.ID).State)
	}
	if out, _ := e.svc.Outbox(ctx); len(out) != 1 || out[0].Draft.ID != held.ID {
		t.Fatalf("after the retry the Outbox lists %+v", out)
	}
	if _, err := e.svc.Retry(ctx, held.ID, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("a held draft retried: %v", err)
	}
}

func TestGateDeliveryIsTheDraftsDelivery(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(time.Minute))
	d := e.propose(reply("x"))
	g := e.gate(d.ID)
	delivery := func() draft.State {
		t.Helper()
		got, err := e.svc.DeliveryOf(ctx, e.gate(d.ID))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := delivery(); got != draft.Held {
		t.Fatalf("open: %s", got)
	}
	e.clock.now = t0.Add(time.Minute)
	e.tick()
	if got := delivery(); got != draft.Queued {
		t.Fatalf("released: %s", got)
	}
	e.fx.failWith = errBoom
	e.svc.Send(ctx)
	if got, want := delivery(), e.get(d.ID).State; got != draft.Failed || got != want {
		t.Fatalf("failed: %s, the draft %s", got, want)
	}
	if got, err := e.svc.DeliveryOf(ctx, gate.Gate{Kind: gate.KindQuestion, Subject: g.Subject}); got != "" || err != nil {
		t.Fatalf("a question gate's delivery %q %v", got, err)
	}
}
