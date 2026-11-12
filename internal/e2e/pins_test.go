package e2e

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

// TestAQuestionCannotPinADraftARuleReleased (RE5-A): the agent proposes COMMENT, which the review rule releases at
// once, then APPROVE held, and asks a question pinning both. turn_end refuses the record, naming the queued draft;
// the agent proposes COMMENT again with hold and asks with it, and the person's pick of approve posts APPROVE alone.
func TestAQuestionCannotPinADraftARuleReleased(t *testing.T) {
	r := newDraftsRig(t)
	r.review()
	released := r.propose("COMMENT", "Two nits, nothing blocking.", "c2", false)
	approve := r.propose("APPROVE", "", "c2", true)
	asked := func(comment api.AgentDraft) string {
		return fmt.Sprintf(`{"state":"asked","summary":"approve or comment","resume":"post what the user picks","question":%s}`,
			choice("Approve the pull request?", draftOption("approve", "Approve", approve), draftOption("comment", "Comment only", comment)))
	}
	_, err := r.c.EndTurn(r.ctx, r.s.Handle, api.TurnEndRequest{Turn: r.s.Round, PR: draftsURL, Record: []byte(asked(released))})
	var refused interface{ Detail() string }
	if !errors.As(err, &refused) || !strings.Contains(refused.Detail(), fmt.Sprintf("draft %d is queued", released.ID)) {
		t.Fatalf("a question pinning queued draft %d was recorded: %v", released.ID, err)
	}
	r.send()
	comment := r.propose("COMMENT", "Two nits, nothing blocking.", "c2", true)
	r.end(asked(comment))
	r.answer("approve")
	r.send()
	if posted := r.pub.posted(); len(posted) != 2 || !strings.Contains(fmt.Sprint(posted[1:]), "APPROVE") {
		t.Errorf("posted %v: want the released COMMENT, then APPROVE alone", posted)
	}
	r.wantState(comment.ID, "dropped")
}
