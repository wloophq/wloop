package cli

import "testing"

func TestDraftProposeRendersTheProfileTextWithItemsAsData(t *testing.T) {
	t.Skip("T2: C4, RF3: item text never becomes template code")
}

func TestDraftProposeHoldsPerWorkflowSubjectAndPerson(t *testing.T) {
	t.Skip("T2: C4: hold and hold_after_sent from drafts")
}

func TestAReactionWithNoPermalinkIsRefusedAtPropose(t *testing.T) { t.Skip("T2: C4") }

func TestDraftProposeOnAFreshSubjectProposesOnItsFirstSweep(t *testing.T) { t.Skip("T2: C4") }

func TestUnitClosedClosesTheEntryAndUnwatchesAfterWarmFor(t *testing.T) {
	t.Skip("T2: C2: a reaction draft with unit.closed, through the Slack plugin's fake store")
}
