package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/draft"
)

// TestAPickSettlesEveryOtherOptionsDraft: when the person's pick answers a question, the draft of every other
// option is withdrawn whatever its state, a failed or queued one too, so none posts or is retried after the
// question decided against it; a sent one stays as it is.
func TestAPickSettlesEveryOtherOptionsDraft(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	te, _, _ := servedOutboxOf(t, &basingPublisher{})
	o := optionDrafts{draftGates{te.Env, te.db, t.Logf}}
	for _, state := range []draft.State{draft.Held, draft.Queued, draft.Failed} {
		d := insertDraft(t, te.db, draft.Draft{Unit: threadUnit, Text: string(state), State: state})
		if err := o.WithdrawOther(ctx, int64(d.ID), messages.ReasonOtherOption); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if got, _ := te.db.Drafts().Get(ctx, d.ID); got.State != draft.Dropped || got.Reason != messages.ReasonOtherOption {
			t.Errorf("a %s draft of another option is %s (%q), want dropped", state, got.State, got.Reason)
		}
	}
	sent := insertDraft(t, te.db, draft.Draft{Unit: threadUnit, Text: "sent", State: draft.Sent})
	_ = o.WithdrawOther(ctx, int64(sent.ID), messages.ReasonOtherOption)
	if got, _ := te.db.Drafts().Get(ctx, sent.ID); got.State != draft.Sent {
		t.Errorf("a sent draft of another option is %s", got.State)
	}
}
