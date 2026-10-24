package config

import (
	"fmt"

	"github.com/wloophq/wloop/internal/domain/placement"
)

// PlacementRule is one rule of the placement map.
type PlacementRule struct {
	Match    string `json:"match"`
	Repo     string `json:"repo,omitempty"`
	Checkout string `json:"checkout,omitempty"`
	Role     string `json:"role,omitempty"`
	Shared   bool   `json:"shared,omitempty"`
}

// PlacementMap is c's placement rules as the placement service reads them.
func (c Config) PlacementMap() placement.Map {
	m := placement.Map{Rules: make([]placement.Rule, len(c.Placement))}
	for i, x := range c.Placement {
		m.Rules[i] = placement.Rule{Match: x.Match, Repo: x.Repo, Checkout: x.Checkout, Role: x.Role, Shared: x.Shared}
	}
	return m
}

func (c Config) validatePlacement() error {
	for i, r := range c.PlacementMap().Rules {
		if err := r.Valid(); err != nil {
			return fmt.Errorf("placement[%d]: %w", i, err)
		}
	}
	return nil
}
