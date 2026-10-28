package tasks

import "testing"

func TestATimeVaryingViewRepagesWhenTheClockMovesATask(t *testing.T) { t.Skip("W2b") }

func TestDisabledPluginTasksAreHidden(t *testing.T) { t.Skip("W2b") }

func TestReviewProjectsStepListsProjectsDueOrWithoutANextAction(t *testing.T) { t.Skip("W2b") }

func TestSequentialProjectShowsItsFirstAction(t *testing.T) { t.Skip("W2b") }

func TestTasksSincePagesAView(t *testing.T) { t.Skip("W2b") }

func TestAKindsOwnDismissAndDoneBindByHandler(t *testing.T) {
	t.Skip("W2b: M3 review, a draft's and a message's dismiss and done register by the verb's name")
}

func TestADraftOnAnUnheldUnitCountsOnlyInTheOutbox(t *testing.T) {
	t.Skip("W2b: G3a review, a draft on a unit no task holds opens a capture task that counts in the Inbox as well as the Outbox")
}
