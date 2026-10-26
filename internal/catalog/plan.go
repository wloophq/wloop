package catalog

import (
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// The ids of a plan item's own actions.
const (
	PlanApply   = "plan_apply"
	PlanDismiss = "plan_dismiss"
)

// StartBlocked prefixes the reason of a turn.start item a start block holds.
const StartBlocked = "round blocked: "

// planSpec is a plan item's action and the actions.toml table that words it.
type planSpec struct {
	words string
	spec  api.ActionSpec
}

var planSpecs = []planSpec{
	{PlanApply, api.ActionSpec{ID: PlanApply, Group: "plan", Key: "Enter", Verb: "approve", Surfaces: []string{SurfaceTUI, SurfaceDetail, SurfaceCLI}, Source: SourceBuiltin}},
	{PlanDismiss, api.ActionSpec{ID: PlanDismiss, Group: "plan", Key: "X", Verb: "dismiss", Surfaces: []string{SurfaceTUI, SurfaceDetail, SurfaceCLI}, Source: SourceBuiltin}},
	{"plan_guide", api.ActionSpec{ID: "guide", Group: "plan", Key: "G", Surfaces: []string{SurfaceTUI, SurfaceDetail},
		Input: &api.InputSpec{Kind: "text", Required: true}, Source: SourceBuiltin}},
	{"trust_run", api.ActionSpec{ID: "trust_run", Group: "plan", Key: "T", Surfaces: blockSurfaces, Source: SourceBuiltin}},
	{"unblock", api.ActionSpec{ID: "unblock", Group: "plan", Key: "D", Surfaces: blockSurfaces, Source: SourceBuiltin}},
}

// PlanSpecs are the actions a plan item may offer, in render order, worded
// by l.
func PlanSpecs(l *intentload.Loaded) []api.ActionSpec {
	out := make([]api.ActionSpec, len(planSpecs))
	for i, p := range planSpecs {
		out[i] = worded(l, p.words, p.spec)
	}
	return out
}

// PlanStates are the actions plan item it offers now, worded by l. A
// turn.start item a start block holds offers the row's trust_run and
// unblock, from row, the actions its pull request's row offers: no plan
// action trusts a folder.
func PlanStates(l *intentload.Loaded, it api.PlanItem, row []api.ActionState) []api.ActionState {
	out := []api.ActionState{}
	turnStart := slices.Contains(it.Steps, builtin.StepTurnStart)
	switch it.Decision {
	case "pending":
		out = append(out, api.ActionState{ID: PlanApply, Enabled: true, Primary: true}, api.ActionState{ID: PlanDismiss, Enabled: true})
		if turnStart {
			out = append(out, texted(l, "plan_guide", api.ActionState{ID: "guide", Enabled: true}, intentload.ActionFactMap(Facts{}.Get)))
		}
	case "blocked":
		out = append(out, api.ActionState{ID: PlanApply, Reason: it.Reason}, api.ActionState{ID: PlanDismiss, Enabled: true})
		if turnStart && strings.HasPrefix(it.Reason, StartBlocked) {
			for _, st := range row {
				if st.ID == "trust_run" || st.ID == "unblock" {
					out = append(out, st)
				}
			}
		}
	case "skipped":
		out = append(out, api.ActionState{ID: PlanApply, Reason: it.Reason})
	}
	return out
}
