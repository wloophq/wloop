package outbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAWindowNeverPostsABroadcastPlacedAfterItsGateOpened(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "msg:C000AAA/1.2")
	e.svc.Gates = noPlacer{e.svc.Gates}
	withPolicy(e, &draft.Policy{Default: window5})
	p := reply("ship it")
	p.Task = "t-1"
	p.Origin = draft.Origin{Kind: draft.OriginAuto, Rule: "r"}
	d := e.propose(p)
	g := e.gate(d.ID)
	pl.broadcast = draft.BroadcastOn
	if err := e.svc.PlaceBroadcasts(ctx); err != nil {
		t.Fatal(err)
	}
	var posted draft.Draft
	e.fx.during = func(d draft.Draft) { posted = d }
	e.clock.now = t0.Add(time.Hour)
	if err := e.tick(); err != nil {
		t.Fatal(err)
	}
	if sent, _, _ := e.svc.Send(ctx); sent == 1 && posted.Broadcast == draft.BroadcastOn {
		t.Fatalf("the window approved rev %d and posted the placement with broadcast on", g.ApprovedRev)
	}
	if got := e.gate(d.ID); got.Status != gate.Open || got.ApprovedRev != e.get(d.ID).Rev {
		t.Fatalf("the placed draft waits on %+v", got)
	}
}

func TestACrashBetweenApprovalAndPublishPostsTheApprovedRev(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "msg:C000AAA/1.2")
	e.svc.Gates = noPlacer{e.svc.Gates}
	p := reply("ship it")
	p.Task = "t-1"
	p.Origin = draft.Origin{Kind: draft.OriginAuto, Rule: "r", Key: "k"}
	p.Policy = &draft.Policy{Default: waiting}
	d := e.propose(p)
	g := e.gate(d.ID)
	if _, err := e.gates.Resolve(ctx, g.ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	pl.broadcast = draft.BroadcastOn
	if err := e.svc.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.PlaceBroadcasts(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.tick(); err != nil {
		t.Fatal(err)
	}
	var posted []draft.Draft
	e.fx.during = func(d draft.Draft) { posted = append(posted, d) }
	if _, _, err := e.svc.Send(ctx); err != nil {
		t.Fatal(err)
	}
	if len(posted) != 1 || posted[0].Broadcast != draft.NoBroadcast {
		t.Fatalf("the person approved rev %d; recovery posted %+v", g.ApprovedRev, posted)
	}
}

func TestAHeldDraftPlacedAtStartWaitsAtItsNewRev(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "msg:C000AAA/1.2")
	d := e.propose(reply("ship it"))
	pl.broadcast = draft.BroadcastOn
	if err := e.svc.PlaceBroadcasts(ctx); err != nil {
		t.Fatal(err)
	}
	placed := e.get(d.ID)
	if placed.Broadcast != draft.BroadcastOn || e.gate(d.ID).ApprovedRev != placed.Rev {
		t.Fatalf("placed %+v, gate %+v", placed, e.gate(d.ID))
	}
	var posted draft.Draft
	e.fx.during = func(d draft.Draft) { posted = d }
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, err := e.svc.Send(ctx); sent != 1 || err != nil || posted.Broadcast != draft.BroadcastOn {
		t.Fatalf("a person's approval of the placed rev: sent %d, %v, posted %+v", sent, err, posted)
	}
}

func TestACrossUnitDraftCountsAsOutside(t *testing.T) {
	e, _ := reviewEnv()
	withPolicy(e, &draft.Policy{Default: waiting, Rules: []draft.Rule{
		{When: factIs{"draft.delivery", "message"}, Names: "message", Approval: noneA}}})
	elsewhere := unit.Unit{Provider: "slack", Key: "C0999ZZZ/9.9"}
	d := e.propose(Proposal{Unit: elsewhere, Kind: "reply", Text: "injected",
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review", Session: "s-worker", Unit: pull}})
	if d.State != draft.Held || e.approval(d.ID) != waiting {
		t.Fatalf("a PR turn's draft into %s: %+v", elsewhere, d)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 {
		t.Fatalf("a PR turn's draft into %s was sent by a rule", elsewhere)
	}
	thread := unit.Unit{Provider: unit.GitHub, Key: "o/r#2"}
	e.svc.Effects = func(unit.ProviderID) (port.Effects, error) {
		return scoping{e.fx, func(t string) (bool, error) { return t == "comment:1", nil }}, nil
	}
	other := e.propose(Proposal{Unit: thread, Kind: "comment", Text: "same provider, another pull request",
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review", Unit: pull}})
	if e.approval(other.ID) != waiting {
		t.Fatalf("a draft on another pull request than its turn's: %+v", e.approval(other.ID))
	}
}

func TestTwoRevisersOfAFailedDraftCannotApproveEachOther(t *testing.T) {
	e, bases := reviewEnv()
	withPolicy(e, sendsPosts)
	d := e.propose(review("REQUEST_CHANGES"))
	bases.Set(pull, "beef002")
	if _, failed, _ := e.svc.Send(ctx); failed != 1 {
		t.Fatalf("failed %d", failed)
	}
	cur := e.get(d.ID)
	nb := "beef002"
	a := Caller{Session: "s-a", Units: []unit.Unit{pull}}
	b := Caller{Session: "s-b", Units: []unit.Unit{pull}}
	once := false
	e.store.beforeUpdate = func(x draft.Draft) {
		if once || x.State != draft.Held {
			return
		}
		once = true
		if _, err := e.svc.AgentAmend(ctx, b, d.ID, Amendment{Payload: payload(`{"event":"APPROVE"}`), Basis: &nb, Rev: cur.Rev}); err != nil {
			t.Errorf("b: %v", err)
		}
	}
	if _, err := e.svc.AgentAmend(ctx, a, d.ID, Amendment{Payload: payload(`{"event":"COMMENT"}`), Basis: &nb, Rev: cur.Rev}); !errors.Is(err, ErrRevConflict) {
		t.Fatalf("the losing reviser: %v", err)
	}
	e.store.beforeUpdate = nil
	open, _ := e.gates.Where(ctx, gates.Query{Kind: gate.KindDraft, Statuses: []gate.Status{gate.Open}})
	if len(open) != 1 {
		t.Fatalf("open gates %+v, want one", open)
	}
	e.decider = gates.New(gates.Deps{Store: e.gates, Reader: e.gates, Profile: draftProfile{}, Clock: e.clock, Publisher: publishTo{e},
		Sessions: driven{"s-a": true, "s-b": true}})
	for _, g := range open {
		for _, s := range []string{"s-a", "s-b"} {
			_ = e.decider.Resolve(ctx, g.ID, gates.Resolution{Intent: "approve", By: task.ActorAgent, Session: s})
		}
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 {
		t.Fatalf("B's revision posted, approved by a session")
	}
}

func TestTheLoserOfAFailedDraftsRaceStampsTheWinner(t *testing.T) {
	e, bases := reviewEnv()
	withPolicy(e, sendsPosts)
	d := e.propose(review("REQUEST_CHANGES"))
	bases.Set(pull, "beef002")
	e.svc.Send(ctx)
	cur := e.get(d.ID)
	nb := "beef002"
	e.store.beforeUpdate = func(x draft.Draft) {
		e.store.beforeUpdate = nil
		won := e.get(d.ID)
		won.State, won.Error, won.Payload, won.Basis = draft.Held, "", []byte(`{"event":"APPROVE"}`), nb
		won.By = draft.Actor{Kind: draft.ActorSession, Session: "s-b"}
		if _, err := e.store.Update(ctx, won, draft.Failed); err != nil {
			t.Error(err)
		}
	}
	a := Caller{Session: "s-a", Units: []unit.Unit{pull}}
	if _, err := e.svc.AgentAmend(ctx, a, d.ID, Amendment{Payload: payload(`{"event":"COMMENT"}`), Basis: &nb, Rev: cur.Rev}); !errors.Is(err, ErrRevConflict) {
		t.Fatalf("the loser: %v", err)
	}
	g := e.gate(d.ID)
	if g.Status != gate.Open || g.ApprovedRev != e.get(d.ID).Rev || !stampsAll(g, []string{"s-a", "s-b", "s-worker"}) {
		t.Fatalf("the gate the loser opened for the winner's content: %+v", g)
	}
}

func TestPersonAmendRacingAgentGivesOneRevConflict(t *testing.T) {
	for _, agentFirst := range []bool{true, false} {
		e, _ := reviewEnv()
		d := e.propose(review("COMMENT"))
		rev := e.get(d.ID).Rev
		var inner error
		e.store.beforeUpdate = func(draft.Draft) {
			e.store.beforeUpdate = nil
			if agentFirst {
				_, inner = e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Text: ptr("the agent's"), Rev: rev})
			} else {
				_, inner = e.svc.Amend(ctx, d.ID, Amendment{Text: ptr("the person's"), Rev: rev})
			}
		}
		var outer error
		if agentFirst {
			_, outer = e.svc.Amend(ctx, d.ID, Amendment{Text: ptr("the person's"), Rev: rev})
		} else {
			_, outer = e.svc.AgentAmend(ctx, worker, d.ID, Amendment{Text: ptr("the agent's"), Rev: rev})
		}
		if (inner == nil) == (outer == nil) || !errors.Is(errors.Join(inner, outer), ErrRevConflict) || !errors.Is(errors.Join(inner, outer), ErrConflict) {
			t.Fatalf("agent inside %v: inner %v, outer %v, want one rev conflict", agentFirst, inner, outer)
		}
		agentWon := (inner == nil) == agentFirst
		want := map[bool]string{true: "the agent's", false: "the person's"}[agentWon]
		if got := e.get(d.ID); got.Text != want {
			t.Fatalf("agent first %v: text %q, want %q", agentFirst, got.Text, want)
		}
		open, _ := e.gates.Where(ctx, gates.Query{Kind: gate.KindDraft, Statuses: []gate.Status{gate.Open}})
		if len(open) != 1 || open[0].ApprovedRev != e.get(d.ID).Rev {
			t.Fatalf("agent first %v: open gates %+v", agentFirst, open)
		}
	}
	e, _ := reviewEnv()
	d := e.propose(review("COMMENT"))
	if _, err := e.svc.Amend(ctx, d.ID, Amendment{Text: ptr("x")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a person at rev 0: %v", err)
	}
	if _, err := e.svc.Edit(ctx, d.ID, "x", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an edit at rev 0: %v", err)
	}
}

func TestOnlyAStaleRefusalStartsAFailureWithTheStaleToken(t *testing.T) {
	for name, c := range map[string]struct {
		live     error
		publish  error
		stale    bool
		contains string
	}{
		"a live read that fails":             {live: errBoom, contains: "boom"},
		"a publisher's text that says stale": {publish: fmt.Errorf("stale: whatever the API said"), contains: "error: stale:"},
		"a publisher's ErrStale":             {publish: fmt.Errorf("github refused: %w", ErrStale), stale: true},
	} {
		t.Run(name, func(t *testing.T) {
			e, bases := reviewEnv()
			d := e.propose(review("COMMENT"))
			if _, err := e.approve(d.ID, nil); err != nil {
				t.Fatal(err)
			}
			bases.Err, e.fx.failWith = c.live, c.publish
			if _, failed, _ := e.svc.Send(ctx); failed != 1 {
				t.Fatalf("failed %d", failed)
			}
			got := e.get(d.ID).Error
			if strings.HasPrefix(got, StalePrefix) != c.stale || !strings.Contains(got, c.contains) {
				t.Fatalf("Error %q", got)
			}
		})
	}
}

// soleGates refuses to open a draft gate while another of its unit is open.
type soleGates struct{ Gates }

func (g soleGates) Open(ctx context.Context, gt gate.Gate, p gates.Policy) (gate.Gate, error) {
	open, err := g.Where(ctx, gates.Query{Kind: gate.KindDraft, Unit: gt.Unit})
	if err != nil || len(open) > 0 {
		return gate.Gate{}, fmt.Errorf("%d open gates on %s: %v", len(open), gt.Unit, err)
	}
	return g.Gates.Open(ctx, gt, p)
}

func TestPlacingAHeldDraftNeverHasTwoOpenGates(t *testing.T) {
	e := newEnv()
	pl := withPlacer(e, "msg:C000AAA/1.2")
	d := e.propose(reply("ship it"))
	e.svc.Gates = soleGates{e.svc.Gates}
	pl.broadcast = draft.BroadcastOn
	if err := e.svc.PlaceBroadcasts(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Held || got.Broadcast != draft.BroadcastOn || e.gate(d.ID).ApprovedRev != got.Rev {
		t.Fatalf("placed %+v, gate %+v", got, e.gate(d.ID))
	}
}

func TestTheSenderRefusesAQueuedDraftPastItsGatesRev(t *testing.T) {
	e := newEnv()
	d := e.propose(reply("approved"))
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	e.store.carry = nil
	cur := e.get(d.ID)
	if _, err := e.store.Update(ctx, cur, draft.Queued); err != nil {
		t.Fatal(err)
	}
	if sent, failed, _ := e.svc.Send(ctx); sent != 0 || failed != 1 || len(e.fx.published) != 0 {
		t.Fatalf("a queued draft at rev %d under a gate of rev %d: sent %d, failed %d", e.get(d.ID).Rev, e.gate(d.ID).ApprovedRev, sent, failed)
	}
	if _, err := e.svc.Retry(ctx, d.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("a retry past the gate's rev: %v", err)
	}
}

func TestAPersonsEditOfAFailedDraftWaitsOnANewGate(t *testing.T) {
	e := newEnv()
	withPolicy(e, draft.Fixed(noneA))
	d := e.propose(reply("rule-sent"))
	ruled := e.gate(d.ID)
	e.fx.failWith = errBoom
	if _, failed, _ := e.svc.Send(ctx); failed != 1 {
		t.Fatalf("failed %d", failed)
	}
	got, err := e.svc.Edit(ctx, d.ID, "a person's words", e.get(d.ID).Rev)
	if err != nil {
		t.Fatal(err)
	}
	g := e.gate(d.ID)
	if got.State != draft.Held || g.ID == ruled.ID || g.Status != gate.Open || g.ApprovedRev != got.Rev || e.approval(d.ID) != waiting {
		t.Fatalf("edited %+v, waits on %+v", got, g)
	}
	e.fx.failWith = nil
	if _, err := e.svc.Retry(ctx, d.ID, true); err == nil {
		t.Fatal("a retry released the person's edit under the rule's approval")
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 {
		t.Fatalf("sent %d", sent)
	}
	if _, err := e.approve(d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 1 {
		t.Fatalf("a person's approval of the edit: sent %d", sent)
	}
}
