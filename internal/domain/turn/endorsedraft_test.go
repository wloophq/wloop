package turn

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/record"
)

func draftOptions() record.Record {
	return record.Record{State: record.Asked, Summary: "s", Resume: "r", Question: &record.Question{Kind: "choice", Text: "q", Options: []record.Option{
		{ID: "approve", Label: "Approve", Consequence: "c", Draft: &record.DraftRef{ID: 42, Rev: 1}},
		{ID: "comment", Label: "Comment", Consequence: "c", Draft: &record.DraftRef{ID: 43, Rev: 1}},
	}}}
}

func sentDraft(approved bool) record.Record {
	return record.Record{State: record.Done, Summary: "s", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "u", Draft: &record.DraftRef{ID: 42, Rev: 1}, Approved: approved}}
}

func TestEndorsedByApprovedDraft(t *testing.T) {
	for _, trigger := range []Trigger{Automated, ByHand} {
		b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", Trigger: trigger, State: Done}},
			Records: []Record{{TurnSeq: 1, At: at(1), Record: draftOptions()}, {TurnSeq: 1, At: at(3), Record: sentDraft(true)}},
			Answers: []Answer{{TurnSeq: 1, OptionID: "approve", Source: Cockpit, Route: Approved, At: at(2)}}}
		if got := b.Endorsed(1); got != EndorsedApproved {
			t.Errorf("%s: %q, want %q", trigger, got, EndorsedApproved)
		}
	}
}

func TestNotEndorsedByRuleRelease(t *testing.T) {
	for _, c := range []struct {
		name    string
		trigger Trigger
		answers []Answer
	}{
		{"a rule sent it", Automated, nil},
		{"a rule sent a by-hand turn's draft", ByHand, nil},
		{"the user picked the option, a rule sent the draft", Automated, []Answer{{TurnSeq: 1, OptionID: "approve", Source: Cockpit, Route: Approved, At: at(2)}}},
		{"a typed publishing answer, a rule sent the draft", Automated, []Answer{{TurnSeq: 1, OptionID: "approve", Source: Cockpit, Route: Typed, At: at(2)}}},
	} {
		asked := draftOptions()
		asked.Question.Options[0].Publishes = true
		b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", Trigger: c.trigger, State: Done}},
			Records: []Record{{TurnSeq: 1, At: at(1), Record: asked}, {TurnSeq: 1, At: at(3), Record: sentDraft(false)}}, Answers: c.answers}
		if got := b.Endorsed(1); got != NotEndorsed {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}
