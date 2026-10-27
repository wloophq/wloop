package gtd

import (
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/gtd"
)

func TestTheTransitionTableHasEveryVerb(t *testing.T) {
	got := slices.Sorted(maps.Keys(moves))
	want := slices.Clone(gtd.Verbs)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("moves has %v, the domain's verbs are %v", got, want)
	}
}
