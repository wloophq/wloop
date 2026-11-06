package tasks_test

import "testing"

func TestMergeFoldsTheOtherTaskIntoTheSurvivorInOneWrite(t *testing.T) {
	t.Skip("T1 C1")
}

func TestMergeKeepsTheClarifiedTaskWhateverTheOrderOfTheRequest(t *testing.T) {
	t.Skip("T1 C5")
}

func TestMergeKeepsTheOlderWhenNeitherOrBothWereClarified(t *testing.T) {
	t.Skip("T1 C5")
}

func TestMergeRefusesTheSameTaskAClosedOneAndAParent(t *testing.T) {
	t.Skip("T1 C1")
}

func TestMergeRefusesATaskAlreadyMergedIntoTheOther(t *testing.T) {
	t.Skip("T1 C1")
}

func TestMergeRecordsAVerbEventOnBothTasks(t *testing.T) {
	t.Skip("T1 C1")
}

func TestSyncOfEveryMovedUnitKeepsItsItemsOnTheSurvivor(t *testing.T) {
	t.Skip("T1 C2")
}

func TestSyncNeverRecreatesTheMergedTask(t *testing.T) {
	t.Skip("T1 C2")
}

func TestAMergeAndASyncOfItsUnitsDoNotInterleave(t *testing.T) {
	t.Skip("T1 C2")
}

func TestSplitGivesTheMergedTaskBackAndSyncsItsUnits(t *testing.T) {
	t.Skip("T1 C3")
}

func TestSplitUnwindsAChainFromItsOutermostMerge(t *testing.T) {
	t.Skip("T1 C3")
}

func TestAWorkflowsMergeOpensTheMergeGate(t *testing.T) {
	t.Skip("T1 C9")
}

func TestTheMergeGateNamesBothTasksInItsWords(t *testing.T) {
	t.Skip("T1 C9")
}

func TestTwoProposalsOfOnePairInEitherOrderOpenOneGate(t *testing.T) {
	t.Skip("T1 C9")
}

func TestAnApprovedMergeGateMergesExactlyItsPair(t *testing.T) {
	t.Skip("T1 C9")
}

func TestAnApprovedMergeGateWhosePairChangedRunsNothing(t *testing.T) {
	t.Skip("T1 C9")
}

func TestMergeSupersedesTheMergedTasksOwnProposalsAndPlans(t *testing.T) {
	t.Skip("T1 C9")
}

func TestSplitSupersedesTheProposalsNamingBothTasks(t *testing.T) {
	t.Skip("T1 C9")
}

func TestAWorkflowCannotSplit(t *testing.T) {
	t.Skip("T1 C9")
}
