package cli

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestServeResolvesLinkedItems(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.Env.changes = change.NewLog(change.DefaultWindow, t.Logf)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "store.db"), te.Env.changes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var built source.Deps
	saved := plugins
	plugins = []source.Plugin{{ID: unit.GitHub, Codec: pr.Codec{}, Build: func(cfg config.Config, sd source.Deps) (source.Module, error) {
		built = sd
		return ghsource.Module(cfg, sd)
	}}}
	defer func() { plugins = saved }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, nil)
	if built.Links == nil || built.Units == nil || built.Units() != d.Units {
		t.Fatalf("source deps: links %v, units set %v", built.Links, built.Units != nil)
	}
	var resolver daemon.PluginRuntime
	for _, rt := range d.Runtimes {
		if rt.ID == linksRuntime {
			resolver = rt
		}
	}
	if resolver.Run == nil {
		t.Fatalf("no resolver among %+v", d.Runtimes)
	}

	thread := unit.Unit{Provider: "thread", Key: "C1 0001"}
	asked := unit.Anchor{Unit: thread, Ref: "C1/0001"}
	kind, summary, target := items.Request, "merge acme/api#7", r7.Unit()
	ledger := te.Env.itemsLedger(db, te.cfg, t.Logf)
	if res, err := ledger.Apply(ctx, items.Extracted, []items.Op{
		{Op: items.OpCreate, Unit: thread, Item: "i1", Anchor: asked, Fields: &items.Fields{Kind: &kind, Summary: &summary}},
		{Op: items.OpLink, Unit: thread, Item: "i1", Anchor: asked, To: &target, LinkKind: link.Merge},
	}); err != nil || len(res.Refused) > 0 {
		t.Fatalf("items: %+v %v", res.Refused, err)
	}
	if _, err := inventory.New(db).AppendEvents(ctx, []dinv.Event{{PR: r7, Kind: dinv.KindMerged, Actor: "ana", At: testNow.Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- resolver.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got, err := ledger.Items(ctx, thread)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 1 && got[0].State == items.Resolved {
			if got[0].Resolution != items.Done || got[0].Evidence.Kind != items.EvidenceUnitEvent {
				t.Fatalf("item = %+v", got[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the merge did not resolve the item: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type runs struct {
	mu    sync.Mutex
	calls [][]unit.Unit
	fail  int
	got   chan struct{}
}

func (r *runs) run(_ context.Context, units []unit.Unit) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, slices.Clone(units))
	defer func() { r.got <- struct{}{} }()
	if r.fail > 0 {
		r.fail--
		return 0, errors.New("store busy")
	}
	return 0, nil
}

func (r *runs) wait(t *testing.T, n int) [][]unit.Unit {
	t.Helper()
	for range n {
		select {
		case <-r.got:
		case <-time.After(10 * time.Second):
			t.Fatalf("waited for run %d", n)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

func TestResolverCatchesUpOnStart(t *testing.T) {
	t.Parallel()
	log := change.NewLog(change.DefaultWindow, t.Logf)
	pull := r7.Unit()
	log.Note(port.TopicSourceEvents, pull)
	r := &runs{got: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go resolveNoted(ctx, r.run, func(context.Context) ([]unit.Unit, error) { return []unit.Unit{pull}, nil }, log, t.Logf)
	if calls := r.wait(t, 1); !slices.Equal(calls[0], []unit.Unit{pull}) {
		t.Fatalf("first run = %v", calls)
	}
}

func TestResolverRetriesFailedUnits(t *testing.T) {
	saved := linksRetry
	linksRetry = 10 * time.Millisecond
	defer func() { linksRetry = saved }()
	log := change.NewLog(change.DefaultWindow, t.Logf)
	pull := r7.Unit()
	r := &runs{got: make(chan struct{}, 8), fail: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seeds := 0
	seed := func(context.Context) ([]unit.Unit, error) {
		if seeds++; seeds == 1 {
			return nil, errors.New("store busy")
		}
		return []unit.Unit{pull}, nil
	}
	go resolveNoted(ctx, r.run, seed, log, t.Logf)
	calls := r.wait(t, 2)
	if !slices.Equal(calls[0], []unit.Unit{pull}) || !slices.Equal(calls[1], []unit.Unit{pull}) {
		t.Fatalf("runs = %v", calls)
	}
}
