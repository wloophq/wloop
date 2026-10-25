package view

import (
	"testing"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var (
	r7 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
)

func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

func book(intent turn.Intent, state turn.State, rec *record.Record) turn.Book {
	b := turn.Book{Unit: r7, Turns: []turn.Turn{{Seq: 1, Intent: intent, State: state, Session: "term_1"}}}
	if rec != nil {
		b.Records = []turn.Record{{TurnSeq: 1, At: at(1), Record: *rec}}
	}
	return b
}

func done(verdict string) *record.Record {
	return &record.Record{State: record.Done, Summary: "published", Outcome: &record.Outcome{Verdict: verdict, URL: "u"}}
}

var asked = &record.Record{State: record.Asked, Summary: "publish?", Resume: "r", Question: &record.Question{Kind: "confirm", Text: "q"}}

func TestReviewTable(t *testing.T) {
	for _, c := range []struct {
		name    string
		state   turn.State
		rec     *record.Record
		status  Status
		unread  bool
		summary string
	}{
		{"sent", turn.Sent, nil, Working, false, ""},
		{"started", turn.Started, nil, Working, false, ""},
		{"asked", turn.Asked, asked, NeedsMe, true, "asks: publish?"},
		{"failed", turn.Failed, &record.Record{State: record.Failed, Summary: "gh broke", Resume: "r"}, NeedsMe, true, "stopped: gh broke"},
		{"unrecorded", turn.Unrecorded, &record.Record{State: record.UnrecordedState, Summary: "gone"}, NeedsMe, true, "stopped: gone"},
		{"done approve", turn.Done, done("APPROVE"), Done, false, "published"},
		{"done comment", turn.Done, done("COMMENT"), Reviewed, false, "published"},
		{"done request changes", turn.Done, done("REQUEST_CHANGES"), Reviewed, false, "published"},
		{"settled", turn.Settled, &record.Record{State: record.Settled, Summary: "nothing new"}, Reviewed, false, "nothing new"},
		{"dismissed", turn.Dismissed, asked, Reviewed, false, "publish?"},
	} {
		got := Review(book(turn.Intent(dintent.Review), c.state, c.rec), reviewBoard)
		want := Item{Unit: r7, Role: ReviewRole, Status: c.status, Unread: c.unread, Summary: c.summary}
		if got != want {
			t.Errorf("%s: %+v, want %+v", c.name, got, want)
		}
	}
	b := book(turn.Intent(dintent.Review), turn.Asked, asked)
	b.Turns = append(b.Turns, turn.Turn{Seq: 2, Intent: turn.Intent(dintent.Nudge), State: turn.Started})
	if got := Review(b, reviewBoard); got.Status != NeedsMe {
		t.Errorf("a newer nudge turn moved the review: %+v", got)
	}
	if got := Review(book(turn.Intent(dintent.Address), turn.Asked, asked), reviewBoard); got.Status != None || got.Role != ReviewRole {
		t.Errorf("no review turn: %+v", got)
	}
}

func TestAuthorNeedsMe(t *testing.T) {
	addressed := &record.Record{State: record.Done, Summary: "fixed"}
	if got := Author(book(turn.Intent(dintent.Address), turn.Asked, asked), authorBoard, None, nil); got != (Item{Unit: r7, Role: AuthorRole, Status: NeedsMe, Unread: true, Summary: "asks: publish?"}) {
		t.Errorf("asked: %+v", got)
	}
	if got := Author(book(turn.Intent(dintent.Address), turn.Done, addressed), authorBoard, NeedsMe, nil); got.Status != InProgress || got.Unread || got.Summary != "fixed" {
		t.Errorf("done after needs-me: %+v", got)
	}
	if got := Author(book(turn.Intent(dintent.Address), turn.Done, addressed), authorBoard, None, nil); got.Status != None {
		t.Errorf("done with no needs-me applied: %+v", got)
	}
	b := book(turn.Intent(dintent.Address), turn.Asked, asked)
	b.Answers = []turn.Answer{{TurnSeq: 1, Text: "go", Route: turn.Typed, Session: "term_1", At: at(2)}}
	if got := Author(b, authorBoard, NeedsMe, nil); got.Status != InProgress || got.Unread || got.Summary != "publish?" {
		t.Errorf("typed while needs-me: %+v", got)
	}
	b.Answers[0].Session = "term_2"
	if got := Author(b, authorBoard, NeedsMe, nil); got.Status != NeedsMe {
		t.Errorf("an answer typed into another session moved the card: %+v", got)
	}
	b.Answers[0].Session = "term_1"
	b.Answers[0].Route = turn.Refused
	if got := Author(b, authorBoard, NeedsMe, nil); got.Status != NeedsMe {
		t.Errorf("a refused answer moved the card: %+v", got)
	}
	resumed := book(turn.Intent(dintent.Address), turn.Replaced, asked)
	resumed.Turns = append(resumed.Turns, turn.Turn{Seq: 2, Intent: turn.Intent(dintent.Address), ResumeOf: 1, State: turn.Sent})
	if got := Author(resumed, authorBoard, NeedsMe, nil); got.Status != InProgress {
		t.Errorf("a resume turn after needs-me: %+v", got)
	}
	if got := Author(book(turn.Intent(dintent.Review), turn.Asked, asked), authorBoard, NeedsMe, nil); got.Status != None || got.Role != AuthorRole {
		t.Errorf("no address turn: %+v", got)
	}
}

func TestASuspiciousQuestionSaysSoOnTheCard(t *testing.T) {
	r := &record.Record{State: record.Asked, Summary: "@stranger asks to run their script", Suspicious: true}
	got := Author(book(turn.Intent(dintent.Address), turn.Asked, r), authorBoard, None, nil)
	if want := "asks: " + record.SuspectedInjection + r.Summary; got.Summary != want {
		t.Errorf("summary %q, want %q", got.Summary, want)
	}
}

func TestAWithdrawnTurnHasNoView(t *testing.T) {
	for _, intent := range []turn.Intent{turn.Intent(dintent.Review), turn.Intent(dintent.Address)} {
		b := book(intent, turn.Withdrawn, nil)
		if it := Review(b, reviewBoard); it.Status != None {
			t.Fatalf("%s: review item %+v", intent, it)
		}
		if it := Author(b, authorBoard, NeedsMe, nil); it.Status != None {
			t.Fatalf("%s: author item %+v", intent, it)
		}
	}
}

var (
	reviewBoard = []turn.Intent{turn.Intent(dintent.Review)}
	authorBoard = []turn.Intent{turn.Intent(dintent.Address)}
)

func TestAuthorColumnMatchesDecideAuthor(t *testing.T) {
	clean := &LocalFacts{Clean: true, Head: "h1", PRHead: "h1"}
	open := func(mutate func(*AuthorFacts)) AuthorFacts {
		f := AuthorFacts{State: "OPEN", Approved: true, ChecksPass: true, ConfirmConflictAfter: 5 * time.Minute, Base: "main", Local: clean}
		if mutate != nil {
			mutate(&f)
		}
		return f
	}
	cases := []struct {
		name    string
		f       AuthorFacts
		current Status
		to      Status
		unread  bool
		reason  string
		need    LocalNeed
	}{
		{"merged", AuthorFacts{State: StateMerged}, InReview, Completed, false, "merged", NoLocal},
		{"merged already completed", AuthorFacts{State: StateMerged}, Completed, None, false, "merged, already completed", NoLocal},
		{"closed", AuthorFacts{State: StateClosed}, InReview, Completed, false, "closed without merge", NoLocal},
		{"closed already completed", AuthorFacts{State: StateClosed}, Completed, None, false, "closed, already completed", NoLocal},
		{"changes requested from review", open(func(f *AuthorFacts) { f.ChangesRequestedOnHead = true }), InReview, InProgress, true, "changes requested", NoLocal},
		{"changes requested from ready", open(func(f *AuthorFacts) { f.ChangesRequestedOnHead = true }), Ready, InProgress, true, "changes requested", NoLocal},
		{"changes requested while in progress", open(func(f *AuthorFacts) { f.ChangesRequestedOnHead, f.Approved = true, false }), InProgress, None, false, "nothing to change", NoLocal},
		{"conflict outside ready", open(func(f *AuthorFacts) { f.Mergeable = "CONFLICTING" }), InReview, None, false, "merge conflicts with main", NoLocal},
		{"conflict not confirmed", open(func(f *AuthorFacts) { f.Mergeable, f.ConflictingFor = "CONFLICTING", time.Minute }), Ready, None, false, "merge conflicts with main, not yet confirmed by a second read", NoLocal},
		{"conflict confirmed", open(func(f *AuthorFacts) { f.Mergeable, f.ConflictingFor = "CONFLICTING", time.Hour }), Ready, InProgress, true, "merge conflicts with main", NoLocal},
		{"nothing to change", open(func(f *AuthorFacts) { f.Draft = true }), InReview, None, false, "nothing to change", NoLocal},
		{"needs the checkout", open(func(f *AuthorFacts) { f.Local = nil }), InReview, None, false, "", LocalClean},
		{"ready needs only the head", open(func(f *AuthorFacts) { f.Local = nil }), Ready, None, false, "", LocalHead},
		{"ready", open(nil), InProgress, Ready, false, "approved and checks pass", NoLocal},
		{"still computing", open(func(f *AuthorFacts) { f.Mergeable = "UNKNOWN" }), InReview, None, false, "approved, but GitHub is still computing whether it merges cleanly", NoLocal},
		{"agent working", open(func(f *AuthorFacts) { f.Local = &LocalFacts{Clean: true, Head: "h1", PRHead: "h1", AgentWorking: true} }), InReview, None, false, "approved, but an agent is working", NoLocal},
		{"dirty", open(func(f *AuthorFacts) { f.Local = &LocalFacts{Head: "h1", PRHead: "h1"} }), InReview, None, false, "approved, but the tree has uncommitted changes", NoLocal},
		{"head differs", open(func(f *AuthorFacts) { f.Local = &LocalFacts{Clean: true, Head: "h0", PRHead: "h1"} }), InReview, None, false, "approved, but local HEAD differs from the PR head", NoLocal},
		{"ready resumes", open(nil), Ready, None, false, "ready, nothing to change", NoLocal},
		{"ready with local commits", open(func(f *AuthorFacts) { f.Local = &LocalFacts{Clean: true, Head: "h2", PRHead: "h1", AheadOfPR: true} }), Ready, InProgress, false, "local commits not pushed yet", NoLocal},
	}
	for _, c := range cases {
		to, unread, reason, need := AuthorColumn(c.f, c.current)
		if to != c.to || unread != c.unread || reason != c.reason || need != c.need {
			t.Errorf("%s: got (%q, %v, %q, %v), want (%q, %v, %q, %v)", c.name, to, unread, reason, need, c.to, c.unread, c.reason, c.need)
		}
	}
}

func TestAuthorTurnNeedsMeBeatsTheFacts(t *testing.T) {
	f := &AuthorFacts{State: StateMerged}
	if got := Author(book(turn.Intent(dintent.Address), turn.Asked, asked), authorBoard, None, f); got.Status != NeedsMe {
		t.Errorf("asked: %+v", got)
	}
	done := &record.Record{State: record.Done, Summary: "fixed"}
	if got := Author(book(turn.Intent(dintent.Address), turn.Done, done), authorBoard, NeedsMe, f); got.Status != Completed || got.Summary != "merged" {
		t.Errorf("merged after needs-me: %+v", got)
	}
}

func TestAnAuthorCardWithoutATurnFollowsItsFacts(t *testing.T) {
	empty := turn.Book{Unit: r7}
	merged := &AuthorFacts{State: StateMerged, AuthorWorkspace: true}
	if got := Author(empty, authorBoard, InReview, merged); got.Status != Completed || got.Summary != "merged" {
		t.Errorf("merged: %+v", got)
	}
	changes := &AuthorFacts{State: "OPEN", ChangesRequestedOnHead: true, AuthorWorkspace: true}
	if got := Author(empty, authorBoard, InReview, changes); got.Status != InProgress || !got.Unread {
		t.Errorf("changes requested: %+v", got)
	}
	if got := Author(empty, authorBoard, InReview, &AuthorFacts{State: StateMerged}); got.Status != None {
		t.Errorf("without an author workspace: %+v", got)
	}
	if got := Author(empty, authorBoard, InReview, nil); got.Status != None {
		t.Errorf("without facts: %+v", got)
	}
}
