package catalog

import (
	"maps"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestOfferFactsFillEveryFactAnOfferReads(t *testing.T) {
	got := slices.Sorted(maps.Keys(OfferFacts(task.Entry{ID: "e-01"})))
	if want := slices.Sorted(slices.Values(builtin.OfferFacts)); !slices.Equal(got, want) {
		t.Errorf("facts %v, want %v", got, want)
	}
}
