package gates_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type profile []gates.Def

func (p profile) Gate(name string) (gates.Def, bool) {
	for _, d := range p {
		if d.Name == name {
			return d, true
		}
	}
	return gates.Def{}, false
}

func (p profile) FirstOf(k gate.Kind) (gates.Def, bool) {
	for _, d := range p {
		if d.Kind == k {
			return d, true
		}
	}
	return gates.Def{}, false
}

var bundled = profile{
	{Name: "draft", Kind: gate.KindDraft, Label: "Send this draft?", Intents: []gates.Intent{{Name: "approve", Status: gate.Approved}, {Name: "discard", Status: gate.Rejected}}},
	{Name: "plan", Kind: gate.KindPlan, Label: "Run this?", Intents: []gates.Intent{{Name: "run", Status: gate.Approved}, {Name: "skip", Status: gate.Rejected}}},
	{Name: "question", Kind: gate.KindQuestion, Label: "The agent asks", Intents: []gates.Intent{{Name: "answer", Status: gate.Answered, Answer: true}, {Name: "dismiss", Status: gate.Dismissed}}},
	{Name: "review-diff", Kind: gate.KindStep, Label: "Review the diff", Intents: []gates.Intent{{Name: "approve", Status: gate.Approved}}},
}

type clock struct{ now func() time.Time }

func (c clock) Now() time.Time { return c.now() }

type policy struct {
	dec gates.Decision
	err error
}

func (p policy) Decide(context.Context, gate.Gate) (gates.Decision, error) { return p.dec, p.err }

type fixture struct {
	svc   *gates.Service
	store *gatestest.Store
	cons  *gatestest.Consequences
}

var opened = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return opened }

func newFixture(t *testing.T, p gates.Policy, prof profile) fixture {
	t.Helper()
	return newFixtureAt(t, p, prof, fixedNow)
}

func newFixtureAt(t *testing.T, p gates.Policy, prof profile, now func() time.Time) fixture {
	t.Helper()
	store := gatestest.NewStore(now)
	cons := &gatestest.Consequences{}
	svc := gates.New(gates.Deps{
		Store: store, Reader: store, Profile: prof, Policy: p, Clock: clock{now},
		Publisher: cons, Releaser: cons, Deliverer: cons, Decider: cons, Unblocker: cons,
	})
	return fixture{svc: svc, store: store, cons: cons}
}

var acme = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}

func (f fixture) open(t *testing.T, g gate.Gate, p gates.Policy) gate.Gate {
	t.Helper()
	if g.Unit.IsZero() {
		g.Unit = task.UnitOf("t-0000000000000001")
	}
	got, err := f.svc.Open(context.Background(), g, p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return got
}

func (f fixture) get(t *testing.T, id int64) gate.Gate {
	t.Helper()
	g, err := f.store.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestNoAgentResolvesAGate(t *testing.T) {
	f := newFixture(t, policy{dec: gates.Decision{Mode: gate.ModeSend, Rule: "rule[0]"}}, bundled)
	g := f.open(t, gate.Gate{Kind: gate.KindQuestion}, policy{dec: gates.Decision{Mode: gate.ModeWait}})
	ctx := context.Background()
	for _, r := range []gates.Resolution{
		{Intent: "answer", Answer: "yes", By: task.ActorAgent},
		{Intent: "dismiss", By: task.ActorPlugin},
		{Intent: "dismiss"},
		{Intent: "dismiss", By: task.ActorWorkflow},
		{Intent: "dismiss", By: task.ActorAgent, Rule: "rule[0]"},
		{Intent: "dismiss", By: task.ActorPerson, Rule: "rule[0]"},
		{By: task.ActorAgent, Rule: gates.RuleWindow},
	} {
		if err := f.svc.Resolve(ctx, g.ID, r); !errors.Is(err, gates.ErrRefused) {
			t.Errorf("Resolve(%+v) = %v, want ErrRefused", r, err)
		}
	}
	if got := f.get(t, g.ID); got.Status != gate.Open {
		t.Fatalf("status = %s, want open", got.Status)
	}
	if len(f.cons.Calls) != 0 {
		t.Fatalf("consequences = %v, want none", f.cons.Calls)
	}
	if err := f.svc.Resolve(ctx, g.ID, gates.Resolution{Intent: "answer", Answer: "yes", By: task.ActorPerson}); err != nil {
		t.Fatalf("a person's answer: %v", err)
	}
	if got := f.get(t, g.ID); got.Status != gate.Answered || got.By != task.ActorPerson || got.Answer != "yes" {
		t.Fatalf("gate = %+v, want answered by a person", got)
	}
}

func TestRecordSentIsNotAResolution(t *testing.T) {
	f := newFixture(t, nil, bundled)
	ctx := context.Background()
	sent, err := f.store.RecordSent(ctx, gate.Gate{Task: "t-0000000000000001", Unit: acme, Kind: gate.KindDraft, Name: "draft", Subject: "4", Intents: []string{"approve", "discard"}})
	if err != nil {
		t.Fatal(err)
	}
	if sent.Status != gate.Approved {
		t.Fatalf("recorded gate is %s, want approved", sent.Status)
	}
	for _, r := range []gates.Resolution{{Intent: "discard", By: task.ActorPerson}, {Intent: "approve", By: task.ActorAgent}} {
		if err := f.svc.Resolve(ctx, sent.ID, r); !errors.Is(err, gates.ErrResolved) {
			t.Errorf("Resolve(%+v) on a recorded post = %v, want ErrResolved", r, err)
		}
	}
	if len(f.cons.Calls) != 0 {
		t.Fatalf("a recorded post applied %v; it is posted already", f.cons.Calls)
	}
}

func TestAPolicyRuleReleasesADraftGate(t *testing.T) {
	send := policy{dec: gates.Decision{Mode: gate.ModeSend, Rule: "rule[1]"}}
	f := newFixture(t, send, bundled)
	g := f.open(t, gate.Gate{Kind: gate.KindDraft, Unit: acme, Subject: "9"}, send)
	if g.Status != gate.Approved || g.Intent != "approve" || g.By != task.ActorWorkflow || g.Rule != "rule[1]" {
		t.Fatalf("gate = %+v, want approved by rule[1]", g)
	}
	if want := []string{"publish 1"}; !equal(f.cons.Calls, want) {
		t.Fatalf("consequences = %v, want %v", f.cons.Calls, want)
	}

	held := f.open(t, gate.Gate{Kind: gate.KindDraft, Unit: acme, Subject: "10"}, policy{dec: gates.Decision{Mode: gate.ModeWait}})
	ctx := context.Background()
	if err := f.svc.Resolve(ctx, held.ID, gates.Resolution{By: task.ActorWorkflow, Rule: "rule[7]"}); !errors.Is(err, gates.ErrRefused) {
		t.Fatalf("a rule the policy does not hold = %v, want ErrRefused", err)
	}
	if err := f.svc.Resolve(ctx, held.ID, gates.Resolution{By: task.ActorWorkflow, Rule: "rule[1]"}); err != nil {
		t.Fatalf("the trusted policy's rule: %v", err)
	}
	if got := f.get(t, held.ID); got.Status != gate.Approved || got.Rule != "rule[1]" {
		t.Fatalf("gate = %+v, want approved by rule[1]", got)
	}

	dry := f.open(t, gate.Gate{Kind: gate.KindDraft, Unit: acme, Subject: "11"}, policy{dec: gates.Decision{Mode: gate.ModeDryRun, Rule: "rule[2]"}})
	if dry.Status != gate.Rejected || dry.Intent != "discard" || dry.Rule != "rule[2]" {
		t.Fatalf("dry run gate = %+v, want rejected by rule[2]", dry)
	}
}

func TestAPolicyRuleReleasesAPlanGate(t *testing.T) {
	f := newFixture(t, nil, bundled)
	g := f.open(t, gate.Gate{Kind: gate.KindPlan, Unit: acme, Subject: "review/first"}, policy{dec: gates.Decision{Mode: gate.ModeRun, Rule: "gates.approval.rule[0]"}})
	if g.Status != gate.Approved || g.Intent != "run" || g.Rule != "gates.approval.rule[0]" {
		t.Fatalf("gate = %+v, want approved by its rule", g)
	}
	if want := []string{"release 1"}; !equal(f.cons.Calls, want) {
		t.Fatalf("consequences = %v, want %v", f.cons.Calls, want)
	}
	dry := f.open(t, gate.Gate{Kind: gate.KindPlan, Unit: acme, Subject: "review/next"}, policy{dec: gates.Decision{Mode: gate.ModeDryRun, Rule: "rule[0]"}})
	if dry.Status != gate.Open {
		t.Fatalf("dry_run on a plan gate = %+v, want it held", dry)
	}
}

func TestAGateWithAPassedDeadlineResolvesToItsDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixtureAt(t, nil, bundled, time.Now)
		ctx := context.Background()
		start := time.Now()
		g := f.open(t, gate.Gate{Kind: gate.KindPlan, Unit: acme, Subject: "review/first"}, policy{dec: gates.Decision{Mode: gate.ModeDelay, Window: time.Minute}})
		if g.Status != gate.Open || g.Default != "run" || g.Deadline != start.Add(time.Minute) {
			t.Fatalf("gate = %+v, want open until %s with default run", g, start.Add(time.Minute))
		}
		if err := f.svc.Resolve(ctx, g.ID, gates.Resolution{Rule: gates.RuleWindow}); !errors.Is(err, gates.ErrRefused) {
			t.Fatalf("the window before its deadline = %v, want ErrRefused", err)
		}
		time.Sleep(time.Minute - time.Nanosecond)
		if err := f.svc.Tick(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got := f.get(t, g.ID); got.Status != gate.Open {
			t.Fatalf("before the deadline the gate is %s", got.Status)
		}
		time.Sleep(time.Nanosecond)
		if err := f.svc.Tick(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		got := f.get(t, g.ID)
		if got.Status != gate.Approved || got.Intent != "run" || got.Rule != gates.RuleWindow || got.By != task.ActorWorkflow {
			t.Fatalf("gate = %+v, want approved run by the window", got)
		}
		if want := []string{"release 1"}; !equal(f.cons.Calls, want) {
			t.Fatalf("consequences = %v, want %v", f.cons.Calls, want)
		}
	})
}

func TestAnIntentsStatusComesFromGatesToml(t *testing.T) {
	odd := profile{{Name: "verdict", Kind: gate.KindStep, Label: "Endorse?", Intents: []gates.Intent{
		{Name: "approve", Status: gate.Rejected}, {Name: "nope", Status: gate.Approved},
	}}}
	f := newFixture(t, nil, odd)
	ctx := context.Background()
	a := f.open(t, gate.Gate{Name: "verdict"}, nil)
	if err := f.svc.Resolve(ctx, a.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson, Status: gate.Approved}); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, a.ID); got.Status != gate.Rejected {
		t.Fatalf("approve resolved to %s; gates.toml says rejected", got.Status)
	}
	b := f.open(t, gate.Gate{Name: "verdict"}, nil)
	if err := f.svc.Resolve(ctx, b.ID, gates.Resolution{Intent: "nope", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if got := f.get(t, b.ID); got.Status != gate.Approved {
		t.Fatalf("nope resolved to %s; gates.toml says approved", got.Status)
	}
	c := f.open(t, gate.Gate{Name: "verdict"}, nil)
	if err := f.svc.Resolve(ctx, c.ID, gates.Resolution{Intent: "maybe", By: task.ActorPerson}); !errors.Is(err, gates.ErrIntent) {
		t.Fatalf("an undeclared intent = %v, want ErrIntent", err)
	}
}

type tasksFake struct {
	held   map[unit.Unit]task.ID
	done   map[unit.Unit]task.ID
	opened []coretasks.OpenRequest
}

func (f *tasksFake) TasksOf(_ context.Context, u unit.Unit) ([]task.Task, error) {
	var out []task.Task
	if id, ok := f.done[u]; ok {
		out = append(out, task.Task{ID: id, Category: task.CategoryDone})
	}
	if id, ok := f.held[u]; ok {
		out = append(out, task.Task{ID: id, Category: task.CategoryReady})
	}
	return out, nil
}

func (f *tasksFake) Open(_ context.Context, r coretasks.OpenRequest) (task.Task, error) {
	f.opened = append(f.opened, r)
	return task.Task{ID: "t-00000000000000ff"}, nil
}

func TestOpenGivesAGateTheTaskOfItsUnitOrACaptureTask(t *testing.T) {
	store := gatestest.NewStore(fixedNow)
	tk := &tasksFake{held: map[unit.Unit]task.ID{acme: "t-00000000000000aa"}}
	svc := gates.New(gates.Deps{Store: store, Reader: store, Profile: bundled, Tasks: tk, Clock: clock{fixedNow}})
	ctx := context.Background()
	held, err := svc.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Unit: acme}, nil)
	if err != nil || held.Task != "t-00000000000000aa" || len(tk.opened) != 0 {
		t.Fatalf("gate on a held unit = %+v, %v, opened %v", held, err, tk.opened)
	}
	thread := unit.Unit{Provider: "slack", Key: "T1/C1/1.2"}
	free, err := svc.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Unit: thread}, nil)
	if err != nil || free.Task != "t-00000000000000ff" {
		t.Fatalf("gate on a unit no task holds = %+v, %v", free, err)
	}
	if len(tk.opened) != 1 || tk.opened[0].Kind != task.KindCapture || tk.opened[0].Unit == nil || *tk.opened[0].Unit != thread || tk.opened[0].Title != "The agent asks" {
		t.Fatalf("capture = %+v, want a capture task on the thread titled by the gate", tk.opened)
	}
	if _, err := svc.Open(ctx, gate.Gate{Name: "missing-gate"}, nil); !errors.Is(err, gates.ErrNoDef) {
		t.Fatalf("an undeclared gate = %v, want ErrNoDef", err)
	}
}

func TestAGateSkipsTheClosedTasksOfItsUnit(t *testing.T) {
	store := gatestest.NewStore(fixedNow)
	thread := unit.Unit{Provider: "slack", Key: "T1/C1/1.2"}
	tk := &tasksFake{held: map[unit.Unit]task.ID{acme: "t-00000000000000aa"}, done: map[unit.Unit]task.ID{acme: "t-00000000000000dd", thread: "t-00000000000000de"}}
	svc := gates.New(gates.Deps{Store: store, Reader: store, Profile: bundled, Tasks: tk, Clock: clock{fixedNow}})
	ctx := context.Background()
	if g, err := svc.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Unit: acme}, nil); err != nil || g.Task != "t-00000000000000aa" {
		t.Fatalf("gate beside a done task = %+v, %v", g, err)
	}
	if g, err := svc.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Unit: thread}, nil); err != nil || g.Task != "t-00000000000000ff" || len(tk.opened) != 1 {
		t.Fatalf("gate on a unit only a done task holds = %+v, %v, opened %v", g, err, tk.opened)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type eventLog struct{ evs []port.Event }

func (l *eventLog) RecordEvents(_ context.Context, evs ...port.Event) error {
	l.evs = append(l.evs, evs...)
	return nil
}

func TestResolvingAStepGateRecordsAnEvent(t *testing.T) {
	store := gatestest.NewStore(fixedNow)
	log := &eventLog{}
	svc := gates.New(gates.Deps{Store: store, Reader: store, Profile: bundled, Clock: clock{fixedNow}, Events: log})
	ctx := context.Background()
	step, err := svc.Open(ctx, gate.Gate{Name: "review-diff", Task: "t-0000000000000001", Unit: acme}, nil)
	if err != nil {
		t.Fatal(err)
	}
	question, err := svc.Open(ctx, gate.Gate{Kind: gate.KindQuestion, Task: "t-0000000000000001", Unit: acme}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(log.evs) != 0 {
		t.Fatalf("opening recorded %+v", log.evs)
	}
	if err := svc.Resolve(ctx, question.ID, gates.Resolution{Intent: "dismiss", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Resolve(ctx, step.ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	want := port.Event{Unit: task.UnitOf("t-0000000000000001"), Kind: gate.EventResolved, Key: fmt.Sprintf("review-diff/%d", step.ID), Actor: "person", Data: "approve", At: fixedNow()}
	if len(log.evs) != 1 || log.evs[0] != want {
		t.Fatalf("events %+v, want only %+v", log.evs, want)
	}
}
