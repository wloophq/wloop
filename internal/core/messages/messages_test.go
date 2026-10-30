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
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/gate"
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
		m.Origin != (message.Origin{Kind: message.OriginTurn, TurnSeq: 1, Session: "term_1"}) || !m.Open() {
		t.Fatalf("message %+v", m)
	}
	if g := r.gateOf(t, m); g.Kind != gate.KindQuestion || g.Status != gate.Open || g.Unit != r7 || string(g.Task) != m.TaskID {
		t.Fatalf("gate %+v of %+v", g, m)
	}

	confirm := newRig(t)
	confirm.started(r8)
	confirm.record(t, r8, 1, askedRecord(&record.Question{Kind: "confirm", Text: "Publish?", Recommend: "yes"}))
	confirm.reconcile(t)
	if m := confirm.only(t); !m.Confirm || m.Options != nil || m.Recommend != "yes" {
		t.Fatalf("confirm %+v", m)
	}
}

func TestAStoppedTurnOpensItsMissingGateNotAMessage(t *testing.T) {
	for _, st := range []record.State{record.Failed, record.UnrecordedState} {
		t.Run(string(st), func(t *testing.T) {
			r := newRig(t)
			r.started(r7)
			r.record(t, r7, 1, record.Record{State: st, Summary: "the build broke", Resume: "rerun it"})
			b := r.book(t, r7)
			b.Turns[0].State = turn.State(st)
			r.books.Put(b)
			for range 2 {
				r.reconcile(t, r7)
			}
			if open := r.open(t); len(open) != 0 {
				t.Fatalf("open %+v", open)
			}
			if g := r.openGates(t, gate.KindQuestion); len(g) != 1 || g[0].Name != "missing-record" || g[0].Subject != turns.MissingSubject("abc") {
				t.Fatalf("gates %+v", g)
			}
			if _, err := r.resolve(message.Message{}, "", messages.Answer{}); err == nil {
				t.Fatal("resolved a gate of no message")
			}
			g := r.openGates(t, gate.KindQuestion)[0]
			if err := r.gates.Resolve(context.Background(), g.ID, gatesResolution("dismiss")); err != nil {
				t.Fatal(err)
			}
			if b := r.book(t, r7); b.Turns[0].State != turn.Dismissed {
				t.Fatalf("turn %s", b.Turns[0].State)
			}
			r.reconcile(t, r7)
			if g := r.openGates(t, gate.KindQuestion); len(g) != 0 {
				t.Fatalf("gates after the dismissal %+v", g)
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
	if g := r.openGates(t, gate.KindQuestion); len(g) != 1 {
		t.Fatalf("gates %+v", g)
	}
}

func TestTurnEventsCloseTheMessage(t *testing.T) {
	for _, c := range []struct {
		name   string
		event  func(t *testing.T, r *rig)
		answer string
	}{
		{"answered", func(t *testing.T, r *rig) {
			r.live()
			if _, err := r.turns.Answer(context.Background(), turns.AnswerRequest{Unit: r7, OptionID: "publish", Source: turn.Cockpit}); err != nil {
				t.Fatal(err)
			}
		}, "Publish (option publish)"},
		{"question_dismissed", func(t *testing.T, r *rig) {
			if err := r.turns.Dismiss(context.Background(), r7); err != nil {
				t.Fatal(err)
			}
		}, ""},
		{"replaced", func(t *testing.T, r *rig) {
			b := r.book(t, r7)
			b.Send(turn.Turn{Intent: b.Turns[0].Intent, ID: "def", Trigger: turn.ByHand, SentTo: "term_2", SentAt: r.clock.Now()})
			r.books.Put(b)
		}, ""},
		{"done", func(t *testing.T, r *rig) {
			r.record(t, r7, 1, record.Record{State: record.Done, Summary: "published", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://example.com/r"}})
		}, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRig(t)
			r.asked(t, r7)
			r.reconcile(t, r7)
			m := r.only(t)
			c.event(t, r)
			r.reconcile(t, r7)
			got := r.message(t, m.ID)
			if got.Open() || got.Answer != c.answer {
				t.Fatalf("closed %+v", got)
			}
			if g := r.gateOf(t, m); g.Status != gate.Superseded || g.By != task.ActorWorkflow {
				t.Fatalf("gate %+v", g)
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
		if back.ID == first.ID || back.Origin.TurnSeq != 1 || r.message(t, first.ID).Open() {
			t.Fatalf("after the withdraw %+v, first %+v", back, r.message(t, first.ID))
		}
		if g := r.openGates(t, gate.KindQuestion); len(g) != 1 || g[0].Subject != subject(back) {
			t.Fatalf("gates %+v", g)
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
	if got := lost.only(t); got.ID != m.ID || got.TaskID == "" || len(got.Options) != 2 || lost.gateOf(t, got).Status != gate.Open {
		t.Fatalf("a message left without its gate was not repaired: %+v", got)
	}
}

func TestDismissingAQuestionGateDismissesTheTurn(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	m := r.only(t)
	if _, err := r.resolve(m, "dismiss", messages.Answer{}); err != nil {
		t.Fatal(err)
	}
	if b := r.book(t, r7); b.Turns[0].State != turn.Dismissed {
		t.Fatalf("turn %s", b.Turns[0].State)
	}
	if got := r.message(t, m.ID); got.Open() || r.gateOf(t, m).Status != gate.Dismissed {
		t.Fatalf("message %+v", got)
	}
	if len(r.open(t)) != 0 || len(r.openGates(t, gate.KindQuestion)) != 0 {
		t.Fatal("the dismissed question came back")
	}
}

func TestContinuingAQuestionGateResumesTheTurn(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	m := r.only(t)
	if _, err := r.resolve(m, "continue", messages.Answer{}); err != nil {
		t.Fatal(err)
	}
	if len(r.launch.launches) != 1 || r.launch.launches[0].ResumeOf != 1 || r.launch.launches[0].Answer != "" {
		t.Fatalf("launches %+v", r.launch.launches)
	}
	if got := r.message(t, m.ID); got.Open() {
		t.Fatalf("message %+v", got)
	}
}

func TestAnsweringAStaleQuestionIsRefused(t *testing.T) {
	r := newRig(t)
	r.asked(t, r7)
	r.reconcile(t, r7)
	m := r.only(t)
	r.record(t, r7, 1, record.Record{State: record.Done, Summary: "published", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://example.com/r"}})
	if _, err := r.answer(m, messages.Answer{OptionID: "publish", Source: turn.Cockpit}); !errors.Is(err, turn.ErrNoQuestion) {
		t.Fatalf("answer to a done turn: %v", err)
	}
	if len(r.book(t, r7).Answers) != 0 {
		t.Fatal("the stale answer was stored")
	}
	r.reconcile(t, r7)
	if got := r.message(t, m.ID); got.Open() {
		t.Fatalf("the stale message is open: %+v", got)
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
			got, err := r.answer(m, messages.Answer{OptionID: "old", Text: "and drop the index", Source: turn.TUI})
			if err != nil || got.Route != c.route {
				t.Fatalf("%+v %v", got, err)
			}
			if typed := r.sess.Typed(); len(typed) != c.typed || (c.typed == 1 && typed[0].Line != "answered Gate=1 Unit=https://github.com/acme/api/pull/7") {
				t.Fatalf("typed %+v", typed)
			}
			if len(r.launch.launches) != 0 {
				t.Fatalf("launched %+v", r.launch.launches)
			}
			b := r.book(t, r7)
			g := r.gateOf(t, m)
			if len(b.Answers) != 1 || b.Answers[0].Route != c.route || b.Answers[0].Source != turn.TUI || b.Turns[0].State != turn.Started ||
				b.Answers[0].Message != m.ID || b.Answers[0].Gate != g.ID || !b.Answers[0].Posted || b.Answers[0].OptionID != "old" || b.Answers[0].Text != "and drop the index" {
				t.Fatalf("book %+v %+v", b.Answers, b.Turns)
			}
			closed := r.message(t, m.ID)
			if closed.Open() || closed.Answer != "Old column (option old). and drop the index" || g.Status != gate.Answered {
				t.Fatalf("message %+v, gate %+v", closed, g)
			}
			if c.route == turn.Queued {
				r.sess.SetAgents()
				if err := r.turns.DeliverStopped(context.Background(), "term_1"); err != nil {
					t.Fatal(err)
				}
				if typed := r.sess.Typed(); len(typed) != 1 || typed[0].Line != "answered Gate=1 Unit=https://github.com/acme/api/pull/7" {
					t.Fatalf("delivered %+v", typed)
				}
			}
		})
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
	if len(r2.open(t)) != 0 || len(r2.openGates(t, gate.KindPost)) != 0 {
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
	if _, err := r.resolve(r.open(t)[0], "dismiss", messages.Answer{}); err != nil {
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
		m.Options != nil || m.Text != "Proposed resolve of item i2: the thread says it is done" {
		t.Fatalf("message %+v", m)
	}
	if g := r.openGates(t, gate.KindProposal); len(g) != 1 || g[0].Subject != subject(m) {
		t.Fatalf("gates %+v", g)
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
	r.ledger.proposals[0].State = coreitems.ProposalAccepted
	if err := r.svc.ReconcileProposals(ctx); err != nil {
		t.Fatal(err)
	}
	if got := r.message(t, m.ID); got.Open() || r.gateOf(t, m).Status != gate.Superseded {
		t.Fatalf("message %+v", got)
	}
}

func TestProposeDedupesAndAttaches(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	req := messages.ProposeRequest{Unit: r7, Text: "Nudge the reviewer?", Origin: message.Origin{Kind: message.OriginWorkflow, Workflow: "nudge"}}
	a, err := r.svc.Propose(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.svc.Propose(ctx, req)
	if err != nil || b.ID != a.ID || b.TaskID != a.TaskID || len(r.openGates(t, gate.KindProposal)) != 1 {
		t.Fatalf("second propose %+v %v", b, err)
	}
	capture, err := r.tasks.Capture(ctx, "follow up", "", &r8)
	if err != nil {
		t.Fatal(err)
	}
	attached, err := r.svc.Propose(ctx, messages.ProposeRequest{Unit: r8, TaskID: string(capture.ID), Text: "Close it?",
		Origin: message.Origin{Kind: message.OriginCore, Workflow: "close"}})
	if err != nil || attached.TaskID != string(capture.ID) || r.gateOf(t, attached).Task != capture.ID {
		t.Fatalf("attached %+v %v", attached, err)
	}
	var answered []messages.Answer
	r.svc.OnAnswer = func(_ context.Context, _ message.Message, a messages.Answer) error {
		answered = append(answered, a)
		return nil
	}
	if _, err := r.resolve(attached, "approve", messages.Answer{}); err != nil {
		t.Fatal(err)
	}
	if len(answered) != 1 || !answered[0].Yes || r.task(t, string(capture.ID)).State != task.State("inbox") {
		t.Fatalf("answered %+v; the task the proposal is on was closed", answered)
	}
	if _, err := r.svc.Propose(ctx, messages.ProposeRequest{Unit: r7, Text: "x", Origin: message.Origin{Kind: message.OriginLedger}}); err == nil {
		t.Fatal("a ledger origin was taken by Propose")
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
	r.started(r7)
	r.live()
	p := post(t, r, "Which column?")
	r.record(t, r7, 1, askedRecord(question()))
	r.reconcile(t, r7)
	got := r.message(t, p.ID)
	if got.Open() || r.gateOf(t, p).Status != gate.Superseded {
		t.Fatalf("the post of an ended turn is %+v", got)
	}
	if _, err := r.answer(p, messages.Answer{Text: "old"}); err == nil {
		t.Fatal("a closed post took an answer")
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
	r.started(r7)
	r.live()
	p := post(t, r, "Which column?")
	r.record(t, r7, 1, askedRecord(question()))
	r.clock.advance(time.Second)
	if _, err := r.answer(p, messages.Answer{Text: "old"}); !errors.Is(err, messages.ErrClosed) {
		t.Fatalf("answer to the post of an asked turn: %v", err)
	}
	if q := r.only(t); q.Kind != message.Question {
		t.Fatalf("open %+v", q)
	}
	if len(r.book(t, r7).Answers) != 0 || len(r.sess.Typed()) != 0 {
		t.Fatal("the answer was stored or typed")
	}
}

func TestCleanDropsBidiOverrides(t *testing.T) {
	r := newRig(t)
	r.ledger.proposals = []coreitems.Proposal{{ID: 4, Unit: r7, Item: "i2", Op: items.Op{Op: items.OpResolve, Item: "i2"},
		Reason: "done‮⁦ here", State: coreitems.ProposalOpen}}
	if err := r.svc.ReconcileProposals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := r.only(t); strings.ContainsAny(m.Text, "‮⁦") {
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
	if _, err := r.answer(p, messages.Answer{Text: "old"}); !errors.Is(err, messages.ErrClosed) {
		t.Fatalf("answer racing the turn's end: %v", err)
	}
	if got := r.message(t, p.ID); got.Open() {
		t.Fatalf("post %+v", got)
	}
	if q := r.only(t); q.Kind != message.Question {
		t.Fatalf("open %+v", q)
	}
}

func TestAGateAnswerReadsBackAsItsAnswer(t *testing.T) {
	m := message.Message{Kind: message.Question, Options: question().Options}
	c := message.Message{Kind: message.Question, Confirm: true}
	for _, x := range []struct {
		m message.Message
		a messages.Answer
	}{
		{m, messages.Answer{OptionID: "publish"}},
		{m, messages.Answer{OptionID: "hold", Text: "until Friday\nthen publish"}},
		{m, messages.Answer{Text: "publish it twice"}},
		{c, messages.Answer{Yes: true}},
		{c, messages.Answer{No: true, Text: "not yet"}},
		{c, messages.Answer{Text: "maybe"}},
	} {
		if got := messages.AnswerOf(x.m, messages.GateAnswer(x.a)); got != x.a {
			t.Errorf("%+v read back as %+v", x.a, got)
		}
	}
}
