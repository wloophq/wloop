package messages_test

import (
	"context"
	"slices"
	"testing"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAQuestionOpensAGateNotATask(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	held, err := r.tasks.Open(ctx, coretasks.OpenRequest{Kind: task.KindAction, Title: "review acme/api#7", Unit: &r7, Origin: task.OriginUser})
	if err != nil {
		t.Fatal(err)
	}
	r.asked(t, r7)
	for range 2 {
		r.reconcile(t, r7)
	}
	m := r.only(t)
	open := r.openGates(t, gate.KindQuestion)
	if len(open) != 1 || open[0].Subject != subject(m) || open[0].Task != held.ID || open[0].Unit != r7 || open[0].Name != "question" ||
		!slices.Equal(open[0].Intents, []string{"answer", "continue", "dismiss"}) {
		t.Fatalf("gates %+v", open)
	}
	if m.TaskID != string(held.ID) {
		t.Fatalf("message %+v", m)
	}
	held7, err := r.taskStore.TasksOf(ctx, r7)
	if err != nil {
		t.Fatal(err)
	}
	if inbox := r.inbox(t); len(held7) != 1 || slices.ContainsFunc(inbox, func(e task.Task) bool { return e.ID != held.ID }) {
		t.Fatalf("tasks of the unit %+v, inbox %+v", held7, inbox)
	}
}

func TestAnsweringAQuestionGateDeliversToItsSession(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.live()
	r.reconcile(t, r7)
	m := r.only(t)
	g := r.gateOf(t, m)
	route, err := r.answer(m, messages.Answer{OptionID: "publish", Text: "and say thanks", Source: turn.Notification})
	if err != nil || route.Route != turn.Typed {
		t.Fatalf("%+v %v", route, err)
	}
	if typed := r.sess.Typed(); len(typed) != 1 || typed[0].Handle != "term_1" {
		t.Fatalf("typed %+v", typed)
	}
	b := r.book(t, r7)
	if len(b.Answers) != 1 || b.Answers[0].Gate != g.ID || b.Answers[0].Message != m.ID || b.Answers[0].OptionID != "publish" ||
		b.Answers[0].Text != "and say thanks" || b.Answers[0].Source != turn.Notification || b.Answers[0].Route != turn.Typed {
		t.Fatalf("answers %+v", b.Answers)
	}
	if got := r.gateOf(t, m); got.Status != gate.Answered || got.By != task.ActorPerson || got.Intent != "answer" {
		t.Fatalf("gate %+v", got)
	}
	if got := r.message(t, m.ID); got.Open() || got.Answer != "Publish (option publish). and say thanks" {
		t.Fatalf("message %+v", got)
	}
	r.reconcile(t, r7)
	if len(r.open(t)) != 0 || len(r.openGates(t, gate.KindQuestion)) != 0 {
		t.Fatal("the answered question came back")
	}
}

func TestAProposalGateDecidesTheLedger(t *testing.T) {
	for _, c := range []struct {
		intent string
		accept bool
	}{{"approve", true}, {"reject", false}} {
		t.Run(c.intent, func(t *testing.T) {
			r := newRig(t)
			r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"}, State: coreitems.ProposalOpen}}
			if err := r.svc.ReconcileProposals(context.Background()); err != nil {
				t.Fatal(err)
			}
			m := r.only(t)
			if _, err := r.resolve(m, c.intent, messages.Answer{}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(r.ledger.decided, []decision{{4, c.accept}}) {
				t.Fatalf("decided %+v", r.ledger.decided)
			}
			if got := r.message(t, m.ID); got.Open() || got.Answer != c.intent {
				t.Fatalf("message %+v", got)
			}
		})
	}
}

func TestAProposalGateIsSupersededByItsMessageGate(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	migrated, err := r.gates.Open(ctx, gate.Gate{Kind: gate.KindProposal, Unit: r7, Subject: gate.ProposalSubject(4)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.gates.Open(ctx, gate.Gate{Kind: gate.KindProposal, Unit: r7, Subject: gate.ProposalSubject(5)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"}, State: coreitems.ProposalOpen}}
	if err := r.svc.ReconcileProposals(ctx); err != nil {
		t.Fatal(err)
	}
	m := r.only(t)
	if g, _ := r.gateStore.Get(ctx, migrated.ID); g.Status != gate.Superseded || g.By != task.ActorWorkflow {
		t.Fatalf("the migrated gate %+v", g)
	}
	open := r.openGates(t, gate.KindProposal)
	if len(open) != 2 || open[0].ID != other.ID || open[1].Subject != subject(m) {
		t.Fatalf("open gates %+v", open)
	}
}

func TestAMigratedProposalGateDecidesItsProposal(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"}, State: coreitems.ProposalOpen}}
	g, err := r.gates.Open(ctx, gate.Gate{Kind: gate.KindProposal, Unit: r7, Subject: gate.ProposalSubject(4)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.gates.Resolve(ctx, g.ID, gatesResolution("approve")); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.ledger.decided, []decision{{4, true}}) {
		t.Fatalf("decided %+v", r.ledger.decided)
	}
}

func TestAMessageOnNoUnitGetsACaptureTask(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	m, err := r.svc.Propose(ctx, messages.ProposeRequest{Text: "Archive the stale projects?\nThree have not moved in a month.",
		Origin: message.Origin{Kind: message.OriginCore, Workflow: "stale-projects"}})
	if err != nil {
		t.Fatal(err)
	}
	g := r.gateOf(t, m)
	if g.Task == "" || string(g.Task) != m.TaskID || g.Unit != (unit.Unit{}) {
		t.Fatalf("gate %+v of %+v", g, m)
	}
	e := r.task(t, string(g.Task))
	if e.Kind != task.KindCapture || e.Title != "Archive the stale projects?" || e.Origin != task.OriginGate || len(e.Units) != 0 {
		t.Fatalf("task %+v", e)
	}
	again, err := r.svc.Propose(ctx, messages.ProposeRequest{Text: "Archive the stale projects?", Origin: message.Origin{Kind: message.OriginCore, Workflow: "stale-projects"}})
	if err != nil || again.ID != m.ID || len(r.openGates(t, gate.KindProposal)) != 1 {
		t.Fatalf("again %+v %v", again, err)
	}
}

func TestARefusedOrphanAnswerAsksAgainThroughTheMissingGate(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.asked(t, r7)
	r.live()
	r.busy()
	r.reconcile(t, r7)
	if _, err := r.answer(r.only(t), messages.Answer{OptionID: "publish", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	b := r.book(t, r7)
	b.Turns[0].State = turn.Unrecorded
	r.books.Put(b)
	r.reconcile(t, r7)
	if g := r.openGates(t, gate.KindQuestion); len(g) != 0 {
		t.Fatalf("a stopped turn whose answer waits asks again: %+v", g)
	}
	if err := r.turns.RefuseAnswer(ctx, r7, r.book(t, r7).Answers[0].ID, "its turn ended"); err != nil {
		t.Fatal(err)
	}
	r.reconcile(t, r7)
	if g := r.openGates(t, gate.KindQuestion); len(g) != 1 || g[0].Name != "missing-record" {
		t.Fatalf("gates after the refusal %+v", g)
	}
}
