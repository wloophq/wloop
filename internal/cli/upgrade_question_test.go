package cli

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// TestAnOpenQuestionOnALiveSessionReachesItAfterTheUpgrade upgrades a version 6 store holding a question its
// live session asked, as the daemon does on start, then answers the question's gate: the answer is typed
// into that session.
func TestAnOpenQuestionOnALiveSessionReachesItAfterTheUpgrade(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	at := testNow.Add(-time.Hour).UTC().Format(event.TimeLayout)
	sqlitetest.At(t, te.storePath(), 6,
		`INSERT INTO turns (seq, provider, unit_key, intent, turn_id, session, sent_to, state, sent_at, started_at) VALUES (1, 'github', 'acme/api#7', 'review', 'r1', 's1', 's1', 'asked', '`+at+`', '`+at+`')`,
		`INSERT INTO turn_records (turn_seq, provider, unit_key, session, hash, at, acked, record) VALUES (1, 'github', 'acme/api#7', 's1', 'h', '`+at+`', 1,
			'{"state":"asked","summary":"drafted","resume":"ready","question":{"kind":"confirm","text":"Publish 7?"}}')`,
		`INSERT INTO messages (id, entry_id, provider, unit_key, kind, text, confirm, origin_kind, turn_seq, session, state, created_at) VALUES
			(10, '', 'github', 'acme/api#7', 'question', 'Publish 7?', 1, 'turn', 1, 's1', 'open', '`+at+`')`,
		`INSERT INTO sessions (session_id, manager, agent, pane, workspace_id, worktree, started_at, mcp_at, provider, unit_key) VALUES
			('s1', 'fake', 'claude', 'p1', 'ws1', '/wt/rev', '`+at+`', '`+at+`', 'github', 'acme/api#7')`,
		`INSERT INTO session_handles (session_id, manager, handle, from_at) VALUES ('s1', 'fake', 'h1', '`+at+`')`,
	)
	ctx := context.Background()
	if err := te.migrateInDaemon(ctx, te.storePath(), t.Logf); err != nil {
		t.Fatal(err)
	}
	log := change.NewLog(change.DefaultWindow, t.Logf)
	db, err := sqlitetest.Open(te.storePath(), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sessions := turnstest.NewSessions()
	sessions.Put("ws1", turns.Terminal{Session: "s1", Key: "h1", LastOutput: testNow.Add(-time.Minute)})
	repo := sqlite.New(db)
	te.Env.Turns = &turns.Service{Intents: registryPort{te.Env}, Store: repo, Registry: repo, Sessions: sessions, Clock: clockFunc(te.Now), Command: brand.Name}
	d := servedMessages(t, te, db, log)
	te.Env.consequences = messageConsequences{d.Messages}
	if err := d.Messages.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	open := waitOpen(t, d, func(ms []message.Message) bool { return len(ms) == 1 })
	if open[0].ID != 10 || open[0].Origin.Session != "s1" {
		t.Fatalf("the migrated question %+v", open[0])
	}
	gs, err := d.GateReader.Where(ctx, gates.Query{Kind: gate.KindQuestion})
	if err != nil || len(gs) != 1 || gs[0].Subject != "10" {
		t.Fatalf("question gates %+v %v", gs, err)
	}
	route := &messages.Delivery{Source: turn.Cockpit}
	if err := d.Gates.Resolve(messages.WithDelivery(ctx, route), gs[0].ID, gates.Resolution{Intent: "answer", Answer: "yes", By: task.ActorPerson}); err != nil {
		t.Fatal(err)
	}
	if route.Route.Route != turn.Typed {
		t.Fatalf("the answer went %+v", route.Route)
	}
	if typed := sessions.Typed(); len(typed) != 1 || typed[0].Handle != "s1" {
		t.Fatalf("typed %+v, want into the asking session", typed)
	}
	books, err := d.Messages.Books.Books(ctx, open[0].Unit)
	if err != nil {
		t.Fatal(err)
	}
	answers := books[open[0].Unit].Answers
	if !slices.ContainsFunc(answers, func(a turn.Answer) bool {
		return a.Message == 10 && a.Gate == gs[0].ID && a.Yes && a.Route == turn.Typed
	}) {
		t.Fatalf("answers %+v", answers)
	}
}
