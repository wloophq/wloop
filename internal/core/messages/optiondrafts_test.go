package messages_test

import (
	"context"
	"errors"
	"fmt"
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
	question, drafted int64
	rev               int64
	by                gate.Resolver
}

type withdrawal struct {
	id     int64
	reason string
}

// optionDrafts keeps drafts and their gates, and approves as gates.Service.ApproveOption promises: it
// refuses a rev other than the draft's and a resolver among the sessions its gate stamps.
type optionDrafts struct {
	mu        sync.Mutex
	drafts    map[int64]draft.Draft
	gates     map[int64]gate.Gate
	approvals []approval
	withdrawn []withdrawal
}

func (o *optionDrafts) put(d draft.Draft, sessions ...string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.drafts[int64(d.ID)] = d
	o.gates[int64(d.ID)] = gate.Gate{ID: 1000 + int64(d.ID), Kind: gate.KindDraft, Unit: d.Unit, Subject: strconv.FormatInt(int64(d.ID), 10), Status: gate.Open,
		Session: d.Origin.Session, Sessions: sessions}
}

func (o *optionDrafts) DraftGate(_ context.Context, id int64) (draft.Draft, gate.Gate, bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	d, ok := o.drafts[id]
	return d, o.gates[id], ok, nil
}

func (o *optionDrafts) ApproveOption(_ context.Context, question, drafted gate.Gate, rev int64, by gate.Resolver) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	id, _ := strconv.ParseInt(drafted.Subject, 10, 64)
	d := o.drafts[id]
	if d.Rev != rev || by.Session != "" && slices.Contains(o.gates[id].Sessions, by.Session) {
		return fmt.Errorf("%w: draft %d", gates.ErrRefused, id)
	}
	o.approvals = append(o.approvals, approval{question.ID, drafted.ID, rev, by})
	g := o.gates[id]
	g.Status, g.ApprovedRev, g.By, g.BySession = gate.Approved, rev, by.By, by.Session
	o.gates[id] = g
	d.State = draft.Queued
	o.drafts[id] = d
	return nil
}

func (o *optionDrafts) WithdrawHeld(_ context.Context, id int64, reason string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.withdrawn = append(o.withdrawn, withdrawal{id, reason})
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
	if len(driven) > 0 {
		r.gates = gates.New(gates.Deps{Store: r.gateStore, Reader: r.gateStore, Profile: profileGates, Tasks: gateTasks{r.taskStore, r.tasks}, Clock: r.clock,
			Deliverer: r.svc, Decider: r.svc, Log: t.Logf, Sessions: drivenSessions(driven)})
		r.svc.Gates = serviceGates{r.gates, r.gateStore}
	}
	od := &optionDrafts{drafts: map[int64]draft.Draft{}, gates: map[int64]gate.Gate{}}
	od.put(heldDraft(42, r7, 1, 1), "term_1", "reviser")
	od.put(heldDraft(43, r7, 1, 1), "term_1")
	od.put(heldDraft(50, r7, 9, 1), "other")
	r.svc.Drafts = od
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
	if want := []approval{{q.ID, 1042, 1, gate.Resolver{By: task.ActorPerson}}}; !slices.Equal(od.approvals, want) {
		t.Fatalf("approvals %+v, want %+v", od.approvals, want)
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
	if len(od.approvals) != 1 || od.approvals[0].drafted != 1043 {
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
			od.gates[42] = gate.Gate{ID: 1042, Kind: gate.KindDraft, Subject: "42", Status: gate.Approved, ApprovedRev: 1, By: task.ActorWorkflow, Rule: "send"}
		}},
		{"a gate of another subject", "approve", func(od *optionDrafts) { g := od.gates[42]; g.Subject = "43"; od.gates[42] = g }},
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
		if err := r.gates.Resolve(ctx, q.ID, c.res); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := len(od.approvals) == 1; got != c.approve {
			t.Fatalf("%s: approvals %+v", c.name, od.approvals)
		}
		if c.approve && od.approvals[0].by != (gate.Resolver{By: task.ActorAgent, Session: "driver"}) {
			t.Fatalf("%s: approved as %+v", c.name, od.approvals[0].by)
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
	g := od.gates[42]
	g.Status, g.ApprovedRev, g.By = gate.Approved, 1, task.ActorPerson
	od.gates[42] = g
	route, err := r.answer(m, messages.Answer{OptionID: "approve", Source: turn.Cockpit})
	if err != nil || route.Route != turn.Approved || len(od.approvals) != 0 || !slices.Equal(od.withdrawn, []withdrawal{{43, messages.ReasonOtherOption}}) {
		t.Fatalf("%+v %v, approvals %+v, withdrawn %+v", route, err, od.approvals, od.withdrawn)
	}
}
