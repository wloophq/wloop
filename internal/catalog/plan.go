package catalog

import (
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// The ids of a plan item's own actions.
const (
	PlanApply   = "plan_apply"
	PlanDismiss = "plan_dismiss"
)

// StartBlocked prefixes the reason of a turn.start item a start block holds.
const StartBlocked = "round blocked: "

var planSpecs = []api.ActionSpec{
	{ID: PlanApply, Label: "Apply", Group: "plan", Key: "Enter", Verb: "approve", Surfaces: []string{SurfaceTUI, SurfaceDetail, SurfaceCLI}, Source: SourceBuiltin},
	{ID: PlanDismiss, Label: "Dismiss", Group: "plan", Key: "X", Verb: "dismiss", Surfaces: []string{SurfaceTUI, SurfaceDetail, SurfaceCLI}, Source: SourceBuiltin},
	{ID: "guide", Label: "Guide", Group: "plan", Key: "G", Surfaces: []string{SurfaceTUI, SurfaceDetail},
		Input: &api.InputSpec{Kind: "text", Required: true, Label: "Guidance", Placeholder: "what the round should look at"}, Source: SourceBuiltin},
	{ID: "trust_run", Label: "Trust folder and run", Group: "plan", Key: "T", Surfaces: blockSurfaces, Source: SourceBuiltin},
	{ID: "unblock", Label: "Dismiss block", Group: "plan", Key: "D", Surfaces: blockSurfaces, Source: SourceBuiltin},
}

// PlanSpecs are the actions a plan item may offer, in render order. The
// slice is shared: callers must not change it.
func PlanSpecs() []api.ActionSpec { return planSpecs }

// PlanStates are the actions plan item it offers now. A turn.start item a
// start block holds offers the row's trust_run and unblock, from row, the
// actions its pull request's row offers: no plan action trusts a folder.
func PlanStates(it api.PlanItem, row []api.ActionState) []api.ActionState {
	out := []api.ActionState{}
	turnStart := slices.Contains(it.Steps, builtin.StepTurnStart)
	switch it.Decision {
	case "pending":
		out = append(out, api.ActionState{ID: PlanApply, Enabled: true, Primary: true}, api.ActionState{ID: PlanDismiss, Enabled: true})
		if turnStart {
			out = append(out, api.ActionState{ID: "guide", Enabled: true, Note: "Runs the round now with your guidance"})
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
