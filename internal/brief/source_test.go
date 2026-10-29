package brief

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInputNotesACutDiff(t *testing.T) {
	r := newRig(t, 1)
	r.src.diff, r.src.diffCut = strings.Repeat("x", 1000), true
	r.q.Run(context.Background())
	if len(r.run.prompts) != 1 || !strings.Contains(r.run.prompts[0], " (the first 1000 bytes; the diff is longer)") {
		t.Fatalf("prompts %q", r.run.prompts)
	}
	runs := runsOf(t, r.st)
	if len(runs) != 1 || runs[0].DiffBytes != nil || !runs[0].DiffCut {
		t.Fatalf("runs %+v", runs)
	}
}

func TestInputWithoutADiff(t *testing.T) {
	r := newRig(t, 1)
	r.src.diffErr = errors.New("GitHub HTTP 406: diff too large")
	r.q.Run(context.Background())
	if len(r.run.prompts) != 1 || !strings.Contains(r.run.prompts[0], "(unavailable: GitHub HTTP 406") {
		t.Fatalf("prompts %q", r.run.prompts)
	}
	runs := runsOf(t, r.st)
	if len(runs) != 1 || runs[0].DiffBytes != nil || runs[0].DiffCut || runs[0].Outcome != OutcomeOK {
		t.Fatalf("runs %+v", runs)
	}
}
