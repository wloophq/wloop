// Package decision is the closed vocabulary of the decisions a task waits on the user for.
package decision

import (
	"cmp"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Kind is a kind of decision.
type Kind string

// The decision kinds; a new kind may be added, none renamed.
const (
	Answer      Kind = "answer"
	ApprovePlan Kind = "approve-plan"
	SendDraft   Kind = "send-draft"
	ReviewStep  Kind = "review-step"
	LastStep    Kind = "last-step"
	PickSomeone Kind = "pick-someone"
	Act         Kind = "act"
)

// Kinds are every kind, in the order the home and the queues list them.
var Kinds = []Kind{Answer, ApprovePlan, SendDraft, ReviewStep, LastStep, PickSomeone, Act}

// Valid says whether k is one of Kinds.
func Valid(k Kind) bool { return slices.Contains(Kinds, k) }

// Map is how the profile maps gate kinds, and each plugin's situations and link signals, to decision kinds.
type Map struct {
	Gates      map[gate.Kind]Kind
	Situations map[unit.ProviderID]map[string]Kind
	Signals    map[unit.ProviderID]map[string]Kind
}

// Source is one unit a task links: its situation, its link signals and when it last moved.
type Source struct {
	Unit      unit.Unit
	Situation string
	Signals   map[string]bool
	Since     time.Time
}

// Found is one decision a task waits on: an open gate (Gate its id) or a linked unit (Unit, with the
// Situation that made it, "" when a signal did).
type Found struct {
	Kind      Kind
	Gate      int64
	Unit      unit.Unit
	Situation string
	Since     time.Time
}

// Of are the decisions of a task's gates and linked units: one per open mapped gate, and per unit one per kind
// its situation or its signals map to, ordered by Since, then kind order.
func (m Map) Of(gates []gate.Gate, links []Source) []Found {
	var out []Found
	for _, g := range gates {
		if k, ok := m.Gates[g.Kind]; ok && g.Status == gate.Open {
			out = append(out, Found{Kind: k, Gate: g.ID, Since: g.OpenedAt})
		}
	}
	for _, l := range links {
		var kinds []Kind
		if k, ok := m.Situations[l.Unit.Provider][l.Situation]; ok {
			out = append(out, Found{Kind: k, Unit: l.Unit, Situation: l.Situation, Since: l.Since})
			kinds = append(kinds, k)
		}
		names := make([]string, 0, len(l.Signals))
		for name, on := range l.Signals {
			if on {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		for _, name := range names {
			if k, ok := m.Signals[l.Unit.Provider][name]; ok && !slices.Contains(kinds, k) {
				out = append(out, Found{Kind: k, Unit: l.Unit, Since: l.Since})
				kinds = append(kinds, k)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Found) int {
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		return cmp.Compare(slices.Index(Kinds, a.Kind), slices.Index(Kinds, b.Kind))
	})
	return out
}

// Names are the kinds of found, each once, in found's order.
func Names(found []Found) []string {
	out := []string{}
	for _, f := range found {
		if !slices.Contains(out, string(f.Kind)) {
			out = append(out, string(f.Kind))
		}
	}
	return out
}
