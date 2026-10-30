package cli

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	coreusage "github.com/wloophq/wloop/internal/core/usage"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	dusage "github.com/wloophq/wloop/internal/domain/usage"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var usageRef = pr.Ref{Owner: "acme", Repo: "api", Number: 41}

type fakeUsage struct {
	mu   sync.Mutex
	asks []coreusage.Turn
}

func (f *fakeUsage) Usage(_ context.Context, t coreusage.Turn) ([]dusage.Model, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asks = append(f.asks, t)
	return []dusage.Model{{Model: "opus", Messages: 2, Tokens: dusage.Tokens{Input: 10, Output: 20}, First: t.From, Last: t.To}}, nil
}

func (f *fakeUsage) asked() []coreusage.Turn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asks)
}

// seedStore writes into the daemon's store before it starts.
func seedStore(t *testing.T, dir string, seed func(context.Context, *sqlite.DB)) {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(dir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seed(context.Background(), db)
}

func endedTurn(t *testing.T, db *sqlite.DB, agent, agentSession string) int64 {
	t.Helper()
	b, err := sqlite.New(db).Save(context.Background(), dturn.Book{Unit: usageRef.Unit(), Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Review), ID: "r1", State: dturn.Done,
		SentAt: testNow.Add(-3 * time.Hour), StartedAt: testNow.Add(-2 * time.Hour), EndedAt: testNow.Add(-time.Hour), Agent: agent, AgentSession: agentSession}}})
	if err != nil {
		t.Fatal(err)
	}
	return b.Turns[0].Seq
}

// serveUntil runs serve and stops it once cond holds on its store.
func serveUntil(t *testing.T, te *testEnv, what string, cond func(*sqlite.DB) bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, te.Env, []string{"--mcp-port", strconv.Itoa(freePort(t))}) }()
	db, err := sqlitetest.Open(filepath.Join(te.StateDir, storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	eventually(t, what, func() bool { return cond(db) })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestServeRunsTheUsageJob(t *testing.T) {
	te := newServeEnv(t)
	reader := &fakeUsage{}
	te.Usage = map[string]coreusage.Reader{"claude": reader}
	var seq int64
	seedStore(t, te.StateDir, func(ctx context.Context, db *sqlite.DB) {
		seq = endedTurn(t, db, "claude", "a1")
		a := sqlite.AgentSession{ID: "a1", Kind: "claude", Home: "/h", Worktree: "/w", Transcript: "/h/projects/-w/a1.jsonl", FirstSeen: testNow.Add(-2 * time.Hour)}
		if err := db.SaveAgentSession(ctx, a); err != nil {
			t.Fatal(err)
		}
	})
	serveUntil(t, te, "the turn's usage", func(db *sqlite.DB) bool {
		var n int
		return db.R().QueryRowContext(context.Background(), `SELECT count(*) FROM turn_usage WHERE turn_seq = ? AND model = 'opus' AND output_tokens = 20`, seq).Scan(&n) == nil && n == 1
	})
	asked := reader.asked()
	if len(asked) == 0 || asked[0].Transcript != "/h/projects/-w/a1.jsonl" || asked[0].Kind != "claude" || !asked[0].From.Equal(testNow.Add(-2*time.Hour)) {
		t.Fatalf("asked %+v", asked)
	}
}

func TestServeInterruptsRunningBriefRuns(t *testing.T) {
	te := newServeEnv(t)
	var id int64
	seedStore(t, te.StateDir, func(ctx context.Context, db *sqlite.DB) {
		var err error
		id, err = inventory.New(db).StartBriefRun(ctx, dinv.BriefRun{PR: pr.Ref(usageRef), Trigger: "background", StartedAt: testNow.Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
	})
	serveUntil(t, te, "the run marked interrupted", func(db *sqlite.DB) bool {
		var outcome string
		return db.R().QueryRowContext(context.Background(), `SELECT outcome FROM brief_runs WHERE id = ?`, id).Scan(&outcome) == nil && outcome == "interrupted"
	})
	if log := te.Err.(*syncBuffer).String(); !strings.Contains(log, "brief: 1 runs cut short by the last stop") {
		t.Fatalf("log %s", log)
	}
}

func TestTheFirstBriefRunInterruptsWhenTheStartUpCouldNot(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	te.cfg.Brief.Enabled = true
	te.interrupted = new(atomic.Bool)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	store := inventory.New(db)
	running := func() int64 {
		id, err := store.StartBriefRun(ctx, dinv.BriefRun{PR: pr.Ref(usageRef), Trigger: "background", StartedAt: testNow.Add(-time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	outcome := func(id int64) string {
		var o string
		if err := db.R().QueryRowContext(context.Background(), `SELECT outcome FROM brief_runs WHERE id = ?`, id).Scan(&o); err != nil {
			t.Fatal(err)
		}
		return o
	}
	left := running()
	d := te.serveDeps(ctx, db, te.cfg, func(string, ...any) {}, &lock.Mutex{}, nil)
	if _, err := d.Brief.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if o := outcome(left); o != "interrupted" {
		t.Fatalf("the run a stop left: %s", o)
	}
	ours := running()
	if _, err := d.Brief.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if o := outcome(ours); o != "running" {
		t.Fatalf("a later run interrupted a running one: %s", o)
	}
}

func TestEchoTurnsAreUnavailable(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	te.Usage = map[string]coreusage.Reader{"claude": &fakeUsage{}}
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seq := endedTurn(t, db, "echo", "a1")
	d := te.serveDeps(context.Background(), db, te.cfg, func(string, ...any) {}, &lock.Mutex{}, nil)
	if _, err := d.Usage.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var source string
	var rows int
	if err := db.R().QueryRowContext(context.Background(), `SELECT usage_source, (SELECT count(*) FROM turn_usage WHERE turn_seq = seq) FROM turns WHERE seq = ?`, seq).Scan(&source, &rows); err != nil ||
		source != coreusage.SourceUnavailable || rows != 0 {
		t.Fatalf("source %q, %d rows, %v", source, rows, err)
	}
}

func TestServeKeepsTheAgentSessionsTheHooksReport(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	te.serveDeps(context.Background(), db, te.cfg, func(string, ...any) {}, &lock.Mutex{}, nil)
	st := turns.SessionStart{Session: "term_1", AgentSession: "a1", Cwd: "/w", Home: "/h", Agent: "claude", Transcript: "/h/projects/-w/a1.jsonl"}
	if err := te.Turns.SessionStarted(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	var kind, session, home, worktree, transcript string
	err = db.R().QueryRowContext(context.Background(), `SELECT kind, session, home, worktree, transcript FROM agent_sessions WHERE agent_session = 'a1'`).Scan(&kind, &session, &home, &worktree, &transcript)
	if err != nil || kind != "claude" || session != "term_1" || home != "/h" || worktree != "/w" || transcript != "/h/projects/-w/a1.jsonl" {
		t.Fatalf("%q %q %q %q %q %v", kind, session, home, worktree, transcript, err)
	}
}
