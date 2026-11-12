package turn

import "github.com/wloophq/wloop/internal/domain/record"

const (
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
// published: only the loop's record of a draft the user approved says so.
// A url an agent wrote, a by-hand trigger and an answer endorse nothing, and
// neither does a rule's or a window's release.
func (b *Book) Endorsed(seq int64) string {
	if !b.published(seq) {
		return NotEndorsed
	}
	if done, _ := b.LastRecord(seq); done.Record.Outcome.Draft != nil && done.Record.Outcome.Approved {
		return EndorsedApproved
	}
	return NotEndorsed
}

func (b *Book) published(seq int64) bool {
	done, ok := b.LastRecord(seq)
	return ok && done.Record.State == record.Done && done.Record.Outcome != nil && done.Record.Outcome.Verdict != ""
}
