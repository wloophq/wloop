package daemon

import "testing"

func TestRunDispatchesThroughHandlers(t *testing.T) {
	t.Skip("B10 operations run through the existing handlers")
}

func TestRunRefusesOperationOutsideVocabulary(t *testing.T) {
	t.Skip("B10 an operation outside the vocabulary is refused")
}

func TestRunNeverResolvesAGate(t *testing.T) { t.Skip("B10 no operation resolves a gate") }

func TestRunStartCapturesThenStarts(t *testing.T) {
	t.Skip("B10 start captures a task, then starts it")
}

func TestRunKeepsTheHandlersRefusal(t *testing.T) { t.Skip("B10 a handler's refusal is kept") }

func TestRunNeedsAuthLikeTheHandlers(t *testing.T) { t.Skip("B10 run needs the token like any route") }
