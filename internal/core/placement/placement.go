// Package placement decides where a unit's work happens.
package placement

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Service struct {
	Map       func() placement.Map
	Overrides placement.Overrides
	Hints     map[unit.ProviderID]placement.Hints
	Defaults  map[unit.ProviderID]placement.Defaults
}

var ErrUnplaced = errors.New("no placement for the unit")

// Place: override, then context map, then hint, then provider default; ErrUnplaced when none.
// This is the only precedence; a plugin supplies Hints and Defaults and never orders them itself.
// A placement with a fixed checkout is Shared: that checkout can hold several units.
func (s *Service) Place(ctx context.Context, u unit.Unit, item string) (placement.Placement, error) {
	p, err := s.place(ctx, u, item)
	p.Shared = p.Shared || p.Checkout != ""
	return p, err
}

func (s *Service) place(ctx context.Context, u unit.Unit, item string) (placement.Placement, error) {
	if s.Overrides != nil {
		p, ok, err := s.Overrides.Override(ctx, u)
		if err != nil {
			return placement.Placement{}, fmt.Errorf("reading the placement override of %s: %w", u.Label(), err)
		}
		if ok {
			p.Source = placement.SourceOverride
			return p, nil
		}
	}
	if s.Map != nil {
		if p, ok := s.Map().Place(u); ok {
			return p, nil
		}
	}
	if h := s.Hints[u.Provider]; h != nil {
		p, ok, err := h.Suggested(ctx, u, item)
		if err != nil {
			return placement.Placement{}, fmt.Errorf("reading the suggested placement of %s: %w", u.Label(), err)
		}
		if ok {
			p.Source = placement.SourceSuggested
			return p, nil
		}
	}
	if d := s.Defaults[u.Provider]; d != nil {
		p, ok, err := d.Default(ctx, u, item)
		if err != nil {
			return placement.Placement{}, fmt.Errorf("reading the default placement of %s: %w", u.Label(), err)
		}
		if ok {
			p.Source = placement.SourceProvider
			return p, nil
		}
	}
	return placement.Placement{}, fmt.Errorf("%s: %w", u.Label(), ErrUnplaced)
}
