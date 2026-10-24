package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// seededStore writes a store holding the given turns, each with one record.
func seededStore(t *testing.T, path string, turns []seedTurn) {
	t.Helper()
	at := testNow.Add(-time.Hour).UTC().Format(event.TimeLayout)
	var stmts []string
	for i, tu := range turns {
		raw, _ := json.Marshal(tu.record)
		stmts = append(stmts,
			fmt.Sprintf(`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, session, state, sent_at, started_at) VALUES (%d, %q, %q, 'review', %q, 'term_1', %q, %q, %q)`,
				i+1, tu.unit.Provider, tu.unit.Key, tu.round, tu.state, at, at),
			fmt.Sprintf(`INSERT INTO turn_records (turn_seq, provider, unit_key, session, hash, at, record) VALUES (%d, %q, %q, 'term_1', %q, %q, '%s')`,
				i+1, tu.unit.Provider, tu.unit.Key, tu.round, at, strings.ReplaceAll(string(raw), "'", "''")))
	}
	db, err := sqlitetest.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		sqlitetest.Exec(t, db, q)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

type seedTurn struct {
	unit   unit.Unit
	round  string
	state  turn.State
	record record.Record
}

func ghUnit(n int) unit.Unit {
	return unit.Unit{Provider: unit.GitHub, Key: fmt.Sprintf("acme/api#%d", n)}
}

func askedTurn(n int) seedTurn {
	return seedTurn{unit: ghUnit(n), round: fmt.Sprintf("r%d", n), state: turn.Asked, record: record.Record{State: record.Asked, Summary: "drafted", Resume: "ready",
		Question: &record.Question{Kind: "confirm", Text: fmt.Sprintf("Publish %d?", n)}}}
}

// servedMessages wires the messages of a daemon on db and runs their worker until the test ends.
func servedMessages(t *testing.T, te *testEnv, db *sqlite.DB, log *change.Log) daemon.Deps {
	t.Helper()
	te.Env.changes = log
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return te.Env.serveMessages(ctx, daemon.Deps{}, db, te.cfg, t.Logf)
}

func waitOpen(t *testing.T, d daemon.Deps, want func([]message.Message) bool) []message.Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		open, err := d.Messages.Store.Open(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if want(open) {
			return open
		}
		if time.Now().After(deadline) {
			t.Fatalf("open messages %+v", open)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOpenQuestionsBecomeMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	done := seedTurn{unit: ghUnit(3), round: "r3", state: turn.Done, record: record.Record{State: record.Done, Summary: "published",
		Outcome: &record.Outcome{Verdict: "APPROVE", URL: "https://example.com/r"}}}
	failed := seedTurn{unit: ghUnit(4), round: "r4", state: turn.Failed, record: record.Record{State: record.Failed, Summary: "the build broke", Resume: "rerun"}}
	seededStore(t, path, []seedTurn{askedTurn(1), askedTurn(2), done, failed})
	log := change.NewLog(change.DefaultWindow, t.Logf)
	db, err := sqlitetest.Open(path, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te := newTestEnv(t, &fake.Manager{})
	d := servedMessages(t, te, db, log)
	open := waitOpen(t, d, func(ms []message.Message) bool {
		return len(ms) == 3 && !slices.ContainsFunc(ms, func(m message.Message) bool { return m.EntryID == "" })
	})
	var units []unit.Unit
	for _, m := range open {
		units = append(units, m.Unit)
		if m.Kind != message.Question || m.EntryID == "" {
			t.Fatalf("message %+v", m)
		}
	}
	if !slices.Equal(units, []unit.Unit{ghUnit(1), ghUnit(2), ghUnit(4)}) {
		t.Fatalf("units %v", units)
	}
	inbox, err := db.Entries().Entries(context.Background(), gtd.Query{View: gtd.ViewInbox})
	if err != nil || len(inbox) != 3 {
		t.Fatalf("inbox %+v %v", inbox, err)
	}
}

func TestLedgerProposalsReachTheInboxOnARealStore(t *testing.T) {
	log := change.NewLog(change.DefaultWindow, t.Logf)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te := newTestEnv(t, &fake.Manager{})
	d := servedMessages(t, te, db, log)
	ledger := te.Env.itemsLedger(db, te.cfg, t.Logf)
	ctx := context.Background()
	u := ghUnit(7)
	kind, summary, changed := items.Todo, "ship the fix", "ship the fix today"
	anchor := unit.Anchor{Unit: u, Ref: "turn:1"}
	propose := func(item string) {
		t.Helper()
		if _, err := ledger.Apply(ctx, items.User, []items.Op{{Op: items.OpCreate, Unit: u, Item: item, Anchor: anchor, Fields: &items.Fields{Kind: &kind, Summary: &summary}}}); err != nil {
			t.Fatal(err)
		}
		res, err := ledger.Apply(ctx, items.Extracted, []items.Op{{Op: items.OpUpdate, Unit: u, Item: item, Anchor: anchor, Fields: &items.Fields{Summary: &changed}}})
		if err != nil || len(res.Proposed) != 1 {
			t.Fatalf("%+v %v", res, err)
		}
	}
	propose("i1")
	open := waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 1 && ms[0].EntryID != "" })
	m := open[0]
	if m.Kind != message.Proposal || m.Origin.Kind != message.OriginLedger || m.Unit != u || m.EntryID == "" {
		t.Fatalf("message %+v", m)
	}
	if _, err := d.Messages.Answer(ctx, m.ID, messages.Answer{OptionID: messages.OptionAccept}); err != nil {
		t.Fatal(err)
	}
	its, err := ledger.Items(ctx, u)
	if err != nil || len(its) != 1 || its[0].Summary != changed || !its[0].Confirmed {
		t.Fatalf("items %+v %v", its, err)
	}
	if got, _, _ := d.Messages.Store.Message(ctx, m.ID); got.State != message.Answered {
		t.Fatalf("message %+v", got)
	}

	propose("i2")
	open = waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 1 && ms[0].ID != m.ID && ms[0].EntryID != "" })
	ps, err := ledger.Proposals(ctx, coreitems.ProposalOpen)
	if err != nil || len(ps) != 1 {
		t.Fatalf("proposals %+v %v", ps, err)
	}
	if _, err := ledger.Decide(ctx, ps[0].ID, false); err != nil {
		t.Fatal(err)
	}
	waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 0 })
	got, _, _ := d.Messages.Store.Message(ctx, open[0].ID)
	if got.State != message.Answered || got.Answer != messages.OptionReject {
		t.Fatalf("decided elsewhere %+v", got)
	}
	if e, _ := db.Entries().Entry(ctx, gtd.ID(got.EntryID)); e.State != gtd.Done {
		t.Fatalf("entry %s", e.State)
	}
}

func TestTheMCPEndpointPostsToTheInbox(t *testing.T) {
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te := newTestEnv(t, &fake.Manager{})
	te.registerSession(t, turn.Session{ID: "term_1", WorkspaceID: "ws-1"})
	d := mcpEndpoint{port: 47801}.wire(te.Env.serveMessages(context.Background(), daemon.Deps{Turns: te.Turns}, db, te.cfg, t.Logf), te.cfg)
	if d.MCP == nil || d.Messages == nil || d.GTD == nil {
		t.Fatalf("deps %+v", d)
	}
	rpc := func(sid, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:47801/mcp", strings.NewReader(body))
		req.Header.Set(api.SessionHeader, "term_1")
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		rec := httptest.NewRecorder()
		d.MCP.ServeHTTP(rec, req)
		return rec
	}
	sid := rpc("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`).Header().Get("Mcp-Session-Id")
	if list := rpc(sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`).Body.String(); !strings.Contains(list, `"inbox_post"`) {
		t.Fatalf("tools %s", list)
	}
	got := rpc(sid, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"inbox_post","arguments":{"text":"hi"}}}`).Body.String()
	if !strings.Contains(got, "no started turn") || !strings.Contains(got, `"isError":true`) {
		t.Fatalf("a post with no turn: %s", got)
	}
}

func TestMessagesCostOnARealStore(t *testing.T) {
	if testing.Short() {
		t.Skip("measures")
	}
	path := filepath.Join(t.TempDir(), "store.db")
	var turns []seedTurn
	const n = 500
	for i := range n {
		turns = append(turns, askedTurn(i+1))
	}
	seededStore(t, path, turns)
	db, err := sqlitetest.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te := newTestEnv(t, &fake.Manager{})
	d := te.Env.serveMessages(context.Background(), daemon.Deps{}, db, te.cfg, t.Logf)
	ctx := context.Background()
	start := time.Now()
	if err := d.Messages.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	first := time.Since(start)
	start = time.Now()
	if err := d.Messages.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	again := time.Since(start)
	start = time.Now()
	if err := d.Messages.Reconcile(ctx, ghUnit(1)); err != nil {
		t.Fatal(err)
	}
	one := time.Since(start)
	start = time.Now()
	open, err := d.Messages.Store.Open(ctx)
	if err != nil || len(open) != n {
		t.Fatalf("open %d %v", len(open), err)
	}
	list := time.Since(start)
	t.Logf("sqlite, %d asked turns: first reconcile %s (messages and entries created), a full reconcile with nothing to do %s, one unit's %s, listing the open messages %s",
		n, first, again, one, list)
}
