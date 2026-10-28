package messages_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAskedRecordBecomesAMessage(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	m := r.only(t)
	q := question()
	if m.Kind != message.Question || m.Unit != r7 || m.Text != q.Text || !slices.Equal(m.Options, q.Options) || m.Recommend != "publish" || m.Confirm ||
		m.Origin != (message.Origin{Kind: message.OriginTurn, TurnSeq: 1, Session: "term_1"}) || m.State != message.Open {
		t.Fatalf("message %+v", m)
	}
	e := r.task(t, m.TaskID)
	if e.Kind != task.KindMessage || e.State != task.State("inbox") || e.Origin != task.OriginMessage || e.Title != q.Text || !slices.Equal(e.Units, []unit.Unit{r7}) {
		t.Fatalf("entry %+v", e)
	}

	confirm := newRig(t)
	confirm.started(r8)
	confirm.record(t, r8, 1, askedRecord(&record.Question{Kind: "confirm", Text: "Publish?", Recommend: "yes"}))
	confirm.reconcile(t)
	if m := confirm.only(t); !m.Confirm || m.Options != nil || m.Recommend != "yes" {
		t.Fatalf("confirm %+v", m)
	}
}

func TestAskedTurnIsOneTask(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.items.byUnit[r7] = []items.Item{{ID: "gh:review", Unit: r7, Kind: "request", Summary: "review it", State: "open",
		WaitingOn: []items.Person{{Key: "github:me"}}, CreatedAt: t0}}
	ctx := context.Background()
	for range 2 {
		r.reconcile(t, r7)
		if err := r.tasks.Sync(ctx, []unit.Unit{r7}); err != nil {
			t.Fatal(err)
		}
	}
	var questions []task.Task
	for _, e := range r.inbox(t) {
		if e.Kind == task.KindMessage {
			questions = append(questions, e)
		}
	}
	if len(questions) != 1 || questions[0].ID != task.ID(r.only(t).TaskID) {
		t.Fatalf("question entries %+v", questions)
	}
}

func TestStoppedTurnBecomesAMessage(t *testing.T) {
	for _, c := range []struct {
		state record.State
		want  string
	}{
		{record.Failed, "The agent's turn failed: the build broke"},
		{record.UnrecordedState, "The agent ended its turn without a record: the build broke"},
	} {
		t.Run(string(c.state), func(t *testing.T) {
			r := newRig(t)
			r.started(r7)
			r.record(t, r7, 1, record.Record{State: c.state, Summary: "the build broke", Resume: "rerun it"})
			r.reconcile(t, r7)
			if m := r.only(t); m.Text != c.want || m.Kind != message.Question || m.Options != nil {
				t.Fatalf("message %+v", m)
			}
		})
	}
}

func TestReaskedTurnUpdatesItsMessage(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	first := r.only(t)
	again := question()
	again.Text = "Publish the approval now?"
	r.record(t, r7, 1, askedRecord(again))
	r.reconcile(t, r7)
	m := r.only(t)
	if m.ID != first.ID || m.TaskID != first.TaskID || m.Text != again.Text {
		t.Fatalf("re-asked %+v, first %+v", m, first)
	}
	if n := len(r.inbox(t)); n != 1 {
		t.Fatalf("inbox holds %d entries", n)
	}
}

func TestTurnEventsCloseTheMessage(t *testing.T) {
	for _, c := range []struct {
		name   string
		event  func(t *testing.T, r *rig)
		state  message.State
		answer string
		task   task.State
	}{
		{"answered", func(t *testing.T, r *rig) {
			r.live()
			if _, err := r.turns.Answer(context.Background(), turns.AnswerRequest{Unit: r7, OptionID: "publish", Source: turn.Cockpit}); err != nil {
				t.Fatal(err)
			}
		}, message.Answered, "Publish (option publish)", task.State("done")},
		{"question_dismissed", func(t *testing.T, r *rig) {
			if err := r.turns.Dismiss(context.Background(), r7); err != nil {
				t.Fatal(err)
			}
		}, message.Dismissed, "", task.State("trash")},
		{"replaced", func(t *testing.T, r *rig) {
			b := r.book(t, r7)
			b.Send(turn.Turn{Intent: b.Turns[0].Intent, ID: "def", Trigger: turn.ByHand, SentTo: "term_2", SentAt: r.clock.Now()})
			r.books.Put(b)
		}, message.Superseded, "", task.State("trash")},
		{"done", func(t *testing.T, r *rig) {
			r.record(t, r7, 1, record.Record{State: record.Done, Summary: "published", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://example.com/r"}})
		}, message.Superseded, "", task.State("trash")},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.asked(t, r7)
			r.reconcile(t, r7)
			m := r.only(t)
			c.event(t, r)
			r.reconcile(t, r7)
			got := r.message(t, m.ID)
			if got.State != c.state || got.Answer != c.answer || got.ClosedAt.IsZero() {
				t.Fatalf("closed %+v", got)
			}
			if e := r.task(t, m.TaskID); e.State != c.task {
				t.Fatalf("entry %s, want %s", e.State, c.task)
			}
			if open := r.open(t); len(open) != 0 {
				t.Fatalf("open %+v", open)
			}
		})
	}
	t.Run("turn_withdrawn", func(t *testing.T) {
		r := newRig(t)
		r.asked(t, r7)
		r.reconcile(t, r7)
		first := r.only(t)
		b := r.book(t, r7)
		b.Send(turn.Turn{Intent: b.Turns[0].Intent, ID: "def", Trigger: turn.ByHand, SentTo: "term_2", SentAt: r.clock.Now()})
		b = r.books.Put(b)
		r.reconcile(t, r7)
		if open := r.open(t); len(open) != 0 {
			t.Fatalf("a replaced turn's question stayed open: %+v", open)
		}
		if _, err := b.Withdraw(2, r.clock.Now()); err != nil {
			t.Fatal(err)
		}
		r.books.Put(b)
		r.reconcile(t, r7)
		back := r.only(t)
		if back.ID == first.ID || back.Origin.TurnSeq != 1 || r.message(t, first.ID).State != message.Superseded {
			t.Fatalf("after the withdraw %+v, first %+v", back, r.message(t, first.ID))
		}
		if n := len(r.inbox(t)); n != 1 {
			t.Fatalf("inbox holds %d entries", n)
		}
	})
}

func TestReconcileRepairsAMissedEvent(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.asked(t, r8)
	r.reconcile(t)
	if n := len(r.open(t)); n != 2 {
		t.Fatalf("open %d", n)
	}
	r.record(t, r7, 1, record.Record{State: record.Done, Summary: "published", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://example.com/r"}})
	r.reconcile(t, r8)
	if n := len(r.open(t)); n != 2 {
		t.Fatalf("a reconcile of r8 touched r7: %d open", n)
	}
	r.reconcile(t)
	if m := r.only(t); m.Unit != r8 {
		t.Fatalf("open %+v", m)
	}

	lost := newRig(t)
	lost.asked(t, r7)
	m, err := lost.msgs.Upsert(context.Background(), message.Message{Unit: r7, Kind: message.Question, Text: "Publish the approval?",
		Origin: message.Origin{Kind: message.OriginTurn, TurnSeq: 1, Session: "term_1"}, CreatedAt: t0})
	if err != nil {
		t.Fatal(err)
	}
	lost.reconcile(t, r7)
	if got := lost.only(t); got.ID != m.ID || got.TaskID == "" || len(got.Options) != 2 {
		t.Fatalf("a message left without its entry was not repaired: %+v", got)
	}
}

func TestDismissingAQuestionMessageDismissesTheTurn(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	m := r.only(t)
	if err := r.svc.Dismiss(context.Background(), m.ID); err != nil {
		t.Fatal(err)
	}
	if b := r.book(t, r7); b.Turns[0].State != turn.Dismissed {
		t.Fatalf("turn %s", b.Turns[0].State)
	}
	if got := r.message(t, m.ID); got.State != message.Dismissed || r.task(t, m.TaskID).State != task.State("trash") {
		t.Fatalf("message %+v", got)
	}
	if err := r.svc.Dismiss(context.Background(), m.ID); !errors.Is(err, messages.ErrClosed) {
		t.Fatalf("dismissing again: %v", err)
	}
}

func TestAnsweringAStaleQuestionIsRefused(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	m := r.only(t)
	r.record(t, r7, 1, record.Record{State: record.Done, Summary: "published", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://example.com/r"}})
	if _, err := r.svc.Answer(context.Background(), m.ID, messages.Answer{OptionID: "publish", Source: turn.Cockpit}); !errors.Is(err, turn.ErrNoQuestion) {
		t.Fatalf("answer to a done turn: %v", err)
	}
	if got := r.message(t, m.ID); got.State != message.Superseded {
		t.Fatalf("the stale message is %s", got.State)
	}
	if len(r.book(t, r7).Answers) != 0 {
		t.Fatal("the stale answer was stored")
	}
}

func post(t *testing.T, r *rig, text string, opts ...record.Option) message.Message {
	t.Helper()
	m, err := r.svc.Post(context.Background(), messages.PostRequest{Session: "term_1", Text: text, Options: opts})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAgentPostAnswerRouting(t *testing.T) {
	opts := []record.Option{{ID: "old", Label: "Old column", Consequence: "keeps it"}, {ID: "new", Label: "New column", Consequence: "migrates"}}
	for _, c := range []struct {
		name    string
		arrange func(r *rig)
		route   turn.Route
		typed   int
	}{
		{"idle", func(r *rig) {}, turn.Typed, 1},
		{"busy", (*rig).busy, turn.Queued, 0},
		{"gone", (*rig).gone, turn.Refused, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.started(r7)
			r.live()
			m := post(t, r, "Which column?", opts...)
			c.arrange(r)
			got, err := r.svc.Answer(context.Background(), m.ID, messages.Answer{OptionID: "old", Text: "and drop the index", Source: turn.TUI})
			if err != nil || got.Route != c.route {
				t.Fatalf("%+v %v", got, err)
			}
			if typed := r.sess.Typed(); len(typed) != c.typed || (c.typed == 1 && typed[0].Line != "answered Message=1 Unit=https://github.com/acme/api/pull/7") {
				t.Fatalf("typed %+v", typed)
			}
			if len(r.launch.launches) != 0 {
				t.Fatalf("launched %+v", r.launch.launches)
			}
			b := r.book(t, r7)
			if len(b.Answers) != 1 || b.Answers[0].Route != c.route || b.Answers[0].Source != turn.TUI || b.Turns[0].State != turn.Started ||
				b.Answers[0].Message != m.ID || !b.Answers[0].Posted || b.Answers[0].OptionID != "old" || b.Answers[0].Text != "and drop the index" {
				t.Fatalf("book %+v %+v", b.Answers, b.Turns)
			}
			closed := r.message(t, m.ID)
			if closed.State != message.Answered || closed.Answer != "Old column (option old). and drop the index" || r.task(t, m.TaskID).State != task.State("done") {
				t.Fatalf("message %+v", closed)
			}
			if c.route == turn.Queued {
				r.sess.SetAgents()
				if err := r.turns.DeliverStopped(context.Background(), "term_1"); err != nil {
					t.Fatal(err)
				}
				if typed := r.sess.Typed(); len(typed) != 1 || typed[0].Line != "answered Message=1 Unit=https://github.com/acme/api/pull/7" {
					t.Fatalf("delivered %+v", typed)
				}
			}
		})
	}
}

func TestAgentPostAnswerRefusesWhatItDoesNotTake(t *testing.T) {
	r := newRig(t)
	r.started(r7)
	r.live()
	m := post(t, r, "Which column?", record.Option{ID: "old", Label: "Old", Consequence: "keeps"}, record.Option{ID: "new", Label: "New", Consequence: "moves"})
	for _, a := range []messages.Answer{{OptionID: "other"}, {Yes: true}, {}} {
		var invalid turns.InvalidAnswer
		if _, err := r.svc.Answer(context.Background(), m.ID, a); !errors.As(err, &invalid) {
			t.Fatalf("%+v: %v", a, err)
		}
	}
	if got := r.message(t, m.ID); got.State != message.Open || len(r.book(t, r7).Answers) != 0 {
		t.Fatalf("a refused answer moved %+v", got)
	}
}

func TestPostNeedsAStartedTurn(t *testing.T) {
	r := newRig(t)
	if _, err := r.svc.Post(context.Background(), messages.PostRequest{Session: "term_1", Text: "hi"}); !errors.Is(err, messages.ErrNoStartedTurn) {
		t.Fatalf("no turn: %v", err)
	}
	r.asked(t, r7)
	if _, err := r.svc.Post(context.Background(), messages.PostRequest{Session: "term_1", Text: "hi"}); !errors.Is(err, messages.ErrNoStartedTurn) {
		t.Fatalf("asked turn: %v", err)
	}
	r2 := newRig(t)
	r2.started(r7)
	var problems record.Problems
	if _, err := r2.svc.Post(context.Background(), messages.PostRequest{Session: "term_1", Text: "pick", Options: []record.Option{{ID: "A b", Label: "x", Consequence: "y"}}}); !errors.As(err, &problems) {
		t.Fatalf("bad options: %v", err)
	}
	if len(r2.open(t)) != 0 {
		t.Fatal("a refused post was stored")
	}
}

func TestPostIsCappedPerTurn(t *testing.T) {
	r := newRig(t)
	r.started(r7)
	for i := range messages.PostsPerTurn {
		post(t, r, "note "+string(rune('a'+i)))
	}
	var capped messages.Capped
	if _, err := r.svc.Post(context.Background(), messages.PostRequest{Session: "term_1", Text: "one more"}); !errors.As(err, &capped) || capped.Max != 3 {
		t.Fatalf("fourth post: %v", err)
	}
	if n := len(r.open(t)); n != 3 {
		t.Fatalf("open %d", n)
	}
	first := r.open(t)[0]
	if err := r.svc.Dismiss(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	post(t, r, "room again")
}

func TestLedgerProposalBecomesAMessage(t *testing.T) {
	r := newRig(t)
	r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"}, Provenance: items.Extracted,
		Reason: "the thread says it is done", State: coreitems.ProposalOpen, At: t0}}
	ctx := context.Background()
	for range 2 {
		if err := r.svc.ReconcileProposals(ctx); err != nil {
			t.Fatal(err)
		}
	}
	m := r.only(t)
	if m.Kind != message.Proposal || m.Origin != (message.Origin{Kind: message.OriginLedger, Proposal: 4}) || m.Unit != r7 ||
		len(m.Options) != 2 || m.Options[0].ID != messages.OptionAccept || m.Options[1].ID != messages.OptionReject {
		t.Fatalf("message %+v", m)
	}
	if e := r.task(t, m.TaskID); e.Kind != task.KindMessage || e.State != task.State("inbox") {
		t.Fatalf("entry %+v", e)
	}
	if n := len(r.inbox(t)); n != 1 {
		t.Fatalf("inbox %d", n)
	}
}

func TestAcceptingAProposalDecidesIt(t *testing.T) {
	for _, c := range []struct {
		answer messages.Answer
		accept bool
	}{
		{messages.Answer{OptionID: messages.OptionAccept}, true},
		{messages.Answer{OptionID: messages.OptionReject}, false},
	} {
		r := newRig(t)
		r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"}, State: coreitems.ProposalOpen}}
		if err := r.svc.ReconcileProposals(context.Background()); err != nil {
			t.Fatal(err)
		}
		m := r.only(t)
		if _, err := r.svc.Answer(context.Background(), m.ID, c.answer); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(r.ledger.decided, []decision{{4, c.accept}}) {
			t.Fatalf("decided %+v", r.ledger.decided)
		}
		if got := r.message(t, m.ID); got.State != message.Answered || got.Answer != c.answer.OptionID || r.task(t, m.TaskID).State != task.State("done") {
			t.Fatalf("message %+v", got)
		}
	}
}

func TestDecidedProposalClosesItsMessage(t *testing.T) {
	r := newRig(t)
	r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"}, State: coreitems.ProposalOpen}}
	ctx := context.Background()
	if err := r.svc.ReconcileProposals(ctx); err != nil {
		t.Fatal(err)
	}
	m := r.only(t)
	if _, err := r.ledger.Decide(ctx, 4, true); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.ReconcileProposals(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.message(t, m.ID); got.State != message.Answered || got.Answer != messages.OptionAccept || r.task(t, m.TaskID).State != task.State("done") {
		t.Fatalf("message %+v", got)
	}
}

func TestProposeDedupesAndAttaches(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	req := messages.ProposeRequest{Unit: r7, Text: "Nudge the reviewer?", Options: []record.Option{{ID: "yes", Label: "Yes", Consequence: "nudges"}},
		Origin: message.Origin{Kind: message.OriginWorkflow, Workflow: "nudge"}}
	a, err := r.svc.Propose(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.svc.Propose(ctx, req)
	if err != nil || b.ID != a.ID || b.TaskID != a.TaskID || len(r.inbox(t)) != 1 {
		t.Fatalf("second propose %+v %v", b, err)
	}
	capture, err := r.tasks.Capture(ctx, "follow up", "", &r8)
	if err != nil {
		t.Fatal(err)
	}
	attached, err := r.svc.Propose(ctx, messages.ProposeRequest{Unit: r8, TaskID: string(capture.ID), Text: "Close it?",
		Origin: message.Origin{Kind: message.OriginCore, Workflow: "close"}})
	if err != nil || attached.TaskID != string(capture.ID) || len(r.inbox(t)) != 2 {
		t.Fatalf("attached %+v %v, inbox %d", attached, err, len(r.inbox(t)))
	}
	var answered []message.Message
	r.svc.OnAnswer = func(_ context.Context, m message.Message, _ messages.Answer) error {
		answered = append(answered, m)
		return nil
	}
	if _, err := r.svc.Answer(ctx, attached.ID, messages.Answer{Text: "yes, close it"}); err != nil {
		t.Fatal(err)
	}
	if len(answered) != 1 || r.task(t, string(capture.ID)).State != task.State("inbox") {
		t.Fatalf("answered %+v; an attached entry another owner holds was closed", answered)
	}
	if _, err := r.svc.Propose(ctx, messages.ProposeRequest{Unit: r7, Text: "x", Origin: message.Origin{Kind: message.OriginLedger}}); err == nil {
		t.Fatal("a ledger origin was taken by Propose")
	}
}

func TestDisabledPluginKeepsTheMessageOpen(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	m := r.only(t)
	r.facts.off[unit.GitHub] = true
	r.record(t, r7, 1, record.Record{State: record.Done, Summary: "published", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://example.com/r"}})
	if err := r.svc.Reconcile(context.Background(), r7); err == nil {
		t.Fatal("closing the entry of a disabled plugin did not fail")
	}
	if got := r.message(t, m.ID); got.State != message.Open || r.task(t, m.TaskID).State != task.State("inbox") {
		t.Fatalf("message %+v", got)
	}
	r.facts.off[unit.GitHub] = false
	r.reconcile(t, r7)
	if got := r.message(t, m.ID); got.State != message.Superseded {
		t.Fatalf("message %+v", got)
	}
}

func TestReconcileCostsLittle(t *testing.T) {
	if testing.Short() {
		t.Skip("measures")
	}
	r := newRig(t)
	const n = 500
	var units []unit.Unit
	for i := range n {
		u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#" + string(rune('A'+i%26)) + string(rune('a'+i/26))}
		units = append(units, u)
		r.asked(t, u)
	}
	start := time.Now()
	r.reconcile(t)
	first := time.Since(start)
	start = time.Now()
	r.reconcile(t, units[0])
	one := time.Since(start)
	t.Logf("first reconcile of %d asked turns: %s; one unit after: %s", n, first, one)
	if got := len(r.open(t)); got != n {
		t.Fatalf("open %d", got)
	}
}

func TestAnAgentPostClosesWhenItsTurnEnds(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.started(r7)
	r.live()
	p := post(t, r, "Which column?")
	r.record(t, r7, 1, askedRecord(question()))
	r.reconcile(t, r7)
	got := r.message(t, p.ID)
	if got.State != message.Superseded || r.task(t, p.TaskID).State != task.State("trash") {
		t.Fatalf("the post of an ended turn is %+v", got)
	}
	if _, err := r.svc.Answer(ctx, p.ID, messages.Answer{Text: "old"}); !errors.Is(err, messages.ErrClosed) {
		t.Fatalf("answer to a closed post: %v", err)
	}
	q := r.only(t)
	if q.Kind != message.Question {
		t.Fatalf("open %+v", q)
	}
	b := r.book(t, r7)
	if _, ok := b.AnsweredAt(1); ok || b.OwesRecord(1) || len(b.Answers) != 0 {
		t.Fatalf("the book reads the question answered: %+v", b.Answers)
	}
}

func TestAnAgentPostOfAnEndedTurnRefusesAnAnswerBeforeReconcile(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.started(r7)
	r.live()
	p := post(t, r, "Which column?")
	r.record(t, r7, 1, askedRecord(question()))
	r.clock.advance(time.Second)
	if _, err := r.svc.Answer(ctx, p.ID, messages.Answer{Text: "old"}); !errors.Is(err, messages.ErrClosed) {
		t.Fatalf("answer to the post of an asked turn: %v", err)
	}
	r.reconcile(t, r7)
	if q := r.only(t); q.Kind != message.Question {
		t.Fatalf("open %+v", q)
	}
	if len(r.book(t, r7).Answers) != 0 || len(r.sess.Typed()) != 0 {
		t.Fatal("the answer was stored or typed")
	}
}

func TestReaskRetitlesTheTask(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.asked(t, r7)
	r.reconcile(t, r7)
	first := r.only(t)
	page, err := r.taskStore.TasksSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	again := question()
	again.Text = "Hold the review instead?"
	r.record(t, r7, 1, askedRecord(again))
	r.reconcile(t, r7)
	if e := r.task(t, first.TaskID); e.Title != again.Text {
		t.Fatalf("entry title %q", e.Title)
	}
	next, err := r.taskStore.TasksSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{Epoch: page.Epoch, Rev: page.Rev})
	if err != nil || next.Full || len(next.Tasks) != 1 || next.Tasks[0].Title != again.Text {
		t.Fatalf("page %+v %v", next, err)
	}
}

func TestCleanDropsBidiOverrides(t *testing.T) {
	r := newRig(t)
	r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"},
		Reason: "done\u202e\u2066 here", State: coreitems.ProposalOpen}}
	if err := r.svc.ReconcileProposals(context.Background()); err != nil {
		t.Fatal(err)
	}
	m := r.only(t)
	if strings.ContainsAny(m.Text, "\u202e\u2066") || strings.ContainsAny(r.task(t, m.TaskID).Title, "\u202e\u2066") {
		t.Fatalf("text %q", m.Text)
	}
}

// racingTurns ends the posting turn just before the post's answer reaches the turns service.
type racingTurns struct {
	messages.Turns
	r *rig
	t *testing.T
}

func (x racingTurns) Answer(ctx context.Context, req turns.AnswerRequest) (turns.AnswerResult, error) {
	x.r.record(x.t, req.Unit, req.Turn, askedRecord(question()))
	return x.Turns.Answer(ctx, req)
}

func TestAnAgentPostWhoseTurnEndsMidAnswerIsClosed(t *testing.T) {
	r := newRig(t)
	r.started(r7)
	r.live()
	p := post(t, r, "Which column?")
	r.svc.Turns = racingTurns{Turns: r.turns, r: r, t: t}
	if _, err := r.svc.Answer(context.Background(), p.ID, messages.Answer{Text: "old"}); !errors.Is(err, messages.ErrClosed) {
		t.Fatalf("answer racing the turn's end: %v", err)
	}
	if got := r.message(t, p.ID); got.State != message.Superseded {
		t.Fatalf("post %+v", got)
	}
	if q := r.only(t); q.Kind != message.Question {
		t.Fatalf("open %+v", q)
	}
}

// failingRetitle fails the next Retitle once.
type failingRetitle struct {
	messages.Tasks
	fail bool
}

func (f *failingRetitle) Retitle(ctx context.Context, id task.ID, title string) error {
	if f.fail {
		f.fail = false
		return errors.New("store busy")
	}
	return f.Tasks.Retitle(ctx, id, title)
}

func TestFailedRetitleIsRetried(t *testing.T) {
	r := newRig(t)
	flaky := &failingRetitle{Tasks: r.tasks}
	r.svc.Tasks = flaky
	r.asked(t, r7)
	r.reconcile(t, r7)
	first := r.only(t)
	again := question()
	again.Text = "Hold the review instead?"
	r.record(t, r7, 1, askedRecord(again))
	flaky.fail = true
	if err := r.svc.Reconcile(context.Background(), r7); err == nil {
		t.Fatal("the failed retitle was not reported")
	}
	r.reconcile(t, r7)
	if e := r.task(t, first.TaskID); e.Title != again.Text {
		t.Fatalf("entry title %q after the retry", e.Title)
	}
	if m := r.only(t); m.Text != again.Text {
		t.Fatalf("message %q", m.Text)
	}
}
