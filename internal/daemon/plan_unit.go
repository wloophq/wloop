package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/turn"
)

// Explainer is an optional capability of Plan: what every workflow of a
// unit's provider decides for that unit now, those that give no item
// included.
type Explainer interface {
	Explain(ctx context.Context, u unit.Unit) (Explained, error)
}

// Explained is a unit's decisions: its items, and a line per workflow that
// gives none.
type Explained struct {
	Unit      unit.Unit
	Items     []workflow.Item
	Workflows []api.UnitWorkflow
	Errors    []string
}

// Explain evaluates every trigger's workflows on eng, which writes nothing,
// and matches the `when` of each workflow that gave u no item against u's
// facts, naming the facts it read and the reasons the providers give.
func Explain(ctx context.Context, eng *automation.Engine, u unit.Unit) (Explained, error) {
	listed, err := eng.Units.List(ctx, u.Provider)
	if err != nil {
		return Explained{}, err
	}
	i := slices.IndexFunc(listed, func(o unit.Unit) bool { return eng.Codecs.Same(o, u) })
	if i < 0 {
		return Explained{Unit: u, Errors: []string{fmt.Sprintf("%s is not among the %s units the workflows read: not tracked, or gone", u.Label(), u.Provider)}}, nil
	}
	u = listed[i]
	plan, err := eng.Evaluate(ctx, "")
	if err != nil {
		return Explained{}, err
	}
	out := Explained{Unit: u}
	gave := map[string]bool{}
	for _, it := range plan.Items {
		if it.Unit == u {
			out.Items = append(out.Items, it)
			gave[it.Workflow] = true
		}
	}
	prefix := u.String() + ": "
	for _, e := range plan.Errors {
		if strings.Contains(e, prefix) {
			out.Errors = append(out.Errors, e)
		}
	}
	facts, factErr := eng.Units.Facts(ctx, u)
	var marks map[string]string
	var marksErr error
	marksRead := false
	var config workflow.Facts
	if eng.Config != nil {
		config = eng.Config()
	}
	ws := slices.Clone(eng.Workflows())
	slices.SortStableFunc(ws, func(a, b workflow.Workflow) int { return strings.Compare(a.Name, b.Name) })
	for _, w := range ws {
		if w.Provider != u.Provider || gave[w.Name] {
			continue
		}
		line := api.UnitWorkflow{Workflow: w.Name, Trigger: string(w.Trigger.Kind)}
		readsMarks := workflow.ReadsMarks(append(w.Approval.Predicates(), w.When)...)
		if readsMarks && !marksRead && eng.Marks != nil {
			marks, marksErr = eng.Marks.Marks(ctx, u)
			marksRead = true
		}
		vars := workflow.Vars{Facts: facts, Trigger: workflow.Facts{"trigger.kind": string(w.Trigger.Kind)}, Config: config, Marks: marks, Now: eng.Clock.Now()}
		switch {
		case w.Trigger.Kind == workflow.TriggerEvent:
			line.Outcome, line.Reason = "waits", "decides when a "+strings.Join(w.Trigger.On, " or ")+" event arrives"
		case factErr != nil:
			line.Outcome, line.Reason = "error", factErr.Error()
		case readsMarks && marksErr != nil:
			line.Outcome, line.Reason = "error", "reading its marks: "+marksErr.Error()
		default:
			ok, err := true, error(nil)
			if w.When != nil {
				ok, err = w.When.Match(vars)
			}
			line.Facts = readFacts(w.When, vars)
			switch {
			case err != nil:
				line.Outcome, line.Reason = "error", err.Error()
			case !ok:
				line.Outcome = "not_matched"
			case w.Trigger.Kind == workflow.TriggerSchedule:
				line.Outcome, line.Reason = "matched", "decides when its schedule fires"
			default:
				line.Outcome, line.Reason = "matched", "no item: this key already ran or was dismissed"
			}
		}
		out.Workflows = append(out.Workflows, line)
	}
	return out, nil
}

// readFacts are the facts pred reads with their values, each followed by
// the reason its provider gives beside it (pr.first.reason beside
// pr.first.due), sorted by name.
func readFacts(pred workflow.Predicate, v workflow.Vars) []api.Fact {
	if pred == nil {
		return nil
	}
	names := slices.Clone(pred.Reads())
	for _, n := range pred.Reads() {
		if i := strings.LastIndex(n, "."); i > 0 {
			if r := n[:i] + ".reason"; r != n {
				if _, ok := v.Facts[r]; ok {
					names = append(names, r)
				}
			}
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	out := make([]api.Fact, 0, len(names))
	for _, n := range names {
		val, ok := any(nil), false
		for _, f := range []workflow.Facts{v.Facts, v.Trigger, v.Config} {
			if val, ok = f[n]; ok {
				break
			}
		}
		text := "(absent)"
		if ok {
			text = turn.FactText(val)
		}
		out = append(out, api.Fact{Name: n, Value: text})
	}
	return out
}

// handlePlanUnit answers what every workflow decides for the unit named
// by raw, provider:key or a pull request's owner/repo#n.
func (s *Server) handlePlanUnit(w http.ResponseWriter, r *http.Request, raw string) {
	ex, ok := s.deps.Plan.(Explainer)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "the daemon's plan explains no unit")
		return
	}
	u, err := planUnit(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	got, err := ex.Explain(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	known := s.presentations(append(itemUnits(got.Items), got.Unit))
	res := api.PlanResponse{Items: s.wirePlanItems(r.Context(), got.Items, known), Errors: append([]string{}, got.Errors...), Unit: got.Unit.String(),
		Label: known[got.Unit].Label, Title: known[got.Unit].Title, Workflows: got.Workflows}
	writeJSON(w, res)
}

func planUnit(raw string) (unit.Unit, error) {
	provider, key, ok := strings.Cut(raw, ":")
	if !ok || provider == "" || key == "" {
		return unit.Unit{}, fmt.Errorf("unit %q is not provider:key", raw)
	}
	return unit.Unit{Provider: unit.ProviderID(provider), Key: key}, nil
}
