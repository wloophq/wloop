package headless_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAHeadlessDraftDeclaringAnEffectItsIntentDoesNotListIsRefused(t *testing.T) {
	closing := reply("Closing this out.")
	closing["kind"], closing["effects"] = "close", []string{"unit.closed"}
	stray := reply("And a stray effect.")
	stray["effects"] = []string{"pr.nudged"}
	plain := reply("No effect at all.")
	plain["kind"] = "close"
	r := newRig(t, scripted{json: answerJSON(t, done, closing, stray, plain)})
	intents := r.svc.Intents
	r.svc.Intents = func(n turn.Intent) (dintent.Def, bool) {
		d, ok := intents(n)
		d.DraftEffects = []string{"entry.nudged", "unit.closed"}
		return d, ok
	}
	got, err := r.svc.Run(context.Background(), headless.Request{Unit: thread})
	if err != nil {
		t.Fatal(err)
	}
	drafts, _ := r.outbox.List(context.Background(), outbox.Filter{})
	if len(drafts) != 2 {
		t.Fatalf("drafts %+v", drafts)
	}
	for _, d := range drafts {
		switch d.Text {
		case "Closing this out.":
			if !slices.Equal(d.Effects, []string{"unit.closed"}) {
				t.Errorf("the declared effect is lost: %+v", d)
			}
		case "No effect at all.":
			if len(d.Effects) != 0 {
				t.Errorf("a close draft with no effect gained %v", d.Effects)
			}
		default:
			t.Errorf("unexpected draft %+v", d)
		}
	}
	if want := "drafts[1] (reply): it declares the effect pr.nudged, which the act intent does not list in draft_effects"; !strings.Contains(got.Record.Next, want) {
		t.Fatalf("the record does not name the intent: %q", got.Record.Next)
	}
}

type approveAll struct{}

func (approveAll) For(context.Context, unit.Unit, draft.Origin) (draft.Approval, error) {
	return draft.Approval{Mode: draft.None}, nil
}

func TestAHeadlessDraftOfAnIntentThatNeverAutoSendsWaits(t *testing.T) {
	r := newRig(t, scripted{json: answerJSON(t, done, reply("On it."))})
	r.outbox.Policies = approveAll{}
	intents := r.svc.Intents
	r.svc.Intents = func(n turn.Intent) (dintent.Def, bool) {
		d, ok := intents(n)
		never := false
		d.AutoSend = &never
		return d, ok
	}
	if _, err := r.svc.Run(context.Background(), headless.Request{Unit: thread}); err != nil {
		t.Fatal(err)
	}
	drafts, _ := r.outbox.List(context.Background(), outbox.Filter{})
	if len(drafts) != 1 || drafts[0].State != draft.Proposed || drafts[0].Approval.Mode != draft.Always {
		t.Fatalf("drafts %+v", drafts)
	}
}
