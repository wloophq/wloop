package inventory

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/placement"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestPlacementOverrideRoundTripsAndZeroClears(t *testing.T) {
	s := Placements{open(t)}
	ctx := context.Background()
	u := unit.Unit{Provider: "slack", Key: "T000/C001/1.1"}
	if _, ok, err := s.Override(ctx, u); ok || err != nil {
		t.Fatalf("none: %v %v", ok, err)
	}
	want := placement.Placement{Repo: "acme/api", Checkout: "main", Role: "work", Shared: true}
	for range 2 {
		if err := s.SetOverride(ctx, u, placement.Placement{Repo: "acme/api", Checkout: "main", Role: "work", Shared: true, Source: "override"}); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok, err := s.Override(ctx, u); !ok || err != nil || got != want {
		t.Fatalf("Override = %+v %v %v", got, ok, err)
	}
	if err := s.SetOverride(ctx, u, placement.Placement{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Override(ctx, u); ok || err != nil {
		t.Fatalf("cleared: %v %v", ok, err)
	}
}
