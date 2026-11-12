package messages_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// The confirm the user sees pins nothing the message keeps: the agent revises #42 and records the same
// confirm pinned to the new rev (or adds a draft to a confirm that had none). The message is unchanged, so
// the user's yes on it approves a rev, or a draft, the question they answered never named.
func TestReattackConfirmPinMovesUnderTheSameMessage(t *testing.T) {
	for _, c := range []struct {
		name  string
		first *record.DraftRef
	}{
		{"pinned rev 1, answered after the agent re-pinned rev 2", &record.DraftRef{ID: 42, Rev: 1}},
		{"no draft, answered after the agent added one", nil},
	} {
		r := newRig(t)
		od := newOptionDrafts(t, r)
		od.put(heldDraft(42, r7, 1, 1), "term_1")
		r.started(r7)
		r.record(t, r7, 1, askedRecord(&record.Question{Kind: "confirm", Text: "Post the review?", Draft: c.first}))
		r.live()
		r.reconcile(t, r7)
		seen := r.only(t)

		od.put(heldDraft(42, r7, 1, 2), "term_1")
		r.record(t, r7, 1, askedRecord(&record.Question{Kind: "confirm", Text: "Post the review?", Draft: &record.DraftRef{ID: 42, Rev: 2}}))
		r.reconcile(t, r7)
		now := r.only(t)
		route, err := r.answer(now, messages.Answer{Yes: true, Source: turn.Cockpit})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if now.ID == seen.ID && route.Route == turn.Approved {
			t.Errorf("%s: message %d unchanged, yes approved %+v", c.name, now.ID, od.approvals)
		}
	}
}
