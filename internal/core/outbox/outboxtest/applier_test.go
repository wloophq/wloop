package outboxtest

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
)

func TestTheFakeAppliesADraftOnce(t *testing.T) {
	t.Parallel()
	a := &Applier{}
	d := draft.Draft{ID: 4, State: draft.Sent, Effects: []string{"entry.nudged"}}
	a.SetPending(d)
	for range 2 {
		if err := a.Apply(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.Applied(); len(got) != 1 || got[0].ID != 4 {
		t.Fatalf("applied %+v", got)
	}
	if p, _ := a.Pending(context.Background()); len(p) != 0 {
		t.Fatalf("pending %+v", p)
	}
}
