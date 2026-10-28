package catalog

import (
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/gate"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// GateIntent is a row action that resolves an intent of the row's open
// gate: the gate's kind and the intent, as gates.toml declares them, and
// whether it is the first of its role, which a row offers beyond its gate.
type GateIntent struct {
	Kind   gate.Kind
	Intent intentload.GateIntent
	Role   builtin.GateRole
	First  bool
}

// RowGateIntents are the intents of the first gate of each kind a row
// offers, in gates.toml's order, the plan gate's first.
func RowGateIntents(l *intentload.Loaded) []GateIntent {
	if l == nil {
		return nil
	}
	var out []GateIntent
	for _, k := range builtin.RowGateKinds {
		def, ok := l.Gates.FirstOf(k)
		if !ok {
			continue
		}
		seen := map[builtin.GateRole]bool{}
		for _, in := range def.Intents {
			role := builtin.GateRoleOf(in.Status, in.Answer)
			out = append(out, GateIntent{Kind: k, Intent: in, Role: role, First: role != "" && !seen[role]})
			seen[role] = true
		}
	}
	return out
}

// RowGateIntent is the row gate intent action id names; false for an id
// no row gate declares.
func RowGateIntent(l *intentload.Loaded, id string) (GateIntent, bool) {
	all := RowGateIntents(l)
	i := slices.IndexFunc(all, func(g GateIntent) bool { return g.Intent.Name == id })
	if i < 0 {
		return GateIntent{}, false
	}
	return all[i], true
}

// RunsHeld says whether action is the plan gate's first approving intent,
// which also runs a round the daily cap held.
func RunsHeld(l *intentload.Loaded, action string) bool {
	g, ok := RowGateIntent(l, action)
	return ok && g.Kind == gate.KindPlan && g.Role == builtin.GateApproves && g.First
}

func gateKey(k gate.Kind, role builtin.GateRole) string {
	i := slices.IndexFunc(builtin.GateKeys, func(g builtin.GateKey) bool { return g.Kind == k && g.Role == role })
	if i < 0 {
		return ""
	}
	return builtin.GateKeys[i].Key
}

// gateActions are the row actions of the row gates' intents, worded by
// gates.toml: a pending round's plan gate offers its intents, its first
// approving intent also runs a round the cap held and its first taking
// text keeps guidance for the next round; a start block's trust gate
// offers its intents, its first approving one only for a folder to trust.
func gateActions(l *intentload.Loaded) []builtinAction {
	var out []builtinAction
	for _, g := range RowGateIntents(l) {
		spec := api.ActionSpec{ID: g.Intent.Name, Label: g.Intent.Label, Summary: g.Intent.Summary, Group: builtin.GroupStart, Surfaces: everywhere()}
		if g.First {
			spec.Key = gateKey(g.Kind, g.Role)
		}
		id := spec.ID
		var offer func(f Facts) (api.ActionState, bool)
		switch {
		case g.Kind == gate.KindQuestion:
			spec.Group, spec.Surfaces = "turn", []string{SurfaceDetail, SurfaceTUI, SurfaceNotification}
			switch {
			case g.Role == builtin.GateTakesText:
				spec.Input = &api.InputSpec{Kind: "answer", Required: true, Label: g.Intent.Label}
				if g.First {
					spec.Verb = "answer"
				}
			case g.Role == builtin.GateRefuses && g.First:
				spec.Verb = "dismiss"
			}
			offer = func(f Facts) (api.ActionState, bool) {
				if !f.Question {
					return absent()
				}
				return on(id)
			}
		case g.Kind == gate.KindTrust && g.First && g.Role == builtin.GateApproves:
			spec.Surfaces = blockSurfaces
			offer = func(f Facts) (api.ActionState, bool) {
				if !f.StartBlocked || f.TrustFolder == "" || len(f.TrustHomes) == 0 || !f.Open {
					return absent()
				}
				return api.ActionState{ID: id, Enabled: true, Primary: true}, true
			}
		case g.Kind == gate.KindTrust:
			spec.Surfaces = blockSurfaces
			offer = func(f Facts) (api.ActionState, bool) {
				if !f.StartBlocked {
					return absent()
				}
				return on(id)
			}
		case g.First && g.Role == builtin.GateApproves:
			spec.Verb, spec.Intake = "approve", true
			offer = func(f Facts) (api.ActionState, bool) {
				if p := f.Row.Pending; p != nil {
					return api.ActionState{ID: id, Enabled: true, Primary: true, Basis: BasisPending + p.Since.UTC().Format(time.RFC3339Nano)}, true
				}
				if f.HeldByCap {
					return api.ActionState{ID: id, Enabled: true, Primary: true, Basis: BasisHeld + f.Row.Summary.Reason, Intent: requestIntent(f, id)}, true
				}
				return absent()
			}
		case g.Role == builtin.GateTakesText:
			spec.Surfaces = []string{SurfaceDetail, SurfaceTUI, SurfaceNotification}
			spec.Input = &api.InputSpec{Kind: "text", Required: true, Label: g.Intent.Label}
			first := g.First
			offer = func(f Facts) (api.ActionState, bool) {
				switch {
				case f.Row.Pending != nil:
					return on(id)
				case first && f.Tracked && f.Open && f.GateOn:
					return on(id)
				}
				return absent()
			}
		default:
			if g.First && g.Role == builtin.GateRefuses {
				spec.Verb = "dismiss"
			}
			offer = func(f Facts) (api.ActionState, bool) {
				if f.Row.Pending == nil {
					return absent()
				}
				return on(id)
			}
		}
		out = append(out, builtinAction{spec, offer})
	}
	return out
}
