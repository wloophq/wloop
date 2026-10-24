package metertest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

type recorder struct {
	testing.TB
	errors, logs []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func TestWithinFailsAndListsRepeats(t *testing.T) {
	ctx, sc := metertest.Start(t, context.Background())
	for range 49 {
		meter.Add(ctx, meter.Sample{Res: meter.SQL, Op: "query", Detail: "SELECT * FROM turns WHERE pr = ?"})
	}
	meter.Add(ctx, meter.Sample{Res: meter.SQL, Op: "query", Detail: "SELECT * FROM prs"})
	meter.Add(ctx, meter.Sample{Res: meter.Git, Op: "fetch", Detail: "git fetch"})

	rec := &recorder{TB: t}
	metertest.Within(rec, sc, metertest.Budget{
		{Res: meter.SQL}:                        4,
		{Res: meter.SQL, Op: "query"}:           10,
		{Res: meter.SQL, Field: metertest.Rows}: 0,
		{Res: meter.Git}:                        1,
	})

	want := []string{"sql calls = 50, budget 4", "sql query calls = 50, budget 10"}
	if strings.Join(rec.errors, "|") != strings.Join(want, "|") {
		t.Errorf("errors = %q, want %q", rec.errors, want)
	}
	if len(rec.logs) != 1 {
		t.Fatalf("logs = %q, want one listing for sql", rec.logs)
	}
	lines := strings.Split(strings.TrimSpace(rec.logs[0]), "\n")
	if len(lines) != 3 || strings.TrimSpace(lines[1]) != "×49 query: SELECT * FROM turns WHERE pr = ?" || strings.TrimSpace(lines[2]) != "×1 query: SELECT * FROM prs" {
		t.Errorf("listing =\n%s", rec.logs[0])
	}
}

func TestLimitZeroValueCountsCalls(t *testing.T) {
	ctx, sc := metertest.Start(t, context.Background())
	meter.Add(ctx, meter.Sample{Res: meter.GitHub, Op: "rest", Points: 1, Bytes: 900})
	meter.Add(ctx, meter.Sample{Res: meter.GitHub, Op: "rest", NotModified: true, Bytes: 0})
	meter.Spend(ctx, meter.GitHub, "graphql", 7)

	checks := map[metertest.Limit]int64{
		{Res: meter.GitHub}:                                     2,
		{Res: meter.GitHub, Op: "rest"}:                         2,
		{Res: meter.GitHub, Op: "graphql"}:                      0,
		{Res: meter.GitHub, Field: metertest.Points}:            8,
		{Res: meter.GitHub, Field: metertest.NotModified}:       1,
		{Res: meter.GitHub, Op: "rest", Field: metertest.Bytes}: 900,
		{Res: meter.SQL}:                                        0,
	}
	for l, want := range checks {
		if got := metertest.Count(sc, l); got != want {
			t.Errorf("Count(%v) = %d, want %d", l, got, want)
		}
	}
}

func TestWithinTally(t *testing.T) {
	got := meter.Tally{
		{Res: meter.Workspace, Op: "worktree ps"}: {Calls: 3, Spawns: 3},
		{Res: meter.Workspace, Op: "repo list"}:   {Calls: 1, Spawns: 1},
		{Res: meter.SQL, Op: "query"}:             {Calls: 9},
	}
	rec := &recorder{TB: t}
	metertest.WithinTally(rec, got, metertest.Budget{
		{Res: meter.Workspace, Field: metertest.Spawns}: 2,
		{Res: meter.SQL}: 9,
	})
	if len(rec.errors) != 1 || rec.errors[0] != "workspace spawns = 4, budget 2" {
		t.Errorf("errors = %q", rec.errors)
	}
	want := "workspace ops:\nworkspace repo list: 1 calls, 1 spawns\nworkspace worktree ps: 3 calls, 3 spawns\n"
	if len(rec.logs) != 1 || rec.logs[0] != want {
		t.Errorf("logs = %q, want %q", rec.logs, want)
	}

	rec = &recorder{TB: t}
	metertest.WithinTally(rec, got, metertest.Budget{{Res: meter.SQL}: 9})
	if len(rec.errors)+len(rec.logs) != 0 {
		t.Errorf("a tally within budget reported %q %q", rec.errors, rec.logs)
	}
}

func TestStartEndsAtCleanup(t *testing.T) {
	var (
		ctx  context.Context
		sc   *meter.Scope
		name string
	)
	t.Run("inner", func(t *testing.T) {
		ctx, sc = metertest.Start(t, context.Background())
		name = t.Name()
		if !meter.Tracing(ctx) {
			t.Error("the scope does not trace")
		}
		meter.Add(ctx, meter.Sample{Res: meter.Agent, Op: "ask", Spawn: true})
	})
	meter.Add(ctx, meter.Sample{Res: meter.Agent, Op: "ask", Spawn: true})

	if sc.Name() != name {
		t.Errorf("scope name = %q, want %q", sc.Name(), name)
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.Agent}); got != 1 {
		t.Errorf("agent calls = %d, want 1: the scope did not end at cleanup", got)
	}
	if got := len(sc.Samples()); got != 1 {
		t.Errorf("samples = %d, want 1", got)
	}
}

func TestWithinReportsOpsOfAnUntracedScope(t *testing.T) {
	ctx, sc := meter.Start(context.Background(), "route:GET /api/rows")
	for range 3 {
		meter.Add(ctx, meter.Sample{Res: meter.SQL, Op: "query", Rows: 2})
	}
	rec := &recorder{TB: t}
	metertest.Within(rec, sc, metertest.Budget{{Res: meter.SQL}: 1})

	want := "sql ops of route:GET /api/rows:\nsql query: 3 calls, 6 rows\n"
	if len(rec.errors) != 1 || len(rec.logs) != 1 || rec.logs[0] != want {
		t.Errorf("errors = %q, logs = %q, want log %q", rec.errors, rec.logs, want)
	}
}
