package messages_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type approval struct {
	drafted int64
	rev     int64
	by      task.Actor
	session string
}

type withdrawal struct {
	id     int64
	reason string
}

// optionDrafts keeps drafts, opens their gates in the rig's gates store through the real gates service,
// and records each approval that service publishes, so ApproveOption's contract is the real one.
type optionDrafts struct {
	mu        sync.Mutex
	r         *rig
	drafts    map[int64]draft.Draft
	gateIDs   map[int64]int64
	override  map[int64]gate.Gate
	approvals []approval
	withdrawn []withdrawal
}

type holds struct{}

func (holds) Decide(context.Context, gate.Gate) (gates.Decision, error) {
	return gates.Decision{Mode: gate.ModeWait}, nil
}

// newOptionDrafts gives r's messages service option drafts, with r's gates service approving them before it
// answers a question, the sessions driven ones the user drives.
func newOptionDrafts(t *testing.T, r *rig, driven ...string) *optionDrafts {
	t.Helper()
	od := &optionDrafts{r: r, drafts: map[int64]draft.Draft{}, gateIDs: map[int64]int64{}, override: map[int64]gate.Gate{}}
	r.gates = gates.New(gates.Deps{Store: r.gateStore, Reader: r.gateStore, Profile: profileGates, Tasks: gateTasks{r.taskStore, r.tasks}, Clock: r.clock,
		Deliverer: r.svc, Decider: r.svc, Publisher: od, Options: r.svc, Log: t.Logf, Sessions: drivenSessions(driven)})
	r.svc.Gates, r.svc.Drafts = serviceGates{r.gates, r.gateStore}, od
	return od
}

// put stores d held, superseding the gate of its earlier rev, and opens the gate its rev waits on.
func (o *optionDrafts) put(d draft.Draft, sessions ...string) {
	ctx := context.Background()
	o.mu.Lock()
	old, had := o.gateIDs[int64(d.ID)]
	o.mu.Unlock()
	if had {
		if _, err := o.r.gateStore.Resolve(ctx, old, gates.Resolution{Status: gate.Superseded, By: task.ActorWorkflow}); err != nil && !errors.Is(err, gates.ErrResolved) {
			panic(err)
		}
	}
	g, err := o.r.gates.Open(ctx, gate.Gate{Kind: gate.KindDraft, Name: "draft", Unit: d.Unit, Subject: strconv.FormatInt(int64(d.ID), 10),
		Session: d.Origin.Session, Sessions: sessions, ApprovedRev: d.Rev}, holds{})
	if err != nil {
		panic(err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.drafts[int64(d.ID)], o.gateIDs[int64(d.ID)] = d, g.ID
}

func (o *optionDrafts) gateOf(id int64) gate.Gate {
	o.mu.Lock()
	gid := o.gateIDs[id]
	o.mu.Unlock()
	g, err := o.r.gateStore.Get(context.Background(), gid)
	if err != nil {
		panic(err)
	}
	return g
}

// release resolves draft id's gate as a rule would, approved with no person.
func (o *optionDrafts) release(id int64) {
	if _, err := o.r.gateStore.Resolve(context.Background(), o.gateOf(id).ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorWorkflow, Rule: "send"}); err != nil {
		panic(err)
	}
}

func (o *optionDrafts) DraftGate(_ context.Context, id int64) (draft.Draft, gate.Gate, bool, error) {
	o.mu.Lock()
	d, ok := o.drafts[id]
	g, over := o.override[id]
	o.mu.Unlock()
	if !ok {
		return draft.Draft{}, gate.Gate{}, false, nil
	}
	if over {
		return d, g, true, nil
	}
	return d, o.gateOf(id), true, nil
}

// ApproveOption is the gates service's, for a test that wraps it.
func (o *optionDrafts) ApproveOption(ctx context.Context, question gate.Gate, r gates.Resolution, drafted gate.Gate, rev int64) error {
	return o.r.gates.ApproveOption(ctx, question, r, drafted, rev)
}

// Publish is the outbox's consequence of an approved draft gate: the draft is queued.
func (o *optionDrafts) Publish(_ context.Context, g gate.Gate) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	id, _ := strconv.ParseInt(g.Subject, 10, 64)
	o.approvals = append(o.approvals, approval{g.ID, g.ApprovedRev, g.By, g.BySession})
	d := o.drafts[id]
	d.State = draft.Queued
	o.drafts[id] = d
	return nil
}

// WithdrawHeld drops draft id as the outbox does: its open gate closes as superseded.
func (o *optionDrafts) WithdrawHeld(ctx context.Context, id int64, reason string) error {
	o.mu.Lock()
	o.withdrawn = append(o.withdrawn, withdrawal{id, reason})
	gid, ok := o.gateIDs[id]
	o.mu.Unlock()
	if !ok {
		return nil
	}
	if _, err := o.r.gateStore.Resolve(ctx, gid, gates.Resolution{Status: gate.Superseded, By: task.ActorWorkflow}); err != nil && !errors.Is(err, gates.ErrResolved) {
		return err
	}
	return nil
}

func heldDraft(id int64, u unit.Unit, seq, rev int64) draft.Draft {
	return draft.Draft{ID: draft.ID(id), Unit: u, State: draft.Held, Rev: rev, Text: "the review",
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: seq, Session: "term_1"}}
}

func draftQuestion() *record.Question {
	return &record.Question{Kind: "choice", Text: "Approve, or comment?", Recommend: "approve", Options: []record.Option{
		{ID: "approve", Label: "Approve", Consequence: "posts the approval", Draft: &record.DraftRef{ID: 42, Rev: 1}},
		{ID: "comment", Label: "Comment", Consequence: "posts a comment", Draft: &record.DraftRef{ID: 43, Rev: 1}},
		{ID: "foreign", Label: "Foreign", Consequence: "c", Draft: &record.DraftRef{ID: 50, Rev: 1}},
		{ID: "hold", Label: "Hold", Consequence: "posts nothing"},
	}}
}

// draftRig is a rig whose live review turn on r7 asks draftQuestion, its drafts 42 and 43 held, and draft
// 50 one of another turn.
func draftRig(t *testing.T, driven ...string) (*rig, *optionDrafts, message.Message) {
	t.Helper()
	r := newRig(t)
	od := newOptionDrafts(t, r, driven...)
	od.put(heldDraft(42, r7, 1, 1), "term_1", "reviser")
	od.put(heldDraft(43, r7, 1, 1), "term_1")
	od.put(heldDraft(50, r7, 9, 1), "other")
	r.started(r7)
	r.record(t, r7, 1, askedRecord(draftQuestion()))
	r.live()
	r.reconcile(t, r7)
	return r, od, r.only(t)
}

type drivenSessions []string

func (d drivenSessions) UserDriven(_ context.Context, id string) (bool, error) {
	return slices.Contains(d, id), nil
}

func TestOptionDraftApprovesWithoutResume(t *testing.T) {
	r, od, m := draftRig(t)
	q := r.gateOf(t, m)
	route, err := r.answer(m, messages.Answer{OptionID: "approve", Source: turn.Cockpit})
	if err != nil || route.Route != turn.Approved {
		t.Fatalf("%+v %v", route, err)
	}
	if want := []approval{{od.gateOf(42).ID, 1, task.ActorPerson, ""}}; !slices.Equal(od.approvals, want) {
		t.Fatalf("approvals %+v, want %+v", od.approvals, want)
	}
	if got := r.gateOf(t, m); got.ID != q.ID || got.Status != gate.Answered || got.By != task.ActorPerson {
		t.Fatalf("the question gate %+v", got)
	}
	b := r.book(t, r7)
	if len(r.sess.Typed()) != 0 || len(r.launch.launches) != 0 || b.Turns[0].State != turn.Asked || len(b.Answers) != 1 || b.Answers[0].Route != turn.Approved {
		t.Fatalf("typed %v, launches %v, book %+v", r.sess.Typed(), r.launch.launches, b)
	}
	if got := r.message(t, m.ID); got.Open() {
		t.Fatalf("message %+v", got)
	}
	r.reconcile(t, r7)
	if open := r.open(t); len(open) != 0 {
		t.Fatalf("the approved question asks again: %+v", open)
	}
	if err := r.svc.Deliver(context.Background(), r.gateOf(t, m)); !errors.Is(err, messages.ErrClosed) {
		t.Fatal(err)
	}
	if len(od.approvals) != 1 || len(r.book(t, r7).Answers) != 1 {
		t.Fatalf("the consequence run again approves again: %+v", od.approvals)
	}
}

func TestOtherOptionDraftsWithdrawn(t *testing.T) {
	r, od, m := draftRig(t)
	if _, err := r.answer(m, messages.Answer{OptionID: "comment", Source: turn.Cockpit}); err != nil {
		t.Fatal(err)
	}
	if want := []withdrawal{{42, messages.ReasonOtherOption}}; !slices.Equal(od.withdrawn, want) {
		t.Fatalf("withdrawn %+v, want %+v: another turn's draft is out of reach", od.withdrawn, want)
	}
	if len(od.approvals) != 1 || od.approvals[0].drafted != od.gateOf(43).ID {
		t.Fatalf("approvals %+v", od.approvals)
	}
}

func TestFreeAnswerResumes(t *testing.T) {
	for _, a := range []messages.Answer{{Text: "rewrite the second finding", Source: turn.Cockpit}, {OptionID: "hold", Source: turn.Cockpit}} {
		r, od, m := draftRig(t)
		route, err := r.answer(m, a)
		if err != nil || route.Route != turn.Typed || len(r.sess.Typed()) != 1 {
			t.Fatalf("%+v: %+v %v, typed %v", a, route, err, r.sess.Typed())
		}
		if len(od.approvals) != 0 || len(od.withdrawn) != 0 {
			t.Fatalf("%+v: approvals %+v, withdrawn %+v", a, od.approvals, od.withdrawn)
		}
	}
}

func TestOptionDraftMustBeHeldOfTurn(t *testing.T) {
	for _, c := range []struct {
		name   string
		option string
		change func(od *optionDrafts)
	}{
		{"another turn's draft", "foreign", func(*optionDrafts) {}},
		{"another unit's draft", "approve", func(od *optionDrafts) { od.put(heldDraft(42, r8, 1, 1)) }},
		{"a person's draft", "approve", func(od *optionDrafts) {
			d := heldDraft(42, r7, 1, 1)
			d.Origin.Kind = draft.OriginUser
			od.put(d)
		}},
		{"a draft no longer held", "approve", func(od *optionDrafts) {
			d := heldDraft(42, r7, 1, 1)
			d.State = draft.Failed
			od.put(d)
		}},
		{"a draft revised since", "approve", func(od *optionDrafts) { od.put(heldDraft(42, r7, 1, 2)) }},
		{"a draft a rule released", "approve", func(od *optionDrafts) {
			od.release(42)
		}},
		{"a gate of another subject", "approve", func(od *optionDrafts) { g := od.gateOf(42); g.Subject = "43"; od.override[42] = g }},
	} {
		r, od, m := draftRig(t)
		c.change(od)
		route, err := r.answer(m, messages.Answer{OptionID: c.option, Source: turn.Cockpit})
		if err != nil || route.Route != turn.Typed {
			t.Fatalf("%s: the pick resumes the turn: %+v %v", c.name, route, err)
		}
		if len(od.approvals) != 0 || len(od.withdrawn) != 0 {
			t.Fatalf("%s: approvals %+v, withdrawn %+v", c.name, od.approvals, od.withdrawn)
		}
	}
}

func TestAnOptionDraftApprovesOnlyWithTheResolversAuthority(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name    string
		res     gates.Resolution
		approve bool
	}{
		{"a session the user drives", gates.Resolution{Intent: "answer", By: task.ActorAgent, Session: "driver"}, true},
		{"the session that revised the draft", gates.Resolution{Intent: "answer", By: task.ActorAgent, Session: "reviser"}, false},
	} {
		r, od, m := draftRig(t, "driver", "reviser")
		q := r.gateOf(t, m)
		c.res.Answer = messages.GateAnswer(messages.Answer{OptionID: "approve"})
		if err := r.gates.Resolve(ctx, q.ID, c.res); (err == nil) != c.approve || !c.approve && !errors.Is(err, gates.ErrRefused) {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := len(od.approvals) == 1; got != c.approve {
			t.Fatalf("%s: approvals %+v", c.name, od.approvals)
		}
		if c.approve && (od.approvals[0].by != task.ActorAgent || od.approvals[0].session != "driver") {
			t.Fatalf("%s: approved as %+v", c.name, od.approvals[0])
		}
		if !c.approve && len(od.withdrawn) != 0 {
			t.Fatalf("%s: withdrawn %+v", c.name, od.withdrawn)
		}
	}
	r, od, m := draftRig(t)
	q := r.gateOf(t, m)
	if err := r.svc.Deliver(ctx, gate.Gate{ID: q.ID, Kind: gate.KindQuestion, Unit: r7, Subject: q.Subject, Status: gate.Answered, Intent: "answer",
		Answer: messages.GateAnswer(messages.Answer{OptionID: "approve"}), By: task.ActorWorkflow, Rule: gates.RuleWindow, Session: q.Session}); err != nil {
		t.Fatal(err)
	}
	if len(od.approvals) != 0 {
		t.Fatalf("a window's default approved a draft: %+v", od.approvals)
	}
}

func TestAnOptionPickCutShortCompletes(t *testing.T) {
	r, od, m := draftRig(t)
	if _, err := r.gateStore.Resolve(context.Background(), od.gateOf(42).ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	route, err := r.answer(m, messages.Answer{OptionID: "approve", Source: turn.Cockpit})
	if err != nil || route.Route != turn.Approved || len(od.approvals) != 0 || !slices.Equal(od.withdrawn, []withdrawal{{43, messages.ReasonOtherOption}}) {
		t.Fatalf("%+v %v, approvals %+v, withdrawn %+v", route, err, od.approvals, od.withdrawn)
	}
}

func TestConfirmYesApprovesItsDraft(t *testing.T) {
	for _, c := range []struct {
		name    string
		a       messages.Answer
		approve bool
	}{
		{"yes", messages.Answer{Yes: true, Source: turn.Cockpit}, true},
		{"no", messages.Answer{No: true, Source: turn.Cockpit}, false},
	} {
		r := newRig(t)
		od := newOptionDrafts(t, r)
		od.put(heldDraft(42, r7, 1, 1), "term_1")
		r.started(r7)
		r.record(t, r7, 1, askedRecord(&record.Question{Kind: "confirm", Text: "Post the review?", Draft: &record.DraftRef{ID: 42, Rev: 1}}))
		r.live()
		r.reconcile(t, r7)
		route, err := r.answer(r.only(t), c.a)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := len(od.approvals) == 1 && route.Route == turn.Approved; got != c.approve {
			t.Fatalf("%s: route %+v, approvals %+v", c.name, route, od.approvals)
		}
		if len(od.withdrawn) != 0 {
			t.Fatalf("%s: withdrawn %+v", c.name, od.withdrawn)
		}
	}
}

// OptionDraft names the pin an answer picks, and ApproveOption, the one owner of the rest, approves it only at
// the pinned rev and for a resolver the draft's gate allows.
func TestAnOptionApprovesOnlyThePinSeenForAResolverItsGateAllows(t *testing.T) {
	ctx := context.Background()
	answer := messages.GateAnswer(messages.Answer{OptionID: "approve"})
	for _, c := range []struct {
		name   string
		r      gates.Resolution
		change func(od *optionDrafts)
		ok     bool
	}{
		{"a person", gates.Resolution{Intent: "answer", Answer: answer, By: task.ActorPerson}, nil, true},
		{"a session the user drives", gates.Resolution{Intent: "answer", Answer: answer, By: task.ActorAgent, Session: "driver"}, nil, true},
		{"the session that revised it", gates.Resolution{Intent: "answer", Answer: answer, By: task.ActorAgent, Session: "reviser"}, nil, false},
		{"the turn's session", gates.Resolution{Intent: "answer", Answer: answer, By: task.ActorAgent, Session: "term_1"}, nil, false},
		{"a rule", gates.Resolution{Intent: "answer", Answer: answer, By: task.ActorWorkflow, Rule: "send"}, nil, false},
		{"a rev other than the pin", gates.Resolution{Intent: "answer", Answer: answer, By: task.ActorPerson},
			func(od *optionDrafts) { od.put(heldDraft(42, r7, 1, 2), "term_1") }, false},
		{"an option without a draft", gates.Resolution{Intent: "answer", Answer: messages.GateAnswer(messages.Answer{OptionID: "hold"}), By: task.ActorPerson}, nil, false},
	} {
		r, od, m := draftRig(t, "driver", "reviser")
		if c.change != nil {
			c.change(od)
		}
		q := r.gateOf(t, m)
		got, rev, ok, err := r.svc.OptionDraft(ctx, q, c.r)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if ok && (got.ID != od.gateOf(42).ID || rev != 1) {
			t.Fatalf("%s: gate %d rev %d", c.name, got.ID, rev)
		}
		approved := ok && r.gates.ApproveOption(ctx, q, c.r, got, rev) == nil
		if approved != c.ok || approved != (od.gateOf(42).Status == gate.Approved) {
			t.Fatalf("%s: approved %v, draft gate %s", c.name, approved, od.gateOf(42).Status)
		}
	}
}

// ApproveOption checks that the resolution answers the question before it approves anything, whoever calls it.
func TestApproveOptionApprovesNothingForAResolutionThatDoesNotAnswer(t *testing.T) {
	ctx := context.Background()
	for _, r := range []gates.Resolution{
		{Intent: "dismiss", Answer: "approve", By: task.ActorPerson},
		{Intent: "continue", Answer: "approve", By: task.ActorPerson},
		{Intent: "no-such-intent", Answer: "approve", By: task.ActorPerson},
		{Intent: "answer", By: task.ActorPerson},
		{Intent: "answer", Answer: "approve", By: task.ActorAgent, Session: "not-driven"},
	} {
		rg, od, m := draftRig(t)
		q := rg.gateOf(t, m)
		if err := rg.gates.ApproveOption(ctx, q, r, od.gateOf(42), 1); err == nil {
			t.Errorf("%+v: approved", r)
		}
		if dg := od.gateOf(42); dg.Status != gate.Open || len(od.approvals) != 0 {
			t.Errorf("%+v: draft gate %s, approvals %+v", r, dg.Status, od.approvals)
		}
		if got := rg.gateOf(t, m); got.Status != gate.Open {
			t.Errorf("%+v: question %s", r, got.Status)
		}
	}
}

// A session that revised the option's draft picks it: no answer at all, so the question stays open and the turn
// hears nothing; a pin gone stale is still the person's answer.
func TestAPickByTheDraftsReviserIsRefusedOutright(t *testing.T) {
	ctx := context.Background()
	answer := messages.GateAnswer(messages.Answer{OptionID: "approve"})
	r, od, m := draftRig(t, "driver", "reviser")
	q := r.gateOf(t, m)
	err := r.gates.Resolve(ctx, q.ID, gates.Resolution{Intent: "answer", Answer: answer, By: task.ActorAgent, Session: "reviser"})
	if !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("the reviser's pick: %v, want ErrRefused", err)
	}
	if got := r.gateOf(t, m); got.Status != gate.Open || od.gateOf(42).Status != gate.Open || len(r.sess.Typed()) != 0 {
		t.Fatalf("question %s, draft gate %s, typed %v: want both open and the turn untouched", got.Status, od.gateOf(42).Status, r.sess.Typed())
	}
}
