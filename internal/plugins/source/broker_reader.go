package source

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/wloophq/wloop/internal/core/port"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type reader struct{ b *Broker }

// Tasks are the host's tasks linked to the plugin's units.
func (r reader) Tasks(ctx context.Context, units []plugin.Unit) ([]src.Task, error) {
	h := r.b.Hosted
	if h == nil || h.Tasks == nil {
		return nil, src.ErrUnsupported
	}
	var out []src.Task
	seen := map[string]bool{}
	var errs []error
	for _, w := range units {
		u, err := r.b.Units.Own(r.b.ID, w)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		ts, err := h.Tasks.TasksOf(ctx, u)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			if seen[string(t.ID)] {
				continue
			}
			seen[string(t.ID)] = true
			props, err := json.Marshal(t.Props)
			if err != nil {
				return nil, err
			}
			out = append(out, src.Task{ID: string(t.ID), Title: t.Title, State: string(t.State), Category: string(t.Category),
				Parent: string(t.Parent), Props: props, Units: Wires(t.Units)})
		}
	}
	return out, errors.Join(errs...)
}

func (reader) Books(context.Context, []plugin.Unit) ([]src.Book, error) {
	return nil, src.ErrUnsupported
}

// RoundState is the host's round bookkeeping of one of the plugin's own
// units, the rounds its open plan gates hold, its overrides, and whether its
// rounds are paused: by its hold or a wider pause.
func (r reader) RoundState(ctx context.Context, w plugin.Unit) (src.RoundState, error) {
	if r.b.Overrides == nil {
		return src.RoundState{}, src.ErrUnsupported
	}
	u, err := r.b.Units.Own(r.b.ID, w)
	if err != nil {
		return src.RoundState{}, err
	}
	var st src.RoundState
	if r.b.Rounds != nil {
		if st, err = r.b.Rounds(ctx, u); err != nil {
			return src.RoundState{}, err
		}
	}
	if r.b.Pending != nil {
		if st.Pending, err = r.b.Pending(ctx, u); err != nil {
			return src.RoundState{}, err
		}
	}
	o, err := r.b.Overrides.Override(ctx, u)
	if err != nil {
		return src.RoundState{}, err
	}
	st.Overrides, st.Paused = o, o[port.OverrideHold] == port.OverrideTrue
	if !st.Paused && r.b.Paused != nil {
		if st.Paused, err = r.b.Paused(ctx, u); err != nil {
			return src.RoundState{}, err
		}
	}
	return st, nil
}

// Workspaces lists the host's workspaces of repo, every one when "".
func (r reader) Workspaces(ctx context.Context, repo string) (src.Listing, error) {
	if r.b.Workspaces == nil {
		return src.Listing{}, src.ErrUnsupported
	}
	return r.b.Workspaces(ctx, repo)
}

// Session is the session with id handle as a plugin's tool reads it: the
// unit of its newest turn, that turn and the effects its intent lets a
// draft carry. A session with no turn has no unit.
func (r reader) Session(ctx context.Context, handle string) (src.Session, error) {
	h := r.b.Hosted
	if h == nil || h.Sessions == nil {
		return src.Session{}, src.ErrUnsupported
	}
	b, ok, err := h.Sessions.BookOf(ctx, handle)
	if err != nil || !ok {
		return src.Session{}, err
	}
	t, ok := b.OfSession(handle)
	if !ok {
		return src.Session{}, nil
	}
	out := src.Session{Unit: Wire(b.Unit), Turn: t.ID, TurnSeq: t.Seq, Intent: string(t.Intent), Role: t.Role}
	out.AllowedEffects = slices.Clone(h.allowedEffects(string(t.Intent)))
	return out, nil
}
