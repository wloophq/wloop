package ingest_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/ingest/ingesttest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

type fixture struct {
	svc   *ingesttest.Service
	src   ingesttest.Source
	store *ingesttest.Store
	log   *change.Log
	reg   ingest.Registration
}

func newFixture(budget int64) *fixture {
	svc := ingesttest.NewService("viewer-1")
	log := change.NewLog(change.DefaultWindow, nil)
	f := &fixture{svc: svc, src: ingesttest.Source{C: ingesttest.NewClient(svc, budget)}, store: ingesttest.NewStore(log), log: log}
	f.reg = ingest.Registration{Source: f.src, Store: f.store, Codec: ingesttest.Codec()}
	return f
}

func exactly(t *testing.T, sc *meter.Scope, want metertest.Budget) {
	t.Helper()
	for l, n := range want {
		if got := metertest.Count(sc, l); got != n {
			t.Errorf("%s = %d, want %d", l, got, n)
		}
	}
}

func client(op string) metertest.Limit { return metertest.Limit{Res: ingesttest.ClientRes, Op: op} }

func points(op string) metertest.Limit {
	return metertest.Limit{Res: ingesttest.ClientRes, Op: op, Field: metertest.Points}
}

func storeOp(op string) metertest.Limit { return metertest.Limit{Res: ingesttest.StoreRes, Op: op} }

func th(key string) unit.Unit { return unit.Unit{Provider: ingesttest.Provider, Key: key} }

func run(t *testing.T, ctx context.Context, f *fixture, now time.Time) ingest.Report {
	t.Helper()
	rep, err := ingest.Run(ctx, f.reg, now)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestIngestDrivesAFakeThreadSource(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	f.svc.Post("C2 0002", "0002", "second ask")

	ctx, sc := metertest.Start(t, context.Background())
	rep := run(t, ctx, f, t0)
	if rep.Provider != ingesttest.Provider || rep.Discovered != 2 || rep.Moved != 2 || rep.Cursor != "0002" {
		t.Fatalf("first report = %+v", rep)
	}
	if !slices.Equal(rep.Read, []unit.Unit{th("C1 0001"), th("C2 0002")}) {
		t.Fatalf("first read = %v", rep.Read)
	}
	if c := f.log.Since(0, port.TopicSourceEvents); !slices.Equal(c.Units, rep.Read) {
		t.Fatalf("noted units = %v", c.Units)
	}
	if got, _ := f.store.Cursor(ctx); got != "0002" {
		t.Fatalf("stored cursor = %q", got)
	}
	exactly(t, sc, metertest.Budget{client("search"): 1, client("stubs"): 0, client("read"): 1, points("read"): 2})

	f.svc.Post("C1 0001", "0003", "a reply")
	gen := f.log.Gen()
	ctx2, sc2 := metertest.Start(t, context.Background())
	rep = run(t, ctx2, f, t0.Add(time.Minute))
	if rep.Discovered != 1 || rep.Moved != 1 || rep.Cursor != "0003" || !slices.Equal(rep.Read, []unit.Unit{th("C1 0001")}) {
		t.Fatalf("second report = %+v", rep)
	}
	exactly(t, sc2, metertest.Budget{client("search"): 1, client("stubs"): 1, points("read"): 1})
	if c := f.log.Since(gen, port.TopicSourceEvents); !slices.Equal(c.Units, []unit.Unit{th("C1 0001")}) {
		t.Fatalf("second noted units = %v", c.Units)
	}
	if got, ok := f.store.Stored("C1 0001"); !ok || got.Version != "0003" || len(got.Replies) != 2 {
		t.Fatalf("stored facts = %+v", got)
	}
	if v, err := f.src.Viewer(ctx2); err != nil || v != "viewer-1" {
		t.Fatalf("Viewer = %q %v", v, err)
	}
}

func TestADMObjectYieldsTwoSegments(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("D1 0001", "0001", "deploy and the review", "deploy", "review")

	ctx, sc := metertest.Start(t, context.Background())
	rep := run(t, ctx, f, t0)
	want := []unit.Unit{th("D1 0001/deploy"), th("D1 0001/review")}
	if rep.Discovered != 1 || rep.Moved != 1 || !slices.Equal(rep.Read, want) {
		t.Fatalf("report = %+v", rep)
	}
	if c := f.log.Since(0, port.TopicSourceEvents); !slices.Equal(c.Units, want) {
		t.Fatalf("noted units = %v", c.Units)
	}
	exactly(t, sc, metertest.Budget{points("read"): 1})
	stubs, err := f.src.Stubs(ctx, []string{"D1 0001"})
	if err != nil || len(stubs) != 1 || len(stubs[0].Light.Units()) != 0 {
		t.Fatalf("a DM stub named its segments: %+v %v", stubs, err)
	}
}

func TestAPushTouchesOneObject(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	f.svc.Post("C2 0002", "0002", "second ask")
	run(t, context.Background(), f, t0)

	f.svc.Post("C2 0002", "0003", "a reply")
	gen := f.log.Gen()
	ctx, sc := metertest.Start(t, context.Background())
	rep, err := ingest.Touch(ctx, f.reg, []string{"C2 0002"}, t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Moved != 1 || rep.Cursor != "0002" || !slices.Equal(rep.Read, []unit.Unit{th("C2 0002")}) {
		t.Fatalf("report = %+v", rep)
	}
	exactly(t, sc, metertest.Budget{client("search"): 0, client("stubs"): 0, client("read"): 1, points("read"): 1})
	if c := f.log.Since(gen, port.TopicSourceEvents); !slices.Equal(c.Units, []unit.Unit{th("C2 0002")}) {
		t.Fatalf("noted units = %v", c.Units)
	}
	if got, _ := f.store.Cursor(ctx); got != "0002" {
		t.Fatalf("a push moved the cursor to %q", got)
	}
	if !f.store.Seen("C2 0002").Equal(t0.Add(time.Second)) || !f.store.Seen("C1 0001").Equal(t0) {
		t.Fatal("a push saw objects it did not touch")
	}
}

func TestTheSourceNeverWrites(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	f.svc.Post("D1 0002", "0002", "two topics", "a", "b")

	ctx, sc := metertest.Start(t, context.Background())
	stubs, _, err := f.src.Discover(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.src.Stubs(ctx, []string{"C1 0001"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.src.Read(ctx, []string{"C1 0001", "D1 0002"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.src.Viewer(ctx); err != nil {
		t.Fatal(err)
	}
	if len(stubs) != 2 || f.log.Gen() != 0 {
		t.Fatalf("stubs %d, log gen %d", len(stubs), f.log.Gen())
	}
	exactly(t, sc, metertest.Budget{storeOp("save"): 0, storeOp("see"): 0})
	if _, ok := f.store.Stored("C1 0001"); ok {
		t.Fatal("a source call stored facts")
	}
	if got, _ := f.store.Cursor(ctx); got != "" {
		t.Fatalf("a source call moved the cursor to %q", got)
	}

	ctx2, sc2 := metertest.Start(t, context.Background())
	run(t, ctx2, f, t0)
	exactly(t, sc2, metertest.Budget{storeOp("save"): 1, storeOp("see"): 1})
}

func TestUnmovedVersionIsNotRead(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	run(t, context.Background(), f, t0)

	gen := f.log.Gen()
	ctx, sc := metertest.Start(t, context.Background())
	rep := run(t, ctx, f, t0.Add(time.Minute))
	if rep.Discovered != 0 || rep.Moved != 0 || rep.Read != nil {
		t.Fatalf("report = %+v", rep)
	}
	exactly(t, sc, metertest.Budget{client("stubs"): 1, client("read"): 0, storeOp("save"): 0})
	if f.log.Gen() != gen {
		t.Fatal("an unmoved pass noted the log")
	}

	f.store.Expire("C1 0001")
	ctx2, sc2 := metertest.Start(t, context.Background())
	rep = run(t, ctx2, f, t0.Add(2*time.Minute))
	if rep.Moved != 1 {
		t.Fatalf("an expired detail was not read: %+v", rep)
	}
	exactly(t, sc2, metertest.Budget{client("read"): 1})
}

func TestDiscoverCursorAdvancesOnlyOnSave(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	f.store.FailSave = errors.New("disk full")

	ctx := context.Background()
	if _, err := ingest.Run(ctx, f.reg, t0); err == nil || !errors.Is(err, f.store.FailSave) {
		t.Fatalf("Run = %v", err)
	}
	if got, _ := f.store.Cursor(ctx); got != "" {
		t.Fatalf("cursor advanced to %q without a save", got)
	}
	if !f.store.Seen("C1 0001").IsZero() {
		t.Fatal("an unsaved object was seen")
	}

	f.store.FailSave = nil
	rep := run(t, ctx, f, t0.Add(time.Minute))
	if rep.Discovered != 1 || rep.Cursor != "0001" {
		t.Fatalf("report = %+v", rep)
	}
	if got, _ := f.store.Cursor(ctx); got != "0001" {
		t.Fatalf("stored cursor = %q", got)
	}
}

func TestSeeNotesNothing(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	run(t, context.Background(), f, t0)

	gen := f.log.Gen()
	later := t0.Add(time.Hour)
	ctx, sc := metertest.Start(t, context.Background())
	run(t, ctx, f, later)
	if !f.store.Seen("C1 0001").Equal(later) {
		t.Fatalf("seen = %v", f.store.Seen("C1 0001"))
	}
	if f.log.Gen() != gen {
		t.Fatal("seeing an object noted the log")
	}
	exactly(t, sc, metertest.Budget{storeOp("see"): 1, storeOp("save"): 0})
}

func TestReadErrorKeepsCursor(t *testing.T) {
	f := newFixture(1)
	f.svc.Post("C1 0001", "0001", "first ask")

	ctx, sc := metertest.Start(t, context.Background())
	_, err := ingest.Run(ctx, f.reg, t0)
	if !errors.Is(err, ingesttest.ErrBudget) {
		t.Fatalf("Run = %v", err)
	}
	if got, _ := f.store.Cursor(ctx); got != "" {
		t.Fatalf("cursor = %q after a failed read", got)
	}
	if _, ok := f.store.Stored("C1 0001"); ok || f.log.Gen() != 0 {
		t.Fatal("a failed read saved facts")
	}
	exactly(t, sc, metertest.Budget{storeOp("save"): 0, storeOp("see"): 0, points("search"): 1, metertest.Limit{Res: ingesttest.ClientRes, Op: "read", Field: metertest.Errors}: 1})
}

type foreign struct{ ingesttest.Source }

func (foreign) Read(context.Context, []string) (port.Facts, error) {
	return ingesttest.Facts{Threads: []ingesttest.ThreadFacts{{Object: "C1 0001", Units: []unit.Unit{{Provider: unit.GitHub, Key: "o/r#1"}}}}}, nil
}

func TestAUnitOfAnotherProviderIsRefused(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	f.reg.Source = foreign{f.src}
	if _, err := ingest.Run(context.Background(), f.reg, t0); err == nil {
		t.Fatal("a github unit from the thread source was saved")
	}
	if f.log.Gen() != 0 {
		t.Fatal("a refused pass noted the log")
	}
	f.reg.Codec = unit.OpaqueCodec{ID: "other"}
	if _, err := ingest.Run(context.Background(), f.reg, t0); err == nil {
		t.Fatal("a source ran under another provider's codec")
	}
}
