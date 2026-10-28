package automation_test

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestSlackNotesCostGitHubNothing(t *testing.T) {
	wf := sweepWF("nudge", always, "notify.self")
	wf.Provider, wf.Slot = unit.GitHub, "pr"
	r := newRig(t, wf)
	thread := unit.Unit{Provider: "slack", Key: "T0001/C0001/1700000001.000100"}
	noteOnMoved(t, r, func() { r.log.Note(port.TopicSourceEvents, thread) })
	r.put(p1, "a")
	r.sweep()
	if got := r.ran("notify.self"); !slices.Equal(got, []unit.Unit{p1}) {
		t.Errorf("ran %v; a Slack note dropped the pull request's run", got)
	}
	moved := freshCheck(r.trace)
	if moved < 0 {
		t.Fatalf("no rev check before the lock in %v", r.trace)
	}
	for _, c := range r.trace[moved+1:] {
		if c == "units.facts" || c == "locked:units.facts" {
			t.Errorf("revalidation read the pull request's facts again after a Slack note: %v", r.trace[moved:])
		}
	}
}
