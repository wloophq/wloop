package cli

import (
	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// servePlacement registers the placement hints and defaults of the enabled
// sources for the daemon's rounds.
func (e *Env) servePlacement(mods []pluginModule) {
	p := &placementSources{hints: map[unit.ProviderID]placement.Hints{}, defaults: map[unit.ProviderID]placement.Defaults{}}
	for _, m := range mods {
		if m.PlacementHints != nil {
			p.hints[m.ID] = m.PlacementHints
		}
		if m.PlacementDefaults != nil {
			p.defaults[m.ID] = m.PlacementDefaults
		}
	}
	e.placementSources = p
}
