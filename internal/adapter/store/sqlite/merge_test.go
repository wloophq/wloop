package sqlite_test

import "testing"

func TestMergeThenSplitWritesTheMergedTaskBackByteForByte(t *testing.T) {
	t.Skip("T1 C3")
}

func TestMergeMovesTheGatesDraftsAndMessagesOfTheMergedTask(t *testing.T) {
	t.Skip("T1 C1")
}

func TestMergeKeepsTheMergedTasksHistoryUnderItsID(t *testing.T) {
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

func TestMergeOfReadsTheLatestRecordOfAMergedTask(t *testing.T) {
	t.Skip("T1 C3")
}
