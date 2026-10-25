package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/meter"
)

func spend(ctx context.Context, res meter.Resource, op string, n int, spawn bool) {
	for range n {
		meter.Add(ctx, meter.Sample{Res: res, Op: op, Spawn: spawn, Dur: time.Millisecond})
	}
}

func meteredRegistry(t *testing.T, now *time.Time) (*meter.Registry, context.Context) {
	t.Helper()
	reg := meter.NewRegistry(func() time.Time { return *now })
	return reg, meter.WithRegistry(context.Background(), reg)
}

func TestTheMeterLoadIsByResource(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	reg, ctx := meteredRegistry(t, &now)
	_, scope := meter.Start(ctx, meter.Job("fast"))
	sctx, _ := meter.Start(ctx, meter.Job("slow"))
	spend(sctx, meter.SQL, "select", 2, false)
	spend(sctx, meter.SQL, "insert", 1, false)
	spend(sctx, meter.Git, "fetch", 1, true)
	scope.End()
	s := newServerWith(t, func(d *Deps) { d.Meter = reg })
	load := s.wireMeter(now).Load
	if len(load.Stats) > len(meter.Resources) {
		t.Fatalf("load %+v", load)
	}
	for _, st := range load.Stats {
		if st.Op != "" {
			t.Errorf("per-op entry %+v", st)
		}
	}
	if got := load.Stats[0]; got.Res != "sql" || got.Calls != 3 {
		t.Fatalf("%+v", load.Stats)
	}
}

func getHealth(t *testing.T, s *Server) api.Health {
	t.Helper()
	return getHealthRaw(t, s, nil)
}

func getHealthRaw(t *testing.T, s *Server, raw *[]byte) api.Health {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if raw != nil {
		*raw = slices.Clone(rec.Body.Bytes())
	}
	var h api.Health
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHealthGrowsAtMost2KB(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	names := []string{"fast", "slow", "sweep", "brief", "usage"}
	build := func(metered bool) []byte {
		reg, ctx := meteredRegistry(t, &now)
		s := newServerWith(t, func(d *Deps) {
			if metered {
				d.Meter = reg
			}
		})
		s.jobs = map[string]*job{}
		for _, name := range names {
			j := newJob(name, time.Hour, time.Minute, func(ctx context.Context) (string, string, error) {
				if metered {
					for _, res := range meter.Resources {
						spend(ctx, res, "op", 4, true)
						meter.Spend(ctx, res, "op", 9)
					}
				}
				return "ok", "", nil
			})
			s.jobs[name] = j
			if metered {
				j.once(ctx, func(string, ...any) {}, func(string, string) {})
			} else {
				j.once(context.Background(), func(string, ...any) {}, func(string, string) {})
			}
		}
		var raw []byte
		getHealthRaw(t, s, &raw)
		return raw
	}
	with, without := build(true), build(false)
	t.Logf("with %d bytes, without %d", len(with), len(without))
	if grew := len(with) - len(without); grew > 2048 {
		t.Fatalf("health grew by %d bytes", grew)
	}
}

func TestAdminMeterListsScopes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	reg, ctx := meteredRegistry(t, &now)
	jctx, job := meter.Start(ctx, meter.Job("fast"))
	spend(jctx, meter.SQL, "select", 1, false)
	pctx, phase := meter.Start(jctx, "collect")
	spend(pctx, meter.GitHub, "graphql", 2, false)
	phase.End()
	job.End()
	rctx, route := meter.Start(ctx, meter.Route("GET /api/rows"))
	spend(rctx, meter.SQL, "select", 4, false)
	route.End()
	_, other := meter.Start(ctx, meter.Route("GET /api/units/{provider}/{key}"))
	other.End()
	spend(ctx, meter.Git, "status", 1, true)
	now = now.Add(time.Hour)

	s := newServerWith(t, func(d *Deps) { d.Meter = reg; d.Now = func() time.Time { return now } })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, api.AdminMeter, nil)
	req.Header.Set(api.Header, api.Version)
	s.Handler().ServeHTTP(rec, req)
	var rep api.MeterReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int64{}
	var names []string
	for _, sc := range rep.Scopes {
		names = append(names, sc.Name)
		for _, st := range sc.Stats {
			calls[sc.Name] += st.Calls
		}
	}
	want := []string{"job:fast", "job:fast/collect", "route:GET /api/rows", "route:GET /api/units/{provider}/{key}", "untagged"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("scopes %v", names)
	}
	if calls["job:fast"] != 3 || calls["job:fast/collect"] != 2 || calls["route:GET /api/rows"] != 4 || calls["untagged"] != 1 {
		t.Fatalf("calls %v", calls)
	}
	if !rep.Since.Equal(reg.Since()) || !rep.At.Equal(now) {
		t.Fatalf("window %v %v", rep.Since, rep.At)
	}
}

func TestDoctorLoadChecks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	reg, ctx := meteredRegistry(t, &now)
	jctx, job := meter.Start(ctx, meter.Job("fast"))
	spend(jctx, meter.SQL, "select", 30, false)
	spend(jctx, meter.GitHub, "graphql", 8, false)
	meter.Spend(jctx, meter.GitHub, "graphql", 20)
	job.End()
	rctx, route := meter.Start(ctx, meter.Route("GET /api/rows"))
	spend(rctx, meter.SQL, "select", 10, false)
	route.End()
	spend(ctx, meter.Git, "fetch", 2, true)
	now = now.Add(2 * time.Hour)

	var got []string
	for _, c := range loadChecks(reg, now) {
		got = append(got, c.Mark+" | "+c.Name+" | "+c.Info)
	}
	want := []string{
		"info | load: sql | 20/h, 0 spawns/h, 0 pts/h over the last 2h0m; top: job:fast 75%, route:GET /api/rows 25%",
		"info | load: github | 4/h, 0 spawns/h, 10 pts/h over the last 2h0m; top: job:fast 100%",
		"info | load: git | 1/h, 1 spawns/h, 0 pts/h over the last 2h0m; top: untagged 100%",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLoadCarriesFallbacksAndCLIOps(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	reg, ctx := meteredRegistry(t, &now)
	spend(ctx, meter.Workspace, "rpc terminal.list", 5, false)
	spend(ctx, meter.Workspace, "worktree ps", 2, true)
	s := newServerWith(t, func(d *Deps) { d.Meter = reg; d.Fallbacks = func() int64 { return 0 } })
	h := struct{ Load api.Load }{s.wireMeter(now).Load}
	if h.Load.Fallbacks == nil || *h.Load.Fallbacks != 0 {
		t.Fatalf("fallbacks %v", h.Load.Fallbacks)
	}
	if len(h.Load.CLIOps) != 1 || h.Load.CLIOps[0].Op != "worktree ps" || h.Load.CLIOps[0].Spawns != 2 {
		t.Fatalf("cli ops %+v", h.Load.CLIOps)
	}
	if !h.Load.Since.Equal(reg.Since()) {
		t.Fatalf("since %v", h.Load.Since)
	}
}

func TestDoctorLoadChecksNameAWindowOfAtLeastAMinute(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	reg := meter.NewRegistry(func() time.Time { return now })
	meter.Add(meter.WithRegistry(context.Background(), reg), meter.Sample{Res: meter.Git, Op: "status", Spawn: true})
	checks := loadChecks(reg, now.Add(7*time.Second))
	if len(checks) != 1 || !strings.Contains(checks[0].Info, "60/h, 60 spawns/h, 0 pts/h over the last 1m;") {
		t.Fatalf("checks %+v", checks)
	}
}
