package sqlite

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

func TestCandidatesNeverSpliceTheView(t *testing.T) {
	t.Parallel()
	for v, c := range map[task.View]task.Category{task.ViewInbox: task.CategoryInbox, task.ViewWaiting: task.CategoryWaiting} {
		if where, args := candidates(v); where != `category = ?` || !slices.Equal(args, []any{string(c)}) {
			t.Errorf("candidates(%q) = %q %v, want the category bound", v, where, args)
		}
	}
	hostile := task.View("inbox' OR 1=1 --")
	if got, args := candidates(hostile); got != `0` || strings.Contains(got, "OR 1=1") || len(args) != 0 {
		t.Fatalf("candidates(%q) = %q, want 0", hostile, got)
	}
}
