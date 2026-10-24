package instructions

import (
	"slices"
	"strings"
	"testing"
	"time"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

func rec(seq int64, m int, head string, r record.Record) turn.Record {
	return turn.Record{TurnSeq: seq, At: at(m), Head: head, Hash: r.Hash(), Record: r}
}

func book() turn.Book {
	triage := strings.Repeat("t", 2000)
	return turn.Book{
		HistoryArtifact: func(intent string) string {
			if intent == "address" {
				return "triage"
			}
			return ""
		},
		Turns: []turn.Turn{
			{Seq: 1, Intent: turn.Intent(dintent.Review), ID: "r1", Trigger: turn.ByHand, State: turn.Done, Head: "h1", SentAt: at(0)},
			{Seq: 2, Intent: turn.Intent(dintent.Address), ID: "a1", Trigger: turn.Automated, State: turn.Done, Head: "h2", Guidance: "skip the nit", SentAt: at(10)},
			{Seq: 3, Intent: turn.Intent(dintent.Review), ID: "r2", Trigger: turn.Automated, State: turn.Asked, Head: "h3", SentAt: at(20)},
		},
		Records: []turn.Record{
			rec(1, 1, "h1", record.Record{State: record.Done, Summary: "approved", Next: "the docs", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "u"}}),
			rec(2, 11, "h2b", record.Record{State: record.Done, Summary: "triaged", Artifact: &record.Artifact{Kind: "triage", Body: triage}}),
			rec(3, 21, "h3", record.Record{State: record.Asked, Summary: "publish?", Resume: "draft ready",
				Outcome:  &record.Outcome{Verdict: "APPROVE", Blocking: 1, NonBlocking: 2},
				Question: &record.Question{Kind: "choice", Text: "Publish it?", Recommend: "publish", Options: []record.Option{{ID: "publish", Label: "Publish", Consequence: "c", Publishes: true}, {ID: "hold", Label: "Hold", Consequence: "c"}}},
				Artifact: &record.Artifact{Kind: "review-draft", Body: "the body", Items: []record.Item{{Path: "a.go", Line: 4, Body: "nit"}}}}),
		},
		Answers: []turn.Answer{{TurnSeq: 2, Text: "drop it", Source: turn.Cockpit, Route: turn.Typed, At: at(12)}},
	}
}

func TestHistoryMapsEndorsementTriageAndWhatTheUserSaid(t *testing.T) {
	h := History(book(), 10, nil)
	if len(h) != 3 || h[0].Intent != "review" || h[0].When != at(20) || h[2].When != at(0) {
		t.Fatalf("newest first: %+v", h)
	}
	first := h[2]
	if first.Verdict != "APPROVE" || first.Endorsed != turn.EndorsedByHand || first.Summary != "approved" || first.Next != "the docs" || first.Trigger != "by-hand" || first.State != "done" {
		t.Fatalf("%+v", first)
	}
	address := h[1]
	if len([]rune(address.Triage)) != 1500 || address.Head != "h2b" || address.Endorsed != "" {
		t.Fatalf("triage %d, %+v", len(address.Triage), address)
	}
	if !slices.Equal(address.Said, []string{"drop it", "skip the nit"}) {
		t.Fatalf("said %q", address.Said)
	}
	if h[0].Verdict != "" || h[0].Endorsed != "" {
		t.Fatalf("a held draft is no verdict: %+v", h[0])
	}
	if len(History(book(), 2, nil)) != 2 {
		t.Fatal("limit")
	}
}

func TestEarlierLeavesOutTheTurnAndThoseAfterIt(t *testing.T) {
	h := History(Earlier(book(), 3), 10, nil)
	if len(h) != 2 || h[0].Intent != "address" {
		t.Fatalf("%+v", h)
	}
}

func TestResumeTakesTheLastAskedRecord(t *testing.T) {
	r := ResumeOf(book(), 3, "Publish (option publish)", "h4", "ignored")
	if r == nil || r.Question == nil || r.Question.Text != "Publish it?" || r.Question.Recommend != "publish" ||
		!slices.Equal(r.Question.Options, []Option{{ID: "publish", Label: "Publish", Publishes: true}, {ID: "hold", Label: "Hold"}}) {
		t.Fatalf("%+v", r)
	}
	if r.Verdict != "APPROVE" || r.Blocking != 1 || r.NonBlocking != 2 || r.ArtifactKind != "review-draft" || r.ArtifactBody != "the body" ||
		!slices.Equal(r.Items, []Item{{Path: "a.go", Line: 4, Body: "nit"}}) {
		t.Fatalf("%+v", r)
	}
	if r.Notes != "draft ready" || r.Head != "h3" || r.CurrentHead != "h4" || r.Answer != "Publish (option publish)" {
		t.Fatalf("%+v", r)
	}
}

func TestResumeOfAnUnrecordedTurnReadsTheLastMessage(t *testing.T) {
	b := book()
	b.Turns = append(b.Turns, turn.Turn{Seq: 4, Intent: turn.Intent(dintent.Address), ID: "a2", Trigger: turn.ByHand, State: turn.Unrecorded, Head: "h5", SentAt: at(30)})
	b.Records = append(b.Records, rec(4, 31, "", record.Unrecorded("cut message")))
	r := ResumeOf(b, 4, "", "h5", "I pushed two commits and was about to reply.")
	if r == nil || r.Notes != "I pushed two commits and was about to reply." || r.Question != nil || r.ArtifactKind != "" || r.Head != "h5" {
		t.Fatalf("%+v", r)
	}
}

func TestResumeOfNoTurnIsNil(t *testing.T) {
	if ResumeOf(book(), 9, "", "", "") != nil {
		t.Fatal("no such turn")
	}
}
