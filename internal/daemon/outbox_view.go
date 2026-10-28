package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const draftPrefix = "draft:"

// outboxStates are the drafts the Outbox shows: those waiting on the user, and those a restart has yet to settle.
var outboxStates = []draft.State{draft.Proposed, draft.Failed, draft.Sending}

// outboxView serves the Outbox: one virtual entry per draft of an enabled plugin, newest first, never stored.
type outboxView struct{ s *Server }

func (v outboxView) Entries(ctx context.Context, q task.Query) ([]task.Entry, error) {
	if len(q.Contexts) > 0 {
		return nil, nil
	}
	ds, err := v.drafts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]task.Entry, 0, len(ds))
	for _, d := range ds {
		out = append(out, draftEntry(d, v.s.unitLabel(d.Unit)))
	}
	return out, nil
}

func (v outboxView) drafts(ctx context.Context) ([]draft.Draft, error) {
	ds, err := v.s.deps.Outbox.List(ctx, outbox.Filter{States: outboxStates})
	return slices.DeleteFunc(ds, func(d draft.Draft) bool { return !v.s.pluginEnabled(d.Unit.Provider) }), err
}

func (v outboxView) Entry(ctx context.Context, id task.ID) (task.Entry, bool, error) {
	n, ok := draftIDOf(id)
	if !ok {
		return task.Entry{}, false, nil
	}
	d, err := v.s.deps.Outbox.Store.Get(ctx, n)
	if errors.Is(err, outbox.ErrNotFound) {
		return task.Entry{}, false, nil
	}
	if err != nil {
		return task.Entry{}, false, err
	}
	return draftEntry(d, v.s.unitLabel(d.Unit)), true, nil
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

// draftEntryStates are the entry states that tell where a draft stands.
var draftEntryStates = map[draft.State]task.State{
	draft.Proposed: task.Inbox, draft.Failed: task.Inbox,
	draft.Approved: task.Next, draft.Sending: task.Next,
	draft.Sent: task.Done, draft.Discarded: task.Trash,
}

func draftEntry(d draft.Draft, where string) task.Entry {
	e := task.Entry{ID: task.ID(draftPrefix + strconv.FormatInt(int64(d.ID), 10)), Kind: task.KindDraft, State: draftEntryStates[d.State],
		Title: draftTitle(d, where), Units: []unit.Unit{d.Unit}, Deadline: d.Deadline, Origin: task.OriginDraft,
		Created: d.CreatedAt, Updated: d.UpdatedAt}
	for _, it := range d.Items {
		e.Items = append(e.Items, task.ItemRef{Unit: d.Unit, Item: it})
	}
	return e
}

func draftTitle(d draft.Draft, where string) string {
	if d.Reaction != "" {
		return fmt.Sprintf("%s :%s: on %s", d.Purpose, d.Reaction, where)
	}
	first, _, _ := strings.Cut(strings.TrimSpace(d.Text), "\n")
	return fmt.Sprintf("%s on %s: %s", d.Purpose, where, first)
}

// withDraft sets a draft entry's draft on v, and its verbs from the draft's actions: the others are refused.
func (s *Server) withDraft(ctx context.Context, e task.Entry, v *api.EntryView) error {
	if e.Kind != task.KindDraft || s.deps.Outbox == nil {
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
	dv := s.draftView(ctx, d)
	v.Draft = &dv
	actions := map[string]api.ActionState{}
	for _, st := range dv.Actions {
		actions[strings.TrimPrefix(st.ID, "draft.")] = st
	}
	for i, vs := range v.Verbs {
		action, ok := draftVerbs[task.Verb(vs.Verb)]
		if !ok {
			v.Verbs[i] = api.VerbState{Verb: vs.Verb, Reason: "a draft takes approve, edit, dismiss and retry"}
			continue
		}
		st := actions[action]
		v.Verbs[i] = api.VerbState{Verb: vs.Verb, Enabled: st.Enabled, Reason: st.Reason}
	}
	return nil
}
