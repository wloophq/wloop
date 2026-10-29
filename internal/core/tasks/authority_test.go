package tasks_test

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/gates/gatestest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/profiles"
)

// profileStates are a loaded tasks.toml as the service's States, with the catalog's authority over it and the
// gates gates names.
type profileStates struct {
	intentload.TasksDef
	gates map[string]gate.Gate
}

func (p profileStates) Gate(verb string) (gate.Gate, bool) {
	g, ok := p.gates[p.Verbs[verb].Gate]
	return g, ok
}

func (p profileStates) Authority(ctx context.Context, verb string, a task.Actor, t task.Task) (bool, bool, error) {
	l := &intentload.Loaded{Tasks: p.TasksDef}
	return catalog.Authority(func() *intentload.Loaded { return l })(ctx, verb, a, t)
}

// tasksToml is the bundled tasks.toml with each edit made in the table of its verb.
func tasksToml(t *testing.T, edits ...verbEdit) intentload.TasksDef {
	t.Helper()
	raw, err := fs.ReadFile(profiles.Default, "default/tasks.toml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, e := range edits {
		head := "[verbs." + e.verb + "]\n"
		at := strings.Index(text, head)
		if at < 0 {
			t.Fatalf("no %s", head)
		}
		end := at + strings.Index(text[at:], "\n\n")
		if end < at {
			end = len(text)
		}
		block := text[at:end]
		if e.old == "" {
			block += "\n" + e.new
		} else if !strings.Contains(block, e.old) {
			t.Fatalf("%s holds no %q", head, e.old)
		} else {
			block = strings.Replace(block, e.old, e.new, 1)
		}
		text = text[:at] + block + text[end:]
	}
	d, problems := intentload.BundledTasks(fstest.MapFS{"tasks.toml": {Data: []byte(text)}})
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	return d
}

type verbEdit struct{ verb, old, new string }

func profileRig(t *testing.T, d intentload.TasksDef) *rig {
	t.Helper()
	r := newRig()
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock,
		States: profileStates{TasksDef: d}, Authority: profileStates{TasksDef: d}.Authority})
	return r
}

func TestAVerbsTransitionComesFromTheProfile(t *testing.T) {
	r := profileRig(t, tasksToml(t, verbEdit{"someday", `to      = "someday"`, `to      = "waiting"`}))
	e := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	res, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbSomeday})
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.State != "waiting" || res.Task.Category != task.CategoryWaiting {
		t.Errorf("someday moved the task to %s (%s), want waiting as the profile's to says", res.Task.State, res.Task.Category)
	}
	r.put(t, task.Task{ID: "t-2", State: "done", Origin: task.OriginUser})
	if _, err := r.svc.Do(context.Background(), "t-2", coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbSomeday}); !errors.Is(err, coretasks.ErrVerb) {
		t.Errorf("someday from done, which its from leaves out: %v", err)
	}
}

func TestAnAgentMayCloseWhenTheProfileSaysSo(t *testing.T) {
	ctx := context.Background()
	e := task.Task{ID: "t-1", State: "next", Origin: task.OriginUser}
	bundled := profileRig(t, tasksToml(t))
	bundled.put(t, e)
	if _, err := bundled.svc.Do(ctx, e.ID, coretasks.Request{Verb: task.VerbDone, Actor: task.ActorAgent}); !errors.Is(err, coretasks.ErrForbidden) {
		t.Fatalf("an agent closing under the bundled profile: %v", err)
	}
	r := profileRig(t, tasksToml(t, verbEdit{"done", `workflow = "true" }`, `workflow = "true", agent = "true" }`}))
	r.put(t, e)
	res, err := r.svc.Do(ctx, e.ID, coretasks.Request{Verb: task.VerbDone, Actor: task.ActorAgent, Source: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.Category != task.CategoryDone {
		t.Errorf("category %s", res.Task.Category)
	}
	if evs := verbEvents(r.store.Events); len(evs) != 1 || evs[0].Actor != "agent:claude" {
		t.Errorf("events %+v, want one by agent:claude", evs)
	}
}

func TestNoActorMeansForbidden(t *testing.T) {
	r := profileRig(t, tasksToml(t))
	e := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	for _, a := range []task.Actor{task.ActorAgent, task.ActorPlugin} {
		if err := r.svc.Accepts(e, task.VerbDone, a); !errors.Is(err, coretasks.ErrForbidden) {
			t.Errorf("%s on done, which names no by.%s: %v", a, a, err)
		}
	}
	if err := r.svc.Accepts(e, task.VerbSomeday, task.ActorWorkflow); !errors.Is(err, coretasks.ErrForbidden) {
		t.Errorf("a workflow on someday, which names no by.workflow: %v", err)
	}
	if err := r.svc.Accepts(e, task.VerbDone, task.ActorWorkflow); err != nil {
		t.Errorf("a workflow on done: %v", err)
	}
	none := coretasks.New(coretasks.Deps{Store: r.store, States: taskstest.BundledStates})
	if err := none.Accepts(e, task.VerbSomeday, task.ActorPerson); err != nil {
		t.Errorf("a person with no profile's authority at hand: %v", err)
	}
	if err := none.Accepts(e, task.VerbDone, task.ActorWorkflow); !errors.Is(err, coretasks.ErrForbidden) {
		t.Errorf("a workflow with no profile's authority at hand: %v", err)
	}
}

func TestAPersonMayRunEveryBundledVerb(t *testing.T) {
	d := tasksToml(t)
	auth := profileStates{TasksDef: d}
	for verb := range d.Verbs {
		allow, gated, err := auth.Authority(context.Background(), verb, task.ActorPerson, task.Task{ID: "t-1", State: "next"})
		if err != nil || !allow || gated {
			t.Errorf("a person on %s: allow %v, gate %v, %v", verb, allow, gated, err)
		}
		if _, ok := d.Verbs[verb].By[string(task.ActorAgent)]; ok {
			t.Errorf("%s has a by.agent; the bundled profile gives agents no verb", verb)
		}
	}
	for _, v := range []string{"done", "dismiss", "next", "waiting", "schedule", "focus"} {
		if d.Verbs[v].By[string(task.ActorWorkflow)] != "true" {
			t.Errorf("%s: by.workflow %q, want true", v, d.Verbs[v].By[string(task.ActorWorkflow)])
		}
	}
}

func TestAByPredicateAllowsPerKind(t *testing.T) {
	r := profileRig(t, tasksToml(t, verbEdit{"someday", `by      = { person = "true" }`, `by      = { person = "true", workflow = 'task.prop.kind == "capture"' }`}))
	capture := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser, Props: task.Props{task.PropKind: {Text: "capture"}}})
	action := r.put(t, task.Task{ID: "t-2", State: "next", Origin: task.OriginUser})
	if err := r.svc.Accepts(capture, task.VerbSomeday, task.ActorWorkflow); err != nil {
		t.Errorf("a workflow on a capture's someday: %v", err)
	}
	if err := r.svc.Accepts(action, task.VerbSomeday, task.ActorWorkflow); !errors.Is(err, coretasks.ErrForbidden) {
		t.Errorf("a workflow on an action's someday: %v", err)
	}
	if _, err := r.svc.Do(context.Background(), action.ID, coretasks.Request{Verb: task.VerbSomeday, Actor: task.ActorWorkflow}); !errors.Is(err, coretasks.ErrForbidden) {
		t.Errorf("Do: %v", err)
	}
	if r.task(t, action.ID).State != "next" {
		t.Error("the refused verb moved the action")
	}
}

func TestAGatedVerbOpensTheGateItsVerbNames(t *testing.T) {
	ctx := context.Background()
	d := tasksToml(t, verbEdit{"done", `by      = { person = "true", workflow = "true" }`, `by      = { person = "true", workflow = "gate" }` + "\ngate    = \"close-check\""})
	confirm := gate.Gate{Name: "close-check", Kind: gate.KindProposal, Intents: []string{"approve", "reject"}}
	r := newRig()
	gs := gatestest.NewStore(func() time.Time { return t0 })
	states := profileStates{TasksDef: d, gates: map[string]gate.Gate{"close-check": confirm}}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, Gates: gs,
		States: states, Authority: states.Authority})
	e := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})

	res, err := r.svc.Do(ctx, e.ID, coretasks.Request{Verb: task.VerbDone, Actor: task.ActorWorkflow, Source: "tidy", Args: map[string]string{"why": "merged"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Task.State != "next" || r.task(t, e.ID).State != "next" {
		t.Errorf("a gated done moved the task to %s", r.task(t, e.ID).State)
	}
	open, err := gs.OfTask(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].Name != "close-check" || open[0].Kind != gate.KindProposal || !slices.Equal(open[0].Intents, confirm.Intents) ||
		open[0].Unit != task.UnitOf(e.ID) || open[0].Status != gate.Open {
		t.Fatalf("gates %+v", open)
	}
	req, ok := coretasks.ProposedVerb(open[0])
	if !ok || req.Verb != task.VerbDone || req.Actor != task.ActorPerson || req.Args["why"] != "merged" {
		t.Fatalf("proposed %+v %v", req, ok)
	}
	if evs := verbEvents(r.store.Events); len(evs) > 0 {
		t.Errorf("a gated verb recorded %+v", evs)
	}
	if _, err := r.svc.Do(ctx, e.ID, req); !errors.Is(err, coretasks.ErrForbidden) {
		t.Fatalf("the proposal of a gate still open: %v", err)
	}
	if _, err := gs.Resolve(ctx, open[0].ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Do(ctx, e.ID, req); err != nil {
		t.Fatal(err)
	}
	if r.task(t, e.ID).Category != task.CategoryDone {
		t.Error("the approved proposal did not close the task")
	}
}

// personGated is a rig whose profile gates verb for the person behind close-check.
func personGated(t *testing.T, verb string) (*rig, *gatestest.Store) {
	t.Helper()
	d := tasksToml(t, verbEdit{verb, `by      = { person = "true"`, `gate    = "close-check"` + "\nby      = { person = \"gate\""})
	r := newRig()
	gs := gatestest.NewStore(func() time.Time { return t0 })
	states := profileStates{TasksDef: d, gates: map[string]gate.Gate{"close-check": {Name: "close-check", Kind: gate.KindProposal, Intents: []string{"approve", "reject"}}}}
	r.svc = coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, Gates: gs,
		States: states, Authority: states.Authority})
	return r, gs
}

func TestApprovingAPersonGatedVerbClosesTheTask(t *testing.T) {
	ctx := context.Background()
	r, gs := personGated(t, "done")
	e := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	if _, err := r.svc.Do(ctx, e.ID, coretasks.Request{Verb: task.VerbDone, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	open, _ := gs.OfTask(ctx, e.ID)
	if len(open) != 1 {
		t.Fatalf("gates %+v", open)
	}
	if _, err := gs.Resolve(ctx, open[0].ID, gates.Resolution{Status: gate.Approved, Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	req, _ := coretasks.ProposedVerb(open[0])
	if _, err := r.svc.Do(ctx, e.ID, req); err != nil {
		t.Fatal(err)
	}
	if got, _ := gs.OfTask(ctx, e.ID); len(got) != 1 || r.task(t, e.ID).Category != task.CategoryDone {
		t.Errorf("category %s, gates %+v: the approval should close the task and open no gate", r.task(t, e.ID).Category, got)
	}
	other := r.put(t, task.Task{ID: "t-2", State: "next", Origin: task.OriginUser})
	if _, err := r.svc.Do(ctx, other.ID, req); !errors.Is(err, coretasks.ErrForbidden) {
		t.Errorf("another task's approval closed t-2: %v", err)
	}
}

func TestAGatedVerbOpensOneGateWhileItIsOpen(t *testing.T) {
	ctx := context.Background()
	r, gs := personGated(t, "done")
	e := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	for range 2 {
		if _, err := r.svc.Do(ctx, e.ID, coretasks.Request{Verb: task.VerbDone, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := gs.OfTask(ctx, e.ID); len(got) != 1 {
		t.Fatalf("gates %+v, want the one still open", got)
	}
	if _, err := gs.Resolve(ctx, 1, gates.Resolution{Status: gate.Rejected, Intent: "reject", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Do(ctx, e.ID, coretasks.Request{Verb: task.VerbDone, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := gs.OfTask(ctx, e.ID); len(got) != 2 {
		t.Errorf("gates %+v, want a new one once the first was rejected", got)
	}
}

func TestAGatedDismissOnADisabledPluginsTaskOpensTheGate(t *testing.T) {
	ctx := context.Background()
	r, gs := personGated(t, "dismiss")
	r.facts.off[unit.GitHub] = true
	e := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser, Units: []unit.Unit{unitU}})
	if _, err := r.svc.Do(ctx, e.ID, coretasks.Request{Verb: task.VerbDismiss, Actor: task.ActorPerson, Source: "cockpit"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := gs.OfTask(ctx, e.ID); len(got) != 1 || r.task(t, e.ID).State != "next" {
		t.Errorf("state %s, gates %+v", r.task(t, e.ID).State, got)
	}
}

func TestAVerbWithNoActorIsRefused(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	allowAll := coretasks.New(coretasks.Deps{Store: r.store, Items: r.items, Facts: r.facts, Me: me, Clock: r.clock, States: taskstest.BundledStates,
		Authority: func(context.Context, string, task.Actor, task.Task) (bool, bool, error) { return true, false, nil }})
	if _, err := allowAll.Do(context.Background(), e.ID, coretasks.Request{Verb: task.VerbDone, Source: "somewhere"}); !errors.Is(err, coretasks.ErrForbidden) || !strings.Contains(err.Error(), "names no actor") {
		t.Fatalf("a request with no actor: %v", err)
	}
	if err := allowAll.Accepts(e, task.VerbDone, ""); !errors.Is(err, coretasks.ErrForbidden) {
		t.Errorf("Accepts with no actor: %v", err)
	}
	if r.task(t, e.ID).State != "next" {
		t.Error("the refused request moved the task")
	}
}

func TestAVerbRunsTheHandlerItsProfileNames(t *testing.T) {
	ctx := context.Background()
	r := profileRig(t, tasksToml(t,
		verbEdit{"area", `handler = "area"`, `handler = "context"`},
		verbEdit{"answer", `handler = "answer"`, `handler = "approve"`}))
	e := r.put(t, task.Task{ID: "t-1", State: "next", Origin: task.OriginUser})
	res, err := r.svc.Do(ctx, e.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbArea, Args: map[string]string{"set": "@calls, @home"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Task.Prop(task.PropContexts).List; !slices.Equal(got, []string{"@calls", "@home"}) {
		t.Errorf("area with the context handler set contexts %q", got)
	}
	var ran []task.Verb
	r.svc.Register(task.OriginMessage, "approve", func(_ context.Context, e task.Task, req coretasks.Request) (coretasks.Result, error) {
		ran = append(ran, req.Verb)
		return coretasks.Result{Task: e}, nil
	})
	m := r.put(t, task.Task{ID: "t-2", State: "inbox", Origin: task.OriginMessage})
	if _, err := r.svc.Do(ctx, m.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbAnswer}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Do(ctx, m.ID, coretasks.Request{Actor: task.ActorPerson, Verb: task.VerbApprove}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ran, []task.Verb{task.VerbAnswer, task.VerbApprove}) || !r.svc.Handles(task.Task{Origin: task.OriginMessage}, task.VerbAnswer) {
		t.Errorf("the approve handler ran %v", ran)
	}
}

func TestWorkflowVerbsRecordEventsWithTheirActor(t *testing.T) {
	r := newRig()
	e := r.put(t, task.Task{ID: "t-1", State: "inbox", Origin: task.OriginUser})
	chain := workflow.Chain{}.Then("triage")
	if _, err := r.svc.Do(context.Background(), e.ID, coretasks.Request{Verb: task.VerbNext, Actor: task.ActorWorkflow, Source: "triage", Chain: chain}); err != nil {
		t.Fatal(err)
	}
	evs := verbEvents(r.store.Events)
	if len(evs) != 1 || evs[0].Kind != "verb_next" || evs[0].Actor != "workflow:triage" {
		t.Fatalf("events %+v", evs)
	}
	data, got := workflow.SplitChain(evs[0].Data)
	if data != "inbox" || got.Depth != 1 || !slices.Equal(got.Workflows, []string{"triage"}) {
		t.Errorf("data %q, chain %+v", data, got)
	}
}
