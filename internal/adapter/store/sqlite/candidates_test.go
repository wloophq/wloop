package sqlite

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

func TestCandidatesNeverSpliceTheView(t *testing.T) {
	t.Parallel()
	for _, v := range []task.View{task.ViewInbox, task.ViewWaiting, task.ViewSomeday} {
		if got, want := candidates(v), `state = '`+string(v)+`'`; got != want {
			t.Errorf("candidates(%q) = %q, want %q", v, got, want)
		}
	}
	hostile := task.View("inbox' OR 1=1 --")
	if got := candidates(hostile); got != `0` || strings.Contains(got, "OR 1=1") {
		t.Fatalf("candidates(%q) = %q, want 0", hostile, got)
	}
}
