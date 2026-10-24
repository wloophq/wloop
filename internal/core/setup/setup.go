// Package setup checks, installs and removes what the loop needs in the
// configuration homes of the coding agents.
package setup

import (
	"context"
	"errors"
	"fmt"
)

// Finding is one entry the loop needs in an agent home.
type Finding struct {
	// Problem is "" when the entry is present and right.
	Home, Entry, Problem, Fix string
}

// AgentHome is one agent's configuration home. Install and Remove
// return a line per entry they add, change, back up or remove.
type AgentHome interface {
	Check(ctx context.Context, home string) ([]Finding, error)
	Install(ctx context.Context, home string, dryRun bool) ([]string, error)
	Remove(ctx context.Context, home string, dryRun bool) ([]string, error)
}

type Service struct{ Home AgentHome }

func (s Service) Check(ctx context.Context, homes []string) ([]Finding, error) {
	var out []Finding
	var errs []error
	for _, h := range homes {
		f, err := s.Home.Check(ctx, h)
		out = append(out, f...)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h, err))
		}
	}
	return out, errors.Join(errs...)
}

func (s Service) Install(ctx context.Context, homes []string, dryRun bool) ([]string, error) {
	return each(homes, func(h string) ([]string, error) { return s.Home.Install(ctx, h, dryRun) })
}

func (s Service) Remove(ctx context.Context, homes []string, dryRun bool) ([]string, error) {
	return each(homes, func(h string) ([]string, error) { return s.Home.Remove(ctx, h, dryRun) })
}

func each(homes []string, do func(string) ([]string, error)) ([]string, error) {
	var out []string
	var errs []error
	for _, h := range homes {
		lines, err := do(h)
		out = append(out, lines...)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", h, err))
		}
	}
	return out, errors.Join(errs...)
}
