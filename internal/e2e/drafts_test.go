package e2e

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
)

const route = "T7: /api/agent/drafts is not served (405)"

func TestDrafts(t *testing.T) {
	t.Run("S1", func(t *testing.T) {
		waits(t, "T13: cli.ServeForTest", route, "T3: a rule releases the draft at the rev it approved", "T9: the send fills the turn's outcome.url",
			"T10: the review intent's rule that sends REQUEST_CHANGES")
		r := newDraftsRig(t)
		r.review()
		d := r.propose("REQUEST_CHANGES", "The migration drops a column still read by the export.", "c2", false)
		r.end(doneOn("REQUEST_CHANGES", d))
		r.wantState(d.ID, "queued")
		r.send()
		r.wantPosted(fmt.Sprintf("%d REQUEST_CHANGES", d.ID))
		sent := r.wantState(d.ID, "sent")
		if url := r.lastOutcomeURL(); url == "" || url != sent.Permalink {
			t.Fatalf("the turn's url %q, want the post's %q", url, sent.Permalink)
		}
		if b := r.book(); len(b.Turns) != 1 || b.Turns[0].State != dturn.Done {
			t.Fatalf("turns %+v", b.Turns)
		}
	})

	t.Run("S2", func(t *testing.T) {
		waits(t, "T13: cli.ServeForTest", route, "T3: gates.ApproveOption panics", "T9: picking an option approves its draft and the send completes the asked turn")
		r := newDraftsRig(t)
		r.review()
		approve := r.propose("APPROVE", "", "c2", false)
		comment := r.propose("COMMENT", "Two nits, nothing blocking.", "c2", false)
		r.wantState(approve.ID, "held")
		r.wantState(comment.ID, "held")
		r.end(fmt.Sprintf(`{"state":"asked","summary":"approve or comment","resume":"post what the user picks","question":%s}`,
			choice("Approve the pull request?", draftOption("approve", "Approve", approve), draftOption("comment", "Comment only", comment))))
		r.answer("approve")
		r.wantState(approve.ID, "queued")
		if d := r.wantState(comment.ID, "dropped"); d.Reason != "other_option" {
			t.Fatalf("the other option's draft dropped for %q", d.Reason)
		}
		r.send()
		r.wantPosted(fmt.Sprintf("%d APPROVE", approve.ID))
		sent := r.wantState(approve.ID, "sent")
		if b := r.book(); len(b.Turns) != 1 || b.Turns[0].State != dturn.Done {
			t.Fatalf("turns %+v", b.Turns)
		}
		if url := r.lastOutcomeURL(); url != sent.Permalink {
			t.Fatalf("the turn's url %q, want %q", url, sent.Permalink)
		}
	})

	t.Run("S3", func(t *testing.T) {
		waits(t, "T13: cli.ServeForTest", route, "T3: Amend of a payload at a rev", "T8: the card's payload edit (args rev and payload on the draft's edit action)")
		r := newDraftsRig(t)
		r.review()
		d := r.propose("APPROVE", "", "c2", false)
		r.end(doneOn("APPROVE", d))
		if err := r.editPayload(d.ID, d.Rev, `{"event":"COMMENT"}`); err != nil {
			t.Fatalf("edit: %v", err)
		}
		edited := r.wantState(d.ID, "held")
		if edited.Rev != d.Rev+1 {
			t.Fatalf("rev %d after the edit, want %d", edited.Rev, d.Rev+1)
		}
		if err := r.approve(d.ID, d.Rev); err == nil {
			t.Fatal("the gate approved the rev the person had edited away")
		}
		if err := r.approve(d.ID, edited.Rev); err != nil {
			t.Fatalf("approve rev %d: %v", edited.Rev, err)
		}
		r.send()
		r.wantPosted(fmt.Sprintf("%d COMMENT", d.ID))
		if b := r.book(); len(b.Turns) != 1 {
			t.Fatalf("turns %+v", b.Turns)
		}
	})

	t.Run("S4", func(t *testing.T) {
		waits(t, "T13: cli.ServeForTest", route, "T2: the store's Revisions panics", "T3: AgentRead and AgentAmend panic")
		r := newDraftsRig(t)
		r.review()
		d := r.propose("REQUEST_CHANGES", "The export reads a dropped column.", "c2", true)
		r.end(fmt.Sprintf(`{"state":"asked","summary":"requests changes","resume":"revise the review as asked","question":%s}`,
			choice("Post this review?", draftOption("post", "Post it", d), `{"id":"hold","label":"Not yet","consequence":"nothing is posted"}`)))
		r.answer("Mention the backfill job too.")
		b := r.book()
		if len(b.Turns) != 2 || b.Turns[1].ResumeOf != b.Turns[0].Seq {
			t.Fatalf("turns %+v", b.Turns)
		}
		r.resumed()
		read, err := r.c.ReadDraft(r.ctx, r.s.Handle, d.ID)
		if err != nil || read.Rev != d.Rev || len(read.Revisions) != 1 {
			t.Fatalf("read %+v %v", read, err)
		}
		text := read.Text + " The backfill job reads it too."
		got, err := r.update(api.AgentDraftUpdate{ID: d.ID, Rev: read.Rev, Text: &text})
		if err != nil || got.Rev != d.Rev+1 || got.State != "held" {
			t.Fatalf("update %+v %v", got, err)
		}
		if _, err := r.update(api.AgentDraftUpdate{ID: d.ID, Rev: read.Rev, Text: &text}); err == nil {
			t.Fatal("an update at the rev already revised was taken")
		}
		r.send()
		r.wantPosted()
		if err := r.approve(d.ID, got.Rev); err != nil {
			t.Fatal(err)
		}
		r.send()
		r.wantPosted(fmt.Sprintf("%d REQUEST_CHANGES", d.ID))
	})

	t.Run("S5", func(t *testing.T) {
		waits(t, "T13: cli.ServeForTest", route, "T3: hold kept through the rules, and gates.ApproveOption", "T9: picking an option approves its draft")
		r := newDraftsRig(t)
		r.review()
		changes := r.propose("REQUEST_CHANGES", "The export may read a dropped column.", "c2", true)
		comment := r.propose("COMMENT", "Does the export still read the dropped column?", "c2", true)
		r.wantState(changes.ID, "held")
		r.wantState(comment.ID, "held")
		r.end(fmt.Sprintf(`{"state":"asked","summary":"unsure the column is still read","resume":"post what the user picks","question":%s}`,
			choice("Request changes or ask?", draftOption("changes", "Request changes", changes), draftOption("ask", "Ask a question", comment))))
		r.send()
		r.wantPosted()
		r.answer("ask")
		if d := r.wantState(changes.ID, "dropped"); d.Reason != "other_option" {
			t.Fatalf("the other option's draft dropped for %q", d.Reason)
		}
		r.send()
		r.wantPosted(fmt.Sprintf("%d COMMENT", comment.ID))
	})

	t.Run("S6", func(t *testing.T) {
		waits(t, "T13: cli.ServeForTest", route, "T3: approve refuses a stale draft and an update's basis is checked live")
		r := newDraftsRig(t)
		r.review()
		d := r.propose("APPROVE", "", "c2", false)
		r.end(doneOn("APPROVE", d))
		r.bases.Set(draftsPR, "c3")
		r.pub.push("c3")
		if err := r.approve(d.ID, d.Rev); err == nil {
			t.Fatal("a stale draft was approved")
		}
		r.send()
		r.wantPosted()
		basis := "c3"
		got, err := r.update(api.AgentDraftUpdate{ID: d.ID, Rev: d.Rev, Basis: &basis})
		if err != nil || got.Basis != "c3" || got.State != "held" {
			t.Fatalf("update %+v %v", got, err)
		}
		if err := r.approve(d.ID, got.Rev); err != nil {
			t.Fatal(err)
		}
		r.send()
		r.wantPosted(fmt.Sprintf("%d APPROVE", d.ID))
	})

	t.Run("S7", func(t *testing.T) {
		waits(t, "T13: cli.ServeForTest", route, "T3: an agent revision supersedes the gate and puts a failed draft back to held",
			"T10: the review intent's rule that sends REQUEST_CHANGES")
		r := newDraftsRig(t)
		r.review()
		d := r.propose("REQUEST_CHANGES", "The export reads a dropped column.", "c2", false)
		r.end(doneOn("REQUEST_CHANGES", d))
		r.wantState(d.ID, "queued")
		r.pub.push("c3")
		r.bases.Set(draftsPR, "c3")
		r.send()
		r.wantPosted()
		r.wantState(d.ID, "failed")
		basis := "c3"
		got, err := r.update(api.AgentDraftUpdate{ID: d.ID, Rev: d.Rev, Payload: json.RawMessage(`{"event":"APPROVE"}`), Basis: &basis})
		if err != nil || got.State != "held" {
			t.Fatalf("update %+v %v", got, err)
		}
		r.send()
		r.wantPosted()
		if g := r.draft(d.ID); g.Gate == 0 || g.Deadline != nil {
			t.Fatalf("the revision's gate %d deadline %v, want one that waits", g.Gate, g.Deadline)
		}
		if err := r.approve(d.ID, got.Rev); err != nil {
			t.Fatal(err)
		}
		r.send()
		r.wantPosted(fmt.Sprintf("%d APPROVE", d.ID))
	})

	t.Run("S8", func(t *testing.T) {
		waits(t, "T13: cli.ServeForTest", route, "T9: draft.failed reopens the round with the draft's pointer", "T10: the review workflow on draft.failed")
		r := newDraftsRig(t)
		r.review()
		withInline := json.RawMessage(`{"event":"REQUEST_CHANGES","comments":[{"path":"export.go","line":40,"body":"This reads the dropped column."}]}`)
		d, err := r.c.ProposeDraft(r.ctx, r.s.Handle, api.AgentDraftProposal{Kind: "review", Text: "One blocking finding.", Payload: withInline, Basis: "c2"})
		if err != nil {
			t.Fatal(err)
		}
		r.end(doneOn("REQUEST_CHANGES", d))
		const refused = "Unprocessable Entity: pull_request_review_thread.line must be part of the diff"
		r.pub.fail(fmt.Errorf("%s", refused))
		r.send()
		if failed := r.wantState(d.ID, "failed"); !strings.Contains(failed.Error, refused) {
			t.Fatalf("the draft failed with %q, want GitHub's message", failed.Error)
		}
		turns := len(r.book().Turns)
		if err := r.d.Sweep(r.ctx); err != nil {
			t.Fatalf("sweep: %v", err)
		}
		if b := r.book(); len(b.Turns) != turns+1 {
			t.Fatalf("the failed send reopened no round: turns %+v", b.Turns)
		}
		r.resumed()
		if text := r.d.Instructions(t, r.s.Round); !strings.Contains(text, strconv.FormatInt(d.ID, 10)) || strings.Contains(text, "This reads the dropped column.") {
			t.Fatalf("the round's instructions carry no pointer to draft %d, or carry its text:\n%s", d.ID, text)
		}
		text := "One blocking finding: export.go:40 reads the dropped column."
		got, err := r.update(api.AgentDraftUpdate{ID: d.ID, Rev: d.Rev, Text: &text, Payload: json.RawMessage(`{"event":"REQUEST_CHANGES"}`)})
		if err != nil || got.State != "held" {
			t.Fatalf("update %+v %v", got, err)
		}
		if err := r.approve(d.ID, got.Rev); err != nil {
			t.Fatal(err)
		}
		r.send()
		r.wantPosted(fmt.Sprintf("%d REQUEST_CHANGES", d.ID))
	})
}
