package turn

import (
	"cmp"
	"slices"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/record"
)

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

// Proposed says whether turn seq of b, or a turn it carries on, proposed d on b's unit: the drafts a turn that
// resumes or replaces another reads, revises and asks about are that turn's too.
func (b *Book) Proposed(seq int64, d draft.Draft) bool {
	if !d.Origin.ByAgent() || d.Origin.TurnSeq == 0 || cmp.Or(d.Origin.Unit, d.Unit) != b.Unit {
		return false
	}
	return slices.Contains(b.lineage(seq), d.Origin.TurnSeq)
}

// lineage is seq and the seqs of the turns it carries on, through ResumeOf and ReplacedBy.
func (b *Book) lineage(seq int64) []int64 {
	out := []int64{seq}
	for i := 0; i < len(out); i++ {
		cur := out[i]
		var prev []int64
		if t, ok := b.bySeq(cur); ok && t.ResumeOf != 0 {
			prev = append(prev, t.ResumeOf)
		}
		for _, t := range b.Turns {
			if t.State == Replaced && t.ReplacedBy == cur {
				prev = append(prev, t.Seq)
			}
		}
		for _, p := range prev {
			if !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
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
