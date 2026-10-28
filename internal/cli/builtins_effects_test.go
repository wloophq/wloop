package cli

import (
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/source"
)

func TestEveryDraftEffectHasAnImplementation(t *testing.T) {
	t.Parallel()
	got, want := slices.Sorted(maps.Keys(draftEffects(effectDeps{}))), slices.Sorted(slices.Values(builtin.EffectNames()))
	if !slices.Equal(got, want) {
		t.Fatalf("implemented %v, builtin.Effects %v", got, want)
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
	on := effectLookup(effectDeps{Modules: []source.Module{{ID: thread}}})
	if _, ok := on(thread, builtin.EffectUnitClosed); !ok {
		t.Fatal("unit.closed on a built plugin that records no closure")
	}
}
