package cli

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var headlessThread = unit.Unit{Provider: "thread", Key: "C1/1"}

func TestServeEndsTheHeadlessTurnsAStopCutShortOnce(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	left := dturn.Book{Unit: headlessThread, Turns: []dturn.Turn{{Intent: dturn.Intent(dintent.Act), ID: "left", Trigger: dturn.ByHand,
		State: dturn.Started, SentAt: testNow, StartedAt: testNow, Mode: dturn.ModeHeadless}}}
	if _, err := sqlite.New(db).Save(ctx, left); err != nil {
		t.Fatal(err)
	}
	d := serveDeps(ctx, te.Env, db, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	if d.Headless == nil || te.Turns.Headless == nil || te.Turns.Usage == nil {
		t.Fatal("headless turns are not wired")
	}
	state := func(round string) dturn.State {
		t.Helper()
		b, ok, err := te.Turns.Store.BookOf(ctx, turns.Key{Turn: round})
		if err != nil || !ok {
			t.Fatalf("%v %v", ok, err)
		}
		tu, _ := b.ByID(round)
		return tu.State
	}
	if got := state("left"); got != dturn.Failed {
		t.Fatalf("the turn a stop left is %s", got)
	}
	if _, err := te.Turns.BeginHeadless(ctx, turns.HeadlessStart{Unit: unit.Unit{Provider: "thread", Key: "C1/2"}, Intent: dturn.Intent(dintent.Act), Turn: "live", Trigger: dturn.ByHand}); err != nil {
		t.Fatal(err)
	}
	serveDeps(ctx, te.Env, db, te.cfg, func(string, ...any) {}, &sync.Mutex{}, nil)
	if got := state("live"); got != dturn.Started {
		t.Fatalf("a later serve ended a live run: %s", got)
	}
}

func TestHeadlessPlacerReadsExistingCheckoutsOnly(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	dir := t.TempDir()
	cfg := te.cfg
	cfg.Placement = []config.PlacementRule{
		{Match: "thread:C1/*", Checkout: dir, Role: "work"},
		{Match: "thread:C2/*", Checkout: filepath.Join(dir, "missing"), Role: "work"},
		{Match: "thread:C3/*", Repo: "acme/api", Role: "work"},
	}
	p := headlessPlacer{e: te.Env, cfg: cfg}
	ctx := context.Background()
	if got, ok, err := p.Checkout(ctx, unit.Unit{Provider: "thread", Key: "C1/1"}, ""); err != nil || !ok || got != dir {
		t.Fatalf("an absolute checkout: %q %v %v", got, ok, err)
	}
	if _, _, err := p.Checkout(ctx, unit.Unit{Provider: "thread", Key: "C2/1"}, ""); err == nil {
		t.Fatal("a missing checkout was placed")
	}
	if got, ok, err := p.Checkout(ctx, unit.Unit{Provider: "thread", Key: "C3/1"}, ""); err != nil || ok {
		t.Fatalf("a new worktree placement: %q %v %v", got, ok, err)
	}
	if got, ok, err := p.Checkout(ctx, unit.Unit{Provider: "thread", Key: "C9/1"}, ""); err != nil || ok {
		t.Fatalf("an unplaced unit: %q %v %v", got, ok, err)
	}
}

func TestHeadlessBuilderRendersActOnAUnitWithNoPayloadReader(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	got, err := headlessBuilder{e: te.Env, cfg: te.cfg}.Instructions(context.Background(), headlessThread, dturn.Intent(dintent.Act), []string{"i1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Act on the open items of " + headlessThread.Label(), "turn_end"} {
		if !strings.Contains(got, want) {
			t.Errorf("the instructions lack %q:\n%s", want, got)
		}
	}
}

func TestTheServedAskerTellsWhenARestrictedCallCannotRun(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Agent = "nobody"
	a := lazyAsker{e: te.Env, cfg: te.cfg, dir: t.TempDir()}
	var r headless.Readier = a
	if err := r.Ready(context.Background(), turns.Ask{Tools: turns.ReadOnlyTools}); err == nil {
		t.Fatal("an unknown agent is ready")
	}
}
