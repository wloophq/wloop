package tasks

import (
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

func TestTheTransitionTableHasEveryVerb(t *testing.T) {
	got := slices.Sorted(maps.Keys(moves))
	want := slices.Clone(task.Verbs)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("moves has %v, the domain's verbs are %v", got, want)
	}
}
