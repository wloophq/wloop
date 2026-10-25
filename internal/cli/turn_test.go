package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/instructions"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestPriorReviewIsYourLatestHeadAndVerdict(t *testing.T) {
	t.Parallel()
	at := func(h int) time.Time { return time.Date(2026, 10, 8, h, 0, 0, 0, time.UTC) }
	got := priorReview([]pr.Review{
		{State: pr.ReviewChangesRequested, ByViewer: true, SubmittedAt: at(10), CommitOid: "c2"},
		{State: pr.ReviewCommented, ByViewer: true, SubmittedAt: at(12), CommitOid: "c3"},
		{State: pr.ReviewApproved, SubmittedAt: at(13), CommitOid: "c4"},
		{State: "PENDING", ByViewer: true, CommitOid: "c5"},
	})
	if got == nil || got.Verdict != pr.ReviewChangesRequested || got.Head != "c3" || !got.At.Equal(at(12)) {
		t.Fatalf("%+v", got)
	}
	if priorReview([]pr.Review{{State: pr.ReviewApproved, SubmittedAt: at(9)}}) != nil {
		t.Fatal("someone else's review counted as yours")
	}
	got = priorReview([]pr.Review{
		{State: pr.ReviewCommented, ByViewer: true, SubmittedAt: at(10), CommitOid: "c2"},
		{State: pr.ReviewApproved, ByViewer: true, SubmittedAt: at(11)},
	})
	if got == nil || got.Head != "c2" || got.Verdict != pr.ReviewApproved {
		t.Fatalf("a review without a commit took the head: %+v", got)
	}
	got = priorReview([]pr.Review{{State: pr.ReviewApproved, ByViewer: true, SubmittedAt: at(11)}})
	if got == nil || got.Head != "" || got.Verdict != pr.ReviewApproved {
		t.Fatalf("%+v", got)
	}
}

func TestTurnPRKeepsTheFactsATurnReads(t *testing.T) {
	t.Parallel()
	p := turnPR(&github.PullRequest{Facts: pr.Facts{Title: "Add x", Author: "ana", AuthorAssociation: "MEMBER", HeadRefOid: "c2", BaseRefName: "main", ViewerRequested: true}})
	if p.Title != "Add x" || p.Author != "ana" || p.Association != "MEMBER" || p.Head != "c2" || p.Base != "main" || p.Prior != nil || !p.ReviewRequested {
		t.Fatalf("%+v", p)
	}
}

var promptRe = regexp.MustCompile("^(.*?) Run `[^`]* instructions (review|address|nudge|probe) --turn ([0-9a-f]{12}) (\\S+)` and follow what it prints\\.$")

// promptOf parses a round prompt: its headline, intent, url and turn id.
func promptOf(t *testing.T, text string) (headline, intent, url, id string) {
	t.Helper()
	m := promptRe.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("not a round prompt: %q", text)
	}
	return m[1], m[2], m[4], m[3]
}

// turnText is the stored text of the turn a round prompt points to.
func turnText(t *testing.T, te *testEnv, prompt string) string {
	t.Helper()
	_, _, _, id := promptOf(t, prompt)
	st := te.Store
	got, ok, err := st.Turn(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("turn %s: %v %v", id, ok, err)
	}
	return got.Text
}

func TestASentTurnIsReadByItsRound(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	sent, err := te.sendTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.ByHand, Unit: r7.Unit(), Payload: &turn.PR{Title: "Add x"}, Guidance: "look at the migration first"})
	if err != nil {
		t.Fatal(err)
	}
	if h, intent, url, id := promptOf(t, sent.Prompt); h != "First review of acme/api#7, asked for by you." || intent != "review" || url != r7.URL() || id != sent.ID {
		t.Fatalf("%q", sent.Prompt)
	}
	agentDaemon(t, te)
	if code := te.run("instructions", "review", "--turn", sent.ID, r7.URL()); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if te.out.String() != sent.Text || !strings.Contains(sent.Text, "## Guidance") || !strings.HasPrefix(sent.Text, "Instructions "+sent.ID+" for review in acme/api.") {
		t.Fatalf("%s", te.out)
	}
}

func TestAStoredTurnSurvivesItsProfileDirectoryGoingAway(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "intent=review.md"), []byte("USER LAYER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	te.cfg.Profile = dir
	sent, err := te.sendTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Unit: r7.Unit(), Payload: &turn.PR{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	agentDaemon(t, te)
	if code := te.run("instructions", "review", r7.URL(), "--turn", sent.ID); code != 0 || !strings.Contains(te.out.String(), "USER LAYER") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

func TestAFollowUpDiffsFromTheHeadItsRecordPublishedOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	drifted := &turn.PR{Title: "Add x", Head: "c9", Prior: &turn.PriorReview{Verdict: pr.ReviewApproved, Head: "m8"}}
	te := newTestEnv(t, &fake.Manager{})
	sent, err := te.sendTurn(ctx, te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.Automated, Planned: turn.PlannedFollowUp, Unit: r7.Unit(), Payload: drifted})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent.Text, "git diff m8..HEAD") {
		t.Fatalf("without a record the review's commit is the anchor:\n%s", sent.Text)
	}

	te = newTestEnv(t, &fake.Manager{})
	ref := pr.Ref(r7)
	b := dturn.Book{Unit: ref.Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Review), ID: "r1", Trigger: dturn.ByHand, Head: "c1", State: dturn.Done, SentAt: testNow}}}
	saved, err := te.repo.Save(ctx, b, port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "r1", At: testNow})
	if err != nil {
		t.Fatal(err)
	}
	saved.Records = []dturn.Record{{TurnSeq: saved.Turns[0].Seq, Head: "c3", Hash: "h", At: testNow,
		Record: record.Record{State: record.Done, Summary: "approved", Outcome: &record.Outcome{Verdict: "APPROVE"}}}}
	if _, err := te.repo.Save(ctx, saved); err != nil {
		t.Fatal(err)
	}
	sent, err = te.sendTurn(ctx, te.cfg, turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.Automated, Planned: turn.PlannedFollowUp, Unit: r7.Unit(), Payload: drifted})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sent.Text, "Your last review covered `c3`") || !strings.Contains(sent.Text, "git diff c3..HEAD") {
		t.Fatalf("the recorded head did not win over the drifted commit:\n%s", sent.Text)
	}
	resumed := turn.Request{Intent: turn.Intent(dintent.Review), Trigger: turn.Automated, Planned: turn.PlannedFollowUp, Unit: r7.Unit(), Payload: drifted, History: []instructions.PastTurn{}}
	if sent, err = te.sendTurn(ctx, te.cfg, resumed); err != nil || !strings.Contains(sent.Text, "git diff c3..HEAD") {
		t.Fatalf("a turn that brings its history lost the recorded head: %v\n%s", err, sent.Text)
	}
}
