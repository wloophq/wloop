package readmodel

import "testing"

func TestAHeldRoundIsAPlanGate(t *testing.T) {
	t.Skip("W3: G3b review, a round the daily cap held is a plan gate, as the design's held round, not a row fact")
}

func TestPendingRoundsAreReadFromPlanGatesAlone(t *testing.T) {
	t.Skip("W3: G3b review, readmodel.Pending, api.Row.Pending and round.Pending go once the pull request row reads plan gates")
}
