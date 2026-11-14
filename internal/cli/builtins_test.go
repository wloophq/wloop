package cli

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestEveryHostTurnEffectHasAnImplementationInTheOneTable(t *testing.T) {
	t.Parallel()
	for _, h := range hostEffects {
		if h.before == nil && h.sent == nil && !h.started {
			t.Errorf("%s runs in no phase", h.name)
		}
		if fx, ok := builtin.EffectOf(h.name); !ok || fx.Plugin || fx.Applies&builtin.AppliesTurn == 0 {
			t.Errorf("%s is no host turn effect of builtin.Effects", h.name)
		}
	}
	for _, name := range builtin.TurnEffectNames() {
		if fx, _ := builtin.EffectOf(name); fx.Plugin {
			continue
		}
		if !slices.ContainsFunc(hostEffects, func(h hostEffect) bool { return h.name == name }) {
			t.Errorf("the turn effect %s is not in the host's table", name)
		}
	}
}
