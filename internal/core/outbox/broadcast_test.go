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
