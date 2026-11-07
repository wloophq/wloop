package outbox

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
)

// discarded proposes a reply with effects and has a person discard it through its gate.
func (e *env) discarded(t *testing.T, effects ...string) draft.Draft {
	t.Helper()
	p := reply("closing")
	p.Effects = effects
	d := e.propose(p)
	if err := e.decider.Resolve(context.Background(), e.gate(d.ID).ID, gates.Resolution{Intent: "discard", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	return e.get(d.ID)
}

func silentEnv() (*env, *effectLog) {
	e := newEnv()
	l := withApplier(e)
	e.svc.Silent = func(name string) bool { return name == "unit.closed" }
	return e, l
}

func TestCloseSilentlyAppliesTheEffectsWithoutPosting(t *testing.T) {
	e, l := silentEnv()
	d := e.discarded(t, "unit.closed")
	got, err := e.svc.CloseSilently(context.Background(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != draft.Dropped || got.Reason != draft.ReasonClosedSilently || got.EffectsAppliedAt.IsZero() {
		t.Fatalf("draft %+v", got)
	}
	if !slices.Equal(l.names(), []string{"unit.closed"}) || len(e.fx.published) != 0 {
		t.Fatalf("applied %v, published %v", l.names(), e.fx.published)
	}
	if err := e.svc.ApplyPending(context.Background()); err != nil || len(l.names()) != 1 {
		t.Fatalf("applied again: %v %v", l.names(), err)
	}
}

func TestCloseSilentlyRefusesAnEffectThatPosts(t *testing.T) {
	e, l := silentEnv()
	d := e.discarded(t, "unit.closed", "task.nudged")
	if _, err := e.svc.CloseSilently(context.Background(), d.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err %v, want invalid", err)
	}
	if got := e.get(d.ID); got.Reason != "user" || len(l.names()) != 0 {
		t.Fatalf("draft %+v, applied %v", got, l.names())
	}
}

func TestCloseSilentlyNeedsADraftThePersonDiscarded(t *testing.T) {
	e, _ := silentEnv()
	p := reply("closing")
	p.Effects = []string{"unit.closed"}
	held := e.propose(p)
	if _, err := e.svc.CloseSilently(context.Background(), held.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a held draft: %v", err)
	}
	none := e.discarded(t)
	if _, err := e.svc.CloseSilently(context.Background(), none.ID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a draft of no effects: %v", err)
	}
}

func TestADiscardedDraftWithoutCloseSilentlyAppliesNothing(t *testing.T) {
	e, l := silentEnv()
	e.discarded(t, "unit.closed")
	if err := e.svc.ApplyPending(context.Background()); err != nil || len(l.names()) != 0 {
		t.Fatalf("applied %v %v", l.names(), err)
	}
}

func TestFailedSilentEffectsAreRetried(t *testing.T) {
	e, l := silentEnv()
	d := e.discarded(t, "unit.closed")
	l.fail["unit.closed"] = errBoom
	if _, err := e.svc.CloseSilently(context.Background(), d.ID); !errors.Is(err, errBoom) {
		t.Fatalf("err %v", err)
	}
	if got := e.get(d.ID); got.Reason != draft.ReasonClosedSilently || !got.EffectsAppliedAt.IsZero() {
		t.Fatalf("after the failure %+v", got)
	}
	delete(l.fail, "unit.closed")
	if err := e.svc.ApplyPending(context.Background()); err != nil || !slices.Equal(l.names(), []string{"unit.closed"}) {
		t.Fatalf("retry applied %v %v", l.names(), err)
	}
	if e.get(d.ID).EffectsAppliedAt.IsZero() {
		t.Fatal("not marked applied")
	}
}
