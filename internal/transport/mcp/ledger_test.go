package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
)

type turnClock struct{}

func (turnClock) Now() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }

var r7 = pr.Ref{Owner: "acme", Repo: "api", Number: 7}.Unit()

// ledgerServer serves turn_end over a real turns service whose turn abc on
// r7 is started on the session term_1, whose process is term_1, with l as
// its ledger.
func ledgerServer(t *testing.T, l *turnstest.Ledger) (*Server, *turnstest.Store, string) {
	t.Helper()
	store := turnstest.NewStore(turnstest.Strict(t))
	reg := turnstest.NewRegistry(turnstest.Strict(t))
	reg.Put(turn.Session{ID: "term_1"})
	svc := &turns.Service{Intents: turnstest.Intents(), Store: store, Registry: reg,
		Sessions: turnstest.NewSessions(), Clock: turnClock{}}
	if l != nil {
		svc.Items = l
	}
	ctx := context.Background()
	if _, err := svc.Send(ctx, turns.SendRequest{Unit: r7, Intent: "review", Turn: "abc", Trigger: turn.Automated, Records: true}); err != nil {
		t.Fatal(err)
	}
	svc.SentTo(ctx, r7, "abc", "term_1")
	if _, err := svc.Start(ctx, "term_1", "abc"); err != nil {
		t.Fatal(err)
	}
	s := &Server{Texts: bundledTexts, Turns: svc, Port: 47800}
	return s, store, call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
}

const ledgerOps = `[{"op":"create","anchor":{"ref":"C1/1"},"fields":{"kind":"question","summary":"Which cap?"}},
 {"op":"resolve","item":"i2","anchor":{"ref":"C1/2"},"resolution":"answered"}]`

func turnEndWith(ops string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"abc","record":{"state":"settled","summary":"s","ledger":%s}}}}`, ops)
}

func TestTurnEndAppliesLedgerOps(t *testing.T) {
	l := &turnstest.Ledger{}
	s, store, sid := ledgerServer(t, l)
	r := decodeResult(t, call(t, s, sid, "term_1", turnEndWith(ledgerOps)))
	if r.Result.IsError || !strings.HasSuffix(r.Result.Content[0].Text, "Ledger: 2 applied.") {
		t.Fatalf("%+v", r.Result)
	}
	books, _ := store.Books(context.Background(), r7)
	created := turn.LedgerOps(r7, books[r7].Records[0])[0].Item
	ops := l.Applied()
	if len(l.Calls) != 1 || l.Calls[0].Provenance != items.Turn || len(ops) != 2 || ops[0].Unit != r7 || ops[1].Unit != r7 ||
		ops[0].Item != created || ops[0].Op != items.OpCreate || ops[1].Op != items.OpResolve {
		t.Fatalf("calls %+v", l.Calls)
	}
}

func TestTurnEndReportsLedgerRefusals(t *testing.T) {
	s, store, sid := ledgerServer(t, &turnstest.Ledger{Refuse: map[int]string{1: "anchor: required"}})
	r := decodeResult(t, call(t, s, sid, "term_1", turnEndWith(ledgerOps)))
	if r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "Ledger: 1 applied, 1 refused: ledger[1] anchor: required") {
		t.Fatalf("%+v", r.Result)
	}
	if books, _ := store.Books(context.Background(), r7); len(books[r7].Records) != 1 {
		t.Fatal("a refused op dropped the record")
	}
}

func TestTurnEndReportsProposals(t *testing.T) {
	s, _, sid := ledgerServer(t, &turnstest.Ledger{Propose: map[int]bool{1: true}})
	r := decodeResult(t, call(t, s, sid, "term_1", turnEndWith(ledgerOps)))
	if r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "Ledger: 1 applied, 1 proposed to the user.") {
		t.Fatalf("%+v", r.Result)
	}
}

func TestTurnEndRefusesAMalformedLedger(t *testing.T) {
	l := &turnstest.Ledger{}
	s, _, sid := ledgerServer(t, l)
	r := decodeResult(t, call(t, s, sid, "term_1", turnEndWith(`[{"op":"resolve","item":"i2","resolution":"answered"}]`)))
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "ledger[0] anchor: required") || len(l.Calls) != 0 {
		t.Fatalf("%+v", r.Result)
	}
}

func TestTurnEndWithoutHandleAppliesNothing(t *testing.T) {
	l := &turnstest.Ledger{}
	s, store, _ := ledgerServer(t, l)
	for _, h := range []string{"", "${ORCA_TERMINAL_HANDLE}"} {
		sid := call(t, s, "", h, initialize).Header().Get("Mcp-Session-Id")
		r := decodeResult(t, call(t, s, sid, h, turnEndWith(ledgerOps)))
		if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, api.SessionHeader) {
			t.Fatalf("%q: %+v", h, r.Result)
		}
	}
	books, _ := store.Books(context.Background(), r7)
	if len(books[r7].Records) != 0 || len(l.Calls) != 0 {
		t.Fatalf("records %d, ledger calls %d", len(books[r7].Records), len(l.Calls))
	}
}

func TestTurnEndWithoutOpsSaysNothingOfTheLedger(t *testing.T) {
	s, _, sid := ledgerServer(t, &turnstest.Ledger{})
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"abc","record":{"state":"settled","summary":"s"}}}}`
	if r := decodeResult(t, call(t, s, sid, "term_1", body)); r.Result.Content[0].Text != "Recorded settled for acme/api#7. End your turn." {
		t.Fatalf("%+v", r.Result)
	}
}

func TestTurnEndSaysWhenOpsWereDropped(t *testing.T) {
	l := &turnstest.Ledger{}
	s, _, sid := ledgerServer(t, l)
	decodeResult(t, call(t, s, sid, "term_1", turnEndWith(ledgerOps)))
	fixed := strings.Replace(turnEndWith(ledgerOps), `"summary":"s"`, `"summary":"fixed"`, 1)
	r := decodeResult(t, call(t, s, sid, "term_1", fixed))
	if r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "Ledger: not applied; this turn already ended (or was replaced).") || len(l.Calls) != 1 {
		t.Fatalf("%+v", r.Result)
	}
}

func TestTurnOpsNamingAnotherUnitAreRefused(t *testing.T) {
	l := &turnstest.Ledger{}
	s, _, sid := ledgerServer(t, l)
	ops := `[{"op":"resolve","item":"i2","unit":{"provider":"github","key":"acme/api#8"},"anchor":{"ref":"C1/2"},"resolution":"answered"}]`
	r := decodeResult(t, call(t, s, sid, "term_1", turnEndWith(ops)))
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "ledger[0] unit: ops act on this turn's unit (acme/api#7); leave unit out") || len(l.Calls) != 0 {
		t.Fatalf("%+v", r.Result)
	}
}
