package cli

import "testing"

func TestTaskSetRefusesAPropWithoutTheWorkflowWriter(t *testing.T) { t.Skip("M1") }

func TestGateOpenStepOpensAProfileGate(t *testing.T) { t.Skip("G2") }

func TestOutboxCLIUsesGates(t *testing.T) { t.Skip("G3a") }

func TestAttachBindsTheCallingSessionToTheTask(t *testing.T) { t.Skip("U1") }

func TestAttachWithoutAHandleRefusesAndChangesNothing(t *testing.T) { t.Skip("U1") }

func TestTaskNewSetsTitleBriefAndProps(t *testing.T) { t.Skip("S1") }

func TestStartWithABriefFileKeepsTheTitleShort(t *testing.T) { t.Skip("S1") }

func TestOperatorSkillNamesOnlyExistingCommands(t *testing.T) { t.Skip("S1") }

func TestStalledNudgeSendsOncePerPerson(t *testing.T) { t.Skip("W1a") }

func TestNudgedEffectWritesLastNudge(t *testing.T) { t.Skip("W1a") }

func TestAutofocusStarsStaleAndPastDue(t *testing.T) { t.Skip("W1a") }

func TestNewTasksGetAKind(t *testing.T) { t.Skip("W1a") }
