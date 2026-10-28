package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/task"
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
	return te.Env.serveMessages(te.serving(ctx), daemon.Deps{}, db, te.cfg, t.Logf)
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
	t.Parallel()
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
		return len(ms) == 2 && !slices.ContainsFunc(ms, func(m message.Message) bool { return m.TaskID == "" })
	})
	var units []unit.Unit
	for _, m := range open {
		units = append(units, m.Unit)
		if m.Kind != message.Question || m.TaskID == "" {
			t.Fatalf("message %+v", m)
		}
	}
	if !slices.Equal(units, []unit.Unit{ghUnit(1), ghUnit(2)}) {
		t.Fatalf("units %v", units)
	}
	gs, err := d.GateReader.Where(context.Background(), gates.Query{Kind: gate.KindQuestion})
	if err != nil || len(gs) != 2 {
		t.Fatalf("gates %+v %v", gs, err)
	}
	for _, m := range open {
		if !slices.ContainsFunc(gs, func(g gate.Gate) bool { return g.Subject == strconv.FormatInt(m.ID, 10) && string(g.Task) == m.TaskID }) {
			t.Fatalf("message %+v has no gate on its task in %+v", m, gs)
		}
	}
}

// messageConsequences apply the message gates' consequences through the messages, as the daemon does.
type messageConsequences struct{ m *messages.Service }

func (c messageConsequences) Release(context.Context, gate.Gate) error { return nil }
func (c messageConsequences) Unblock(context.Context, gate.Gate) error { return nil }
func (c messageConsequences) Deliver(ctx context.Context, g gate.Gate) error {
	return c.m.Deliver(ctx, g)
}
func (c messageConsequences) Decide(ctx context.Context, g gate.Gate) error {
	return c.m.Decide(ctx, g)
}

func TestLedgerProposalsReachTheInboxOnARealStore(t *testing.T) {
	t.Parallel()
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
	open := waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 1 && ms[0].TaskID != "" })
	m := open[0]
	if m.Kind != message.Proposal || m.Origin.Kind != message.OriginLedger || m.Unit != u || m.TaskID == "" {
		t.Fatalf("message %+v", m)
	}
	te.Env.consequences = messageConsequences{d.Messages}
	gateOf := func(m message.Message) gate.Gate {
		t.Helper()
		gs, err := d.GateReader.Where(ctx, gates.Query{Kind: gate.KindProposal, Unit: u})
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(gs, func(g gate.Gate) bool { return g.Subject == strconv.FormatInt(m.ID, 10) })
		if i < 0 {
			t.Fatalf("no open gate of message %d in %+v", m.ID, gs)
		}
		return gs[i]
	}
	if m.Text != "Proposed update of item i1 on "+u.Label()+" (from extracted; you confirmed the item)" {
		t.Fatalf("text %q", m.Text)
	}
	if err := d.Gates.Resolve(ctx, gateOf(m).ID, gates.Resolution{Intent: "approve", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	its, err := ledger.Items(ctx, u)
	if err != nil || len(its) != 1 || its[0].Summary != changed || !its[0].Confirmed {
		t.Fatalf("items %+v %v", its, err)
	}
	if got, _, _ := d.Messages.Store.Message(ctx, m.ID); got.Open() || got.Answer != "approve" {
		t.Fatalf("message %+v", got)
	}
	if ps, _ := ledger.Proposals(ctx, coreitems.ProposalAccepted); len(ps) != 1 {
		t.Fatalf("accepted %+v", ps)
	}

	propose("i2")
	open = waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 1 && ms[0].ID != m.ID && ms[0].TaskID != "" })
	if err := d.Gates.Resolve(ctx, gateOf(open[0]).ID, gates.Resolution{Intent: "reject", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 0 })
	if ps, _ := ledger.Proposals(ctx, coreitems.ProposalOpen); len(ps) != 0 {
		t.Fatalf("open proposals %+v", ps)
	}
	if its, _ := ledger.Items(ctx, ghUnit(7)); len(its) != 2 || its[1].Summary != summary {
		t.Fatalf("a rejected proposal changed its item: %+v", its)
	}
}

func TestTheMCPEndpointPostsToTheInbox(t *testing.T) {
	t.Parallel()
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te := newTestEnv(t, &fake.Manager{})
	te.registerSession(t, turn.Session{ID: "term_1", WorkspaceID: "ws-1"})
	d := mcpEndpoint{port: 47801}.wire(te.Env.serveMessages(te.serving(context.Background()), daemon.Deps{Turns: te.Turns}, db, te.cfg, t.Logf), te.cfg)
	if d.MCP == nil || d.Messages == nil || d.Tasks == nil {
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
	t.Parallel()
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
	d := te.Env.serveMessages(te.serving(context.Background()), daemon.Deps{}, db, te.cfg, t.Logf)
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

func TestARefusedAcceptKeepsItsProposalOpen(t *testing.T) {
	t.Parallel()
	log := change.NewLog(change.DefaultWindow, t.Logf)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te := newTestEnv(t, &fake.Manager{})
	d := servedMessages(t, te, db, log)
	te.Env.consequences = messageConsequences{d.Messages}
	ledger := te.Env.itemsLedger(db, te.cfg, t.Logf)
	ctx := context.Background()
	u := ghUnit(7)
	summary := "ship the fix today"
	op := items.Op{Op: items.OpUpdate, Unit: u, Item: "gone", Anchor: unit.Anchor{Unit: u, Ref: "turn:1"}, Fields: &items.Fields{Summary: &summary}}
	if err := db.Items().Commit(ctx, coreitems.Commit{Proposals: []coreitems.Proposal{{Unit: u, Item: "gone", Op: op, Provenance: items.Extracted,
		State: coreitems.ProposalOpen, At: testNow}}}); err != nil {
		t.Fatal(err)
	}
	m := waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 1 && ms[0].TaskID != "" })[0]
	gs, err := d.GateReader.Where(ctx, gates.Query{Kind: gate.KindProposal, Unit: u})
	if err != nil || len(gs) != 1 {
		t.Fatalf("gates %+v %v", gs, err)
	}
	err = d.Gates.Resolve(ctx, gs[0].ID, gates.Resolution{Intent: "approve", By: task.ActorPerson})
	if !errors.Is(err, messages.ErrNotApplied) || !strings.Contains(err.Error(), `unknown "gone"`) {
		t.Fatalf("approving a refused op: %v", err)
	}
	if open := ps(t, ledger); len(open) != 1 {
		t.Fatalf("open proposals %+v", open)
	}
	open, err := d.GateReader.Where(ctx, gates.Query{Kind: gate.KindProposal, Unit: u})
	if err != nil || len(open) != 1 || open[0].ID == gs[0].ID ||
		open[0].Subject != strconv.FormatInt(m.ID, 10) && open[0].Subject != gate.ProposalSubject(ps(t, ledger)[0].ID) {
		t.Fatalf("open gates %+v %v", open, err)
	}
	if its, _ := ledger.Items(ctx, u); len(its) != 0 {
		t.Fatalf("items %+v", its)
	}
}

// ps are ledger's open proposals.
func ps(t *testing.T, ledger *coreitems.Ledger) []coreitems.Proposal {
	t.Helper()
	open, err := ledger.Proposals(context.Background(), coreitems.ProposalOpen)
	if err != nil {
		t.Fatal(err)
	}
	return open
}
