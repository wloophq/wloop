package cli

import (
	"context"
	"errors"
	"maps"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/config"
	coreplacement "github.com/wloophq/wloop/internal/core/placement"
	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type placementSources struct {
	hints    map[unit.ProviderID]placement.Hints
	defaults map[unit.ProviderID]placement.Defaults
}

// placements places the units of cfg's rounds: the user's overrides when a
// store is open, the context map, then the plugins' hints and defaults, with
// GitHub's own default.
func (e *Env) placements(cfg config.Config) *coreplacement.Service {
	s := &coreplacement.Service{
		Map:      cfg.PlacementMap,
		Hints:    map[unit.ProviderID]placement.Hints{},
		Defaults: map[unit.ProviderID]placement.Defaults{unit.GitHub: githubPlacement{e.Store}},
	}
	if e.Store != nil {
		s.Overrides = inventory.Placements{Store: e.Store}
	}
	if p := e.placementSources; p != nil {
		maps.Copy(s.Hints, p.hints)
		maps.Copy(s.Defaults, p.defaults)
	}
	return s
}

// shared says whether u's placement lets its workspace hold other units'
// sessions; a unit with no placement is not shared.
func (e *Env) shared(ctx context.Context, cfg config.Config, u unit.Unit) (bool, error) {
	if u.IsZero() {
		return false, nil
	}
	p, err := e.placements(cfg).Place(ctx, u, "")
	if errors.Is(err, coreplacement.ErrUnplaced) {
		return false, nil
	}
	return p.Shared, err
}

// githubPlacement is the GitHub default: the pull request's repository, in
// a workspace of its own, as its author or a reviewer by the stored author.
type githubPlacement struct{ store *inventory.Store }

func (g githubPlacement) Default(ctx context.Context, u unit.Unit, _ string) (placement.Placement, bool, error) {
	ref, err := pr.ParseKey(u.Key)
	if err != nil || u.Provider != unit.GitHub {
		return placement.Placement{}, false, nil
	}
	p := placement.Placement{Repo: ref.Owner + "/" + ref.Repo, Role: placement.RoleReview}
	if g.store == nil {
		return p, true, nil
	}
	viewer, err := g.store.Viewer(ctx, unit.GitHub)
	if err != nil {
		return placement.Placement{}, false, err
	}
	stored, ok, err := g.store.PR(ctx, ref)
	if err != nil {
		return placement.Placement{}, false, err
	}
	if ok && viewer != "" && stored.Author == viewer {
		p.Role = placement.RoleAuthor
	}
	return p, true, nil
}
