package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/daemon"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/record"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// lostApply is a ledger whose daemon stopped after the record was saved and
// before its ops applied.
type lostApply struct{ turns.Ledger }

func (lostApply) Apply(context.Context, items.Provenance, []items.Op) (coreitems.Result, error) {
	return coreitems.Result{}, errors.New("daemon stopped")
}

// ledgerTurn is a test env whose daemon turns apply to the real ledger over
// its store, with item i2 open on u and turn abc started on term_1.
func ledgerTurn(t *testing.T, round string) (*testEnv, unit.Unit) {
	t.Helper()
	te := newTestEnv(t, &fake.Manager{})
	te.Env.serveTurnLedger(te.db, te.cfg, t.Logf)
	if te.Turns.Items == nil {
		t.Fatal("no ledger wired")
	}
	u := ghUnit(7)
	ctx := context.Background()
	kind, sum := items.Question, "Is the cap configurable?"
	if _, err := te.Env.itemsLedger(te.db, te.cfg, t.Logf).Apply(ctx, items.Extracted, []items.Op{{Op: items.OpCreate, Unit: u, Item: "i2",
		Anchor: unit.Anchor{Ref: "comment:1"}, Fields: &items.Fields{Kind: &kind, Summary: &sum}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := te.Turns.Send(ctx, turns.SendRequest{Unit: u, Intent: "review", Turn: round, Trigger: dturn.Automated, Records: true}); err != nil {
		t.Fatal(err)
	}
	te.registerSession(t, dturn.Session{ID: "term_1", WorkspaceID: "ws-1"})
	te.Turns.SentTo(ctx, u, round, "term_1")
	if _, err := te.Turns.Start(ctx, "term_1", round); err != nil {
		t.Fatal(err)
	}
	return te, u
}

func turnLog(t *testing.T, te *testEnv, u unit.Unit) []coreitems.LogEntry {
	t.Helper()
	all, err := te.db.Items().Log(context.Background(), u, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(all, func(e coreitems.LogEntry) bool { return e.Provenance != items.Turn })
}

func outcomesOf(log []coreitems.LogEntry) []string {
	out := make([]string, len(log))
	for i, e := range log {
		out[i] = e.Item + " " + e.Outcome
	}
	return out
}

func TestRetriedTurnEndAppliesOnce(t *testing.T) {
	te, u := ledgerTurn(t, "abc")
	ctx := context.Background()
	kind, sum := items.Todo, "Add a test for the backoff"
	rec := record.Record{State: record.Settled, Summary: "nothing new", Ledger: []items.Op{
		{Op: items.OpCreate, Anchor: unit.Anchor{Ref: "comment:2"}, Fields: &items.Fields{Kind: &kind, Summary: &sum}},
		{Op: items.OpResolve, Item: "i2", Anchor: unit.Anchor{Ref: "comment:3"}, Resolution: items.Answered},
	}}
	real := te.Turns.Items
	te.Turns.Items = lostApply{real}
	first, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec})
	if err != nil || !first.Added || len(turnLog(t, te, u)) != 0 {
		t.Fatalf("the lost apply: %+v %v", first, err)
	}
	te.Turns.Items = real
	retry, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec})
	if err != nil || retry.Added || retry.Ledger == nil || len(retry.Ledger.Applied) != 2 || len(retry.Ledger.Refused) != 0 {
		t.Fatalf("the retry: %+v %v", retry, err)
	}
	created := dturn.LedgerOps(u, te.unitBook(t, u).Records[0])[0].Item
	if got, want := outcomesOf(turnLog(t, te, u)), []string{created + " applied", "i2 applied"}; !slices.Equal(got, want) {
		t.Fatalf("log %q, want %q", got, want)
	}
	before, err := te.Turns.Items.Items(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	again, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec})
	if err != nil || again.Ledger == nil || slices.ContainsFunc(again.Ledger.Applied, func(a coreitems.Applied) bool { return a.Changed }) {
		t.Fatalf("the second retry: %+v %v", again, err)
	}
	if got := outcomesOf(turnLog(t, te, u)[2:]); !slices.Equal(got, []string{created + " noop", "i2 noop"}) {
		t.Fatalf("the second retry logged %q", got)
	}
	after, _ := te.Turns.Items.Items(ctx, u)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the item set changed:\n%+v\n%+v", before, after)
	}
	if len(after) != 2 || after[1].ID != created || after[1].Provenance != items.Turn || after[0].State != items.Resolved {
		t.Fatalf("items %+v", after)
	}
}

func TestHistoryShowsLedgerOps(t *testing.T) {
	te, u := ledgerTurn(t, "abc")
	ctx := context.Background()
	other := "x"
	rec := record.Record{State: record.Settled, Summary: "answered", Ledger: []items.Op{
		{Op: items.OpResolve, Item: "i2", Anchor: unit.Anchor{Ref: "comment:3"}, Resolution: items.Answered},
		{Op: items.OpUpdate, Item: "i3", Anchor: unit.Anchor{Ref: "comment:4"}, Fields: &items.Fields{Summary: &other}},
	}}
	if _, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec}); err != nil {
		t.Fatal(err)
	}
	b := te.unitBook(t, u)
	past, err := te.Env.turnHistory(ctx, u, b)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"resolved i2: answered", `refused i3: item: unknown "i3"`}; len(past) != 1 || !slices.Equal(past[0].Ledger, want) {
		t.Fatalf("history %+v", past)
	}
	open, err := te.Env.itemsReader().OpenItems(ctx, u)
	if err != nil || len(open) != 0 {
		t.Fatalf("open items after the resolve: %+v %v", open, err)
	}
}

func TestTurnsWithoutALedgerListNoItems(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	if te.Env.itemsReader() != nil {
		t.Fatal("a reader without a ledger")
	}
	past, err := te.Env.turnHistory(context.Background(), ghUnit(7), dturn.Book{})
	if err != nil || past != nil {
		t.Fatalf("%+v %v", past, err)
	}
}

func (te *testEnv) unitBook(t *testing.T, u unit.Unit) dturn.Book {
	t.Helper()
	books, err := te.Turns.Store.Books(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	return books[u]
}

func TestTheMCPEndpointAppliesTurnLedgerOnARealStore(t *testing.T) {
	te, u := ledgerTurn(t, "abc")
	d := mcpEndpoint{port: 47801}.wire(daemon.Deps{Turns: te.Turns}, te.cfg)
	rpc := func(handle, sid, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:47801/mcp", strings.NewReader(body))
		if handle != "" {
			req.Header.Set(api.SessionHeader, handle)
		}
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		rec := httptest.NewRecorder()
		d.MCP.ServeHTTP(rec, req)
		return rec
	}
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`
	end := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"abc","record":{"state":"settled","summary":"s","ledger":[
		{"op":"create","anchor":{"ref":"comment:2"},"fields":{"kind":"todo","summary":"Add a test"}},
		{"op":"resolve","item":"i2","anchor":{"ref":"comment:3"},"resolution":"answered"}]}}}}`
	for _, h := range []string{"", "${ORCA_TERMINAL_HANDLE}"} {
		sid := rpc(h, "", initialize).Header().Get("Mcp-Session-Id")
		if got := rpc(h, sid, end).Body.String(); !strings.Contains(got, api.SessionHeader) || !strings.Contains(got, `"isError":true`) {
			t.Fatalf("%q: %s", h, got)
		}
	}
	if b := te.unitBook(t, u); len(b.Records) != 0 || len(turnLog(t, te, u)) != 0 {
		t.Fatalf("a handle-less turn_end wrote: records %d, ops %d", len(b.Records), len(turnLog(t, te, u)))
	}
	sid := rpc("term_1", "", initialize).Header().Get("Mcp-Session-Id")
	if got := rpc("term_1", sid, end).Body.String(); !strings.Contains(got, "Ledger: 2 applied.") {
		t.Fatalf("%s", got)
	}
	if its, _ := te.Turns.Items.Items(context.Background(), u); len(its) != 2 || its[0].State != items.Resolved {
		t.Fatalf("items %+v", its)
	}
}

func TestTurnLedgerCostOnARealStore(t *testing.T) {
	if testing.Short() {
		t.Skip("measures")
	}
	te, u := ledgerTurn(t, "abc")
	ctx := context.Background()
	ledger := te.Env.itemsLedger(te.db, te.cfg, t.Logf)
	const nItems, nLogged = 300, 3000
	kind := items.Request
	var ops []items.Op
	for i := range nItems {
		sum := fmt.Sprintf("request %d", i)
		ops = append(ops, items.Op{Op: items.OpCreate, Unit: u, Item: fmt.Sprintf("e%d", i), Anchor: unit.Anchor{Ref: fmt.Sprintf("C1/%d", i)}, Fields: &items.Fields{Kind: &kind, Summary: &sum}})
	}
	if _, err := ledger.Apply(ctx, items.Extracted, ops); err != nil {
		t.Fatal(err)
	}
	for range nLogged / nItems {
		if _, err := ledger.Apply(ctx, items.Extracted, ops); err != nil {
			t.Fatal(err)
		}
	}
	var turnOps []items.Op
	for i := range record.LedgerMax {
		turnOps = append(turnOps, items.Op{Op: items.OpResolve, Item: fmt.Sprintf("e%d", i), Anchor: unit.Anchor{Ref: "comment:9"}, Resolution: items.Done})
	}
	start := time.Now()
	got, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: record.Record{State: record.Settled, Summary: "s", Ledger: turnOps}})
	if err != nil || len(got.Ledger.Applied) != record.LedgerMax {
		t.Fatalf("%+v %v", got.Ledger, err)
	}
	recordTook := time.Since(start)
	start = time.Now()
	const reads = 20
	for range reads {
		if open, err := te.Env.itemsReader().OpenItems(ctx, u); err != nil || len(open) != nItems+1-record.LedgerMax {
			t.Fatalf("%d %v", len(open), err)
		}
	}
	openTook := time.Since(start) / reads
	b := te.unitBook(t, u)
	start = time.Now()
	for range reads {
		if past, err := te.Env.turnHistory(ctx, u, b); err != nil || len(past[0].Ledger) != record.LedgerMax {
			t.Fatalf("%+v %v", past, err)
		}
	}
	t.Logf("%d items, %d logged ops: turn_end with %d ops %v; open items %v per turn; history %v per turn",
		nItems, len(turnLog(t, te, u))+nLogged+nItems, record.LedgerMax, recordTook, openTook, time.Since(start)/reads)
}

func TestTurnOpsStayOnTheTurnsUnit(t *testing.T) {
	te, u := ledgerTurn(t, "abc")
	ctx := context.Background()
	other := ghUnit(8)
	kind, sum := items.Todo, "planted on another unit"
	rec := record.Record{State: record.Settled, Summary: "s", Ledger: []items.Op{
		{Op: items.OpResolve, Item: "i2", Anchor: unit.Anchor{Unit: other, Ref: "comment:9"}, Resolution: items.Answered},
		{Op: items.OpCreate, Unit: other, Anchor: unit.Anchor{Ref: "comment:2"}, Fields: &items.Fields{Kind: &kind, Summary: &sum}},
	}}
	_, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec})
	var bad record.Problems
	if !errors.As(err, &bad) || len(bad) != 1 || bad[0] != "ledger[1] unit: ops act on this turn's unit (acme/api#7); leave unit out" {
		t.Fatalf("%v", err)
	}
	if its, _ := te.Turns.Items.Items(ctx, other); len(its) != 0 {
		t.Fatalf("items on another unit: %+v", its)
	}
	if b := te.unitBook(t, u); len(b.Records) != 0 || len(turnLog(t, te, u)) != 0 {
		t.Fatal("a refused record was stored or applied")
	}
	rec.Ledger[1].Unit = u
	if got, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: rec}); err != nil || len(got.Ledger.Applied) != 2 {
		t.Fatalf("the turn's own unit, named: %+v %v", got, err)
	}
}

func TestRestatedCreateInALaterRecordIsANoop(t *testing.T) {
	te, u := ledgerTurn(t, "abc")
	ctx := context.Background()
	kind, sum := items.Todo, "Add a test for the backoff"
	create := items.Op{Op: items.OpCreate, Anchor: unit.Anchor{Ref: "comment:2"}, Fields: &items.Fields{Kind: &kind, Summary: &sum}}
	asked := record.Record{State: record.Asked, Summary: "asking", Resume: "r", Question: &record.Question{Kind: "free", Text: "Which?"},
		Ledger: []items.Op{create, create}}
	if _, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: asked}); err != nil {
		t.Fatal(err)
	}
	done := record.Record{State: record.Settled, Summary: "settled", Ledger: []items.Op{create}}
	got, err := te.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: "term_1", Turn: "abc", Record: done})
	if err != nil || len(got.Ledger.Applied) != 1 || got.Ledger.Applied[0].Changed {
		t.Fatalf("the restated create: %+v %v", got.Ledger, err)
	}
	its, _ := te.Turns.Items.Items(ctx, u)
	if len(its) != 3 {
		t.Fatalf("items %+v", its)
	}
}
