package tasks_test

import "testing"

func TestMergeFoldsTheOtherTaskIntoTheSurvivorInOneApply(t *testing.T) {
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

func TestMergeRecordsAVerbEventOnBothTasks(t *testing.T) {
	t.Skip("T1 C1")
}

func TestSyncOfEveryMovedUnitKeepsItsItemsOnTheSurvivor(t *testing.T) {
	t.Skip("T1 C2")
}

func TestSyncNeverRecreatesTheMergedTask(t *testing.T) {
	t.Skip("T1 C2")
}

func TestSplitGivesTheMergedTaskBackAndTheSurvivorKeepsWhatArrivedAfter(t *testing.T) {
	t.Skip("T1 C3")
}

func TestSplitUnwindsAChainFromItsOutermostMerge(t *testing.T) {
	t.Skip("T1 C3")
}

func TestAWorkflowsMergeOpensTheMergeGate(t *testing.T) {
	t.Skip("T1 C9")
}

func TestAnApprovedMergeGateMergesExactlyItsPair(t *testing.T) {
	t.Skip("T1 C9")
}

func TestAWorkflowCannotSplit(t *testing.T) {
	t.Skip("T1 C9")
}

func TestRelateLinksBothTasksAndIsIdempotent(t *testing.T) {
	t.Skip("T2 C4")
}

func TestUnrelateTakesTheLinkOffBoth(t *testing.T) {
	t.Skip("T2 C4")
}

func TestMergeCarriesTheMergedTasksRelationsToTheSurvivor(t *testing.T) {
	t.Skip("T2 C4")
}

func TestAWorkflowCannotRelate(t *testing.T) {
	t.Skip("T2 C9")
}
