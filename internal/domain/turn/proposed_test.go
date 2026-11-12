package turn_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestProposedIsTheTurnsChain(t *testing.T) {
	b := turn.Book{Turns: []turn.Turn{{Seq: 1}, {Seq: 2, ResumeOf: 1}, {Seq: 3}, {Seq: 4, ResumeOf: 2}}}
	of := func(kind draft.OriginKind, seq int64) draft.Draft {
		return draft.Draft{Origin: draft.Origin{Kind: kind, TurnSeq: seq}}
	}
	for _, c := range []struct {
		name string
		seq  int64
		d    draft.Draft
		want bool
	}{
		{"its own turn's", 1, of(draft.OriginTurn, 1), true},
		{"a session's on its turn", 1, of(draft.OriginSession, 1), true},
		{"the turn it resumes", 2, of(draft.OriginTurn, 1), true},
		{"the turn its chain starts at", 4, of(draft.OriginTurn, 1), true},
		{"a turn resuming it", 1, of(draft.OriginTurn, 2), false},
		{"another chain's", 3, of(draft.OriginTurn, 1), false},
		{"a rule's", 1, draft.Draft{Origin: draft.Origin{Kind: draft.OriginAuto}}, false},
		{"no turn's", 0, of(draft.OriginSession, 0), false},
	} {
		if got := b.Proposed(c.seq, c.d); got != c.want {
			t.Errorf("%s: Proposed(%d) = %v, want %v", c.name, c.seq, got, c.want)
		}
	}
}
