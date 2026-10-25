package turn

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/record"
)

func TestHistoryCarriesTheDeclaredArtifact(t *testing.T) {
	note := record.Record{State: record.Done, Summary: "s", Artifact: &record.Artifact{Kind: "author-note", Body: "the note"}}
	b := Book{Unit: r7,
		Turns:   []Turn{{Seq: 1, Intent: "ask-author", State: Done, SentAt: at(0)}, {Seq: 2, Intent: "address", State: Done, SentAt: at(1)}},
		Records: []Record{{TurnSeq: 1, At: at(1), Record: note}, {TurnSeq: 2, At: at(2), Record: note}},
		HistoryArtifact: func(intent string) string {
			if intent == "ask-author" {
				return "author-note"
			}
			return "triage"
		},
	}
	h := b.History(10)
	if h[1].Triage != "the note" || h[0].Triage != "" {
		t.Fatalf("only the declared kind of each intent is carried: %+v", h)
	}
	b.HistoryArtifact = nil
	if h := b.History(10); h[0].Triage != "" || h[1].Triage != "" {
		t.Fatalf("no lookup, no artifact: %+v", h)
	}
}

func TestLastVerdictIsTheNewestPublishedTurnBeyondTheHistory(t *testing.T) {
	approve := record.Record{State: record.Done, Summary: "approved", Outcome: &record.Outcome{Verdict: "APPROVE"}}
	b := Book{Unit: r7,
		Turns:   []Turn{{Seq: 1, Intent: "review", Trigger: ByHand, State: Done, Head: "h0", SentAt: at(0)}},
		Records: []Record{{TurnSeq: 1, Head: "h1", At: at(1), Record: approve}},
	}
	for i := int64(2); i <= 12; i++ {
		b.Turns = append(b.Turns, Turn{Seq: i, Intent: "review", Trigger: Automated, State: Settled, Head: "h2", SentAt: at(int(i))})
		b.Records = append(b.Records, Record{TurnSeq: i, Head: "h2", At: at(int(i)), Record: record.Record{State: record.Settled, Summary: "nothing new"}})
	}
	b.Turns = append(b.Turns, Turn{Seq: 13, Intent: "address", Trigger: ByHand, State: Done, SentAt: at(13)})
	b.Records = append(b.Records, Record{TurnSeq: 13, Head: "h3", At: at(13), Record: approve})
	got, ok := b.LastVerdict("review")
	if !ok || got.Head != "h1" || got.Verdict != "APPROVE" || got.Endorsed != EndorsedByHand || !got.At.Equal(at(1)) {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := (&Book{Unit: r7, Turns: b.Turns[1:12], Records: b.Records[1:12]}).LastVerdict("review"); ok {
		t.Fatal("a settled turn published a verdict")
	}
}
