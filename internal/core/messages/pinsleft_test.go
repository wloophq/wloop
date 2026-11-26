package messages_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// postQuestion asks which of two held drafts to post: every option posts one.
func postQuestion() *record.Question {
	return &record.Question{Kind: "choice", Text: "Approve, or comment?", Recommend: "approve", Options: []record.Option{
		{ID: "approve", Label: "Post the approval", Consequence: "posts the approval", Draft: &record.DraftRef{ID: 42, Rev: 1}},
		{ID: "comment", Label: "Post the comment", Consequence: "posts a comment", Draft: &record.DraftRef{ID: 43, Rev: 1}},
	}}
}

// pinsRig is a rig whose live review turn on r7 asks q, its drafts 42 and 43 held.
func pinsRig(t *testing.T, q *record.Question) (*rig, *optionDrafts, message.Message) {
	t.Helper()
	r := newRig(t)
	od := newOptionDrafts(t, r)
	od.put(heldDraft(42, r7, 1, 1), "term_1")
	od.put(heldDraft(43, r7, 1, 1), "term_1")
	r.started(r7)
	r.record(t, r7, 1, askedRecord(q))
	r.live()
	r.pass(t, r7)
	return r, od, r.only(t)
}

// leave moves draft id to state for reason, its gate closing with status as the route that moved it closes it.
func (o *optionDrafts) leave(id int64, state draft.State, reason string, status gate.Status) {
	o.mu.Lock()
	d := o.drafts[id]
	d.State, d.Reason = state, reason
	o.drafts[id] = d
	o.mu.Unlock()
	res := gates.Resolution{Status: status, By: task.ActorWorkflow}
	switch status {
	case gate.Rejected:
		res = gates.Resolution{Status: status, Intent: "discard", By: task.ActorPerson}
	case gate.Approved:
		res = gates.Resolution{Status: status, Intent: "approve", By: task.ActorWorkflow, Rule: "send"}
	}
	if _, err := o.r.gateStore.Resolve(context.Background(), o.gateOf(id).ID, res); err != nil && !errors.Is(err, gates.ErrResolved) {
		panic(err)
	}
}

// requireMoot fails t unless m is closed with no answer, its gate superseded, and the turn that asked it dismissed
// for messages.ReasonPinsLeft.
func requireMoot(t *testing.T, r *rig, m message.Message) {
	t.Helper()
	if open := r.open(t); len(open) != 0 {
		t.Fatalf("open messages %+v, want the question closed", open)
	}
	if got := r.message(t, m.ID); got.Open() || got.Answer != "" {
		t.Fatalf("message %+v, want closed with no answer", got)
	}
	if g := r.gateOf(t, m); g.Status != gate.Superseded || g.By != task.ActorWorkflow {
		t.Fatalf("question gate %s by %s, want superseded by the workflow", g.Status, g.By)
	}
	if open := r.openGates(t, gate.KindQuestion); len(open) != 0 {
		t.Fatalf("open question gates %+v, want none", open)
	}
	b := r.book(t, r7)
	if st := b.Turns[0].State; st != turn.Dismissed {
		t.Fatalf("turn %s, want dismissed", st)
	}
	if !slices.ContainsFunc(r.books.Events(), func(ev port.Event) bool {
		return ev.Kind == turns.KindTurnDismissedByRule && ev.Data == messages.ReasonPinsLeft
	}) {
		t.Fatalf("events %+v, want the turn dismissed for %s", r.books.Events(), messages.ReasonPinsLeft)
	}
}

func TestAQuestionClosesWhenItsLastPinnedDraftLeaves(t *testing.T) {
	for _, c := range []struct {
		name   string
		state  draft.State
		reason string
		status gate.Status
	}{
		{"discarded", draft.Dropped, "user", gate.Rejected},
		{"withdrawn as moot", draft.Dropped, draft.WithdrawMoot, gate.Superseded},
		{"withdrawn as its unit ended", draft.Dropped, draft.WithdrawEnded, gate.Superseded},
		{"posted by another route", draft.Queued, "", gate.Approved},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, od, m := pinsRig(t, postQuestion())
			od.leave(42, draft.Dropped, "user", gate.Rejected)
			r.pass(t, r7)
			if g := r.gateOf(t, m); g.Status != gate.Open {
				t.Fatalf("question gate %s after one of two drafts left, want open", g.Status)
			}
			od.leave(43, c.state, c.reason, c.status)
			r.pass(t, r7)
			requireMoot(t, r, m)
		})
	}
}

func TestAConfirmClosesWhenItsDraftLeaves(t *testing.T) {
	q := &record.Question{Kind: "confirm", Text: "Post the approval?", Draft: &record.DraftRef{ID: 42, Rev: 1}}
	r, od, m := pinsRig(t, q)
	od.leave(42, draft.Dropped, "user", gate.Rejected)
	r.pass(t, r7)
	requireMoot(t, r, m)
}

// TestAQuestionWithAPinStillWaitingStaysOpen: one draft left, the other waits; the question stays, and picking the
// option whose draft left is refused, naming why, with nothing approved or resumed.
func TestAQuestionWithAPinStillWaitingStaysOpen(t *testing.T) {
	r, od, m := pinsRig(t, postQuestion())
	od.leave(42, draft.Dropped, "user", gate.Rejected)
	r.pass(t)
	q := r.gateOf(t, m)
	if q.Status != gate.Open || r.book(t, r7).Turns[0].State != turn.Asked {
		t.Fatalf("question gate %s, turn %s: want open and asked", q.Status, r.book(t, r7).Turns[0].State)
	}
	err := r.gates.PickOption(context.Background(), q.ID, gates.Resolution{Intent: "answer", Answer: messages.GateAnswer(messages.Answer{OptionID: "approve"}),
		By: task.ActorPerson}, 1, nil)
	if !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("picking the option whose draft left: %v, want ErrRefused", err)
	}
	if len(od.approvals) != 0 || r.gateOf(t, m).Status != gate.Open {
		t.Fatalf("approvals %+v, question %s: want none, still open", od.approvals, r.gateOf(t, m).Status)
	}
	if _, err := r.answer(m, messages.Answer{OptionID: "comment", Source: turn.Cockpit}); err != nil {
		t.Fatalf("picking the option whose draft waits: %v", err)
	}
	if len(od.approvals) != 1 || od.approvals[0].drafted != od.gateOf(43).ID {
		t.Fatalf("approvals %+v, want draft 43's", od.approvals)
	}
}

// TestAQuestionWithAnOptionPostingNothingStaysOpen: an option that pins no draft is still an answer the question
// takes, so its drafts leaving leave it open.
func TestAQuestionWithAnOptionPostingNothingStaysOpen(t *testing.T) {
	q := postQuestion()
	q.Options = append(q.Options, record.Option{ID: "hold", Label: "Hold", Consequence: "posts nothing"})
	r, od, m := pinsRig(t, q)
	od.leave(42, draft.Dropped, "user", gate.Rejected)
	od.leave(43, draft.Dropped, draft.WithdrawMoot, gate.Superseded)
	r.pass(t, r7)
	if g := r.gateOf(t, m); g.Status != gate.Open || r.book(t, r7).Turns[0].State != turn.Asked {
		t.Fatalf("question gate %s, turn %s: want open and asked", g.Status, r.book(t, r7).Turns[0].State)
	}
}

// TestAQuestionStuckBeforeThisCloseHealsOnTheFirstPass: both drafts left while nothing reconciled; the first pass
// over every book, as the daemon's start runs, closes the question.
func TestAQuestionStuckBeforeThisCloseHealsOnTheFirstPass(t *testing.T) {
	r, od, m := pinsRig(t, postQuestion())
	od.leave(42, draft.Dropped, "user", gate.Rejected)
	od.leave(43, draft.Dropped, "user", gate.Rejected)
	r.pass(t)
	requireMoot(t, r, m)
	r.pass(t)
	if open := r.open(t); len(open) != 0 {
		t.Fatalf("open messages %+v after a second pass, want none", open)
	}
}

// TestAPickInFlightIsNotMooted: the pick of a confirm queues its draft before it answers the question; a pass that
// runs in between finds the question being decided and leaves it to the pick.
func TestAPickInFlightIsNotMooted(t *testing.T) {
	q := &record.Question{Kind: "confirm", Text: "Post the approval?", Draft: &record.DraftRef{ID: 42, Rev: 1}}
	r, od, m := pinsRig(t, q)
	hook := &interleaved{optionDrafts: od}
	hook.then = func() {
		r.pass(t, r7)
	}
	r.gates = gates.New(gates.Deps{Store: r.gateStore, Reader: r.gateStore, Profile: profileGates, Tasks: gateTasks{r.taskStore, r.tasks}, Clock: r.clock,
		Deliverer: r.svc, Decider: r.svc, Publisher: hook, Options: r.svc, Log: t.Logf, Sessions: drivenSessions(nil)})
	r.svc.Gates = serviceGates{r.gates, r.gateStore}
	route, err := r.answer(m, messages.Answer{Yes: true, Source: turn.Cockpit})
	if err != nil || route.Route != turn.Approved {
		t.Fatalf("the pick: %+v %v, want approved", route, err)
	}
	if g := r.gateOf(t, m); g.Status != gate.Answered {
		t.Fatalf("question gate %s, want answered by the pick", g.Status)
	}
	if slices.ContainsFunc(r.books.Events(), func(ev port.Event) bool { return ev.Kind == turns.KindTurnDismissedByRule }) {
		t.Fatalf("the turn was dismissed during the pick: %+v", r.books.Events())
	}
}

// TestADraftChangeClosesTheQuestionItLeft: the worker reconciles the unit a draft change notes, so the question its
// last pinned draft left closes without waiting for another turn or task change.
func TestADraftChangeClosesTheQuestionItLeft(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, od, m := pinsRig(t, postQuestion())
		log := change.NewLog(64, t.Logf)
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { r.svc.Run(ctx, log) })
		t.Cleanup(func() {
			cancel()
			wg.Wait()
		})
		synctest.Wait()
		od.leave(42, draft.Dropped, "user", gate.Rejected)
		od.leave(43, draft.Dropped, "user", gate.Rejected)
		log.Note(port.TopicDrafts, r7)
		synctest.Wait()
		requireMoot(t, r, m)
	})
}

// TestAnAnsweredQuestionIsNotMooted: the pick approved draft 42 and answered the question, and its other draft
// was withdrawn, while the message still settles with a gate open again; its drafts left through the pick, so the
// question is not closed as one whose drafts left, and its turn is not dismissed.
func TestAnAnsweredQuestionIsNotMooted(t *testing.T) {
	ctx := context.Background()
	r, od, m := pinsRig(t, postQuestion())
	q := r.gateOf(t, m)
	if _, err := r.gateStore.Resolve(ctx, q.ID, gates.Resolution{Status: gate.Answered, Intent: "answer",
		Answer: messages.GateAnswer(messages.Answer{OptionID: "approve"}), By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	od.leave(42, draft.Queued, "", gate.Approved)
	od.leave(43, draft.Dropped, messages.ReasonOtherOption, gate.Superseded)
	r.pass(t, r7)
	if r.book(t, r7).Turns[0].State == turn.Dismissed {
		t.Fatal("the turn of an answered question was dismissed as if its drafts left")
	}
	if slices.ContainsFunc(r.books.Events(), func(ev port.Event) bool { return ev.Kind == turns.KindTurnDismissedByRule }) {
		t.Fatalf("events %+v, want no dismissal", r.books.Events())
	}
}

// TestADraftOfAnotherUnitClosesItsQuestion: draft 43 posts on r8 though its turn ran on r7; a pass over r8, the unit
// its change notes, closes r7's question once both drafts left.
func TestADraftOfAnotherUnitClosesItsQuestion(t *testing.T) {
	r := newRig(t)
	od := newOptionDrafts(t, r)
	od.put(heldDraft(42, r7, 1, 1), "term_1")
	elsewhere := heldDraft(43, r8, 1, 1)
	elsewhere.Origin.Unit = r7
	od.put(elsewhere, "term_1")
	r.started(r7)
	r.record(t, r7, 1, askedRecord(postQuestion()))
	r.live()
	r.pass(t, r7)
	m := r.only(t)
	od.leave(42, draft.Dropped, "user", gate.Rejected)
	r.pass(t, r7)
	od.leave(43, draft.Dropped, draft.WithdrawMoot, gate.Superseded)
	r.pass(t, r8)
	requireMoot(t, r, m)
}

// racingGates let a close run while a pass is between building its list of open messages and opening their gates:
// the pass's sweep of missing gates, its last read before it opens them, starts the close and waits until the close
// has superseded the question's gate.
type racingGates struct {
	serviceGates
	armed      *atomic.Bool
	superseded chan struct{}
	closing    func()
}

func (g racingGates) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	if q.Kind == gate.KindQuestion && q.Unit.IsZero() && g.armed.CompareAndSwap(true, false) {
		go g.closing()
		<-g.superseded
	}
	return g.serviceGates.Where(ctx, q)
}

func (g racingGates) SupersedeQuestion(ctx context.Context, id int64, settle func(context.Context) (bool, error)) (bool, error) {
	closed, err := g.serviceGates.SupersedeQuestion(ctx, id, settle)
	close(g.superseded)
	return closed, err
}

// TestAPassDuringTheCloseOpensNoGate: a pass that listed the question open, its turn still asking, reaches the
// question's gates after the close superseded the gate and before it closed the message; it opens the message no new
// gate.
func TestAPassDuringTheCloseOpensNoGate(t *testing.T) {
	ctx := context.Background()
	r, od, m := pinsRig(t, postQuestion())
	od.leave(42, draft.Dropped, "user", gate.Rejected)
	od.leave(43, draft.Dropped, "user", gate.Rejected)
	closed := make(chan struct{})
	g := racingGates{serviceGates: serviceGates{r.gates, r.gateStore}, armed: &atomic.Bool{}, superseded: make(chan struct{})}
	g.closing = func() {
		defer close(closed)
		if err := r.svc.CloseLeftQuestions(ctx); err != nil {
			t.Errorf("the close: %v", err)
		}
	}
	r.svc.Gates = g
	g.armed.Store(true)
	err := r.svc.Reconcile(ctx, r7)
	<-closed
	if err != nil {
		t.Fatal(err)
	}
	requireMoot(t, r, m)
	all, werr := r.gateStore.Where(ctx, gates.Query{Kind: gate.KindQuestion, Statuses: []gate.Status{gate.Open, gate.Answered,
		gate.Dismissed, gate.Superseded}})
	if werr != nil {
		t.Fatal(werr)
	}
	if len(all) != 1 {
		t.Fatalf("question gates %+v, want the first alone, no gate reopened", all)
	}
}

// TestTheWorkerHealsOnceItsDraftsAreReadable: the daemon starts with a question whose drafts all left while it was
// down; the worker's first pass waits for Ready, the outbox being built, and then closes it.
func TestTheWorkerHealsOnceItsDraftsAreReadable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, od, m := pinsRig(t, postQuestion())
		od.leave(42, draft.Dropped, "user", gate.Rejected)
		od.leave(43, draft.Dropped, "user", gate.Rejected)
		ready := make(chan struct{})
		r.svc.Ready = ready
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		wg.Go(func() { r.svc.Run(ctx, change.NewLog(64, t.Logf)) })
		t.Cleanup(func() {
			cancel()
			wg.Wait()
		})
		synctest.Wait()
		if len(r.open(t)) != 1 {
			t.Fatal("the worker ran a pass before its drafts were readable")
		}
		close(ready)
		synctest.Wait()
		requireMoot(t, r, m)
	})
}

// pass is a pass of the messages worker over units: their reconcile, then the close of the questions whose drafts
// left.
func (r *rig) pass(t *testing.T, units ...unit.Unit) {
	t.Helper()
	r.reconcile(t, units...)
	if err := r.svc.CloseLeftQuestions(context.Background()); err != nil {
		t.Fatal(err)
	}
}
