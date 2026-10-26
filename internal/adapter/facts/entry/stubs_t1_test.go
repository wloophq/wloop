package factentry

import "testing"

func TestEntryListsTheOpenEntriesOfEnabledPlugins(t *testing.T) {
	t.Skip("T1: C1: every entry neither done nor in the trash, of providers whose plugin is enabled")
}

func TestEntryFactsMatchTheProposalsJob(t *testing.T) {
	t.Skip("T1: C1: nudge_due_at, items_held, waiting_days in the config location, as internal/core/proposals computes them")
}

func TestEntryDueIsTheNextNudgeOrResurfaceTime(t *testing.T) {
	t.Skip("T1: RF8: Dueer returns the next nudge-due or resurface time")
}

func TestAnEntrysOwnerIsItsUnit(t *testing.T) {
	t.Skip("T1: C1: a step on an entry shows on its unit's timeline")
}
