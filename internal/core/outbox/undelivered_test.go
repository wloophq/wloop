package outbox

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

type failedDrafts []draft.Draft

func (f failedDrafts) List(_ context.Context, filter Filter) ([]draft.Draft, error) {
	if !slices.Equal(filter.States, []draft.State{draft.Failed}) {
		return nil, nil
	}
	return f, nil
}

// The undelivered drafts are the newest approved gates of the drafts whose
// delivery failed, by task.
func TestUndeliveredAreTheApprovedGatesOfTheFailedDrafts(t *testing.T) {
	t.Parallel()
	var asked gates.Query
	where := func(_ context.Context, q gates.Query) ([]gate.Gate, error) {
		asked = q
		return []gate.Gate{
			{ID: 3, Task: "t-1", Kind: gate.KindDraft, Status: gate.Approved, Subject: "5"},
			{ID: 8, Task: "t-1", Kind: gate.KindDraft, Status: gate.Approved, Subject: "5"},
		}, nil
	}
	got, err := Undelivered(context.Background(), failedDrafts{{ID: 5}}, where)
	if err != nil {
		t.Fatal(err)
	}
	if gs := got[task.ID("t-1")]; len(gs) != 1 || gs[0].ID != 8 || !slices.Equal(asked.Subjects, []string{"5"}) {
		t.Fatalf("undelivered %+v, asked %+v; want the newest approved gate of draft 5", got, asked)
	}
}
