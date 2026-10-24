// Package instructions is what a turn's instructions carry from the loop's
// book: the earlier turns and, for a resume turn, what the stopped session
// left. Plain data; the templates of internal/turn render it.
package instructions

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// HistoryMax is how many earlier turns the instructions carry.
const HistoryMax = 10

// PastTurn is one earlier turn. The first row of fields is the loop's, the
// second an agent's, Said the user's.
type PastTurn struct {
	When                                            time.Time
	Intent, Trigger, State, Verdict, Head, Endorsed string
	Summary, Next, Triage                           string
	Said                                            []string
	// Ledger is one line per op of the turn's records with its outcome.
	Ledger []string
}

// OpenItem is one open item of the unit, as its instructions list it.
type OpenItem struct{ ID, Kind, Summary, WaitingOn, Since string }

type Option struct {
	ID, Label string
	Publishes bool
}

type Question struct {
	Text, Recommend string
	Options         []Option
}

type Item struct {
	Path string
	Line int
	Body string
}

// Resume is what a session that stopped left for the next one.
type Resume struct {
	Question                         *Question
	Verdict                          string
	Blocking, NonBlocking            int
	ArtifactKind, ArtifactBody       string
	Items                            []Item
	Notes, Head, CurrentHead, Answer string
}

// History is b's earlier turns, each naming its ledger ops with the outcome
// log holds for them.
func History(b turn.Book, limit int, log []turn.OpOutcome) []PastTurn {
	var out []PastTurn
	for _, e := range b.HistoryWith(limit, log) {
		out = append(out, PastTurn(e))
	}
	return out
}

// Earlier is the book without turn seq and the turns sent after it, for the
// history of seq's own instructions.
func Earlier(b turn.Book, seq int64) turn.Book {
	var turns []turn.Turn
	for _, t := range b.Turns {
		if t.Seq < seq {
			turns = append(turns, t)
		}
	}
	b.Turns = turns
	return b
}

// ResumeOf is what turn seq left: its last asked record's question, draft and
// artifact, and the notes of its last record, or lastMessage when the
// session stopped without recording. nil when the book has no such turn.
func ResumeOf(b turn.Book, seq int64, answer, currentHead, lastMessage string) *Resume {
	chain := b.Chain(seq)
	if len(chain) == 0 {
		return nil
	}
	t := chain[0]
	r := &Resume{Head: t.Head, CurrentHead: currentHead, Answer: answer}
	if last, ok := b.LastRecord(seq); ok {
		r.Notes = last.Record.Resume
		if last.Head != "" {
			r.Head = last.Head
		}
	}
	if (t.State == turn.Unrecorded || r.Notes == "") && lastMessage != "" {
		r.Notes = lastMessage
	}
	if asked, ok := lastAsked(b, seq); ok {
		fill(r, asked)
	}
	return r
}

func lastAsked(b turn.Book, seq int64) (record.Record, bool) {
	for i := len(b.Records) - 1; i >= 0; i-- {
		if r := b.Records[i]; r.TurnSeq == seq && r.Record.State == record.Asked {
			return r.Record, true
		}
	}
	return record.Record{}, false
}

func fill(r *Resume, asked record.Record) {
	if q := asked.Question; q != nil {
		r.Question = &Question{Text: q.Text, Recommend: q.Recommend}
		for _, o := range q.Options {
			r.Question.Options = append(r.Question.Options, Option{ID: o.ID, Label: o.Label, Publishes: o.Publishes})
		}
	}
	if o := asked.Outcome; o != nil {
		r.Verdict, r.Blocking, r.NonBlocking = o.Verdict, o.Blocking, o.NonBlocking
	}
	if a := asked.Artifact; a != nil {
		r.ArtifactKind, r.ArtifactBody = a.Kind, a.Body
		for _, it := range a.Items {
			r.Items = append(r.Items, Item{Path: it.Path, Line: it.Line, Body: it.Body})
		}
	}
}
