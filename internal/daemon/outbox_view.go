package daemon

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
)

const draftPrefix = "draft:"

// gatesView serves the profile's views over gates, never stored: each gate in the view stands as a task, a
// draft gate as its draft's own task, any other as the task it is on.
type gatesView struct{ s *Server }

// gateIn is a gate a view over gates ranges over, and the draft of a draft gate.
type gateIn struct {
	gate  gate.Gate
	draft *draft.Draft
}

func (v gatesView) Tasks(ctx context.Context, q task.Query) ([]task.Task, error) {
	gs, err := v.s.gatesIn(ctx, string(q.View))
	if err != nil {
		return nil, err
	}
	var out []task.Task
	seen := map[task.ID]bool{}
	for _, g := range gs {
		e, ok, err := v.s.gateTask(ctx, g)
		if err != nil {
			return nil, err
		}
		if ok && !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	return out, nil
}

// gateTask is the task gate g stands as in a view: a draft's own task, which takes the properties of the task
// the gate is on that it does not set itself, so the area and contexts narrow it as they do that task; else
// the task the gate is on; false for a gate on no task.
func (s *Server) gateTask(ctx context.Context, g gateIn) (task.Task, bool, error) {
	var on task.Task
	if g.gate.Task != "" {
		var err error
		on, err = s.deps.Tasks.Task(ctx, g.gate.Task)
		if err != nil && !errors.Is(err, coretasks.ErrNoTask) {
			return task.Task{}, false, err
		}
	}
	if g.draft == nil {
		return on, on.ID != "", nil
	}
	e, err := draftTask(*g.draft, g.gate, s.unitLabel(g.draft.Unit), s.loaded().Tasks)
	if err != nil {
		s.logf("outbox: draft %d: %v", g.draft.ID, err)
		return task.Task{}, false, nil
	}
	for name, v := range on.Props {
		if _, set := e.Props[name]; !set {
			e.SetProp(name, v, on.Written[name].By, on.Written[name].At)
		}
	}
	return e, true, nil
}

// gatesIn are the gates view lists, oldest first: of the open gates and the approved draft gates whose
// delivery has not settled, those its when holds over, but those on a disabled plugin's unit when it hides
// them.
func (s *Server) gatesIn(ctx context.Context, view string) ([]gateIn, error) {
	def := s.loaded().Views
	spec, ok := def.View(view)
	if !ok || spec.Over != task.OverGates || s.deps.GateReader == nil {
		return nil, nil
	}
	cands, err := s.gateCandidates(ctx)
	if err != nil {
		return nil, err
	}
	var out []gateIn
	for _, g := range cands {
		if spec.HideDisabled && !g.gate.Unit.IsZero() && !s.pluginEnabled(g.gate.Unit.Provider) {
			continue
		}
		delivery := ""
		if g.draft != nil {
			delivery = string(g.draft.State)
		}
		in, err := catalog.GateViewAt(def, view, g.gate, intentload.GateFacts(g.gate, delivery), s.deps.Now())
		if err != nil {
			return nil, err
		}
		if in {
			out = append(out, g)
		}
	}
	return out, nil
}

// gateCandidates are the open gates and the approved draft gates whose draft is queued, sending or failed, each
// draft gate with its draft, oldest first.
func (s *Server) gateCandidates(ctx context.Context) ([]gateIn, error) {
	open, err := s.deps.GateReader.Where(ctx, gates.Query{})
	if err != nil {
		return nil, err
	}
	var out []gateIn
	seen := map[int64]bool{}
	for _, g := range open {
		seen[g.ID] = true
		out = append(out, gateIn{gate: g})
	}
	if o := s.deps.Outbox; o != nil {
		ds, err := o.List(ctx, outbox.Filter{States: []draft.State{draft.Held, draft.Queued, draft.Sending, draft.Failed}})
		if err != nil {
			return nil, err
		}
		byID := map[draft.ID]draft.Draft{}
		for _, d := range ds {
			byID[d.ID] = d
		}
		for i := range out {
			if out[i].gate.Kind != gate.KindDraft {
				continue
			}
			if id, ok := draftIDOf(task.ID(draftPrefix + out[i].gate.Subject)); ok {
				if d, ok := byID[id]; ok {
					out[i].draft = &d
				}
			}
		}
		for _, d := range ds {
			if d.State == draft.Held {
				continue
			}
			g, ok, err := o.GateOf(ctx, d)
			if err != nil {
				return nil, err
			}
			if ok && !seen[g.ID] && g.Status == gate.Approved {
				seen[g.ID] = true
				out = append(out, gateIn{gate: g, draft: &d})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b gateIn) int { return cmp.Compare(a.gate.ID, b.gate.ID) })
	return out, nil
}

func (v gatesView) Task(ctx context.Context, id task.ID) (task.Task, bool, error) {
	n, ok := draftIDOf(id)
	if !ok {
		return task.Task{}, false, nil
	}
	d, err := v.s.deps.Outbox.Store.Get(ctx, n)
	if errors.Is(err, outbox.ErrNotFound) {
		return task.Task{}, false, nil
	}
	if err != nil {
		return task.Task{}, false, err
	}
	g, _, err := v.s.deps.Outbox.GateOf(ctx, d)
	if err != nil {
		return task.Task{}, false, err
	}
	e, err := draftTask(d, g, v.s.unitLabel(d.Unit), v.s.loaded().Tasks)
	return e, err == nil, err
}

func draftIDOf(id task.ID) (draft.ID, bool) {
	raw, ok := strings.CutPrefix(string(id), draftPrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	return draft.ID(n), err == nil && n > 0
}

// draftCategories are the task categories that tell where a draft stands.
var draftCategories = map[draft.State]task.Category{
	draft.Held: task.CategoryInbox, draft.Failed: task.CategoryInbox,
	draft.Queued: task.CategoryReady, draft.Sending: task.CategoryReady,
	draft.Sent: task.CategoryDone, draft.Dropped: task.CategoryDropped,
}

// draftTask is d's virtual task; g is its gate, whose window is the task's due date.
func draftTask(d draft.Draft, g gate.Gate, where string, st task.Profile) (task.Task, error) {
	e := task.Task{ID: task.ID(draftPrefix + strconv.FormatInt(int64(d.ID), 10)), Title: draftTitle(d, where), Units: []unit.Unit{d.Unit}, Origin: task.OriginDraft,
		Created: d.CreatedAt, Updated: d.UpdatedAt}
	if err := e.MoveTo(draftCategories[d.State], st); err != nil {
		return task.Task{}, err
	}
	if g.Status == gate.Open {
		e.SetRole(st, task.RoleDeadline, task.Value{Time: g.Deadline}, task.WriterPlugin, d.UpdatedAt)
	}
	for _, it := range d.Items {
		e.Items = append(e.Items, task.ItemRef{Unit: d.Unit, Item: it})
	}
	return e, nil
}

func draftTitle(d draft.Draft, where string) string {
	if d.Reaction != "" {
		return fmt.Sprintf("%s :%s: on %s", d.Purpose, d.Reaction, where)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(d.Text), "\n")
	return fmt.Sprintf("%s on %s: %s", d.Purpose, where, first)
}

// withDraft sets a draft task's gate on v, its subject the draft, and its verbs: approve and dismiss while its gate
// is open, dismiss also while its delivery may still go, edit and retry as the draft's actions stand; the others
// are refused.
func (s *Server) withDraft(ctx context.Context, e task.Task, v *api.TaskView) error {
	if e.Origin != task.OriginDraft || s.deps.Outbox == nil {
		return nil
	}
	n, ok := draftIDOf(e.ID)
	if !ok {
		return nil
	}
	d, err := s.deps.Outbox.Store.Get(ctx, n)
	if err != nil {
		return err
	}
	g, _, err := s.deps.Outbox.GateOf(ctx, d)
	if err != nil {
		return err
	}
	dv := s.draftView(ctx, d, g)
	if g.ID != 0 {
		v.Gates = []api.GateView{s.draftGateView(g, dv)}
	}
	actions := map[string]api.ActionState{}
	for _, st := range dv.Actions {
		actions[strings.TrimPrefix(st.ID, "draft.")] = st
	}
	facts := s.draftFacts(d)
	open := g.Status == gate.Open
	for i, vs := range v.Verbs {
		action, ok := draftHandlers[s.loaded().Tasks.Handler(vs.Verb)]
		switch {
		case !ok:
			v.Verbs[i] = api.VerbState{Verb: vs.Verb, Reason: "a draft takes approve, edit, dismiss and retry"}
		case action == "approve":
			v.Verbs[i] = api.VerbState{Verb: vs.Verb, Enabled: open && !facts.Disabled && !facts.NoPublisher, Reason: actions["edit"].Reason}
		case action == "discard":
			v.Verbs[i] = api.VerbState{Verb: vs.Verb, Enabled: open || d.State == draft.Queued || d.State == draft.Failed, Reason: actions["edit"].Reason}
		default:
			st := actions[action]
			v.Verbs[i] = api.VerbState{Verb: vs.Verb, Enabled: st.Enabled, Reason: st.Reason}
		}
		if v.Verbs[i].Enabled {
			v.Verbs[i].Reason = ""
		}
	}
	return nil
}
