package cli

import (
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestEveryDraftEffectHasAnImplementation(t *testing.T) {
	t.Parallel()
	var want []string
	for _, name := range builtin.EffectNames() {
		if fx, _ := builtin.EffectOf(name); fx.Provider == "" {
			want = append(want, name)
		}
	}
	got := slices.Sorted(maps.Keys(draftEffects(effectDeps{})))
	if slices.Sort(want); !slices.Equal(got, want) {
		t.Fatalf("implemented %v, the core's draft effects %v", got, want)
	}
}

func TestAPluginEffectStaysPendingWhileItsPluginIsNotBuilt(t *testing.T) {
	t.Parallel()
	thread := unit.ProviderID("thread")
	off := effectLookup(effectDeps{})
	if _, ok := off(thread, builtin.EffectUnitClosed); ok {
		t.Fatal("unit.closed applies on a provider whose plugin is not built")
	}
	if _, ok := off(thread, builtin.EffectTaskNudged); !ok {
		t.Fatal("task.nudged needs no plugin")
	}
	on := effectLookup(effectDeps{Modules: []pluginModule{{ID: thread}}})
	if _, ok := on(thread, builtin.EffectUnitClosed); !ok {
		t.Fatal("unit.closed on a built plugin that records no closure")
	}
}
