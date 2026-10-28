package cli

import (
	"context"
	"maps"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// roundPlanner is a plugin's planner as the host launches and skips rounds
// with it: a running plugin's client, or a built-in module's planner.
type roundPlanner interface {
	Plan(ctx context.Context, u plugin.Unit, st src.RoundState) (src.Plan, error)
}

// effectApplier applies a plugin's turn and draft effects.
type effectApplier interface {
	ApplyEffect(ctx context.Context, req src.EffectRequest) error
}

// overrideReader reads the host's overrides of a unit by name.
type overrideReader interface {
	Override(ctx context.Context, u unit.Unit) (map[string]string, error)
}

// roundBookkeeping is the host's round bookkeeping of u in st: its marks,
// its last sent round, its agent home, its rounds today and every unit's,
// and the start block holding it.
func (e *Env) roundBookkeeping(ctx context.Context, u unit.Unit, st *round.Store) (src.RoundState, error) {
	p := st.PRs[u.Key]
	day := st.Days[round.DayKey(e.Now())]
	out := src.RoundState{Marks: maps.Clone(p.Marks), SentAt: p.SentAt, AgentHome: p.AgentHome, Today: day.PerPR[u.Key], DayTotal: day.Total}
	if out.Marks == nil {
		out.Marks = map[string]string{}
	}
	b, err := e.roundBlock(ctx, u)
	if err != nil {
		return src.RoundState{}, err
	}
	out.Blocked = b
	return out, nil
}

// roundBlock is the start block holding u's round, nil for none.
func (e *Env) roundBlock(ctx context.Context, u unit.Unit) (*src.Blocked, error) {
	if e.Rec == nil {
		return nil, nil
	}
	blocks, err := e.Rec.FirstRoundBlocks(ctx)
	if err != nil {
		return nil, err
	}
	ev, ok := blocks[u]
	if !ok {
		return nil, nil
	}
	b, _ := inventory.StartBlockOf(ev)
	return &src.Blocked{Question: b.Question, Root: b.Root, Folder: b.Folder, Since: ev.At}, nil
}

// roundStateOf is u's round state as a plan reads it: the bookkeeping of st,
// the rounds its open plan gates hold, the host overrides o holds, and
// whether u's rounds are paused.
func (e *Env) roundStateOf(ctx context.Context, u unit.Unit, st *round.Store, o overrideReader) (src.RoundState, error) {
	out, err := e.roundBookkeeping(ctx, u, st)
	if err != nil {
		return src.RoundState{}, err
	}
	if out.Pending, err = e.pendingOf(ctx, u); err != nil {
		return src.RoundState{}, err
	}
	out.Overrides = map[string]string{}
	if o != nil {
		if out.Overrides, err = o.Override(ctx, u); err != nil {
			return src.RoundState{}, err
		}
	}
	if out.Paused = out.Overrides[port.OverrideHold] == port.OverrideTrue; !out.Paused {
		if out.Paused, err = e.roundsPaused(ctx, u); err != nil {
			return src.RoundState{}, err
		}
	}
	return out, nil
}

// storedRoundState is u's bookkeeping in the stored round state, which the
// broker serves a plugin's RoundState with.
func (e *Env) storedRoundState(ctx context.Context, u unit.Unit) (src.RoundState, error) {
	st := round.New()
	if e.Rounds != nil {
		var err error
		if st, err = e.Rounds.Load(ctx); err != nil {
			return src.RoundState{}, err
		}
	}
	return e.roundBookkeeping(ctx, u, st)
}
