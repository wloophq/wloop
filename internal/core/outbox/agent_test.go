package outbox

import "testing"

func TestProposeVerifiesBasisLive(t *testing.T) {
	t.Skip("T3")
}

func TestProposeRefusesInvalidPayload(t *testing.T) {
	t.Skip("T3")
}

func TestHoldWaits(t *testing.T) {
	t.Skip("T3")
}

func TestHoldUnderDryRun(t *testing.T) {
	t.Skip("T3")
}

func TestHoldReappliedOnRevision(t *testing.T) {
	t.Skip("T3")
}

func TestAgentRevisionOpensWaitingGate(t *testing.T) {
	t.Skip("T3")
}

func TestFailedRevisedGoesHeld(t *testing.T) {
	t.Skip("T3")
}

// TestSendPostsOnlyApprovedRev asserts at Publish, where the draft leaves held: a gate whose ApprovedRev is not
// the held draft's Rev, or is 0, queues nothing.
func TestSendPostsOnlyApprovedRev(t *testing.T) {
	t.Skip("T3")
}

func TestApproveRefusesStale(t *testing.T) {
	t.Skip("T3")
}

func TestSendFailsStaleWithoutPublishing(t *testing.T) {
	t.Skip("T3")
}

func TestAmendRevConflict(t *testing.T) {
	t.Skip("T3")
}

func TestAgentScopeTurnOrSessionOrigin(t *testing.T) {
	t.Skip("T3")
}

func TestTargeterDecidesWithinUnit(t *testing.T) {
	t.Skip("T3")
}

func TestAttachPostCoversNewDeliveries(t *testing.T) {
	t.Skip("T3")
}
