package cli

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestEveryOnSentEffectRunsOnARoute(t *testing.T) {
	t.Parallel()
	for _, e := range onSent {
		runs := false
		for _, c := range e.on {
			runs = runs || c.guide != nil || c.sent != nil || c.state != nil || c.unsent != nil
		}
		if !runs {
			t.Errorf("%s runs on no route", e.name)
		}
		for rt, c := range e.on {
			if c.writesIf != nil && c.state == nil {
				t.Errorf("%s: route %d says when it writes but has no state phase", e.name, rt)
			}
			if c.writesIf != nil && rt != routeLaunch {
				t.Errorf("%s: route %d says when it writes, but off the launch route that is read before the round is sent", e.name, rt)
			}
		}
	}
	theirs := daemon.Builtins()[builtin.SlotOnSent]
	for _, name := range builtin.Names[builtin.SlotOnSent] {
		if !slices.ContainsFunc(onSent, func(e onSentEffect) bool { return e.name == name }) && !slices.Contains(theirs, name) {
			t.Errorf("on_sent %s is in neither the command line's table nor the daemon's", name)
		}
	}
}
