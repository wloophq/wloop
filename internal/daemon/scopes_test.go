package daemon

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/intake"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
	"github.com/wloophq/wloop/internal/readmodel"
)

func totalOf(reg *meter.Registry, name string) (meter.Total, bool) {
	for _, tot := range reg.Totals() {
		if tot.Name == name {
			return tot, true
		}
	}
	return meter.Total{}, false
}

func serve(t *testing.T, s *Server, ctx context.Context, method, path string) {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, method, path, nil)
	req.Header.Set(api.Header, api.Version)
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
}

func TestRouteScopeNamesPattern(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	reg := meter.NewRegistry(nil)
	serve(t, s, meter.WithRegistry(context.Background(), reg), http.MethodGet, "/api/units/github/o%2Fr%231")
	if _, ok := totalOf(reg, "route:GET "+api.UnitDetail); !ok {
		t.Fatalf("totals %+v", reg.Totals())
	}
}

func TestEveryRouteIsScoped(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	for _, op := range api.Operations {
		reg := meter.NewRegistry(nil)
		ctx, cancel := context.WithTimeout(meter.WithRegistry(context.Background(), reg), 200*time.Millisecond)
		path := op.Path
		for _, w := range []string{"{owner}", "{repo}", "{n}", "{action}", "{name}", "{id}", "{handle}", "{key}"} {
			path = strings.ReplaceAll(path, w, "1")
		}
		serve(t, s, ctx, op.Method, path)
		cancel()
		if _, ok := totalOf(reg, meter.Route(op.Pattern())); !ok {
			t.Errorf("%s has no scope; totals %+v", op.Pattern(), reg.Totals())
		}
	}
}

func TestRequestUnderTestScopeNests(t *testing.T) {
	t.Parallel()
	s := newServerWith(t, func(d *Deps) {
		d.Identity = func(ctx context.Context) (src.Identity, error) {
			meter.Add(ctx, meter.Sample{Res: meter.GitHub, Op: "viewer"})
			return identityOf("me"), nil
		}
	})
	ctx, scope := metertest.Start(t, context.Background())
	serve(t, s, ctx, http.MethodGet, "/api/rows")
	metertest.Within(t, scope, metertest.Budget{{Res: meter.GitHub}: 1})
}

func TestIntakeDrivesReachTheRegistry(t *testing.T) {
	t.Parallel()
	reg := meter.NewRegistry(nil)
	s, _ := newIntakeServer(t, nil, func(d *Deps) { d.Meter = reg })
	ctx := context.Background()
	at := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	waiting, _, err := s.deps.Intakes.Accept(ctx, intake.Intake{Unit: r7.Unit(), Intent: turn.Intent(dintent.Review), Source: "cli", Request: []byte(`{"action":"review","guidance":"again"}`), State: intake.Briefing, AcceptedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	s.recoverIntakes(ctx)
	waitFor(t, func() bool { return intakeOf(t, s, waiting.Seq).State == intake.Started })
	s.driving.Wait()
	tot, ok := totalOf(reg, "loop:intake")
	if !ok || tot.Tally.Total(meter.GitHub).Calls != 1 {
		t.Fatalf("totals %+v", reg.Totals())
	}
	if _, ok := totalOf(reg, meter.Untagged); ok {
		t.Fatalf("untagged samples: %+v", reg.Totals())
	}
}

func TestFastPhasesNest(t *testing.T) {
	t.Parallel()
	reg := meter.NewRegistry(nil)
	s := newServerWith(t, func(d *Deps) {
		d.Meter = reg
		d.StateDir = t.TempDir()
		d.Collector = failingCollector(t, *d)
	})
	ctx := meter.WithRegistry(context.Background(), reg)
	s.jobs["fast"].once(ctx, t.Logf, func(string, string) {})
	var fast meter.Tally
	for _, sc := range s.wireMeter(time.Now()).Scopes {
		if sc.Name == meter.Job("fast") {
			for _, st := range sc.Stats {
				fast = fast.Merge(meter.Tally{{Res: meter.Resource(st.Res), Op: st.Op}: {Calls: st.Calls}})
			}
		}
	}
	sum := meter.Tally{}
	var phases int
	for _, tot := range reg.Totals() {
		if strings.HasPrefix(tot.Name, "job:fast/") {
			sum = sum.Merge(tot.Tally)
			phases++
		}
	}
	if phases != 2 {
		t.Errorf("%d phases, want 2: turns, untrack", phases)
	}
	if got, want := sum.Total(meter.SQL).Calls, fast.Total(meter.SQL).Calls; got != want || want == 0 {
		t.Fatalf("phases sum %d sql calls, job %d (want equal and above 0)", got, want)
	}
}

func TestAnIngestJobRunsUnderItsScope(t *testing.T) {
	t.Parallel()
	reg := meter.NewRegistry(nil)
	job := ingest.Job{Every: time.Minute, Prepare: func(ctx context.Context, _ time.Time) (ingest.Plan, string, error) {
		meter.Add(ctx, meter.Sample{Res: meter.SQL, Op: "query"})
		return ingest.Plan{}, "budget low", nil
	}}
	s := newServerWith(t, func(d *Deps) {
		d.Meter = reg
		d.Ingest = []IngestJob{{ID: unit.GitHub, Job: job}}
	})
	j, ok := s.jobs["ingest:github"]
	if !ok {
		t.Fatalf("jobs %v", slices.Collect(maps.Keys(s.jobs)))
	}
	j.once(meter.WithRegistry(context.Background(), reg), t.Logf, func(string, string) {})
	if tot, ok := totalOf(reg, "job:ingest:github"); !ok || tot.Tally.Total(meter.SQL).Calls != 1 {
		t.Fatalf("totals %+v", reg.Totals())
	}
	if st := j.status(); st.Every != time.Minute || st.LastErr != "" {
		t.Fatalf("status %+v", st)
	}
}

type meteredRows struct{ *fakeRows }

func (p meteredRows) Listed(ctx context.Context) ([]unit.Unit, error) {
	meter.Add(ctx, meter.Sample{Res: meter.SQL, Op: "query"})
	return p.fakeRows.Listed(ctx)
}

func TestRowFeedRebuildsUnderItsScope(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	log := withLog(t, s)
	m := readmodel.New(log, nil, nil, nil, nil, meteredRows{&fakeRows{}})
	m.UseClock(s.deps.Now)
	s.modelOnce.Do(func() { s.model = m })
	reg := meter.NewRegistry(nil)
	ctx, cancel := context.WithCancel(meter.WithRegistry(context.Background(), reg))
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.feedRows(ctx)
	}()
	waitFor(t, func() bool { return s.hub.position() != (api.Since{}) })
	cancel()
	<-done
	if _, ok := totalOf(reg, meter.Loop("feed")); !ok {
		t.Fatalf("no loop:feed scope: %+v", reg.Totals())
	}
	if _, ok := totalOf(reg, meter.Untagged); ok {
		t.Fatalf("untagged samples: %+v", reg.Totals())
	}
}

type slowOnly struct{}

func (slowOnly) SlowJob(context.Context) (string, string, error) { return "", "", nil }

// drain runs every run of j that is due now, as its loop would, and returns
// how many ran.
func drain(t *testing.T, j *job) int {
	t.Helper()
	n := 0
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		ok := j.wait(ctx)
		cancel()
		if !ok {
			return n
		}
		j.once(context.Background(), t.Logf, func(string, string) {})
		if n++; n > 10 {
			t.Fatalf("%s runs without end", j.name)
		}
	}
}

func TestTheFastJobRunsOncePerIngestion(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clock := &stepClock{now: wakeStart}
		job := ingest.Job{Every: 2 * time.Minute, Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
			return ingest.Plan{}, "nothing to read", nil
		}}
		s := newServerWith(t, func(d *Deps) {
			d.Cfg.Sweep.Fast = config.Days(2 * time.Minute)
			d.Cfg.Sweep.Slow = config.Days(30 * time.Minute)
			d.Collector = slowOnly{}
			d.Ingest = []IngestJob{{ID: unit.GitHub, Job: job}}
		})
		in, fast := s.jobs[ingestJobName(unit.GitHub)], s.jobs["fast"]
		in.clock, fast.clock = clock, clock
		fast.once(context.Background(), t.Logf, func(string, string) {})
		ingested, ran := 0, 0
		for k := 1; k <= 10; k++ {
			clock.set(wakeStart.Add(time.Duration(k) * 2 * time.Minute))
			ran += drain(t, fast)
			in.once(context.Background(), t.Logf, func(string, string) {})
			ingested++
			ran += drain(t, fast)
		}
		if ran != ingested {
			t.Fatalf("fast ran %d times over %d ingestion runs, want once each", ran, ingested)
		}
		clock.set(wakeStart.Add(20*time.Minute + 30*time.Minute))
		if n := drain(t, fast); n != 1 {
			t.Fatalf("with no ingestion for a slow period the watchdog ran fast %d times, want 1", n)
		}
	})
}
