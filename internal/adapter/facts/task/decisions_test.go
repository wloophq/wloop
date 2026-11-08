package facttask_test

import (
	"context"
	"slices"
	"testing"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestDecisionsFactFromGatesAndLinks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.decisions = decision.Map{
		Gates:      map[gate.Kind]decision.Kind{gate.KindPlan: decision.ApprovePlan},
		Situations: map[unit.ProviderID]map[string]decision.Kind{chatA.Provider: {"merge": decision.LastStep}},
	}
	e.task(task.Task{ID: "t-00000000000000a1", State: "next", Category: task.CategoryReady, Title: "a", Units: []unit.Unit{chatA}})
	e.links[chatA] = facttask.Link{Situation: "merge", Moved: monday.Add(time.Hour)}
	opened := monday
	gs := e.db.Gates(func() time.Time { return opened })
	if _, err := gs.Open(context.Background(), gate.Gate{Task: "t-00000000000000a1", Unit: task.UnitOf("t-00000000000000a1"), Kind: gate.KindPlan, Name: "plan", Subject: "1", Intents: []string{"approve"}}); err != nil {
		t.Fatal(err)
	}
	f := e.facts("t-00000000000000a1")
	if got := f["task.decisions"]; !slices.Equal(got.([]string), []string{"approve-plan", "last-step"}) {
		t.Fatalf("task.decisions = %v", got)
	}
	if got := f["task.decision_since"].(time.Time); !got.Equal(opened) {
		t.Fatalf("task.decision_since = %v, want the gate's opening %v", got, opened)
	}
}

func TestDecisionsFactEmptyWithoutMapping(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "t-00000000000000b2", State: "next", Category: task.CategoryReady, Title: "b", Units: []unit.Unit{chatA}})
	e.links[chatA] = facttask.Link{Situation: "merge", Signals: map[string]bool{"ready": true}}
	f := e.facts("t-00000000000000b2")
	if got := f["task.decisions"]; len(got.([]string)) != 0 {
		t.Fatalf("task.decisions = %v, want none", got)
	}
	if got := f["task.decision_since"].(time.Time); !got.IsZero() {
		t.Fatalf("task.decision_since = %v, want zero", got)
	}
}

func TestAClosedTaskWaitsOnNoDecision(t *testing.T) {
	t.Parallel()
	for state, category := range map[task.State]task.Category{"done": task.CategoryDone, "trash": task.CategoryDropped} {
		e := newEnv(t)
		e.decisions = decision.Map{Situations: map[unit.ProviderID]map[string]decision.Kind{chatA.Provider: {"waits_on_viewer": decision.Answer}}}
		closed := e.task(task.Task{ID: "t-00000000000000c3", State: state, Category: category, Title: "c", Units: []unit.Unit{chatA}})
		e.links[chatA] = facttask.Link{Situation: "waits_on_viewer", Moved: monday}
		f, err := e.p.TaskFacts(context.Background(), closed)
		if err != nil {
			t.Fatal(err)
		}
		if got := f["task.decisions"]; len(got.([]string)) != 0 {
			t.Errorf("a %s task: task.decisions = %v, want none", state, got)
		}
	}
}
