package facttask_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestRunsLiveCountsLiveSessions(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.task(task.Task{ID: "t-a", Kind: task.KindCapture, State: "next", Category: task.CategoryReady, Title: "a"})
	e.task(task.Task{ID: "t-b", Kind: task.KindCapture, State: "next", Category: task.CategoryReady, Title: "b"})
	a, b := task.UnitOf("t-a"), task.UnitOf("t-b")
	e.sessions = []turn.Session{
		{ID: "s1", Handle: "h1", Unit: a},
		{ID: "s2", Handle: "h2", Unit: a},
		{ID: "s3", Unit: a},
		{ID: "s4", Handle: "h4", Unit: a, ClosedAt: monday},
		{ID: "s5", Handle: "h5", Unit: b},
	}
	if got := e.facts("t-a")["task.runs_live"]; got != 2 {
		t.Errorf("runs_live of t-a = %v, want 2", got)
	}
	if got := e.facts("t-b")["task.runs_live"]; got != 1 {
		t.Errorf("runs_live of t-b = %v, want 1", got)
	}
}
