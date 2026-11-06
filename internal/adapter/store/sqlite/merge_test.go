package sqlite_test

import "testing"

func TestMergeThenSplitOfAnUntouchedTaskWritesItBackByteForByte(t *testing.T) {
	t.Skip("T1 C3")
}

func TestSplitGivesBackTheGatesAndHeldDraftsOfTheMergedTasksUnits(t *testing.T) {
	t.Skip("T1 C3")
}

func TestMergeMovesTheGatesDraftsAndMessagesOfTheMergedTask(t *testing.T) {
	t.Skip("T1 C1")
}

func TestMergeHandlesEveryColumnTaskRefsFinds(t *testing.T) {
	t.Skip("T1 C1")
}

func TestAMergedTasksIDReadsAsTheSurvivor(t *testing.T) {
	t.Skip("T1 C1")
}

func TestSplitKeepsTheMarksRetentionDroppedWhileMerged(t *testing.T) {
	t.Skip("T1 C3")
}

func TestSplitRefusesATaskStoredAgain(t *testing.T) {
	t.Skip("T1 C3")
}

func TestTaskFollowsAnAliasChainToTheStoredTask(t *testing.T) {
	t.Skip("T1 C1")
}

func TestTaskRefusesAnAliasCycle(t *testing.T) {
	t.Skip("T1 C1")
}

func TestMergeOfReadsTheRecordOfTheHighestEventID(t *testing.T) {
	t.Skip("T1 C3")
}

func TestApplyRefusesAnUpdateOfAMergedTask(t *testing.T) {
	t.Skip("T1 C2")
}

func TestMergeAndSplitStartANewPositionEpoch(t *testing.T) {
	t.Skip("T1 C1")
}
