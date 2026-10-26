package outbox

import "testing"

func TestASentDraftAppliesItsEffectsOnce(t *testing.T) {
	t.Skip("T2: C2: exactly its Effects, through the effect table")
}

func TestEffectsAreAppliedOnceAcrossARestart(t *testing.T) {
	t.Skip("T2: C2, RF7: effects_applied_at and the catch-up pass")
}

func TestNoEffectIsDerivedFromKind(t *testing.T) {
	t.Skip("T2: C2: a close draft with no effects leaves closure_posted 0")
}

func TestTheApprovalIsConfigThenLegacyThenManifestThenAlways(t *testing.T) {
	t.Skip("T2: C4: Proposal.Approval resolution order")
}

func TestADraftWhoseProposerSetsAutoSendFalseWaits(t *testing.T) {
	t.Skip("T2: C4: whatever its approval")
}

func TestDuplicateReadsPurposeDeliveryAndObservedFor(t *testing.T) {
	t.Skip("T2: C4: duplicate and would keyed on purpose and delivery")
}
