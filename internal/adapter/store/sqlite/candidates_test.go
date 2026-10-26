package sqlite

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/gtd"
)

func TestCandidatesNeverSpliceTheView(t *testing.T) {
	t.Parallel()
	for _, v := range []gtd.View{gtd.ViewInbox, gtd.ViewWaiting, gtd.ViewSomeday} {
		if got, want := candidates(v), `state = '`+string(v)+`'`; got != want {
			t.Errorf("candidates(%q) = %q, want %q", v, got, want)
		}
	}
	hostile := gtd.View("inbox' OR 1=1 --")
	if got := candidates(hostile); got != `0` || strings.Contains(got, "OR 1=1") {
		t.Fatalf("candidates(%q) = %q, want 0", hostile, got)
	}
}
