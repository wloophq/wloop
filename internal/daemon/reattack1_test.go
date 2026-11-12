package daemon

import (
	"context"
	"fmt"
	"testing"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// RE1-A: a draft the driven session proposed, approved by the person, failed "not confirmed after a restart".
// The session that wrote it attests it was not posted and requeues it: a person's confirm, given by a session.
func TestReattackProposerConfirmsItsOwnDraftWasNotPosted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r, _, mine, _ := finalRig(t)
	d, err := r.o.Propose(ctx, outbox.Proposal{Unit: r7u, Kind: draft.Kind("reply"), Text: "the session's text",
		Origin: draft.Origin{Kind: draft.OriginSession, Session: mine}, Policy: draft.Fixed(draft.Approval{Mode: draft.Send})})
	if err != nil {
		t.Fatal(err)
	}
	d, _ = r.o.Store.Get(ctx, d.ID)
	if d.State != draft.Queued {
		t.Fatalf("draft %s", d.State)
	}
	d.State = draft.Sending
	if d, err = r.o.Store.Update(ctx, d, draft.Queued); err != nil {
		t.Fatal(err)
	}
	d.State, d.Error = draft.Failed, outbox.NotConfirmed
	if d, err = r.o.Store.Update(ctx, d, draft.Sending); err != nil {
		t.Fatal(err)
	}
	rec := postFrom(t, r.s, "term_2", fmt.Sprintf("/api/tasks/draft:%d/retry", d.ID), fmt.Sprintf(`{"source":"claude","args":{"rev":"%d","confirm":"true"}}`, d.Rev))
	got, _ := r.o.Store.Get(ctx, d.ID)
	if rec.Code == 200 && got.State == draft.Queued {
		t.Errorf("session %s, the draft's proposer, confirmed it was not posted and requeued it: %d %s", mine, rec.Code, rec.Body)
	}
}
