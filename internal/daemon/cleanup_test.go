package daemon

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/unit"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	coreviews "github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	"github.com/wloophq/wloop/internal/domain/turn"
)

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

// failingCollector registers the fast job and fails its collection at once,
// on a closed store, so the job runs without GitHub.
func failingCollector(t *testing.T, d Deps) *ghsource.Collector {
	t.Helper()
	db, err := sqlitetest.Open(filepath.Join(d.StateDir, "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	closed := inventory.New(db)
	db.Close()
	return &ghsource.Collector{Store: closed, GH: unknownBudget{}, Cfg: d.Cfg, Now: time.Now}
}

// unknownBudget admits every read and serves none: a collector on a closed
// store fails before it reads GitHub.
type unknownBudget struct{ ghsource.GitHub }

func (unknownBudget) Allow(dgh.Priority) bool            { return true }
func (unknownBudget) Budget() map[dgh.Resource]dgh.Limit { return nil }

func TestTheClosedLineIsPinnedForClaude(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	reg, sess := turnstest.NewRegistry(turnstest.Strict(t)), turnstest.NewSessions()
	reg.Put(turn.Session{ID: "term_1", Pane: "tab-1:leaf-1", WorkspaceID: "ws-1", Worktree: "/wt/a", Home: "/home/me/.claude-p",
		AgentSession: "s-1", Managed: true, IdleSince: now.Add(-2 * time.Hour), PromptSeenAt: now.Add(-3 * time.Hour)})
	sess.Put("ws-1", turns.Terminal{Session: "term_1", Key: "tab-1"})
	sess.SetAgents(turns.AgentObservation{WorkspaceID: "ws-1", Pane: "tab-1:leaf-1", TerminalKey: "tab-1", State: turn.AgentDone, StateSince: now.Add(-2 * time.Hour)})
	s := &turns.Service{Intents: turnstest.Intents(), Store: turnstest.NewStore(turnstest.Strict(t)), Registry: reg, Sessions: sess, Clock: fixedClock(now),
		AgentFor: func(unit.Unit) turns.Resumer { return claude.Agent{} }}
	if err := s.CloseSession(context.Background(), "term_1", "idle"); err != nil {
		t.Fatal(err)
	}
	want := `idle; resume with: cd '/wt/a' && CLAUDE_CONFIG_DIR='/home/me/.claude-p' claude --resume s-1`
	var got []string
	for _, ev := range reg.Events() {
		if ev.Kind == turns.KindSessionClosed {
			got = append(got, ev.Data)
		}
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("closed %q, want %q", got, want)
	}
}

type countingTurns struct {
	*turnstest.Store
	mu    sync.Mutex
	opens int
}

func (c *countingTurns) Open(ctx context.Context) ([]unit.Unit, error) {
	c.mu.Lock()
	c.opens++
	c.mu.Unlock()
	return c.Store.Open(ctx)
}

type countingBooks struct {
	mu    sync.Mutex
	reads int
}

func (c *countingBooks) Books(context.Context, ...unit.Unit) (map[unit.Unit]turn.Book, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads++
	return nil, nil
}

func TestTheFastJobReconcilesTheTurnsAndTheViews(t *testing.T) {
	t.Parallel()
	store := &countingTurns{Store: turnstest.NewStore(turnstest.Strict(t))}
	books := &countingBooks{}
	d := testDeps(t)
	d.Collector = failingCollector(t, d)
	d.Turns = &turns.Service{Intents: turnstest.Intents(), Store: store, Registry: turnstest.NewRegistry(turnstest.Strict(t)), Sessions: turnstest.NewSessions(), Clock: fixedClock(time.Now())}
	d.Views = &coreviews.Service{Books: books, Intents: turnstest.Intents(), State: viewstest.NewState(viewstest.Strict(t))}
	s := New(d)
	s.jobs["fast"].once(context.Background(), t.Logf, func(string, string) {})
	if store.opens != 1 || books.reads != 1 {
		t.Fatalf("one fast tick reconciled the turns %d times and the views %d times", store.opens, books.reads)
	}
}
