package turn_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestProposedIsTheTurnsChain(t *testing.T) {
	r7 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	r8 := unit.Unit{Provider: unit.GitHub, Key: "acme/api#8"}
	b := turn.Book{Unit: r7, Turns: []turn.Turn{{Seq: 1}, {Seq: 2, ResumeOf: 1}, {Seq: 3}, {Seq: 4, ResumeOf: 2},
		{Seq: 5, State: turn.Replaced, ReplacedBy: 6}, {Seq: 6}}}
	of := func(kind draft.OriginKind, seq int64) draft.Draft {
		return draft.Draft{Unit: r7, Origin: draft.Origin{Kind: kind, TurnSeq: seq}}
	}
	onR8 := of(draft.OriginTurn, 1)
	onR8.Unit = r8
	fromR8 := of(draft.OriginTurn, 1)
	fromR8.Origin.Unit = r8
	toR7 := onR8
	toR7.Origin.Unit = r7
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
		{"the turn it replaces", 6, of(draft.OriginTurn, 5), true},
		{"a turn replacing it", 5, of(draft.OriginTurn, 6), false},
		{"another unit's", 1, onR8, false},
		{"another unit's turn's", 1, fromR8, false},
		{"its turn's, on another unit", 1, toR7, true},
		{"a rule's", 1, draft.Draft{Origin: draft.Origin{Kind: draft.OriginAuto}}, false},
		{"no turn's", 0, of(draft.OriginSession, 0), false},
	} {
		if got := b.Proposed(c.seq, c.d); got != c.want {
			t.Errorf("%s: Proposed(%d) = %v, want %v", c.name, c.seq, got, c.want)
		}
	}
}
