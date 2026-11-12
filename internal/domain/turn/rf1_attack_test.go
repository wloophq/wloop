package turn

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/record"
)

// The agent leaves outcome.draft out and writes the permalink of a rule-sent draft as outcome.url.
func urlOnlyDone() record.Record {
	return record.Record{State: record.Done, Summary: "s", Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://github.com/acme/api/pull/7#pullrequestreview-1"}}
}

func TestAttackByHandURLOnlyIsEndorsed(t *testing.T) {
	if err := urlOnlyDone().ValidateShape(); err != nil {
		t.Fatal(err)
	}
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", Trigger: ByHand, State: Done}},
		Records: []Record{{TurnSeq: 1, At: at(3), Record: urlOnlyDone()}}}
	if got := b.Endorsed(1); got != NotEndorsed {
		t.Fatalf("a url the agent wrote, no draft the user approved: endorsed %q", got)
	}
}

func TestAttackRefusedPinnedPickEndorsesResumedURL(t *testing.T) {
	asked := draftOptions()
	asked.Question.Options[0].Publishes = true
	if err := asked.ValidateShape(); err != nil {
		t.Fatal(err)
	}
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", Trigger: Automated, State: Asked}, {Seq: 2, Intent: "review", Trigger: Automated, State: Done, ResumeOf: 1}},
		Records: []Record{{TurnSeq: 1, At: at(1), Record: asked}, {TurnSeq: 2, At: at(3), Record: urlOnlyDone()}},
		Answers: []Answer{{TurnSeq: 1, OptionID: "approve", Source: Cockpit, Route: Resumed, At: at(2)}}}
	if got := b.Endorsed(2); got != NotEndorsed {
		t.Fatalf("a pick pinned to rev 1 the gates refused, a rule-sent later rev: endorsed %q", got)
	}
}

func TestAttackPublishesConfirmEndorsesResumedURL(t *testing.T) {
	confirm := record.Record{State: record.Asked, Summary: "s", Resume: "r", Question: &record.Question{Kind: "confirm", Text: "Publish?", Publishes: true}}
	if err := confirm.ValidateShape(); err != nil {
		t.Fatal(err)
	}
	b := Book{Unit: r7, Turns: []Turn{{Seq: 1, Intent: "review", Trigger: Automated, State: Asked}, {Seq: 2, Intent: "review", Trigger: Automated, State: Done, ResumeOf: 1}},
		Records: []Record{{TurnSeq: 1, At: at(1), Record: confirm}, {TurnSeq: 2, At: at(3), Record: urlOnlyDone()}},
		Answers: []Answer{{TurnSeq: 1, Yes: true, Source: Cockpit, Route: Resumed, At: at(2)}}}
	if got := b.Endorsed(2); got != NotEndorsed {
		t.Fatalf("a new publishes confirm's yes, a rule-sent draft: endorsed %q", got)
	}
}
