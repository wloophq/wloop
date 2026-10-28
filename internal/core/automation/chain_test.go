package automation_test

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var (
	ta = unit.Unit{Provider: unit.Task, Key: "t-0000000000000001"}
	tb = unit.Unit{Provider: unit.Task, Key: "t-0000000000000002"}
)

// verbWF is a task workflow that reacts to the verb events on and runs a
// verb, once per event.
func verbWF(name string, on ...string) workflow.Workflow {
	w := eventWF(name, unit.Task, "task", always, on...)
	w.Steps = []workflow.Step{{Use: "classify", Reach: reach["classify"]}}
	w.Key = []string{"trigger.event.id"}
	return w
}

// chainRig runs each workflow's verb as the task.verb step does: it records
// the verb's event carrying the chain of the event that planned it, the
// workflow added; moves says which verb each workflow runs.
func chainRig(t *testing.T, moves map[string]string, wfs ...workflow.Workflow) *rig {
	t.Helper()
	r := newRig(t, wfs...)
	for _, u := range []unit.Unit{ta, tb} {
		r.units.Put(u, workflow.Facts{"task.fingerprint": u.Key, "task.repo": ""})
	}
	r.steps.OnRun("classify", func(u unit.Unit) {
		calls := r.steps.Ran()
		why := calls[len(calls)-1].Why
		var chain workflow.Chain
		if why.Event != nil {
			chain = why.Event.Chain
		}
		r.events.Append(workflow.Event{Unit: u, Kind: "verb_" + moves[why.Workflow], Actor: "workflow:" + why.Workflow,
			Chain: chain.Then(why.Workflow), At: t0})
	})
	r.sweep()
	return r
}

// runs are the workflows that ran, in order.
func (r *rig) runs() []string {
	var out []string
	for _, c := range r.steps.Ran() {
		out = append(out, c.Why.Workflow)
	}
	return out
}

// settle sweeps until the chain stops or n sweeps pass.
func (r *rig) settle(n int) {
	for range n {
		before := len(r.steps.Ran())
		r.sweep()
		if len(r.steps.Ran()) == before {
			return
		}
	}
}

func TestAWorkflowMoveTriggersAnotherWorkflow(t *testing.T) {
	r := chainRig(t, map[string]string{"clarify": "next", "kind": "focus"},
		verbWF("clarify", "verb_done"), verbWF("kind", "verb_next"))
	r.events.Append(workflow.Event{Unit: ta, Kind: "verb_done", Actor: "person:cockpit", At: t0})
	r.settle(5)
	if got := r.runs(); !slices.Equal(got, []string{"clarify", "kind"}) {
		t.Errorf("ran %v, want the person's done to plan clarify, whose next plans kind", got)
	}
}

func TestAWorkflowChainStopsAtItsDepth(t *testing.T) {
	names := []string{"w1", "w2", "w3", "w4"}
	moves := map[string]string{}
	var wfs []workflow.Workflow
	for i, n := range names {
		moves[n] = names[(i+1)%len(names)]
		wfs = append(wfs, verbWF(n, "verb_"+n))
	}
	r := chainRig(t, moves, wfs...)
	r.events.Append(workflow.Event{Unit: ta, Kind: "verb_w1", Actor: "person:cockpit", At: t0})
	r.settle(10)
	if got := r.runs(); !slices.Equal(got, []string{"w1", "w2", "w3"}) {
		t.Errorf("ran %v, want the chain to stop after 3 workflows, the default depth", got)
	}

	wfs[3].Trigger.Depth = 4
	r = chainRig(t, moves, wfs...)
	r.events.Append(workflow.Event{Unit: ta, Kind: "verb_w1", Actor: "person:cockpit", At: t0})
	r.settle(10)
	if got := r.runs(); !slices.Equal(got, names) {
		t.Errorf("ran %v, want w4, whose depth is 4, to close the chain", got)
	}
}

func TestAWorkflowNeverReentersItsChain(t *testing.T) {
	r := chainRig(t, map[string]string{"a": "b", "b": "a"}, verbWF("a", "verb_a"), verbWF("b", "verb_b"))
	r.events.Append(workflow.Event{Unit: ta, Kind: "verb_a", Actor: "person:cockpit", At: t0})
	r.settle(10)
	if got := r.runs(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("ran %v, want a then b, and no a again on b's move", got)
	}
	r.events.Append(workflow.Event{Unit: tb, Kind: "verb_a", Actor: "person:cockpit", At: t0})
	r.settle(10)
	if got := r.runs(); !slices.Equal(got, []string{"a", "b", "a", "b"}) {
		t.Errorf("ran %v, want a new chain from a person's verb", got)
	}
}

func TestTriggerActorsLimitsATriggerToPeople(t *testing.T) {
	people := verbWF("people", "verb_done")
	people.Trigger.Actors = []string{"person"}
	r := chainRig(t, map[string]string{"closer": "done", "people": "focus"}, verbWF("closer", "verb_next"), people)
	r.events.Append(workflow.Event{Unit: ta, Kind: "verb_next", Actor: "person:cockpit", At: t0})
	r.settle(5)
	if got := r.runs(); !slices.Equal(got, []string{"closer"}) {
		t.Fatalf("ran %v, want people to ignore the done closer ran", got)
	}
	r.events.Append(workflow.Event{Unit: tb, Kind: "verb_done", Actor: "person:tui", At: t0})
	r.settle(5)
	if got := r.runs(); !slices.Equal(got, []string{"closer", "people"}) {
		t.Errorf("ran %v, want people on a person's done", got)
	}
}

func TestARepoPauseHoldsTaskWorkflows(t *testing.T) {
	w := sweepWF("tidy", always, "classify")
	w.Provider, w.Slot = unit.Task, "task"
	r := newRig(t, w)
	r.units.Put(ta, workflow.Facts{"task.fingerprint": "a", "task.repo": "acme/api"})
	r.units.Put(tb, workflow.Facts{"task.fingerprint": "b", "task.repo": "acme/web"})
	r.pause = automation.Pause{Repos: []string{"Acme/API"}}
	r.sweep()
	if got := r.ran("classify"); !slices.Equal(got, []unit.Unit{tb}) {
		t.Errorf("ran %v, want only the task of the repository the pause does not name", got)
	}
	if it, ok := planItem(r.evaluate(), "tidy", ta); !ok || it.Decision != workflow.DecisionSkipped || it.Reason != "paused" {
		t.Errorf("the paused task's item %+v %v", it, ok)
	}
}
