package outbox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// placing names the target of a draft proposed without one.
type placing struct {
	*fakeEffects
	target string
	refuse error
	asked  []draft.Draft
}

func (p *placing) Place(ctx context.Context, d draft.Draft) (string, error) {
	p.asked = append(p.asked, d)
	return p.target, p.refuse
}

func withPlacer(e *env, target string) *placing {
	p := &placing{fakeEffects: e.fx, target: target}
	e.svc.Effects = func(unit.ProviderID) (port.Effects, error) { return p, nil }
	return p
}

var segment = unit.Unit{Provider: "slack", Key: "T000AAA/D000AAA/seg/s1"}

func TestADraftWithNoTargetStoresItsPlacement(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "msg:D000AAA/1700000000.000100")
	p := reply("done")
	p.Unit, p.Items = segment, []string{"i1"}
	d := e.propose(p)
	if d.Target != "msg:D000AAA/1700000000.000100" || e.get(d.ID).Target != d.Target {
		t.Fatalf("target %q, stored %q", d.Target, e.get(d.ID).Target)
	}
	if len(pl.asked) != 1 || pl.asked[0].Unit != segment || len(pl.asked[0].Items) != 1 {
		t.Fatalf("asked %+v", pl.asked)
	}
}

func TestAnExplicitTargetIsNotPlaced(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "msg:elsewhere")
	p := reply("done")
	p.Unit, p.Target = segment, "U000BBB"
	if d := e.propose(p); d.Target != "U000BBB" || len(pl.asked) != 0 {
		t.Fatalf("target %q, asked %d", d.Target, len(pl.asked))
	}
}

func TestAnEmptyPlacementKeepsTheUnitItself(t *testing.T) {
	e := newEnv()
	withPlacer(e, "")
	if d := e.propose(reply("on it")); d.Target != "" {
		t.Fatalf("target %q", d.Target)
	}
}

func TestADraftThePluginCannotPlaceIsRefused(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "")
	pl.refuse = errors.New("names neither a target nor items")
	p := reply("done")
	p.Unit = segment
	if _, err := e.svc.Propose(ctx, p); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "neither a target nor items") {
		t.Fatalf("propose: %v", err)
	}
	if got, _ := e.store.List(ctx, Filter{}); len(got) != 0 {
		t.Fatalf("stored %v", got)
	}
}

func TestAPlacementIsInsideTheUnit(t *testing.T) {
	e := newEnv()
	withPolicy(e, none)
	withPlacer(e, "msg:D000AAA/1700000000.000100")
	p := reply("done")
	p.Unit, p.Items = segment, []string{"i1"}
	d := e.propose(p)
	if e.approval(d.ID).Mode != draft.Send || d.State != draft.Queued {
		t.Fatalf("%+v", d)
	}
}

func TestApprovingADraftProposedUnplacedPlacesIt(t *testing.T) {
	e := newEnv()
	p := reply("done")
	p.Unit, p.Items = segment, []string{"i1"}
	d := e.propose(p)
	withPlacer(e, "msg:D000AAA/1700000000.000100")
	got, err := e.approve(d.ID, nil)
	if err != nil || got.Target != "msg:D000AAA/1700000000.000100" || e.get(d.ID).Target != got.Target {
		t.Fatalf("approve: %+v %v", got, err)
	}
}

func approvedUnplaced(e *env) draft.Draft {
	p := reply("done")
	p.Unit, p.Items = segment, []string{"i1"}
	d, err := e.approve(e.propose(p).ID, nil)
	if err != nil {
		panic(err)
	}
	return d
}

func TestSendingAnApprovedUnplacedDraftPlacesItFirst(t *testing.T) {
	e := newEnv()
	d := approvedUnplaced(e)
	withPlacer(e, "msg:D000AAA/1700000000.000100")
	var published string
	e.fx.during = func(p draft.Draft) { published = p.Target }
	if sent, failed, err := e.svc.Send(ctx); err != nil || sent != 1 || failed != 0 {
		t.Fatalf("send: %d %d %v", sent, failed, err)
	}
	if got := e.get(d.ID); published != "msg:D000AAA/1700000000.000100" || got.State != draft.Sent || got.Target != published {
		t.Fatalf("published to %q, stored %+v", published, got)
	}
}

func TestAnApprovedDraftThePluginCannotPlaceFailsUnpublished(t *testing.T) {
	e := newEnv()
	d := approvedUnplaced(e)
	pl := withPlacer(e, "")
	pl.refuse = errors.New("none of the items names a message")
	if _, failed, _ := e.svc.Send(ctx); failed != 1 {
		t.Fatalf("failed %d", failed)
	}
	if got := e.get(d.ID); got.State != draft.Failed || !strings.Contains(got.Error, "none of the items") || len(e.fx.published) != 0 {
		t.Fatalf("%+v published %v", got, e.fx.published)
	}
}

// recordingSaves records every draft the Outbox stores over an existing one.
func recordingSaves(e *env) *[]draft.Draft {
	var saved []draft.Draft
	e.store.beforeUpdate = func(d draft.Draft) { saved = append(saved, d) }
	return &saved
}

func TestTheClaimStoresThePlacedTarget(t *testing.T) {
	e := newEnv()
	approvedUnplaced(e)
	withPlacer(e, "msg:D000AAA/1700000000.000100")
	s := recordingSaves(e)
	e.svc.Send(ctx)
	if len(*s) == 0 || (*s)[0].State != draft.Sending || (*s)[0].Target != "msg:D000AAA/1700000000.000100" {
		t.Fatalf("saved %+v", *s)
	}
}

func TestARefusedPlacementNeverClaimsTheDraft(t *testing.T) {
	e := newEnv()
	d := approvedUnplaced(e)
	pl := withPlacer(e, "")
	pl.refuse = errors.New("none of the items names a message")
	s := recordingSaves(e)
	if _, failed, _ := e.svc.Send(ctx); failed != 1 {
		t.Fatalf("failed %d", failed)
	}
	for _, saved := range *s {
		if saved.State == draft.Sending {
			t.Fatalf("claimed %+v", saved)
		}
	}
	if got := e.get(d.ID); got.State != draft.Failed || got.Target != "" || !strings.Contains(got.Error, "none of the items") {
		t.Fatalf("%+v", got)
	}
}
