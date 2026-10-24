package turn

import "github.com/wloophq/wloop/internal/domain/record"

const (
	EndorsedByHand   = "yes: by hand"
	EndorsedApproved = "yes: the user approved"
	NotEndorsed      = "no"
)

// Chain is the turn and the turns it resumes, newest first.
func (b *Book) Chain(seq int64) []Turn {
	var chain []Turn
	seen := map[int64]bool{}
	for seq != 0 && !seen[seq] {
		t, ok := b.bySeq(seq)
		if !ok {
			break
		}
		seen[seq] = true
		chain = append(chain, *t)
		seq = t.ResumeOf
	}
	return chain
}

// Endorsed says whether the user stands behind the verdict the turn
// published. A turn without a done verdict published nothing to endorse.
func (b *Book) Endorsed(seq int64) string {
	chain := b.Chain(seq)
	if len(chain) == 0 || !b.published(seq) {
		return NotEndorsed
	}
	if chain[len(chain)-1].Trigger == ByHand {
		return EndorsedByHand
	}
	for _, t := range chain {
		for _, a := range b.Answers {
			if a.TurnSeq != t.Seq || a.Source == Terminal || (a.Route != Typed && a.Route != Resumed) {
				continue
			}
			if q, ok := b.answered(a); ok && q.Endorses(a.OptionID, a.Yes) {
				return EndorsedApproved
			}
		}
	}
	return NotEndorsed
}

func (b *Book) published(seq int64) bool {
	done, ok := b.LastRecord(seq)
	return ok && done.Record.State == record.Done && done.Record.Outcome != nil && done.Record.Outcome.Verdict != ""
}

// answered is the question the answer answered: the newest asked record of
// its turn at or before it.
func (b *Book) answered(a Answer) (*record.Question, bool) {
	for i := len(b.Records) - 1; i >= 0; i-- {
		r := b.Records[i]
		if r.TurnSeq == a.TurnSeq && r.Record.State == record.Asked && r.Record.Question != nil && !r.At.After(a.At) {
			return r.Record.Question, true
		}
	}
	return nil, false
}
