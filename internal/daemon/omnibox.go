package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
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
		out.Preview = &api.OmniboxProposal{Operations: s.asking(ctx, omniboxOperations(res.Operations))}
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

// omniboxUnit is u with the open tasks that hold it and the actions its row offers now: the row the loop
// holds, else the one its plugin derives from its source. A unit whose row cannot be read offers none and says
// why.
func (s *Server) omniboxUnit(ctx context.Context, g *coretasks.Service, refs *taskRefs, u unit.Unit) (api.OmniboxUnit, error) {
	out := api.OmniboxUnit{Unit: u, Label: s.unitLabel(u), Tasks: []api.TaskView{}, Actions: []api.ActionSpec{}}
	row, held, ok, err := s.omniboxRow(ctx, u)
	switch {
	case ctx.Err() != nil:
		return api.OmniboxUnit{}, ctx.Err()
	case err != nil:
		s.logf("omnibox: the row of %s: %v", u.Label(), err)
		out.Why = api.OmniboxUnitUnreadable
	case ok:
		out.Held, out.Permalink = held, row.Link
		if held {
			out.Unit, out.Label = row.Unit, s.unitLabel(row.Unit)
		} else {
			out.Row = &row
		}
		specs := s.servedSpecs()
		for _, a := range row.Actions {
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
		if task.UnitOf(e.ID) == out.Unit || slices.Contains(e.LinkedUnits(), out.Unit) {
			v, err := vs.view(ctx, e)
			if err != nil {
				return api.OmniboxUnit{}, err
			}
			out.Tasks = append(out.Tasks, v)
		}
	}
	if !ok && out.Why == "" && len(out.Tasks) == 0 {
		out.Why = api.OmniboxUnitNoRow
	}
	if out.Why != "" {
		out.Params = map[string]string{"unit": out.Label}
	}
	return out, nil
}

// omniboxRow is u's row: the one the loop holds, else the one its plugin derives from its source; ok is false
// for a unit with neither.
func (s *Server) omniboxRow(ctx context.Context, u unit.Unit) (row api.Row, held, ok bool, err error) {
	rd, held, err := s.readRow(ctx, u)
	if err != nil || held {
		return rd.Row, held, held, err
	}
	row, ok, err = s.pluginRow(ctx, api.UnitRowQuery{Unit: u})
	return row, false, ok, err
}

// asking are ops, each action with what its unit's row asks before it runs and the guidance its spec takes; a
// row that cannot be read asks nothing here, and the run answers for it. An action that takes no guidance
// carries none, and one that does carries it cut at its max, as it will run.
func (s *Server) asking(ctx context.Context, ops []api.OmniboxOperation) []api.OmniboxOperation {
	rows := map[unit.Unit]api.Row{}
	specs := s.servedSpecs()
	for i := range ops {
		op := &ops[i]
		op.Confirm, op.Input = "", nil
		if op.Op != omnibox.OpAction || op.Unit == nil {
			continue
		}
		if j := slices.IndexFunc(specs, func(sp api.ActionSpec) bool { return sp.ID == op.Action }); j >= 0 && takesGuidance(specs[j].Input) {
			in := *specs[j].Input
			op.Input = &in
			if r := []rune(op.Guidance); in.Max > 0 && len(r) > in.Max {
				op.Guidance = string(r[:in.Max])
			}
		} else {
			op.Guidance = ""
		}
		row, read := rows[*op.Unit]
		if !read {
			var err error
			if row, _, _, err = s.omniboxRow(ctx, *op.Unit); err != nil && ctx.Err() == nil {
				s.logf("omnibox: the row of %s: %v", op.Unit.Label(), err)
			}
			rows[*op.Unit] = row
		}
		st, _ := offerOf(row, op.Action)
		op.Confirm = st.Confirm
	}
	return ops
}

// takesGuidance says whether an action asking in takes free text as its guidance.
func takesGuidance(in *api.InputSpec) bool { return in != nil && in.Kind == "text" }

// taskRefs find what a token names among the open tasks, the rows and the plugins' addresses.
type taskRefs struct {
	s       *Server
	g       *coretasks.Service
	tasks   []task.Task
	read    bool
	labels  map[unit.Unit]string
	labeled bool
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

// listed are the labels of the units the rows list.
func (t *taskRefs) listed(ctx context.Context) (map[unit.Unit]string, error) {
	if t.labeled {
		return t.labels, nil
	}
	labels, err := t.s.readModel().Labels(ctx)
	if err != nil {
		return nil, err
	}
	t.labels, t.labeled = labels, true
	return labels, nil
}

func (t *taskRefs) same(a, b unit.Unit) bool {
	if t.s.deps.Units == nil {
		return a == b
	}
	return t.s.deps.Units.Same(a, b)
}

// claim is the unit address is: the one a plugin's resolver claims, else the one a unit codec reads from an
// address or from a key; false when none does.
func (t *taskRefs) claim(ctx context.Context, address string) (unit.Unit, bool, error) {
	if t.s.deps.Resolve != nil {
		if u, ok, err := t.s.deps.Resolve(ctx, address); err != nil || ok {
			return u, ok, err
		}
	}
	if t.s.deps.Units != nil {
		if u, ok := t.s.deps.Units.ParseURL(address); ok {
			return u, true, nil
		}
		if u, ok := t.s.deps.Units.ParseKey(address); ok {
			return u, true, nil
		}
	}
	return unit.Unit{}, false, nil
}

// known is u as the loop names it: the unit an open task links or a row lists that is the same as u, else u.
// Rows that cannot be read name none.
func (t *taskRefs) known(ctx context.Context, open []task.Task, u unit.Unit) unit.Unit {
	for _, e := range open {
		for _, k := range e.LinkedUnits() {
			if t.same(k, u) {
				return k
			}
		}
	}
	labels, err := t.listed(ctx)
	if err != nil {
		return u
	}
	for k := range labels {
		if t.same(k, u) {
			return k
		}
	}
	return u
}

// Find is the unit an address or a key names, as the loop names it, or the units whose label or short label
// token is, among those the open tasks link and those the rows list, each with the open task that holds it; or
// the open tasks whose id starts with token. Labels are not compared while the rows cannot be read: a label the
// tasks alone make unique may be one of two.
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
	u, ok, err := t.claim(ctx, token)
	if err != nil {
		return nil, err
	}
	if ok {
		u = t.known(ctx, open, u)
		return []omnibox.Ref{{Unit: &u, Task: holder(u)}}, nil
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
	labels, err := t.listed(ctx)
	if err != nil {
		return nil, err
	}
	want := api.Fold(token)
	var seen []unit.Unit
	name := func(u unit.Unit, label string, id task.ID) {
		label = api.Fold(label)
		_, short, _ := strings.Cut(label, "/")
		if (label == want || short == want) && !slices.ContainsFunc(seen, func(k unit.Unit) bool { return t.same(k, u) }) {
			seen = append(seen, u)
			out = append(out, omnibox.Ref{Unit: &u, Task: id})
		}
	}
	for _, e := range open {
		for _, u := range e.LinkedUnits() {
			name(u, t.s.unitLabel(u), e.ID)
		}
	}
	for _, u := range slices.SortedFunc(maps.Keys(labels), api.CompareUnits) {
		name(u, labels[u], "")
	}
	return out, nil
}
