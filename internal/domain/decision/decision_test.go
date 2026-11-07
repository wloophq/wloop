package decision

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestKindsAreTheMasterPlanVocabulary(t *testing.T) {
	want := []Kind{"answer", "approve-plan", "send-draft", "review-step", "last-step", "pick-someone", "act"}
	if !slices.Equal(Kinds, want) {
		t.Fatalf("Kinds = %v, want %v", Kinds, want)
	}
	for _, k := range want {
		if !Valid(k) {
			t.Errorf("Valid(%q) = false", k)
		}
	}
	if Valid("merge") {
		t.Error(`Valid("merge") = true`)
	}
}

var t0 = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

func alphaMap() Map {
	return Map{
		Gates:      map[gate.Kind]Kind{gate.KindQuestion: Answer, gate.KindPlan: ApprovePlan},
		Situations: map[unit.ProviderID]map[string]Kind{"alpha": {"merge": LastStep}},
		Signals:    map[unit.ProviderID]map[string]Kind{"alpha": {"ready": LastStep}},
	}
}

func TestOfMapsGatesSituationsAndSignals(t *testing.T) {
	u := unit.Unit{Provider: "alpha", Key: "acme/api#8"}
	question := gate.Gate{ID: 7, Kind: gate.KindQuestion, Status: gate.Open, OpenedAt: t0, Intents: []string{"answer"}}
	got := alphaMap().Of(
		[]gate.Gate{question},
		[]Source{{Unit: u, Situation: "merge", Signals: map[string]bool{"ready": true}, Since: t0.Add(time.Hour)}},
	)
	want := []Found{
		{Kind: Answer, Gate: &question, Since: t0},
		{Kind: LastStep, Unit: u, Situation: "merge", Since: t0.Add(time.Hour)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Of = %+v, want %+v", got, want)
	}
}

func TestOfSignalAloneFindsTheKind(t *testing.T) {
	u := unit.Unit{Provider: "alpha", Key: "acme/api#9"}
	got := alphaMap().Of(nil, []Source{{Unit: u, Situation: "review", Signals: map[string]bool{"ready": true, "stalled": true}, Since: t0}})
	want := []Found{{Kind: LastStep, Unit: u, Since: t0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Of = %+v, want %+v", got, want)
	}
}

func TestOfIgnoresUnmappedAndClosedGates(t *testing.T) {
	got := alphaMap().Of(
		[]gate.Gate{{ID: 1, Kind: gate.KindPlan, Status: gate.Approved, OpenedAt: t0}, {ID: 2, Kind: gate.KindTrust, Status: gate.Open}},
		[]Source{{Unit: unit.Unit{Provider: "alpha", Key: "a#1"}, Situation: "review", Signals: map[string]bool{"ready": false}},
			{Unit: unit.Unit{Provider: "beta", Key: "b"}, Situation: "merge"}},
	)
	if len(got) != 0 {
		t.Fatalf("Of = %+v, want nothing", got)
	}
}

func TestOfOneFoundPerGateOrderedBySinceThenKind(t *testing.T) {
	got := alphaMap().Of([]gate.Gate{
		{ID: 3, Kind: gate.KindQuestion, Status: gate.Open, OpenedAt: t0.Add(time.Minute)},
		{ID: 4, Kind: gate.KindQuestion, Status: gate.Open, OpenedAt: t0},
		{ID: 5, Kind: gate.KindPlan, Status: gate.Open, OpenedAt: t0},
	}, nil)
	ids := []int64{}
	for _, f := range got {
		ids = append(ids, f.Gate.ID)
	}
	if !slices.Equal(ids, []int64{4, 5, 3}) {
		t.Fatalf("gates in order %v, want [4 5 3]", ids)
	}
}

func TestKindsOfFoundsKeepsEachOnceInOrder(t *testing.T) {
	got := Names([]Found{{Kind: LastStep}, {Kind: Answer}, {Kind: LastStep}})
	if !slices.Equal(got, []string{"last-step", "answer"}) {
		t.Fatalf("Names = %v", got)
	}
}
