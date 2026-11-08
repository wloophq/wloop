package outbox

import (
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
)

func TestAProposedDraftStoresTheBroadcastItsPublisherPlaces(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "")
	pl.broadcast = draft.BroadcastOn
	p := reply("done")
	p.Broadcast = draft.BroadcastOff
	d := e.propose(p)
	if d.Broadcast != draft.BroadcastOn || e.get(d.ID).Broadcast != draft.BroadcastOn {
		t.Fatalf("broadcast %q, stored %q", d.Broadcast, e.get(d.ID).Broadcast)
	}
	if len(pl.asked) != 1 || pl.asked[0].Broadcast != draft.BroadcastOff {
		t.Fatalf("asked %+v", pl.asked)
	}
}

func TestAPublisherThatPlacesNothingHasNoBroadcast(t *testing.T) {
	e := newEnv()
	p := reply("done")
	p.Broadcast = draft.BroadcastOn
	if d := e.propose(p); d.Broadcast != draft.NoBroadcast {
		t.Fatalf("broadcast %q", d.Broadcast)
	}
}

func TestAnUnknownBroadcastIsRefused(t *testing.T) {
	e := newEnv()
	p := reply("done")
	p.Broadcast = "loud"
	if _, err := e.svc.Propose(ctx, p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("propose: %v", err)
	}
	pl := withPlacer(e, "")
	pl.broadcast = "loud"
	if _, err := e.svc.Propose(ctx, reply("done")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("propose placed loud: %v", err)
	}
}

func TestTurningTheBroadcastOffWaitsForAPersonAgain(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(10*time.Minute))
	withPlacer(e, "").broadcast = draft.BroadcastOn
	d := e.propose(reply("done"))
	first := e.gate(d.ID)
	got, err := e.svc.SetBroadcast(ctx, d.ID, false)
	if err != nil || got.Broadcast != draft.BroadcastOff || e.get(d.ID).Broadcast != draft.BroadcastOff {
		t.Fatalf("set: %+v %v", got, err)
	}
	if old, _ := e.gates.Get(ctx, first.ID); old.Status != gate.Superseded || e.approval(d.ID) != waiting {
		t.Fatalf("the windowed gate %+v", old)
	}
	if got, err := e.svc.SetBroadcast(ctx, d.ID, true); err != nil || got.Broadcast != draft.BroadcastOn {
		t.Fatalf("set on: %+v %v", got, err)
	}
}

func TestADraftThatRepliesInNoThreadHasNoBroadcastToSet(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("done"))
	if _, err := e.svc.SetBroadcast(ctx, d.ID, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("set: %v", err)
	}
	if e.get(d.ID).Broadcast != draft.NoBroadcast {
		t.Fatalf("stored %q", e.get(d.ID).Broadcast)
	}
}

func TestApprovingAndSendingKeepTheBroadcastThePersonSet(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "")
	pl.broadcast = draft.BroadcastOn
	d := e.propose(reply("done"))
	if _, err := e.svc.SetBroadcast(ctx, d.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	var published draft.Broadcast
	e.fx.during = func(p draft.Draft) { published = p.Broadcast }
	if sent, _, err := e.svc.Send(ctx); err != nil || sent != 1 {
		t.Fatalf("send: %d %v", sent, err)
	}
	if published != draft.BroadcastOff || e.get(d.ID).Broadcast != draft.BroadcastOff {
		t.Fatalf("published %q, stored %q", published, e.get(d.ID).Broadcast)
	}
}

func TestAHeldDraftStoredWithNoBroadcastIsPlacedOnceAtStart(t *testing.T) {
	e := newEnv()
	withPolicy(e, window(10*time.Minute))
	old := e.propose(reply("done"))
	firstGate := e.gate(old.ID)
	reaction := e.propose(Proposal{Unit: thread, Kind: draft.React, Reaction: "eyes", Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "r", Key: "k"}})
	pl := withPlacer(e, "")
	pl.broadcast = draft.BroadcastOn
	if err := e.svc.PlaceBroadcasts(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.get(old.ID)
	if got.Broadcast != draft.BroadcastOn || got.State != draft.Held {
		t.Fatalf("placed %+v", got)
	}
	if g := e.gate(old.ID); g.ID != firstGate.ID || g.Status != gate.Open || g.Deadline != firstGate.Deadline {
		t.Fatalf("the gate changed: %+v, was %+v", g, firstGate)
	}
	if e.get(reaction.ID).Broadcast != draft.NoBroadcast {
		t.Fatalf("a reaction placed %q", e.get(reaction.ID).Broadcast)
	}
	if len(pl.asked) != 1 {
		t.Fatalf("asked %d, want only the message", len(pl.asked))
	}
	if err := e.svc.PlaceBroadcasts(ctx); err != nil || len(pl.asked) != 1 {
		t.Fatalf("a placed draft asked again: %d %v", len(pl.asked), err)
	}
}

func TestADraftPlacedInNoThreadAtStartKeepsNone(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("done"))
	withPlacer(e, "")
	if err := e.svc.PlaceBroadcasts(ctx); err != nil || e.get(d.ID).Broadcast != draft.NoBroadcast {
		t.Fatalf("%q %v", e.get(d.ID).Broadcast, err)
	}
}

func TestAPublisherThatCannotPlaceAtStartLeavesTheDraft(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("done"))
	withPlacer(e, "").refuse = errors.New("not logged in")
	if err := e.svc.PlaceBroadcasts(ctx); err != nil || e.get(d.ID).Broadcast != draft.NoBroadcast || e.get(d.ID).State != draft.Held {
		t.Fatalf("%+v %v", e.get(d.ID), err)
	}
}

func TestAnAmendOfTextAndBroadcastChangesBothOrNeither(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("done"))
	text, on := "better", true
	if _, err := e.svc.Amend(ctx, d.ID, Amendment{Text: &text, Broadcast: &on}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("amend: %v", err)
	}
	if got := e.get(d.ID); got.Text != "done" || got.Rev != d.Rev {
		t.Fatalf("a refused amend saved %+v", got)
	}
	withPlacer(e, "").broadcast = draft.BroadcastOff
	d = e.propose(reply("again"))
	first := e.gate(d.ID)
	got, err := e.svc.Amend(ctx, d.ID, Amendment{Text: &text, Broadcast: &on})
	if err != nil || got.Text != "better" || got.Broadcast != draft.BroadcastOn || got.Rev != d.Rev+1 {
		t.Fatalf("amend: %+v %v", got, err)
	}
	if old, _ := e.gates.Get(ctx, first.ID); old.Status != gate.Superseded {
		t.Fatalf("the first gate %+v", old)
	}
}

func TestBroadcastOfIsWhatProposingWouldPlaceAndStoresNothing(t *testing.T) {
	e := newEnv()
	if b, err := e.svc.BroadcastOf(ctx, reply("done")); err != nil || b != draft.NoBroadcast {
		t.Fatalf("a publisher that places nothing: %q %v", b, err)
	}
	pl := withPlacer(e, "msg:D000AAA/1700000000.000100")
	pl.broadcast = draft.BroadcastOn
	p := reply("done")
	p.Unit, p.Items = segment, []string{"i1"}
	b, err := e.svc.BroadcastOf(ctx, p)
	if err != nil || b != draft.BroadcastOn {
		t.Fatalf("broadcast %q %v", b, err)
	}
	if len(pl.asked) != 1 || pl.asked[0].Unit != segment || pl.asked[0].Delivery != draft.Message || pl.asked[0].Kind != draft.Reply {
		t.Fatalf("asked %+v", pl.asked)
	}
	if ds, _ := e.svc.Store.List(ctx, Filter{}); len(ds) != 0 {
		t.Fatalf("stored %+v", ds)
	}
	pl.refuse = errBoom
	if _, err := e.svc.BroadcastOf(ctx, p); !errors.Is(err, errBoom) {
		t.Fatalf("a refused placement: %v", err)
	}
}
