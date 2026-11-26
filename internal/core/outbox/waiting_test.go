package outbox

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/gate"
)

// What the tasks wait on is read once per pass, whoever asks: the open gates
// but the draft gates an open message pins, and the approved gates of the
// drafts whose delivery failed.
func TestWaitingIsReadOncePerPass(t *testing.T) {
	t.Parallel()
	var wheres, pins int
	r := WaitingReads{
		Where: func(_ context.Context, q gates.Query) ([]gate.Gate, error) {
			wheres++
			if len(q.Subjects) > 0 {
				return []gate.Gate{{ID: 9, Task: "t-2", Kind: gate.KindDraft, Status: gate.Approved, Subject: "5"}}, nil
			}
			return []gate.Gate{
				{ID: 1, Task: "t-1", Kind: gate.KindQuestion, Status: gate.Open, Subject: "40"},
				{ID: 2, Task: "t-1", Kind: gate.KindDraft, Status: gate.Open, Subject: "41"},
				{ID: 3, Task: "t-1", Kind: gate.KindDraft, Status: gate.Open, Subject: "42"},
			}, nil
		},
		Pinned: func(context.Context) (map[int64]int64, error) {
			pins++
			return map[int64]int64{41: 40}, nil
		},
		Drafts: failedDrafts{{ID: 5}},
	}
	m := decision.Map{Gates: map[gate.Kind]decision.Kind{gate.KindQuestion: "answer", gate.KindDraft: "send-draft"}}
	ctx := coretasks.WithPass(context.Background(), &coretasks.Pass{})
	for range 3 {
		w, err := ReadWaiting(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.Of(m, "t-1", nil); len(got) != 2 || got[0].Gate.ID != 1 || got[1].Gate.ID != 3 {
			t.Fatalf("t-1 waits on %+v, want gates 1 and 3: the pinned draft's gate is the question's", got)
		}
		if got := w.Of(m, "t-2", nil); len(got) != 1 || got[0].Gate.ID != 9 {
			t.Fatalf("t-2 waits on %+v, want the failed delivery's gate", got)
		}
		if w.Pinned[41] != 40 {
			t.Fatalf("pins %v", w.Pinned)
		}
	}
	if wheres != 2 || pins != 1 {
		t.Fatalf("read the gates %d times and the pins %d, want each once per pass", wheres, pins)
	}
}
