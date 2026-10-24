package turns_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func withOps(r record.Record) record.Record {
	kind, sum := items.Question, "Which cap?"
	r.Ledger = []items.Op{
		{Op: items.OpCreate, Anchor: unit.Anchor{Ref: "C1/1"}, Fields: &items.Fields{Kind: &kind, Summary: &sum}},
		{Op: items.OpResolve, Item: "i2", Anchor: unit.Anchor{Ref: "C1/2"}, Resolution: items.Answered},
	}
	return r
}

func startedRound(t *testing.T, s *turns.Service, round string) {
	t.Helper()
	ctx := context.Background()
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: round, Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, r7, round, "term_1")
	if _, err := s.Start(ctx, "term_1", round); err != nil {
		t.Fatal(err)
	}
}

func TestRecordTurnEndAppliesTheRecordsOps(t *testing.T) {
	s, store, _, _, _ := newService(t)
	l := &turnstest.Ledger{}
	s.Items = l
	startedRound(t, s, "abc")
	rec := withOps(record.Record{State: record.Settled, Summary: "nothing new"})
	got, err := s.RecordTurnEnd(context.Background(), turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec})
	if err != nil || got.Ledger == nil || len(got.Ledger.Applied) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	created := turn.LedgerOps(r7, bookOf(t, store, r7).Records[0])[0].Item
	ops := l.Applied()
	if len(l.Calls) != 1 || l.Calls[0].Provenance != items.Turn || ops[0].Unit != r7 || ops[1].Unit != r7 || ops[0].Item != created || !strings.HasPrefix(created, "t") || ops[1].Item != "i2" {
		t.Fatalf("calls %+v", l.Calls)
	}
	if stored := bookOf(t, store, r7).Records[0].Record.Ledger; stored[0].Item != "" || !stored[0].Unit.IsZero() {
		t.Fatalf("the stored record took the defaults: %+v", stored)
	}
}

func TestARetriedRecordAppliesItsOpsAgain(t *testing.T) {
	s, _, _, _, _ := newService(t)
	l := &turnstest.Ledger{Err: errors.New("database is locked")}
	s.Items = l
	startedRound(t, s, "abc")
	rec := withOps(record.Record{State: record.Settled, Summary: "nothing new"})
	ctx := context.Background()
	got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec})
	if err != nil || !got.Added || got.Ledger == nil || len(got.Ledger.Refused) != 2 || !strings.Contains(got.Ledger.Refused[0].Reason, "may not have applied (database is locked); calling turn_end again with the same record is safe") {
		t.Fatalf("a failed apply: %+v %v", got, err)
	}
	l.Err = nil
	again, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec})
	if err != nil || again.Added || again.Ledger == nil || len(again.Ledger.Applied) != 2 || len(l.Calls) != 2 {
		t.Fatalf("the retry: %+v %v %d", again, err, len(l.Calls))
	}
}

func TestADisabledPluginRefusesEveryOp(t *testing.T) {
	s, _, _, _, _ := newService(t)
	s.Items = &turnstest.Ledger{Err: &coreitems.DisabledError{Provider: unit.GitHub}}
	startedRound(t, s, "abc")
	got, err := s.RecordTurnEnd(context.Background(), turns.RecordRequest{Session: "term_1", Turn: "abc", Record: withOps(record.Record{State: record.Settled, Summary: "s"})})
	if err != nil || len(got.Ledger.Refused) != 2 || got.Ledger.Refused[1].Reason != "github is disabled" || got.Ledger.Refused[1].Index != 1 {
		t.Fatalf("%+v %v", got.Ledger, err)
	}
}

func TestLateRecordAppliesNoOps(t *testing.T) {
	s, _, _, _, _ := newService(t)
	l := &turnstest.Ledger{}
	s.Items = l
	ctx := context.Background()
	startedRound(t, s, "abc")
	s.Send(ctx, turns.SendRequest{Unit: r7, Intent: turn.Intent(dintent.Review), Turn: "def", Trigger: turn.Automated, Records: true})
	s.SentTo(ctx, r7, "def", "term_1")
	got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: withOps(record.Record{State: record.Settled, Summary: "late"})})
	if err != nil || got.Note == "" || got.Ledger != nil || len(l.Calls) != 0 || got.Dropped != 2 || !strings.Contains(got.DroppedNote(), "not applied; this turn already ended") {
		t.Fatalf("a late record: %+v %v %+v", got, err, l.Calls)
	}

	if _, err := s.Start(ctx, "term_1", "def"); err != nil {
		t.Fatal(err)
	}
	first := withOps(record.Record{State: record.Settled, Summary: "first"})
	if got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "def", Record: first}); err != nil || got.Ledger == nil || got.Dropped != 0 || got.DroppedNote() != "" {
		t.Fatalf("the turn's record: %+v %v", got, err)
	}
	second := withOps(record.Record{State: record.Done, Summary: "second", Outcome: &record.Outcome{Verdict: "COMMENT", URL: "https://github.com/acme/api/pull/7#r"}})
	got, err = s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "def", Record: second})
	if err != nil || !got.Added || got.Ledger != nil || len(l.Calls) != 1 || got.Dropped != 2 {
		t.Fatalf("a different record for an ended turn: %+v %v %d", got, err, len(l.Calls))
	}
	if again, _ := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "def", Record: second}); again.Ledger != nil || len(l.Calls) != 1 {
		t.Fatalf("a retry of that record applied: %+v", again)
	}
}

func TestAnAskedThenDoneTurnAppliesBothRecords(t *testing.T) {
	s, _, _, _, _ := newService(t)
	l := &turnstest.Ledger{}
	s.Items = l
	startedRound(t, s, "abc")
	ctx := context.Background()
	if got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: withOps(askedRecord())}); err != nil || got.Ledger == nil {
		t.Fatalf("asked: %+v %v", got, err)
	}
	done := withOps(record.Record{State: record.Settled, Summary: "settled"})
	if got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: done}); err != nil || got.Ledger == nil || len(l.Calls) != 2 {
		t.Fatalf("done after asked: %+v %v", got, err)
	}
}

func TestTheApplyOutlivesTheCallersContext(t *testing.T) {
	s, _, _, _, _ := newService(t)
	l := &ctxLedger{}
	s.Items = l
	startedRound(t, s, "abc")
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	got, err := s.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: withOps(record.Record{State: record.Settled, Summary: "s"})})
	if err != nil || l.err != nil || len(got.Ledger.Applied) != 2 {
		t.Fatalf("%+v %v %v", got.Ledger, err, l.err)
	}
}

// ctxLedger cancels the caller's context before it applies, as a client
// that drops after the save does.
type ctxLedger struct {
	turnstest.Ledger
	cancel func()
	err    error
}

func (l *ctxLedger) Apply(ctx context.Context, prov items.Provenance, ops []items.Op) (coreitems.Result, error) {
	l.cancel()
	l.err = ctx.Err()
	return l.Ledger.Apply(ctx, prov, ops)
}
