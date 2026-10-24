package inventory

import (
	"context"

	"github.com/wloophq/wloop/internal/core/port"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

// ByUnit is the store as the daemon's inventory roles read it, keyed by
// unit; a unit that is no pull request reads as the zero reference.
type ByUnit struct{ *Store }

func (s ByUnit) PR(ctx context.Context, u unit.Unit) (dinv.PR, bool, error) {
	return s.Store.PR(ctx, unitRef(u))
}

func (s ByUnit) PRs(ctx context.Context, units []unit.Unit) (map[unit.Unit]dinv.PR, error) {
	got, err := s.Store.PRs(ctx, unitRefs(units))
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.PR, len(got))
	for ref, p := range got {
		out[ref.Unit()] = p
	}
	return out, nil
}

func (s ByUnit) Tracked(ctx context.Context) ([]unit.Unit, error) {
	refs, err := s.Store.Tracked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]unit.Unit, len(refs))
	for i, ref := range refs {
		out[i] = ref.Unit()
	}
	return out, nil
}

func (s ByUnit) SetWorkspace(ctx context.Context, u unit.Unit, ws dinv.Workspace) error {
	return s.Store.SetWorkspace(ctx, unitRef(u), ws)
}

func (s ByUnit) Events(ctx context.Context, u unit.Unit) ([]dinv.Event, error) {
	return s.Store.Events(ctx, unitRef(u))
}

func (s ByUnit) Override(ctx context.Context, u unit.Unit) (dinv.Override, error) {
	return s.Store.Override(ctx, unitRef(u))
}

func (s ByUnit) Overrides(ctx context.Context) (map[unit.Unit]dinv.Override, error) {
	got, err := s.Store.Overrides(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[unit.Unit]dinv.Override, len(got))
	for ref, o := range got {
		out[ref.Unit()] = o
	}
	return out, nil
}

func (s ByUnit) ClearMute(ctx context.Context, u unit.Unit, kind, waitingOn string) (bool, error) {
	return s.Store.ClearMute(ctx, unitRef(u), kind, waitingOn)
}

// SetNextGuidance keeps guidance for u's next round.
func (s ByUnit) SetNextGuidance(ctx context.Context, u unit.Unit, guidance string) error {
	return s.Store.SetNextGuidance(ctx, unitRef(u), guidance)
}

func (s ByUnit) BriefRecord(ctx context.Context, u unit.Unit) (dinv.BriefRecord, bool, error) {
	return s.Store.BriefRecord(ctx, unitRef(u))
}

func unitRef(u unit.Unit) github.Ref {
	ref, _ := port.PROf(port.Event{Unit: u})
	return ref
}

func unitRefs(units []unit.Unit) []github.Ref {
	refs := make([]github.Ref, len(units))
	for i, u := range units {
		refs[i] = unitRef(u)
	}
	return refs
}
