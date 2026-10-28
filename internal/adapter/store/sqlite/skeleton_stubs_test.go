package sqlite

import "testing"

func TestV7PropsMovesUserFields(t *testing.T) { t.Skip("M2") }

func TestV7PropsSetsKindFromOrigin(t *testing.T) { t.Skip("M2") }

func TestV7PropsKeepsProjectsAsParents(t *testing.T) { t.Skip("M2") }

func TestStoreFiltersByCategory(t *testing.T) { t.Skip("M2") }

func TestEveryPendingDraftHasAnOpenGate(t *testing.T) { t.Skip("G1") }

func TestADelayDraftCarriesItsDeadline(t *testing.T) { t.Skip("G1") }

func TestAPendingRoundCarriesItsWindow(t *testing.T) { t.Skip("G1") }

func TestAnsweredQuestionsMigrateAsAnsweredGates(t *testing.T) { t.Skip("G1") }

func TestAnOpenQuestionKeepsItsRouteThroughV7Gates(t *testing.T) { t.Skip("G1") }

func TestItemProposalsBecomeGates(t *testing.T) { t.Skip("G1") }

func TestAGateOnAUnitWithNoTaskHasANullTask(t *testing.T) { t.Skip("G1") }

func TestV7BriefsMovesTagsIntoRecords(t *testing.T) { t.Skip("T1") }

func TestV7WakesTurnsSnoozesAndMutesIntoWakes(t *testing.T) { t.Skip("W2a") }
