package headless_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/messages/messagestest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// preparedAct is the rig's act with a role whose prepare fills the turn's
// inputs and a guidance.max of 12, as a profile could give it.
func preparedAct(r *rig, p *turnstest.Prepares) {
	r.svc.Starter = turnstest.Starter(turnstest.Defs{Registry: turnstest.Intents(), Edit: func(d *turns.Def) {
		d.Prepare, d.Guidance.Max = "action:act.prepare", 12
	}}, r.store, p)
}

func TestANewHeadlessTurnRunsItsRolesPrepareOnItsCutGuidance(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done)})
	p := &turnstest.Prepares{Filled: map[string]any{"who": "ana"}}
	preparedAct(r, p)
	if _, err := r.svc.Run(context.Background(), headless.Request{Unit: thread, Items: []string{"i1"}, Guidance: "Ask about the retries first.", Trigger: turn.Automated}); err != nil {
		t.Fatal(err)
	}
	if saw := p.Saw(); !slices.Equal(saw, []string{"Ask about th"}) {
		t.Fatalf("the prepare saw %q", saw)
	}
	b := r.book(t, thread)
	if len(b.Turns) != 1 || b.Turns[0].Guidance != "Ask about th" || b.Turns[0].Inputs["who"] != "ana" {
		t.Fatalf("turns %+v", b.Turns)
	}
	if len(r.asker.asks) != 1 {
		t.Fatalf("%d calls", len(r.asker.asks))
	}
}

func TestAHeadlessTurnItsPrepareRefusesStartsNothing(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done)})
	preparedAct(r, &turnstest.Prepares{Refusal: fmt.Errorf("%w: nobody to ask", turns.ErrRefused)})
	if _, err := r.svc.Start(context.Background(), headless.Request{Unit: thread, Trigger: turn.ByHand}); !errors.Is(err, turns.ErrRefused) {
		t.Fatalf("start: %v", err)
	}
	if b := r.book(t, thread); len(b.Turns) != 0 || len(r.asker.asks) != 0 {
		t.Fatalf("a refused start recorded %+v and asked %d times", b.Turns, len(r.asker.asks))
	}
}

func TestAHeadlessRunAgainResumesWithoutAPrepare(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, asked())}, scripted{json: answerJSON(t, done)})
	p := &turnstest.Prepares{Filled: map[string]any{"who": "ana"}}
	preparedAct(r, p)
	ctx := context.Background()
	if _, err := r.svc.Run(ctx, headless.Request{Unit: thread, Items: []string{"i1"}, Trigger: turn.Automated}); err != nil {
		t.Fatal(err)
	}
	m := r.inbox()
	if err := m.Reconcile(ctx, thread); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Gates.(*messagestest.Gates).Answer(ctx, questions(t, m)[0], messages.Answer{OptionID: "mon", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	if n := len(p.Saw()); n != 1 || len(r.asker.asks) != 2 {
		t.Fatalf("%d prepares, %d calls", n, len(r.asker.asks))
	}
	b := r.book(t, thread)
	if len(b.Turns) != 2 || b.Turns[1].ResumeOf != b.Turns[0].Seq || b.Turns[1].Inputs["who"] != "ana" {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestAHeadlessTurnIsAPersonsOnlyWhenItsEntrySaysSo(t *testing.T) {
	for _, c := range []struct {
		name string
		req  headless.Request
		want int
	}{
		{"asked by a person, automated", headless.Request{Unit: thread, Trigger: turn.Automated, ByPerson: true}, 1},
		{"by hand, not asked by a person", headless.Request{Unit: thread, Trigger: turn.ByHand}, 0},
	} {
		r := newRig(t, scripted{json: answerJSON(t, done)})
		fx := &turnstest.Effects{}
		r.svc.Starter.Effects = fx
		if _, err := r.svc.Run(context.Background(), c.req); err != nil {
			t.Fatal(err)
		}
		if got := fx.Befores(); got != c.want {
			t.Errorf("%s: %d before phases, want %d", c.name, got, c.want)
		}
	}
}

func TestAHeadlessTurnRecordsNoGuidanceItsIntentDoesNotHand(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done)})
	fx := &turnstest.Effects{}
	r.svc.Starter.Effects = fx
	if _, err := r.svc.Run(context.Background(), headless.Request{Unit: thread, Guidance: "Ask about the retries first.", Trigger: turn.ByHand, ByPerson: true}); err != nil {
		t.Fatal(err)
	}
	if got := fx.Guidances(); len(got) != 0 {
		t.Fatalf("guidance events %q", got)
	}
}
