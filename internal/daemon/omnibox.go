package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/omnibox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// handleOmniboxResolve classifies typed text: a pasted address's unit with its tasks and actions, a known
// verb's operations to confirm, a search, or the agent. It runs nothing.
func (s *Server) handleOmniboxResolve(w http.ResponseWriter, r *http.Request) {
	var in api.OmniboxText
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	g, ok := s.tasks(w)
	if !ok {
		return
	}
	ctx := r.Context()
	refs := &taskRefs{s: s, g: g}
	res, err := omnibox.Resolver{Refs: refs, Vocabulary: s.omniboxVocabulary}.Resolve(ctx, in.Text)
	switch {
	case errors.Is(err, omnibox.ErrNoText):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeTasksError(w, err)
		return
	}
	out := api.OmniboxResolved{Kind: res.Kind, Why: res.Why, Params: res.Params}
	if res.Unit != nil {
		ou, err := s.omniboxUnit(ctx, g, refs, *res.Unit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out.Unit = &ou
	}
	if len(res.Operations) > 0 {
		out.Preview = &api.OmniboxProposal{Operations: wireOperations(res.Operations)}
	}
	writeJSON(w, out)
}

func (s *Server) omniboxVocabulary() omnibox.Vocabulary { return OmniboxVocabulary(s.loaded()) }

// OmniboxVocabulary is l's omnibox verbs, compared case and accents aside: what the omnibox runs, typed or
// proposed by the agent.
func OmniboxVocabulary(l *intentload.Loaded) omnibox.Vocabulary {
	out := omnibox.Vocabulary{Fold: api.Fold}
	for _, v := range l.Omnibox.Verbs {
		out.Verbs = append(out.Verbs, omnibox.Verb{ID: v.ID, Words: slices.Clone(v.Words), Action: v.Action, TaskVerb: v.Verb, Op: v.Op})
	}
	return out
}

// omniboxUnit is u with the open tasks that hold it and the actions its row offers now.
func (s *Server) omniboxUnit(ctx context.Context, g *coretasks.Service, refs *taskRefs, u unit.Unit) (api.OmniboxUnit, error) {
	out := api.OmniboxUnit{Unit: u, Label: s.unitLabel(u), Tasks: []api.TaskView{}, Actions: []api.ActionSpec{}}
	d, ok, err := s.detail(ctx, u)
	if err != nil {
		return api.OmniboxUnit{}, err
	}
	if ok {
		out.Permalink = d.Row.Link
		specs := s.servedSpecs()
		for _, a := range d.Row.Actions {
			if i := slices.IndexFunc(specs, func(sp api.ActionSpec) bool { return sp.ID == a.ID }); a.Enabled && i >= 0 {
				out.Actions = append(out.Actions, specs[i])
			}
		}
	}
	open, err := refs.open(ctx)
	if err != nil {
		return api.OmniboxUnit{}, err
	}
	vs := s.taskViews(ctx, g, false)
	for _, e := range open {
		if task.UnitOf(e.ID) == u || slices.Contains(e.LinkedUnits(), u) {
			v, err := vs.view(ctx, e)
			if err != nil {
				return api.OmniboxUnit{}, err
			}
			out.Tasks = append(out.Tasks, v)
		}
	}
	return out, nil
}

// taskRefs find what a token names among the open tasks and the plugins' addresses.
type taskRefs struct {
	s     *Server
	g     *coretasks.Service
	tasks []task.Task
	read  bool
}

func (t *taskRefs) open(ctx context.Context) ([]task.Task, error) {
	if t.read {
		return t.tasks, nil
	}
	n, err := t.g.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	t.tasks, t.read = n.Open(), true
	return t.tasks, nil
}

// claim is the unit a plugin claims address is; false when none does.
func (t *taskRefs) claim(ctx context.Context, address string) (unit.Unit, bool, error) {
	if t.s.deps.Resolve == nil {
		return unit.Unit{}, false, nil
	}
	return t.s.deps.Resolve(ctx, address)
}

// Find is the unit an address or a unit's label names, with the open task that holds it, or the open tasks
// whose id starts with token.
func (t *taskRefs) Find(ctx context.Context, token string) ([]omnibox.Ref, error) {
	open, err := t.open(ctx)
	if err != nil {
		return nil, err
	}
	holder := func(u unit.Unit) task.ID {
		if i := slices.IndexFunc(open, func(e task.Task) bool { return slices.Contains(e.LinkedUnits(), u) }); i >= 0 {
			return open[i].ID
		}
		return ""
	}
	if u, ok, err := t.claim(ctx, token); err != nil || ok {
		return []omnibox.Ref{{Unit: &u, Task: holder(u)}}, err
	}
	var out []omnibox.Ref
	if strings.HasPrefix(token, task.IDPrefix) {
		for _, e := range open {
			if strings.HasPrefix(string(e.ID), token) {
				ref := omnibox.Ref{Task: e.ID}
				if u, ok := e.FirstUnit(); ok {
					ref.Unit = &u
				}
				out = append(out, ref)
			}
		}
		return out, nil
	}
	want := api.Fold(token)
	var seen []unit.Unit
	for _, e := range open {
		for _, u := range e.LinkedUnits() {
			if slices.Contains(seen, u) {
				continue
			}
			label := api.Fold(t.s.unitLabel(u))
			_, short, _ := strings.Cut(label, "/")
			if label == want || short == want {
				seen = append(seen, u)
				out = append(out, omnibox.Ref{Unit: &u, Task: e.ID})
			}
		}
	}
	return out, nil
}

// wireOperations are ops as the API names them.
func wireOperations(ops []omnibox.AskOp) []api.OmniboxOperation {
	out := make([]api.OmniboxOperation, len(ops))
	for i, op := range ops {
		out[i] = api.OmniboxOperation(op)
	}
	return out
}
