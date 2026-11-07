package home_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/home"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func queueWords() fakeWords {
	w := words()
	w.kinds[decision.SendDraft] = home.KindWords{View: "decide-send-draft", Queue: "Drafts to send", Empty: "No draft waits.", Primary: "send"}
	w.kinds[decision.LastStep] = home.KindWords{View: "decide-last-step", Queue: "Last steps", Empty: "Nothing waits."}
	w.plugins["alpha"] = home.PluginWords{Primary: map[decision.Kind]string{decision.LastStep: "open"}, Situations: map[string]string{"merge": "can merge"},
		Kinds: map[decision.Kind]home.PluginKind{decision.LastStep: {Queue: "Ready to land"}}}
	w.plugins["beta"] = home.PluginWords{Primary: map[decision.Kind]string{decision.LastStep: "close"}}
	return w
}

func TestQueueOldestFirstOneItemPerDecision(t *testing.T) {
	a := task.Task{ID: "t-a", Title: "a"}
	b := task.Task{ID: "t-b", Title: "b"}
	question := func(id int64) *gate.Gate { return &gate.Gate{ID: id, Intents: []string{"answer", "dismiss"}} }
	d := fakeDecisions{
		"t-a": {{Kind: decision.Answer, Gate: question(1), Since: t0.Add(2 * time.Hour)}, {Kind: decision.Answer, Gate: question(2), Since: t0.Add(3 * time.Hour)}},
		"t-b": {{Kind: decision.Answer, Gate: question(3), Since: t0.Add(time.Hour)}, {Kind: decision.LastStep, Unit: unit.Unit{Provider: "alpha", Key: "x"}, Since: t0}},
	}
	s := home.Service{Lists: &fakeLists{sets: map[task.View][]task.Task{"decide-answer": {a, b}}}, Decisions: d, Words: queueWords()}
	q, err := s.Queue(context.Background(), decision.Answer, nil)
	if err != nil {
		t.Fatal(err)
	}
	var gates []int64
	for _, it := range q.Items {
		gates = append(gates, it.Found.Gate.ID)
	}
	if !slices.Equal(gates, []int64{3, 1, 2}) {
		t.Fatalf("gates in order %v, want [3 1 2]: one item per decision of the kind, the oldest first", gates)
	}
}

func TestQueuePrimaryForGateIsFirstIntent(t *testing.T) {
	e := task.Task{ID: "t-a"}
	for intents, want := range map[string]string{"approve,edit": "approve", "edit,send": "send"} {
		g := &gate.Gate{ID: 9, Kind: gate.KindDraft, Intents: strings.Split(intents, ",")}
		d := fakeDecisions{"t-a": {{Kind: decision.SendDraft, Gate: g, Since: t0}}}
		s := home.Service{Lists: &fakeLists{sets: map[task.View][]task.Task{"decide-send-draft": {e}}}, Decisions: d, Words: queueWords()}
		q, _ := s.Queue(context.Background(), decision.SendDraft, nil)
		if p := q.Items[0].Primary; p.Kind != "gate" || p.ID != want {
			t.Errorf("intents %s: primary %+v, want gate %s", intents, p, want)
		}
	}
}

func TestQueuePrimaryForSituationFromPluginLayer(t *testing.T) {
	a, b, c := task.Task{ID: "t-a"}, task.Task{ID: "t-b"}, task.Task{ID: "t-c"}
	d := fakeDecisions{
		"t-a": {{Kind: decision.LastStep, Unit: unit.Unit{Provider: "alpha", Key: "x"}, Situation: "merge", Since: t0}},
		"t-b": {{Kind: decision.LastStep, Unit: unit.Unit{Provider: "beta", Key: "y"}, Since: t0.Add(time.Hour)}},
		"t-c": {{Kind: decision.LastStep, Unit: unit.Unit{Provider: "gamma", Key: "z"}, Since: t0.Add(2 * time.Hour)}},
	}
	s := home.Service{Lists: &fakeLists{sets: map[task.View][]task.Task{"decide-last-step": {a, b, c}}}, Decisions: d, Words: queueWords()}
	q, _ := s.Queue(context.Background(), decision.LastStep, nil)
	got := []home.Action{q.Items[0].Primary, q.Items[1].Primary, q.Items[2].Primary}
	want := []home.Action{{Kind: "open"}, {Kind: "action", ID: "close"}, {Kind: "open"}}
	if !slices.Equal(got, want) {
		t.Fatalf("primaries %+v, want %+v", got, want)
	}
	if it := q.Items[0]; it.Situation != "can merge" || it.Found.Unit.Provider != "alpha" {
		t.Fatalf("item %+v", it)
	}
	if q.Title != "Last steps" {
		t.Fatalf("title %q: items of several plugins keep the kind's", q.Title)
	}
}

func TestQueueOfOnePluginUsesItsTitle(t *testing.T) {
	d := fakeDecisions{"t-a": {{Kind: decision.LastStep, Unit: unit.Unit{Provider: "alpha", Key: "x"}, Situation: "merge", Since: t0}}}
	s := home.Service{Lists: &fakeLists{sets: map[task.View][]task.Task{"decide-last-step": {{ID: "t-a"}}}}, Decisions: d, Words: queueWords()}
	q, _ := s.Queue(context.Background(), decision.LastStep, nil)
	if q.Title != "Ready to land" || q.Empty != "Nothing waits." {
		t.Fatalf("queue %+v", q)
	}
}

func TestQueueKeepNarrows(t *testing.T) {
	d := fakeDecisions{"t-a": {{Kind: decision.Answer, Since: t0}}, "t-b": {{Kind: decision.Answer, Since: t0}}}
	s := home.Service{Lists: &fakeLists{sets: map[task.View][]task.Task{"decide-answer": {{ID: "t-a"}, {ID: "t-b"}}}}, Decisions: d, Words: queueWords()}
	q, _ := s.Queue(context.Background(), decision.Answer, func(_ context.Context, e task.Task) (bool, error) { return e.ID == "t-b", nil })
	if len(q.Items) != 1 || q.Items[0].Task.ID != "t-b" {
		t.Fatalf("items %+v", q.Items)
	}
}

func TestHomeCounterCountsTheDecisionsItsQueueLists(t *testing.T) {
	two, one, none := task.Task{ID: "t-two"}, task.Task{ID: "t-one"}, task.Task{ID: "t-none"}
	draft := func(id int64, p unit.ProviderID) decision.Found {
		return decision.Found{Kind: decision.SendDraft, Gate: &gate.Gate{ID: id, Intents: []string{"send"}}, Unit: unit.Unit{Provider: p, Key: "k"}, Since: t0}
	}
	d := fakeDecisions{"t-two": {draft(1, "alpha"), draft(2, "alpha")}, "t-one": {draft(3, "beta"), draft(4, "beta")}}
	w := queueWords()
	w.profile.Counters = []home.Counter{{ID: "send", Kind: decision.SendDraft}}
	w.kinds[decision.SendDraft] = home.KindWords{View: "decide-send-draft", Noun: plural("draft", "drafts"), Clause: home.Forms{"other": "{counter} wait"}}
	w.plugins["alpha"] = home.PluginWords{Noun: plural("alpha draft", "alpha drafts")}
	w.plugins["beta"] = home.PluginWords{Noun: plural("beta draft", "beta drafts")}
	s := home.Service{Lists: &fakeLists{sets: map[task.View][]task.Task{"decide-send-draft": {two, one, none}}}, Decisions: d, Words: w}
	h, err := s.Home(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.Queue(context.Background(), decision.SendDraft, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := h.Counts[0]; c.N != 4 || c.N != len(q.Items) {
		t.Fatalf("counter %d, queue %d: the counter counts the decisions its queue lists", c.N, len(q.Items))
	}
	if c := h.Counts[0]; c.Label != "2 alpha drafts and 2 beta drafts" {
		t.Fatalf("label %q: each plugin's share counts its decisions", c.Label)
	}
}
