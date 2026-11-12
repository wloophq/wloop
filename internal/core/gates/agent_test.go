package gates_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

var waits = policy{dec: gates.Decision{Mode: gate.ModeWait}}

// draftGate opens a waiting draft gate on acme for rev 3, proposed by s-worker and revised by s-reviser.
func (f fixture) draftGate(t *testing.T) gate.Gate {
	t.Helper()
	return f.open(t, gate.Gate{Kind: gate.KindDraft, Unit: acme, Subject: "42", Session: "s-worker", ApprovedRev: 3,
		Sessions: []string{"s-worker", "s-reviser"}}, waits)
}

func (f fixture) question(t *testing.T, session string) gate.Gate {
	t.Helper()
	return f.open(t, gate.Gate{Kind: gate.KindQuestion, Unit: acme, Subject: "9", Session: session}, waits)
}

func approveBy(session string) gates.Resolution {
	return gates.Resolution{Intent: "approve", By: task.ActorAgent, Session: session}
}

func TestProposerCannotResolve(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, nil, bundled)
	f.drive("s-worker", "s-user")
	g := f.draftGate(t)
	if err := f.svc.Resolve(ctx, g.ID, approveBy("s-worker")); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("the proposer resolved its draft's gate: %v", err)
	}
	if got := f.get(t, g.ID); got.Status != gate.Open || len(f.cons.Calls) != 0 {
		t.Fatalf("after the proposer's try: %+v, %v", got, f.cons.Calls)
	}
	if err := f.svc.Resolve(ctx, g.ID, approveBy("s-user")); err != nil {
		t.Fatalf("a session the user drives that never touched the draft: %v", err)
	}
	if got := f.get(t, g.ID); got.Status != gate.Approved || got.ApprovedRev != 3 || got.BySession != "s-user" {
		t.Fatalf("approved %+v", got)
	}
}

func TestReviserCannotResolve(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, nil, bundled)
	f.drive("s-reviser")
	g := f.draftGate(t)
	if err := f.svc.Resolve(ctx, g.ID, approveBy("s-reviser")); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("a reviser resolved the gate of its revision: %v", err)
	}
	if err := f.svc.TaskEnded(ctx, f.get(t, g.ID).Task, task.ActorAgent, "s-reviser", opened); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, g.ID); got.Status == gate.Approved {
		t.Fatalf("a reviser's end of the task approved the draft: %+v", got)
	}
}

func TestSupersedeForAgentClosesOnlyAnOpenDraftGate(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, nil, bundled)
	g := f.draftGate(t)
	if _, err := f.svc.SupersedeForAgent(ctx, g.ID, ""); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("no session: %v", err)
	}
	q := f.question(t, "s-worker")
	if _, err := f.svc.SupersedeForAgent(ctx, q.ID, "s-new"); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("a question gate: %v", err)
	}
	closed, err := f.svc.SupersedeForAgent(ctx, g.ID, "s-new")
	if err != nil || closed.Status != gate.Superseded || !slices.Contains(closed.Sessions, "s-reviser") {
		t.Fatalf("superseded %+v %v", closed, err)
	}
	if _, err := f.svc.SupersedeForAgent(ctx, g.ID, "s-new"); !errors.Is(err, gates.ErrResolved) {
		t.Fatalf("twice: %v", err)
	}
	if len(f.cons.Calls) != 0 {
		t.Fatalf("a revision ran a consequence: %v", f.cons.Calls)
	}
}

func answer(by task.Actor, session string) gates.Resolution {
	return gates.Resolution{Intent: "answer", Answer: "Approve (option a)", By: by, Session: session}
}

func TestApproveOptionPinsRev(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name string
		r    gates.Resolution
		rev  int64
		edit func(g *gate.Gate)
	}{
		{"a rev the draft is not at", answer(task.ActorPerson, ""), 2, nil},
		{"rev 0", answer(task.ActorPerson, ""), 0, nil},
		{"a resolver that revised the draft", answer(task.ActorAgent, "s-reviser"), 3, nil},
		{"a resolver that proposed the draft", answer(task.ActorAgent, "s-worker"), 3, nil},
		{"a draft on another unit", answer(task.ActorPerson, ""), 3, func(g *gate.Gate) { g.Unit.Key = "acme/api#8" }},
		{"a draft of another session", answer(task.ActorPerson, ""), 3, func(g *gate.Gate) { g.Session, g.Sessions = "s-other", nil }},
		{"a rule", gates.Resolution{Intent: "answer", Answer: "x", By: task.ActorWorkflow, Rule: "rule 0"}, 3, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, nil, bundled)
			f.drive("s-worker", "s-reviser")
			dg := gate.Gate{Kind: gate.KindDraft, Unit: acme, Subject: "42", Session: "s-worker", ApprovedRev: 3, Sessions: []string{"s-worker", "s-reviser"}}
			if c.edit != nil {
				c.edit(&dg)
			}
			d := f.open(t, dg, waits)
			q := f.question(t, "s-worker")
			if err := f.svc.ApproveOption(ctx, q, c.r, d, c.rev); !errors.Is(err, gates.ErrRefused) {
				t.Fatalf("ApproveOption = %v, want ErrRefused", err)
			}
			if f.get(t, q.ID).Status != gate.Open || f.get(t, d.ID).Status != gate.Open || len(f.cons.Calls) != 0 {
				t.Fatalf("a refused option resolved something: %+v %+v %v", f.get(t, q.ID), f.get(t, d.ID), f.cons.Calls)
			}
		})
	}
	t.Run("a draft revised since the option", func(t *testing.T) {
		f := newFixture(t, nil, bundled)
		d := f.draftGate(t)
		q := f.question(t, "s-worker")
		if _, err := f.svc.SupersedeForAgent(ctx, d.ID, "s-worker"); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.ApproveOption(ctx, q, answer(task.ActorPerson, ""), d, 3); !errors.Is(err, gates.ErrRefused) {
			t.Fatalf("ApproveOption = %v, want ErrRefused", err)
		}
		if f.get(t, q.ID).Status != gate.Open {
			t.Fatal("the question was answered for a draft that was revised")
		}
	})
	t.Run("a person's pick at the rev", func(t *testing.T) {
		f := newFixture(t, nil, bundled)
		d := f.draftGate(t)
		q := f.question(t, "s-worker")
		if err := f.svc.ApproveOption(ctx, q, answer(task.ActorPerson, ""), d, 3); err != nil {
			t.Fatal(err)
		}
		gq, gd := f.get(t, q.ID), f.get(t, d.ID)
		if gq.Status != gate.Answered || gq.Answer != "Approve (option a)" || gd.Status != gate.Approved || gd.By != task.ActorPerson || gd.ApprovedRev != 3 {
			t.Fatalf("question %+v, draft %+v", gq, gd)
		}
		if !slices.Equal(f.cons.Calls, []string{"deliver 2", "publish 1"}) {
			t.Fatalf("consequences %v", f.cons.Calls)
		}
	})
}

func TestApproveOptionIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, nil, bundled)
	f.drive("s-user")
	d := f.draftGate(t)
	q := f.question(t, "s-worker")
	r := answer(task.ActorAgent, "s-user")
	if err := f.svc.Resolve(ctx, q.ID, r); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := f.svc.ApproveOption(ctx, q, r, d, 3); err != nil {
			t.Fatalf("call %d after a cut: %v", i, err)
		}
	}
	if gd := f.get(t, d.ID); gd.Status != gate.Approved || gd.BySession != "s-user" {
		t.Fatalf("draft gate %+v", gd)
	}
	if !slices.Equal(f.cons.Calls, []string{"deliver 2", "publish 1"}) {
		t.Fatalf("consequences %v, want each once", f.cons.Calls)
	}
	other := answer(task.ActorPerson, "")
	other.Answer = "Something else"
	if err := f.svc.ApproveOption(ctx, q, other, d, 3); !errors.Is(err, gates.ErrResolved) {
		t.Fatalf("another answer to the resolved question: %v", err)
	}
}

// revisingDeliverer supersedes draft gate id, as an agent's revision does, while it delivers a question's
// answer.
type revisingDeliverer struct {
	svc *gates.Service
	id  int64
}

func (d *revisingDeliverer) Deliver(ctx context.Context, g gate.Gate) error {
	_, err := d.svc.SupersedeForAgent(ctx, d.id, "s-worker")
	return err
}

func TestApproveOptionLosesARevisionRaceAfterTheQuestion(t *testing.T) {
	ctx := context.Background()
	store := gatestest.NewStore(fixedNow)
	cons := &gatestest.Consequences{}
	rev := &revisingDeliverer{}
	svc := gates.New(gates.Deps{Store: store, Reader: store, Profile: bundled, Clock: clock{fixedNow}, Publisher: cons, Deliverer: rev})
	rev.svc = svc
	d, err := svc.Open(ctx, gate.Gate{Kind: gate.KindDraft, Unit: acme, Subject: "42", Session: "s-worker", ApprovedRev: 3, Sessions: []string{"s-worker"}}, waits)
	if err != nil {
		t.Fatal(err)
	}
	rev.id = d.ID
	q, err := svc.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Unit: acme, Subject: "9", Session: "s-worker"}, waits)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ApproveOption(ctx, q, answer(task.ActorPerson, ""), d, 3); !errors.Is(err, gates.ErrResolved) {
		t.Fatalf("ApproveOption = %v, want the draft's approval to lose", err)
	}
	gq, _ := store.Get(ctx, q.ID)
	gd, _ := store.Get(ctx, d.ID)
	if gq.Status != gate.Answered || gd.Status != gate.Superseded || len(cons.Calls) != 0 {
		t.Fatalf("question %s, draft %s, consequences %v", gq.Status, gd.Status, cons.Calls)
	}
}
