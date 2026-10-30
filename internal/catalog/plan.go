package catalog

import (
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/gate"
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
}

// PlanSpecs are the actions a plan item may offer, in render order, worded
// by l, then the intents of the trust gate a start block holds it on.
func PlanSpecs(l *intentload.Loaded) []api.ActionSpec {
	out := make([]api.ActionSpec, len(planSpecs))
	for i, p := range planSpecs {
		out[i] = worded(l, p.words, p.spec)
	}
	for _, b := range gateActions(l) {
		if g, _ := RowGateIntent(l, b.spec.ID); g.Kind == gate.KindTrust {
			b.spec.Group, b.spec.Source = "plan", SourceBuiltin
			out = append(out, b.spec)
		}
	}
	return out
}

// PlanStates are the actions plan item it offers now, worded by l. A
// turn.start item a start block holds offers the intents of its trust gate
// that row, the actions its pull request's row, offers: no plan action
// trusts a folder.
func PlanStates(l *intentload.Loaded, it api.PlanItem, row []api.ActionState) []api.ActionState {
	out := []api.ActionState{}
	turnStart := slices.Contains(it.Steps, builtin.StepTurnStart)
	switch it.Decision {
	case "pending", "deferred":
		out = append(out, api.ActionState{ID: PlanApply, Enabled: true, Primary: true}, api.ActionState{ID: PlanDismiss, Enabled: true})
		if turnStart {
			out = append(out, texted(l, "plan_guide", api.ActionState{ID: "guide", Enabled: true}, intentload.ActionFactMap(Facts{}.Get)))
		}
	case "blocked":
		out = append(out, api.ActionState{ID: PlanApply, Reason: it.Reason}, api.ActionState{ID: PlanDismiss, Enabled: true})
		if turnStart && strings.HasPrefix(it.Reason, StartBlocked) {
			for _, st := range row {
				if g, ok := RowGateIntent(l, st.ID); ok && g.Kind == gate.KindTrust {
					out = append(out, st)
				}
			}
		}
	case "skipped":
		out = append(out, api.ActionState{ID: PlanApply, Reason: it.Reason})
	}
	return out
}
