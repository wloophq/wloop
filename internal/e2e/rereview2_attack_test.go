package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
)

// as is the rig's client acting for session handle.
func (r *draftsRig) as(handle string) *client.Client {
	c := *r.c
	c.Handle = handle
	return &c
}

// questionGate is the open question gate on the pull request.
func (r *draftsRig) questionGate() (int64, string) {
	r.t.Helper()
	var gs []api.GateView
	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var err error
		if gs, err = r.c.Gates(r.ctx); err != nil {
			r.t.Fatal(err)
		}
		for _, g := range gs {
			if g.Kind != "question" || g.Unit != draftsPR.String() {
				continue
			}
			for _, in := range g.Intents {
				if in.Answer {
					return g.ID, in.ID
				}
			}
		}
	}
	r.t.Fatalf("no open question in %+v", gs)
	return 0, ""
}

// RR2-A: the agent revises the pinned draft after asking; the person's pick of the option pinned at rev 1 must not
// approve rev 2, which they never saw.
func TestRR2OptionDoesNotApproveContentThePersonDidNotSee(t *testing.T) {
	r := newDraftsRig(t)
	r.review()
	d := r.propose("REQUEST_CHANGES", "Seen by the person.", "c2", true)
	r.end(fmt.Sprintf(`{"state":"asked","summary":"s","resume":"r","question":%s}`,
		choice("Post?", draftOption("post", "Post it", d), `{"id":"hold","label":"Not yet","consequence":"nothing"}`)))
	unseen := "Swapped after the question was asked."
	ev := []byte(`{"event":"APPROVE"}`)
	got, err := r.update(api.AgentDraftUpdate{ID: d.ID, Rev: d.Rev, Text: &unseen, Payload: ev})
	if err != nil {
		t.Fatalf("revise after asking: %v", err)
	}
	r.answer("post")
	r.send()
	r.wantPosted()
	if s := r.draft(d.ID).State; s == "queued" || s == "sent" {
		t.Fatalf("the pick of rev %d approved rev %d (%s)", d.Rev, got.Rev, s)
	}
}

// RR2-B: the session that proposed and pinned the draft, which the user drives, answers its own question through
// the option: the user decides through it, so the draft posts.
func TestRR2AProposerTheUserDrivesPicksItsOwnOption(t *testing.T) {
	r := newDraftsRig(t)
	r.review()
	d := r.propose("REQUEST_CHANGES", "Proposed by the session.", "c2", true)
	r.end(fmt.Sprintf(`{"state":"asked","summary":"s","resume":"r","question":%s}`,
		choice("Post?", draftOption("post", "Post it", d), `{"id":"hold","label":"Not yet","consequence":"nothing"}`)))
	id, intent := r.questionGate()
	if _, err := r.as(r.s.Handle).ResolveGate(r.ctx, id, intent, api.TaskRequest{Args: map[string]string{"answer": "post"}, Source: "claude"}); err != nil {
		t.Fatalf("the proposer's pick: %v", err)
	}
	r.send()
	r.wantPosted(fmt.Sprintf("%d REQUEST_CHANGES", d.ID))
}

// RR2-C: a second session revises the draft, the asking turn pins that revision, and the reviser, which the user
// drives, picks the option: the pick answers the question and posts the revision it pinned.
func TestRR2AReviserTheUserDrivesPicksTheOptionPinningItsRevision(t *testing.T) {
	r := newDraftsRig(t)
	other := r.d.Start(t, draftsPR, "review")
	r.review()
	asker := r.s
	d := r.propose("REQUEST_CHANGES", "Proposed by the asker.", "c2", true)
	rs := r.as(other.Handle)
	text := "Revised by the other session."
	rev, err := rs.UpdateDraft(r.ctx, other.Handle, api.AgentDraftUpdate{ID: d.ID, Rev: d.Rev, Text: &text})
	if err != nil {
		t.Skipf("a second session cannot revise the asker's draft here: %v", err)
	}
	r.s = asker
	pinned := d
	pinned.Rev = rev.Rev
	r.end(fmt.Sprintf(`{"state":"asked","summary":"s","resume":"r","question":%s}`,
		choice("Post?", draftOption("post", "Post it", pinned), `{"id":"hold","label":"Not yet","consequence":"nothing"}`)))
	id, intent := r.questionGate()
	if _, err := rs.ResolveGate(r.ctx, id, intent, api.TaskRequest{Args: map[string]string{"answer": "post"}, Source: "claude"}); err != nil {
		t.Fatalf("the reviser's pick: %v", err)
	}
	r.send()
	r.wantPosted(fmt.Sprintf("%d REQUEST_CHANGES", d.ID))
}
