package messages_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAQuestionOpensAGateNotATask(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	held, err := r.tasks.Open(ctx, coretasks.OpenRequest{Title: "review acme/api#7", Unit: &r7, Origin: task.OriginUser})
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
		m.Origin.Session == "" || open[0].Session != m.Origin.Session ||
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
	if e.Title != "Archive the stale projects?" || e.Origin != task.OriginGate || len(e.Units) != 0 {
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

func TestAMissingGateMovesOffAClosedTask(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	held, err := r.tasks.Open(ctx, coretasks.OpenRequest{Title: "review acme/api#7", Unit: &r7, Origin: task.OriginUser})
	if err != nil {
		t.Fatal(err)
	}
	r.started(r7)
	r.record(t, r7, 1, record.Record{State: record.Failed, Summary: "the build broke", Resume: "rerun it"})
	b := r.book(t, r7)
	b.Turns[0].State = turn.Failed
	r.books.Put(b)
	r.reconcile(t, r7)
	first := r.openGates(t, gate.KindQuestion)
	if len(first) != 1 || first[0].Task != held.ID {
		t.Fatalf("gates %+v", first)
	}
	if err := r.tasks.Close(ctx, held.ID, task.CategoryDone); err != nil {
		t.Fatal(err)
	}
	r.reconcile(t, r7)
	if g, _ := r.gateStore.Get(ctx, first[0].ID); g.Status != gate.Superseded {
		t.Fatalf("the gate on the done task %+v", g)
	}
	open := r.openGates(t, gate.KindQuestion)
	if len(open) != 1 || open[0].Name != "missing-record" || open[0].Task == held.ID || open[0].Subject != first[0].Subject {
		t.Fatalf("open gates %+v", open)
	}
	if e := r.task(t, string(open[0].Task)); e.Category == task.CategoryDone || e.Category == task.CategoryDropped {
		t.Fatalf("the moved gate is on %+v", e)
	}
}

func TestAnAnswerWhoseResumeFailsIsLoggedAndItsQuestionAsksAgain(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	m := r.only(t)
	r.launch.err = errors.New("git fetch: Permission denied (publickey)")
	_, err := r.answer(m, messages.Answer{OptionID: "publish", Source: turn.Cockpit})
	if err == nil || !strings.Contains(err.Error(), "Permission denied (publickey)") {
		t.Fatalf("answer = %v", err)
	}
	if b := r.book(t, r7); len(b.Answers) != 1 || b.Answers[0].Route != turn.Refused || b.Turns[0].State != turn.Asked {
		t.Fatalf("answers %+v, turns %+v", b.Answers, b.Turns)
	}
	if !slices.ContainsFunc(r.logged(), func(l string) bool { return strings.Contains(l, "Permission denied (publickey)") }) {
		t.Fatalf("log %q", r.logged())
	}
	r.reconcile(t, r7)
	again := r.openGates(t, gate.KindQuestion)
	if len(again) != 1 {
		t.Fatalf("the question asks again through %+v", again)
	}
	r.launch.err = nil
	if _, err := r.answer(r.only(t), messages.Answer{OptionID: "publish", Source: turn.Cockpit}); err != nil || len(r.launch.launches) != 1 {
		t.Fatalf("the second answer = %v, launches %+v", err, r.launch.launches)
	}
}

// TestAReconcileRacingADecisionLeavesNoGateOnTheClosedMessage holds a reconcile that saw a proposal's message
// open, with its gate decided, right before it opens the message a new gate, while the decision closes the
// message: wherever the decision's close is when the reconcile goes on, no gate stays open on the closed
// message, which would read the decided proposal open again.
func TestAReconcileRacingADecisionLeavesNoGateOnTheClosedMessage(t *testing.T) {
	for _, closing := range []bool{false, true} {
		name := map[bool]string{false: "the decision closed the message", true: "the decision is closing the message"}[closing]
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			ctx := context.Background()
			r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"}, State: coreitems.ProposalOpen}}
			if err := r.svc.ReconcileProposals(ctx); err != nil {
				t.Fatal(err)
			}
			m := r.only(t)
			g, err := r.gateStore.Resolve(ctx, r.gateOf(t, m).ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson})
			if err != nil {
				t.Fatal(err)
			}
			held := &holdingStore{Store: r.msgs, checked: make(chan struct{}), goOn: make(chan struct{}), closing: make(chan struct{}), close: make(chan struct{})}
			r.svc.Store = held
			reconciled := make(chan error, 1)
			go func() { reconciled <- r.svc.ReconcileProposals(context.WithValue(ctx, reconcilerKey{}, true)) }()
			<-held.checked
			decided := make(chan error, 1)
			decideCtx := ctx
			if closing {
				decideCtx = context.WithValue(ctx, deciderKey{}, true)
			}
			go func() { decided <- r.svc.Decide(decideCtx, g) }()
			if closing {
				<-held.closing
				close(held.goOn)
				wait(t, reconciled)
				close(held.close)
				wait(t, decided)
			} else {
				close(held.close)
				wait(t, decided)
				close(held.goOn)
				wait(t, reconciled)
			}
			if got := r.message(t, m.ID); got.Open() {
				t.Fatalf("message %+v", got)
			}
			if open := r.openGates(t, gate.KindProposal); len(open) != 0 {
				t.Fatalf("open gates on the decided proposal %+v", open)
			}
		})
	}
}

func wait(t *testing.T, done <-chan error) {
	t.Helper()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type reconcilerKey struct{}

type deciderKey struct{}

// holdingStore holds the reconciler's first read of a message, after it reads, until goOn, and the decider's
// close, before it closes, until close.
type holdingStore struct {
	message.Store
	once           sync.Once
	checked, goOn  chan struct{}
	closing, close chan struct{}
}

func (s *holdingStore) Message(ctx context.Context, id int64) (message.Message, bool, error) {
	m, ok, err := s.Store.Message(ctx, id)
	if ctx.Value(reconcilerKey{}) != nil {
		s.once.Do(func() {
			close(s.checked)
			<-s.goOn
		})
	}
	return m, ok, err
}

func (s *holdingStore) Close(ctx context.Context, id int64, answer string, at time.Time) error {
	if ctx.Value(deciderKey{}) != nil {
		close(s.closing)
		<-s.close
	}
	return s.Store.Close(ctx, id, answer, at)
}
