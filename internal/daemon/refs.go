package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// can says whether the enabled plugin of p serves capability c.
func (s *Server) can(p unit.ProviderID, c string) bool { return api.PluginCan(s.deps.Plugins, p, c) }

// sameUnit says whether a and b name the same unit, as their provider's
// codec compares keys.
func (s *Server) sameUnit(a, b unit.Unit) bool { return s.deps.Units.Same(a, b) }

// forge says whether u is a unit of the forge's plugin, whose units the
// brief queue, the intakes and the rounds serve.
func (s *Server) forge(u unit.Unit) bool {
	return s.deps.Forge != "" && u.Provider == s.deps.Forge
}

// pluginActs says whether name is an action of u's plugin: one the
// catalog declares, or one u's row offers.
func (s *Server) pluginActs(ctx context.Context, u unit.Unit, name string) bool {
	if _, ok := s.presenter(u.Provider); !ok || !s.pluginEnabled(u.Provider) {
		return false
	}
	if slices.ContainsFunc(s.servedSpecs(), func(sp api.ActionSpec) bool { return sp.ID == name }) {
		return true
	}
	row, err := s.currentRow(ctx, u)
	if err != nil {
		return false
	}
	_, ok := offerOf(row, name)
	return ok
}

func removeDoneResult() api.RemoveDoneResult {
	return api.RemoveDoneResult{Removed: []api.Named{}, Kept: []api.Kept{}}
}

// unitOfAddress is the unit raw names through the codecs: <provider>:<key>
// of a provider units knows, or a web address a codec reads.
func unitOfAddress(units *unit.Registry, raw string) (unit.Unit, error) {
	if p, key, ok := strings.Cut(raw, ":"); ok && !strings.HasPrefix(key, "//") {
		if c, known := units.Codec(unit.ProviderID(p)); known {
			if err := c.Valid(key); err != nil {
				return unit.Unit{}, err
			}
			return unit.Unit{Provider: unit.ProviderID(p), Key: key}, nil
		}
	}
	if u, ok := units.ParseURL(raw); ok {
		return u, nil
	}
	return unit.Unit{}, badRequest{fmt.Sprintf("%q names no unit: give a unit's web address or <provider>:<key>", raw)}
}

// parseForgeURL is the unit of the forge a web address names.
func (s *Server) parseForgeURL(raw string) (unit.Unit, error) {
	u, ok := s.deps.Units.ParseURL(raw)
	if !ok || !s.forge(u) {
		return unit.Unit{}, fmt.Errorf("%q names no %s unit", raw, s.deps.Forge)
	}
	return u, nil
}

// named is u with its label.
func named(u unit.Unit) api.Named { return api.Named{Unit: u, Label: u.Label()} }
