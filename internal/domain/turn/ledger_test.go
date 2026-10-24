package turn

import (
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestLedgerOpsDefaultTheUnitAndNameCreates(t *testing.T) {
	other := unit.Unit{Provider: "thread", Key: "x"}
	kind, sum := items.Question, "Which cap?"
	create := items.Op{Op: items.OpCreate, Anchor: unit.Anchor{Ref: "a"}, Fields: &items.Fields{Kind: &kind, Summary: &sum}}
	rec := Record{TurnSeq: 3, Hash: "0123456789abcdef", Record: record.Record{Ledger: []items.Op{
		create,
		{Op: items.OpResolve, Item: "i2", Anchor: unit.Anchor{Ref: "b"}, Resolution: items.Answered},
		{Op: items.OpCreate, Item: "mine", Unit: other, Anchor: unit.Anchor{Ref: "c"}, Fields: &items.Fields{Kind: &kind, Summary: &sum}},
	}}}
	rec.Record.Ledger = append(rec.Record.Ledger, create)
	got := LedgerOps(r7.Unit(), rec)
	later := LedgerOps(r7.Unit(), Record{TurnSeq: 3, Hash: "fedcba9876543210", Record: record.Record{Ledger: []items.Op{create}}})
	next := LedgerOps(r7.Unit(), Record{TurnSeq: 4, Hash: "0123456789abcdef", Record: record.Record{Ledger: []items.Op{create}}})
	if len(got[0].Item) != 9 || got[3].Item != got[0].Item+".1" || later[0].Item != got[0].Item || next[0].Item == got[0].Item {
		t.Fatalf("create ids %s %s %s %s", got[0].Item, got[3].Item, later[0].Item, next[0].Item)
	}
	if got[0].Unit != r7.Unit() || got[0].Unit != r7.Unit() || got[1].Item != "i2" || got[1].Unit != r7.Unit() ||
		got[2].Item != "mine" || got[2].Unit != other {
		t.Fatalf("%+v", got)
	}
	if rec.Record.Ledger[0].Item != "" || !rec.Record.Ledger[0].Unit.IsZero() {
		t.Fatal("the stored record changed")
	}
}

func TestHistoryNamesEachOpWithItsOutcome(t *testing.T) {
	resolve := items.Op{Op: items.OpResolve, Item: "i2", Anchor: unit.Anchor{Ref: "a"}, Resolution: items.Answered}
	update := items.Op{Op: items.OpUpdate, Item: "i3", Anchor: unit.Anchor{Ref: "b"}, Fields: &items.Fields{}}
	reopen := items.Op{Op: items.OpReopen, Item: "i4", Anchor: unit.Anchor{Ref: "c"}}
	b := Book{Unit: r7.Unit(),
		Turns: []Turn{{Seq: 1, Intent: "review", State: Done, SentAt: at(0)}, {Seq: 2, Intent: "review", State: Done, SentAt: at(2)}},
		Records: []Record{
			{TurnSeq: 1, Hash: "aaaaaaaaaaaa", At: at(1), Record: record.Record{State: record.Done, Summary: "s", Ledger: []items.Op{resolve, update, reopen}}},
			{TurnSeq: 2, Hash: "bbbbbbbbbbbb", At: at(3), Record: record.Record{State: record.Done, Summary: "s", Ledger: []items.Op{resolve}}},
		},
	}
	at1 := func(o items.Op) items.Op { o.Unit = r7.Unit(); return o }
	log := []OpOutcome{
		{Op: at1(resolve), Outcome: "applied", At: at(1)},
		{Op: at1(update), Outcome: "refused", Reason: "anchor: required", At: at(1)},
		{Op: at1(resolve), Outcome: "applied", At: at(1)},
		{Op: at1(resolve), Outcome: "noop", At: at(3)},
	}
	h := b.HistoryWith(10, log)
	if want := []string{"resolved i2: answered", "refused i3: anchor: required", "reopen i4: no outcome logged"}; !slices.Equal(h[1].Ledger, want) {
		t.Fatalf("the first turn: %q", h[1].Ledger)
	}
	if want := []string{"resolve i2: no change"}; !slices.Equal(h[0].Ledger, want) {
		t.Fatalf("the second turn: %q", h[0].Ledger)
	}
	if plain := b.History(10); plain[0].Ledger == nil || (&Book{Unit: r7.Unit(), Turns: b.Turns}).History(10)[0].Ledger != nil {
		t.Fatal("a turn with no ops has no ledger lines")
	}
}
