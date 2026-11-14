package cli

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestEveryTurnEffectRunsOnARoute(t *testing.T) {
	t.Parallel()
	for _, e := range onSent {
		runs := false
		for _, c := range e.on {
			runs = runs || c.guide != nil || c.unsent != nil
		}
		if !runs {
			t.Errorf("%s runs on no route", e.name)
		}
	}
	theirs := daemon.Builtins().Effects
	for _, name := range builtin.TurnEffectNames() {
		if fx, _ := builtin.EffectOf(name); fx.Plugin {
			continue
		}
		if !slices.ContainsFunc(onSent, func(e onSentEffect) bool { return e.name == name }) && !slices.Contains(theirs, name) {
			t.Errorf("the turn effect %s is in neither the command line's table nor the daemon's", name)
		}
	}
}
