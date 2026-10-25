package cli

import (
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestEveryDraftEffectHasAnImplementation(t *testing.T) {
	t.Parallel()
	got, want := slices.Sorted(maps.Keys(draftEffects())), slices.Sorted(slices.Values(builtin.EffectNames()))
	if !slices.Equal(got, want) {
		t.Fatalf("implemented %v, builtin.Effects %v", got, want)
	}
}
