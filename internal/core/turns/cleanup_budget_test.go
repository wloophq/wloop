package turns_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

type staleListing struct {
	turns.Registry
	afterList func()
}

func (r staleListing) Sessions(ctx context.Context) ([]turn.Session, error) {
	got, err := r.Registry.Sessions(ctx)
	r.afterList()
	return got, err
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlite.New(db)
}

func TestKeepSessionsBudget(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	agentSessions := turnstest.NewSessions()
	for i := range 75 {
		sess := loopSession(fmt.Sprintf("term_%d", i))
		sess.WorkspaceID = fmt.Sprintf("repo::/wt/acme-api-%d", i)
		if i < 5 {
			sess.WorkspaceID = cleanupWS
		}
		if err := store.SaveSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 5 {
		h := fmt.Sprintf("term_%d", i)
		agentSessions.Put(cleanupWS, turns.Terminal{Session: h, Key: "tab-" + h, Title: "review 18/10 10:00", Agent: "claude", LastOutput: t0})
		agents, _ := agentSessions.Agents(ctx)
		agentSessions.SetAgents(append(agents, turns.AgentObservation{WorkspaceID: cleanupWS, Pane: "tab-" + h + ":leaf-1", TerminalKey: "tab-" + h, State: turn.AgentWorking})...)
		book := turn.Book{Unit: ref(100 + i), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: fmt.Sprintf("r%d", i), Trigger: turn.Automated, State: turn.Done, Session: h, SentTo: h, SentAt: t0, StartedAt: t0, EndedAt: t0}}}
		if _, err := store.Save(ctx, book); err != nil {
			t.Fatal(err)
		}
	}
	s := &turns.Service{Intents: turnstest.Intents(), Store: store, Registry: store, Sessions: agentSessions, Clock: fixed(t0), Command: brand.Name, FastJob: 0}

	ctx, sc := metertest.Start(t, ctx)
	if err := s.KeepSessions(ctx); err != nil {
		t.Fatal(err)
	}
	metertest.Within(t, sc, metertest.Budget{
		{Res: meter.SQL}:             5,
		{Res: meter.Tx, Op: "write"}: 0,
	})
}

func TestKeepIdleRereadsBeforeWrite(t *testing.T) {
	ctx := context.Background()
	sess := turnstest.NewSessions()
	reg := turnstest.NewRegistry()
	tstore := turnstest.NewStore()
	cleanupCase{sess: loopSession("term_1")}.put(reg, tstore, sess)
	moved := loopSession("term_1")
	moved.Home = "/home/moved"
	s := &turns.Service{Intents: turnstest.Intents(), Store: tstore, Sessions: sess, Clock: fixed(t0), Command: brand.Name,
		Registry: staleListing{Registry: reg, afterList: func() { reg.Put(moved) }}}
	if err := s.KeepSessions(ctx); err != nil {
		t.Fatal(err)
	}
	got, _, _ := reg.Session(ctx, "term_1")
	if got.Home != "/home/moved" || got.IdleSince.IsZero() {
		t.Fatalf("session = %+v, want the moved home kept and the idle period started", got)
	}
}
