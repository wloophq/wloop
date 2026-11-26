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

// Outside a pass, what one task waits on reads none of the open gates but
// that task's, which its caller read.
func TestWaitingOfOneTaskOutsideAPassReadsItsOwnGates(t *testing.T) {
	t.Parallel()
	var open int
	r := WaitingReads{Where: func(_ context.Context, q gates.Query) ([]gate.Gate, error) {
		if len(q.Subjects) == 0 {
			open++
		}
		return []gate.Gate{{ID: 7, Task: "t-2", Kind: gate.KindQuestion, Status: gate.Open, Subject: "70"}}, nil
	}}
	own := []gate.Gate{{ID: 1, Task: "t-1", Kind: gate.KindQuestion, Status: gate.Open, Subject: "40"}}
	m := decision.Map{Gates: map[gate.Kind]decision.Kind{gate.KindQuestion: "answer"}}
	w, err := ReadWaitingOf(context.Background(), r, "t-1", own)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Of(m, "t-1", nil); len(got) != 1 || got[0].Gate.ID != 1 || open != 0 {
		t.Fatalf("t-1 waits on %+v after %d reads of every open gate, want its own gate and none", got, open)
	}
	ctx := coretasks.WithPass(context.Background(), &coretasks.Pass{})
	if w, err = ReadWaitingOf(ctx, r, "t-1", own); err != nil {
		t.Fatal(err)
	}
	if got := w.Of(m, "t-2", nil); len(got) != 1 || open != 1 {
		t.Fatalf("in a pass, t-2 waits on %+v after %d reads of every open gate, want the pass's one read", got, open)
	}
}
