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
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const draftPrefix = "draft:"

// outboxView serves the Outbox: one virtual task per draft of an enabled plugin whose gate is open, or whose gate
// approved it and whose delivery failed or a restart has yet to settle, newest first, never stored.
type outboxView struct{ s *Server }

func (v outboxView) Tasks(ctx context.Context, q task.Query) ([]task.Task, error) {
	if len(q.Contexts) > 0 {
		return nil, nil
	}
	ds, err := v.drafts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]task.Task, 0, len(ds))
	for _, p := range ds {
		d := p.Draft
		e, err := draftTask(d, p.Gate, v.s.unitLabel(d.Unit), v.s.loaded().Tasks)
		if err != nil {
			v.s.logf("outbox: draft %d: %v", d.ID, err)
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (v outboxView) drafts(ctx context.Context) ([]outbox.Pending, error) {
	ps, err := v.s.deps.Outbox.Outbox(ctx)
	return slices.DeleteFunc(ps, func(p outbox.Pending) bool { return !v.s.pluginEnabled(p.Draft.Unit.Provider) }), err
}

func (v outboxView) Task(ctx context.Context, id task.ID) (task.Task, bool, error) {
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

func (v outboxView) Count(ctx context.Context, _ string) (int, error) {
	ds, err := v.drafts(ctx)
	return len(ds), err
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
func draftTask(d draft.Draft, g gate.Gate, where string, st task.States) (task.Task, error) {
	e := task.Task{ID: task.ID(draftPrefix + strconv.FormatInt(int64(d.ID), 10)), Title: draftTitle(d, where), Units: []unit.Unit{d.Unit}, Origin: task.OriginDraft,
		Created: d.CreatedAt, Updated: d.UpdatedAt}
	if err := e.MoveTo(draftCategories[d.State], st); err != nil {
		return task.Task{}, err
	}
	if g.Status == gate.Open {
		e.SetProp(task.PropDue, task.Value{Time: g.Deadline}, task.WriterPlugin, d.UpdatedAt)
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
		action, ok := draftHandlers[cmp.Or(s.loaded().Tasks.Handler(vs.Verb), vs.Verb)]
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
