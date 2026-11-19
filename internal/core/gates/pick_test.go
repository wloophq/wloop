package gates_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// pins is a question whose one option pins the draft of a gate the test opens, at rev 3.
type pins struct {
	store    *gatestest.Store
	question int64
	drafted  int64
}

func (p *pins) OptionDraft(ctx context.Context, q gate.Gate, r gates.Resolution) (gate.Gate, int64, bool, error) {
	if q.ID != p.question || r.Answer != "Approve (option a)" {
		return gate.Gate{}, 0, false, nil
	}
	g, err := p.store.Get(ctx, p.drafted)
	return g, 3, err == nil, err
}

func (p *pins) Pinning(ctx context.Context, d gate.Gate) (gate.Gate, string, int64, bool, error) {
	if d.Subject != "42" {
		return gate.Gate{}, "", 0, false, nil
	}
	q, err := p.store.Get(ctx, p.question)
	return q, "Approve (option a)", 3, err == nil && q.Status == gate.Open, err
}

func pickFixture(t *testing.T) (fixture, gate.Gate, gate.Gate) {
	t.Helper()
	store := gatestest.NewStore(fixedNow)
	cons := &gatestest.Consequences{}
	p := &pins{store: store}
	svc := gates.New(gates.Deps{Store: store, Reader: store, Profile: bundled, Clock: clock{fixedNow}, Publisher: cons, Deliverer: cons,
		Sessions: drivenSessions{}, Options: p})
	f := fixture{svc: svc, store: store, cons: cons}
	d := f.draftGate(t)
	q := f.question(t, "s-worker")
	p.question, p.drafted = q.ID, d.ID
	return f, d, q
}

// A person edits the draft an option pins as they pick the option: the edit's gate is approved, so what posts is
// what they edited, and the question is answered with the option.
func TestAPickWithAnEditApprovesTheEditedDraft(t *testing.T) {
	ctx := context.Background()
	f, d, q := pickFixture(t)
	var edited gate.Gate
	edit := func(ctx context.Context, drafted gate.Gate) (gate.Gate, error) {
		if err := f.svc.Supersede(ctx, drafted.ID, task.ActorPerson); err != nil {
			return gate.Gate{}, err
		}
		g, err := f.svc.Open(ctx, gate.Gate{Kind: gate.KindDraft, Unit: acme, Subject: "42", Session: "s-worker", ApprovedRev: 4}, waits)
		edited = g
		return g, err
	}
	if err := f.svc.PickOption(ctx, q.ID, answer(task.ActorPerson, ""), 3, edit); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, edited.ID); got.Status != gate.Approved || got.ApprovedRev != 4 || got.By != task.ActorPerson {
		t.Fatalf("the edit's gate %+v, want approved at rev 4 by the person", got)
	}
	if got := f.get(t, q.ID); got.Status != gate.Answered || got.Answer != "Approve (option a)" {
		t.Fatalf("question %+v, want answered with the option", got)
	}
	if got := f.get(t, d.ID); got.Status != gate.Superseded {
		t.Fatalf("the pinned rev's gate %+v, want superseded by the edit", got)
	}
}

// A pick at a rev the draft no longer waits on refuses, before the edit runs: the person saw another version.
func TestAPickAtAnotherRevChangesNothing(t *testing.T) {
	ctx := context.Background()
	f, d, q := pickFixture(t)
	edit := func(context.Context, gate.Gate) (gate.Gate, error) {
		t.Fatal("the edit ran for a rev the draft is not at")
		return gate.Gate{}, nil
	}
	if err := f.svc.PickOption(ctx, q.ID, answer(task.ActorPerson, ""), 2, edit); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("a pick at rev 2: %v, want ErrRefused", err)
	}
	if f.get(t, q.ID).Status != gate.Open || f.get(t, d.ID).Status != gate.Open || len(f.cons.Calls) != 0 {
		t.Fatalf("question %+v, draft %+v, consequences %v: want both open", f.get(t, q.ID), f.get(t, d.ID), f.cons.Calls)
	}
	if err := f.svc.PickOption(ctx, q.ID, gates.Resolution{Intent: "answer", Answer: "Hold", By: task.ActorPerson}, 3, nil); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("a pick of an option that pins no draft: %v, want ErrRefused", err)
	}
}
