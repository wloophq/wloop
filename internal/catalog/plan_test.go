package catalog

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func ids(states []api.ActionState) []string {
	var out []string
	for _, st := range states {
		out = append(out, st.ID)
	}
	return out
}

func TestAPendingItemOffersApplyAndDismiss(t *testing.T) {
	got := PlanStates(api.PlanItem{Decision: "pending", Steps: []string{"session.close"}}, nil)
	if !slices.Equal(ids(got), []string{PlanApply, PlanDismiss}) || !got[0].Enabled || !got[1].Enabled {
		t.Fatalf("%+v", got)
	}
	got = PlanStates(api.PlanItem{Decision: "pending", Steps: []string{builtin.StepTurnStart}}, nil)
	if !slices.Equal(ids(got), []string{PlanApply, PlanDismiss, "guide"}) {
		t.Fatalf("%+v", got)
	}
	if got := PlanStates(api.PlanItem{Decision: "run"}, nil); len(got) != 0 {
		t.Fatalf("a run item offers %+v", got)
	}
}

func TestAStartBlockedItemOffersTheRowsTrustActionsOnly(t *testing.T) {
	row := []api.ActionState{{ID: "trust_run", Enabled: true, Primary: true, Note: "Trust /w in ~/.claude, then run the round"}, {ID: "unblock", Enabled: true}, {ID: "mute", Enabled: true}}
	it := api.PlanItem{Decision: "blocked", Reason: "round blocked: trust this folder?", Steps: []string{builtin.StepTurnStart}}
	got := PlanStates(it, row)
	if !slices.Equal(ids(got), []string{PlanApply, PlanDismiss, "trust_run", "unblock"}) || got[0].Enabled || got[0].Reason != it.Reason || got[2] != row[0] {
		t.Fatalf("%+v", got)
	}
	it.Reason = "held by you"
	if got := PlanStates(it, row); !slices.Equal(ids(got), []string{PlanApply, PlanDismiss}) {
		t.Fatalf("a hold offers %+v", got)
	}
}

func TestPlanSpecsCoverEveryPlanState(t *testing.T) {
	specs := map[string]bool{}
	for _, s := range PlanSpecs() {
		specs[s.ID] = true
	}
	row := []api.ActionState{{ID: "trust_run"}, {ID: "unblock"}}
	for _, it := range []api.PlanItem{
		{Decision: "pending", Steps: []string{builtin.StepTurnStart}},
		{Decision: "blocked", Reason: "round blocked: x", Steps: []string{builtin.StepTurnStart}},
		{Decision: "skipped"},
	} {
		for _, st := range PlanStates(it, row) {
			if !specs[st.ID] {
				t.Errorf("%s has no spec", st.ID)
			}
		}
	}
}
