package source

import (
	"context"

	"github.com/wloophq/wloop/internal/core/port"

	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

type reader struct{ b *Broker }

func (reader) Tasks(context.Context, []plugin.Unit) ([]src.Task, error) {
	return nil, src.ErrUnsupported
}

func (reader) Books(context.Context, []plugin.Unit) ([]src.Book, error) {
	return nil, src.ErrUnsupported
}

// RoundState is the host's round bookkeeping of one of the plugin's own
// units, its overrides, and whether its rounds are paused: by its hold or a
// wider pause.
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

func (reader) Workspaces(context.Context, string) (src.Listing, error) {
	return src.Listing{}, src.ErrUnsupported
}

func (reader) Session(context.Context, string) (src.Session, error) {
	return src.Session{}, src.ErrUnsupported
}
