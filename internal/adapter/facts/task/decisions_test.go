package facttask_test

import (
	"context"
	"slices"
	"testing"
	"time"

	facttask "github.com/wloophq/wloop/internal/adapter/facts/task"
	"github.com/wloophq/wloop/internal/core/gates"
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
	if n := e.reads["every open gate"]; n != 0 {
		t.Fatalf("one task's facts outside a pass read every open gate %d times, want the task's own gates alone", n)
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

func TestDecisionsSkipDraftPinnedByOpenQuestion(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.decisions = decision.Map{Gates: map[gate.Kind]decision.Kind{gate.KindQuestion: decision.Answer, gate.KindDraft: decision.SendDraft}}
	id := task.ID("t-00000000000000d4")
	e.task(task.Task{ID: id, State: "next", Category: task.CategoryReady, Title: "d"})
	gs := e.db.Gates(func() time.Time { return monday })
	open := func(kind gate.Kind, subject string) gate.Gate {
		g, err := gs.Open(context.Background(), gate.Gate{Task: id, Unit: task.UnitOf(id), Kind: kind, Subject: subject, Intents: []string{"approve"}})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	open(gate.KindDraft, "101")
	q := open(gate.KindQuestion, "9")
	e.pins[101] = 9
	if got := e.facts(id)["task.decisions"]; !slices.Equal(got.([]string), []string{"answer"}) {
		t.Fatalf("task.decisions = %v, want the question alone", got)
	}
	if _, err := gs.Resolve(context.Background(), q.ID, gates.Resolution{Status: gate.Answered, Intent: "answer", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if got := e.facts(id)["task.decisions"]; len(got.([]string)) != 0 {
		t.Fatalf("task.decisions = %v while the answered message settles, want none", got)
	}
	delete(e.pins, 101)
	if got := e.facts(id)["task.decisions"]; !slices.Equal(got.([]string), []string{"send-draft"}) {
		t.Fatalf("task.decisions = %v once the message closed, want the draft back", got)
	}
}
