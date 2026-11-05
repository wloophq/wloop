package api

import "testing"

func TestFacetMechanicsDiffNamesWhatDiffers(t *testing.T) {
	one := FacetSpec{ID: "opened", Sort: "time", Buckets: []Bucket{{ID: "1d", Within: "24h"}, {ID: "older"}}}
	other := one
	other.Buckets = []Bucket{{ID: "1d", Within: "48h"}, {ID: "older"}}
	other.Label = "Other label"
	switch {
	case one.MechanicsDiff(other) == "":
		t.Error("buckets with another within are another mechanic")
	case one.MechanicsDiff(one) != "":
		t.Error("a facet differs from itself")
	case (FacetSpec{Match: "exact"}).MechanicsDiff(FacetSpec{}) != "":
		t.Error("exact is the default match")
	}
}
