package meter_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/meter"
)

func registryCtx() (context.Context, *meter.Registry) {
	r := meter.NewRegistry(nil)
	return meter.WithRegistry(context.Background(), r), r
}

func registrySum(r *meter.Registry) meter.Tally {
	sum := meter.Tally{}
	for _, tot := range r.Totals() {
		sum = sum.Merge(tot.Tally)
	}
	return sum
}

func TestRegistryTotalsAreDisjoint(t *testing.T) {
	root, r := registryCtx()
	outer, so := meter.Start(root, meter.Job("fast"))
	mid, sm := meter.Start(outer, "collect")
	inner, si := meter.Start(mid, "inventory")
	ctxs := []context.Context{root, outer, mid, inner}

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := g; i < 1000; i += 8 {
				meter.Add(ctxs[i%len(ctxs)], meter.Sample{Res: meter.SQL, Op: "query", Rows: 2})
			}
		}()
	}
	wg.Wait()
	si.End()
	sm.End()
	so.End()

	for sc, want := range map[*meter.Scope]int64{so: 750, sm: 500, si: 250} {
		if got := sc.Tally().Total(meter.SQL).Calls; got != want {
			t.Errorf("%s = %d calls, want %d", sc.Name(), got, want)
		}
	}
	got := registrySum(r).Total(meter.SQL)
	if got.Calls != 1000 || got.Rows != 2000 {
		t.Fatalf("registry sum = %d calls, %d rows; want 1000, 2000", got.Calls, got.Rows)
	}
	names := map[string]int64{}
	for _, tot := range r.Totals() {
		names[tot.Name] = tot.Tally.Total(meter.SQL).Calls
	}
	want := map[string]int64{meter.Untagged: 250, "job:fast": 250, "job:fast/collect": 250, "job:fast/collect/inventory": 250}
	for name, n := range want {
		if names[name] != n {
			t.Errorf("%s = %d calls, want %d (all: %v)", name, names[name], n, names)
		}
	}
	if len(names) != len(want) {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestParentIncludesChild(t *testing.T) {
	ctx, parent := meter.Start(context.Background(), "parent")
	child, sc := meter.Start(ctx, "child")
	meter.Add(ctx, meter.Sample{Res: meter.Git, Op: "fetch", Spawn: true})
	meter.Add(child, meter.Sample{Res: meter.Git, Op: "fetch", Spawn: true})
	meter.Add(child, meter.Sample{Res: meter.SQL, Op: "query"})

	if sc.Name() != "parent/child" {
		t.Errorf("child name = %q", sc.Name())
	}
	if got := sc.Tally().Total(meter.Git).Calls; got != 1 {
		t.Errorf("child git = %d, want 1", got)
	}
	pt := parent.Tally()
	if pt.Total(meter.Git).Calls != 2 || pt.Total(meter.Git).Spawns != 2 || pt.Total(meter.SQL).Calls != 1 {
		t.Errorf("parent tally = %v, want 2 git spawns and 1 sql", pt)
	}
}

func TestSpendAddsNoCall(t *testing.T) {
	root, r := registryCtx()
	ctx, sc := meter.Start(root, "s")
	meter.Add(ctx, meter.Sample{Res: meter.GitHub, Op: "graphql", Spawn: true})
	meter.Spend(ctx, meter.GitHub, "graphql", 3)
	meter.Spend(root, meter.GitHub, "graphql", 5)

	got := sc.Tally()[meter.Key{Res: meter.GitHub, Op: "graphql"}]
	if got.Calls != 1 || got.Points != 3 {
		t.Errorf("scope = %+v, want 1 call and 3 points", got)
	}
	sum := registrySum(r).Total(meter.GitHub)
	if sum.Calls != 1 || sum.Points != 8 {
		t.Errorf("registry = %+v, want 1 call and 8 points", sum)
	}
}

func TestAddWithoutScopeAllocatesNothing(t *testing.T) {
	ctx := context.Background()
	x := meter.Sample{Res: meter.SQL, Op: "query", Detail: "select 1"}
	if n := testing.AllocsPerRun(1000, func() { meter.Add(ctx, x) }); n != 0 {
		t.Errorf("Add allocated %v times per run", n)
	}
}

func TestDetailOnlyWhenTracing(t *testing.T) {
	ctx, plain := meter.Start(context.Background(), "plain")
	if meter.Tracing(ctx) {
		t.Fatal("a plain context traces")
	}
	meter.Add(ctx, meter.Sample{Res: meter.SQL, Op: "query", Detail: "select 1"})
	if s := plain.Samples(); s != nil {
		t.Errorf("an untraced scope kept samples %v", s)
	}

	traced := meter.Trace(context.Background())
	outer, so := meter.Start(traced, "outer")
	inner, si := meter.Start(outer, "inner")
	if !meter.Tracing(inner) {
		t.Fatal("a scope under Trace does not trace")
	}
	meter.Add(inner, meter.Sample{Res: meter.SQL, Op: "query", Detail: "select 1"})
	for _, sc := range []*meter.Scope{so, si} {
		s := sc.Samples()
		if len(s) != 1 || s[0].Detail != "select 1" {
			t.Errorf("%s samples = %v, want one with its detail", sc.Name(), s)
		}
	}

	nested, sn := meter.Start(ctx, "nested")
	meter.Add(nested, meter.Sample{Res: meter.SQL, Op: "query", Detail: "select 2"})
	for _, sc := range []*meter.Scope{plain, sn} {
		if s := sc.Samples(); s != nil {
			t.Errorf("%s kept samples %v", sc.Name(), s)
		}
	}
}

func TestUntagged(t *testing.T) {
	root, r := registryCtx()
	meter.Add(root, meter.Sample{Res: meter.Workspace, Op: "worktree ps", Spawn: true})
	meter.Add(context.Background(), meter.Sample{Res: meter.Workspace, Op: "worktree ps"})

	tots := r.Totals()
	if len(tots) != 1 || tots[0].Name != meter.Untagged {
		t.Fatalf("totals = %+v, want one untagged", tots)
	}
	if got := tots[0].Tally.Total(meter.Workspace); got.Calls != 1 || got.Spawns != 1 {
		t.Errorf("untagged = %+v, want 1 call and 1 spawn", got)
	}
	if tots[0].Runs != 0 || tots[0].Last != nil {
		t.Errorf("untagged runs = %d, last = %v; want none", tots[0].Runs, tots[0].Last)
	}
}

func TestEndTwice(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	r := meter.NewRegistry(clock)
	ctx, sc := meter.Start(meter.WithRegistry(context.Background(), r), meter.Loop("views"))
	meter.Add(ctx, meter.Sample{Res: meter.Work, Op: "rebuild rows"})
	now = now.Add(2 * time.Second)
	first := sc.End()
	now = now.Add(time.Second)
	second := sc.End()

	if first.Total(meter.Work).Calls != 1 || second.Total(meter.Work).Calls != 1 {
		t.Errorf("ends = %v, %v; want 1 work call each", first, second)
	}
	tots := r.Totals()
	if len(tots) != 1 || tots[0].Runs != 1 || tots[0].Wall != 2*time.Second || tots[0].LastWall != 2*time.Second {
		t.Fatalf("totals = %+v, want one run of 2s", tots)
	}
	if !tots[0].LastAt.Equal(time.Unix(1002, 0)) || tots[0].Last.Total(meter.Work).Calls != 1 {
		t.Errorf("last = %v at %v", tots[0].Last, tots[0].LastAt)
	}
	if !r.Since().Equal(time.Unix(1000, 0)) {
		t.Errorf("since = %v", r.Since())
	}
}

func TestSampleAfterEndReachesRegistryOnly(t *testing.T) {
	root, r := registryCtx()
	ctx, sc := meter.Start(root, meter.Route("GET /api/rows"))
	meter.Add(ctx, meter.Sample{Res: meter.SQL, Op: "query"})
	sc.End()
	meter.Add(ctx, meter.Sample{Res: meter.SQL, Op: "query"})

	if got := sc.Tally().Total(meter.SQL).Calls; got != 1 {
		t.Errorf("scope = %d calls, want 1", got)
	}
	tots := r.Totals()
	if len(tots) != 1 || tots[0].Tally.Total(meter.SQL).Calls != 2 || tots[0].Last.Total(meter.SQL).Calls != 1 {
		t.Errorf("totals = %+v, want 2 calls with 1 in the last run", tots)
	}
}

func BenchmarkAdd(b *testing.B) {
	root, _ := registryCtx()
	outer, _ := meter.Start(root, meter.Job("fast"))
	ctx, _ := meter.Start(outer, "collect")
	x := meter.Sample{Res: meter.SQL, Op: "query", Rows: 1}
	b.ReportAllocs()
	for b.Loop() {
		meter.Add(ctx, x)
	}
}

func TestRegistryAttachedBelowAScope(t *testing.T) {
	ctx, sc := meter.Start(context.Background(), "outer")
	r := meter.NewRegistry(nil)
	meter.Add(meter.WithRegistry(ctx, r), meter.Sample{Res: meter.SQL, Op: "query"})

	if got := sc.Tally().Total(meter.SQL).Calls; got != 1 {
		t.Errorf("scope = %d calls, want 1", got)
	}
	tots := r.Totals()
	if len(tots) != 1 || tots[0].Name != "outer" || tots[0].Tally.Total(meter.SQL).Calls != 1 {
		t.Errorf("totals = %+v, want 1 call under outer", tots)
	}
}

func TestRegistryRecentCountsOnlyTheWindow(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	r := meter.NewRegistry(func() time.Time { return now })
	ctx := meter.WithRegistry(context.Background(), r)
	sweep := func(calls int) {
		c, sc := meter.Start(ctx, meter.Job("sweep"))
		for range calls {
			meter.Add(c, meter.Sample{Res: meter.Git, Op: "status", Spawn: true})
		}
		sc.End()
	}
	sweep(300)
	for range 12 {
		now = now.Add(time.Minute)
		sweep(5)
	}

	since, recent := r.Recent(10 * time.Minute)
	if want := now.Add(-10 * time.Minute); !since.Equal(want) {
		t.Errorf("since = %v, want %v", since, want)
	}
	if len(recent) != 1 || recent[0].Runs != 11 || recent[0].Tally.Total(meter.Git).Calls != 55 || recent[0].Tally.Total(meter.Git).Spawns != 55 {
		t.Fatalf("recent = %+v; want the 11 sweeps since the mark, 55 git calls", recent)
	}
	if tots := r.Totals(); tots[0].Tally.Total(meter.Git).Calls != 360 {
		t.Errorf("totals = %d git calls, want 360", tots[0].Tally.Total(meter.Git).Calls)
	}
}

func TestRegistryRecentBeforeTheWindowFillsIsSinceStart(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	r := meter.NewRegistry(func() time.Time { return now })
	meter.Add(meter.WithRegistry(context.Background(), r), meter.Sample{Res: meter.SQL, Op: "query"})
	now = now.Add(3 * time.Minute)
	since, recent := r.Recent(10 * time.Minute)
	if !since.Equal(r.Since()) || len(recent) != 1 || recent[0].Tally.Total(meter.SQL).Calls != 1 {
		t.Fatalf("since %v, recent %+v", since, recent)
	}
}
