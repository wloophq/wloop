package ghsource

import "testing"

func TestPRReviewedAppliesOnTheLaunchRouteOnly(t *testing.T) {
	t.Skip("X8c2: pr.reviewed knows its route only in process; the cell becomes route-free or the request carries the route")
}
