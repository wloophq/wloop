package prrow

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/record"
)

// TestANotificationOffersNoPinnedAnswer leaves out of a row's one-tap answers each that approves a pinned
// draft, which a notification does not show.
func TestANotificationOffersNoPinnedAnswer(t *testing.T) {
	ids := func(as []api.AttentionAction) []string {
		var out []string
		for _, a := range as {
			out = append(out, a.ID)
		}
		return out
	}
	pin := &record.DraftRef{ID: 42, Rev: 3}
	choice := &api.QuestionView{Kind: "choice", Recommend: "b", Options: []record.Option{{ID: "a", Label: "Comment"}, {ID: "b", Label: "Approve", Draft: pin}}}
	if got := ids(answerChoices(choice)); !slices.Equal(got, []string{"answer:option:a"}) {
		t.Errorf("choice answers %q, want only the unpinned option", got)
	}
	confirm := &api.QuestionView{Kind: "confirm", Recommend: "yes", Draft: pin}
	if got := ids(answerChoices(confirm)); !slices.Equal(got, []string{"answer:no"}) {
		t.Errorf("confirm answers %q, want only no", got)
	}
}
