package source_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	psource "github.com/wloophq/wloop/internal/plugins/source"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestTheBrokerAnswersAPluginsPredicateOverEachSetOfFactsInOneCall(t *testing.T) {
	t.Parallel()
	b, _ := brokerOver(t)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	var calls int
	var ages []any
	b.Hosted = &psource.Hosted{Now: func() time.Time { return now }, Holds: func(_ context.Context, predicate string, sets []map[string]any) ([]src.Holding, error) {
		calls++
		out := make([]src.Holding, len(sets))
		for i, facts := range sets {
			ages = append(ages, facts["acme.age"])
			out[i] = src.Holding{Holds: facts["acme.owner"] != "solo"}
		}
		return out, nil
	}}
	sets := [][]src.Fact{
		{{Name: "acme.owner", Value: src.String("solo")}, {Name: "acme.age", Value: src.Since(now.Add(-time.Hour))}},
		{{Name: "acme.owner", Value: src.String("team")}},
	}
	got, err := b.Host().Services.Holds(t.Context(), "reviewer_required", sets)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []src.Holding{{Holds: false}, {Holds: true}}) || calls != 1 {
		t.Errorf("holds %v in %d calls, want [false true] in one", got, calls)
	}
	if ages[0] != time.Hour || ages[1] != nil {
		t.Errorf("ages %v, want the temporal fact resolved at the host's now and a missing fact absent", ages)
	}
}

func TestAPredicateThatFailsFailsTheQuestion(t *testing.T) {
	t.Parallel()
	b, _ := brokerOver(t)
	b.Hosted = &psource.Hosted{Holds: func(context.Context, string, []map[string]any) ([]src.Holding, error) {
		return nil, errors.New("plugin acme sets no predicate solo")
	}}
	if _, err := b.Host().Services.Holds(t.Context(), "solo", [][]src.Fact{nil}); err == nil {
		t.Error("Holds answered a predicate the host could not evaluate")
	}
	b.Hosted = nil
	if _, err := b.Host().Services.Holds(t.Context(), "solo", [][]src.Fact{nil}); !errors.Is(err, src.ErrUnsupported) {
		t.Errorf("Holds without the host's profile = %v, want ErrUnsupported", err)
	}
}
