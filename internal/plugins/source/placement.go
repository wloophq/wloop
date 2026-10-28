package source

import (
	"context"

	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Placements is a source plugin's Planner.Placement as placement reads
// it: its default and its suggestion.
type Placements struct {
	ID   string
	Kind *Kind
}

var (
	_ placement.Defaults = Placements{}
	_ placement.Hints    = Placements{}
)

func (p Placements) Default(ctx context.Context, u unit.Unit, item string) (placement.Placement, bool, error) {
	ps, err := p.read(ctx, u, item)
	return placementOf(ps.Default), ps.Default != nil, err
}

func (p Placements) Suggested(ctx context.Context, u unit.Unit, item string) (placement.Placement, bool, error) {
	ps, err := p.read(ctx, u, item)
	return placementOf(ps.Suggested), ps.Suggested != nil, err
}

func (p Placements) read(ctx context.Context, u unit.Unit, item string) (src.Placements, error) {
	c, err := p.Kind.Client(p.ID)
	if err != nil {
		return src.Placements{}, err
	}
	return c.Placement(ctx, Wire(u), item)
}

func placementOf(p *src.Placement) placement.Placement {
	if p == nil {
		return placement.Placement{}
	}
	return placement.Placement{Repo: p.Repo, Checkout: p.Checkout, Role: p.Role, Shared: p.Shared}
}
