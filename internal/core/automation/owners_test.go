package automation_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func (r *rig) stored(kind string) []port.Event {
	var out []port.Event
	for _, ev := range r.ledger.Events() {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func eventUnits(evs []port.Event) []unit.Unit {
	out := make([]unit.Unit, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Unit)
	}
	return out
}

func TestAStepRanOnASessionLandsOnItsOwner(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.units.SetOwners(s1, p1)
	r.sweep()
	ran := r.stored(workflow.KindStepRan)
	if !slices.Equal(eventUnits(ran), []unit.Unit{p1}) {
		t.Fatalf("workflow_step_ran on %v, want on %s", eventUnits(ran), p1)
	}
	if k := ran[0].Key; !strings.HasPrefix(k, "close/session/t1/") || !strings.HasSuffix(k, ":0") || ran[0].Actor != "close" || ran[0].Data != "session.close ran" {
		t.Errorf("event = %+v, want the subject's key and actor close", ran[0])
	}
	if planned := r.stored(workflow.KindPlanned); len(planned) == 0 || planned[0].Unit != s1 {
		t.Errorf("workflow_planned on %v, want on the subject", eventUnits(planned))
	}
}

func TestAStepRanWithNoOwnerStaysOnTheSubject(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.sweep()
	if ran := r.stored(workflow.KindStepRan); !slices.Equal(eventUnits(ran), []unit.Unit{s1}) || ran[0].Actor != "close" {
		t.Errorf("workflow_step_ran = %+v, want one on %s with actor close", ran, s1)
	}
}

func TestTheOwnerIsResolvedBeforeTheStepDropsIt(t *testing.T) {
	w := sweepWF("remove", always, "workspace.remove")
	w.Provider, w.Slot = unit.Workspace, "workspace"
	r := newRig(t, w)
	r.put(w1, "a")
	r.units.SetOwners(w1, p1)
	r.steps.OnRun("workspace.remove", func(u unit.Unit) { r.units.SetOwners(u) })
	r.sweep()
	if ran := r.stored(workflow.KindStepRan); !slices.Equal(eventUnits(ran), []unit.Unit{p1}) {
		t.Errorf("workflow_step_ran on %v, want on %s", eventUnits(ran), p1)
	}
}

func TestEachOwnerGetsOneStepRan(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close", "notify.self"))
	r.put(s1, "a")
	r.units.SetOwners(s1, p1, p2)
	r.sweep()
	ran := r.stored(workflow.KindStepRan)
	var got []string
	for _, ev := range ran {
		got = append(got, ev.Unit.Key+" "+ev.Key[len(ev.Key)-2:])
	}
	if want := []string{"o/r#1 :0", "o/r#2 :0", "o/r#1 :1", "o/r#2 :1"}; !slices.Equal(got, want) {
		t.Errorf("workflow_step_ran = %v, want %v", got, want)
	}
}

func TestARefusedStepStaysOnTheSubject(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.units.SetOwners(s1, p1)
	r.steps.Fail("session.close", s1, automation.Refused{Reason: "the agent is busy"})
	r.sweep()
	if refused := r.stored(workflow.KindStepRefused); !slices.Equal(eventUnits(refused), []unit.Unit{s1}) {
		t.Errorf("workflow_refused on %v, want on the subject", eventUnits(refused))
	}
	if ran := r.stored(workflow.KindStepRan); len(ran) != 0 {
		t.Errorf("a refused step wrote %+v", ran)
	}
}

func TestAnOwnerThatCannotBeReadRunsNothing(t *testing.T) {
	r := newRig(t, sweepWF("close", idle, "session.close"))
	r.put(s1, "a")
	r.units.FailOwners(s1, errors.New("database is locked"))
	r.sweep()
	if got := r.ran("session.close"); len(got) != 0 {
		t.Fatalf("ran %v with an unreadable owner", got)
	}
	r.units.FailOwners(s1, nil)
	r.units.SetOwners(s1, p1)
	r.sweep()
	if ran := r.stored(workflow.KindStepRan); !slices.Equal(eventUnits(ran), []unit.Unit{p1}) {
		t.Errorf("workflow_step_ran on %v, want on %s", eventUnits(ran), p1)
	}
}

func TestAnItemAppliedByHandLandsOnItsOwner(t *testing.T) {
	r := newRig(t, windowed(sweepWF("close", idle, "session.close"), 5*time.Minute))
	r.put(s1, "a")
	r.units.SetOwners(s1, p1)
	r.sweep()
	it := r.mustItem("close", s1)
	if _, err := r.e.Act(context.Background(), it.ID, workflow.ActionApply, ""); err != nil {
		t.Fatal(err)
	}
	if ran := r.stored(workflow.KindStepRan); !slices.Equal(eventUnits(ran), []unit.Unit{p1}) {
		t.Errorf("workflow_step_ran on %v, want on %s", eventUnits(ran), p1)
	}
}
