package ingest_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestRunJobRunsPrepareIngestAndAfter(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	var steps []string
	var after ingest.Report
	j := ingest.Job{Every: time.Minute, Prepare: func(_ context.Context, now time.Time) (ingest.Plan, string, error) {
		steps = append(steps, "prepare "+now.Format(time.RFC3339))
		return ingest.Plan{Registration: f.reg, After: func(_ context.Context, rep ingest.Report, err error) (string, string, error) {
			steps = append(steps, "after")
			after = rep
			return "read " + rep.Cursor, "", err
		}}, "", nil
	}}
	summary, skipped, err := ingest.RunJob(context.Background(), j, t0)
	if err != nil || summary != "read 0001" || skipped != "" {
		t.Fatalf("RunJob = %q %q %v", summary, skipped, err)
	}
	if !slices.Equal(steps, []string{"prepare " + t0.Format(time.RFC3339), "after"}) {
		t.Fatalf("steps %q", steps)
	}
	if !slices.Equal(after.Read, []unit.Unit{th("C1 0001")}) {
		t.Fatalf("After saw %+v", after)
	}
	if got, _ := f.store.Cursor(context.Background()); got != "0001" {
		t.Fatalf("stored cursor %q", got)
	}
}

func TestRunJobHandsTheIngestErrorToAfter(t *testing.T) {
	f := newFixture(0)
	f.svc.Post("C1 0001", "0001", "first ask")
	var seen error
	j := ingest.Job{Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
		return ingest.Plan{Registration: f.reg, After: func(_ context.Context, _ ingest.Report, err error) (string, string, error) {
			seen = err
			return "skipped", "budget", nil
		}}, "", nil
	}}
	summary, skipped, err := ingest.RunJob(context.Background(), j, t0)
	if seen == nil || err != nil || summary != "skipped" || skipped != "budget" {
		t.Fatalf("RunJob = %q %q %v; After saw %v", summary, skipped, err, seen)
	}
}

func TestRunJobWithoutAfterSummarizesTheReport(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	j := ingest.Job{Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
		return ingest.Plan{Registration: f.reg}, "", nil
	}}
	summary, skipped, err := ingest.RunJob(context.Background(), j, t0)
	if err != nil || summary != "1 discovered, 1 read" || skipped != "" {
		t.Fatalf("RunJob = %q %q %v", summary, skipped, err)
	}
}

func TestALowBudgetSkipRunsNothing(t *testing.T) {
	f := newFixture(100)
	f.svc.Post("C1 0001", "0001", "first ask")
	j := ingest.Job{Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
		return ingest.Plan{Registration: f.reg, After: func(context.Context, ingest.Report, error) (string, string, error) {
			t.Fatal("After ran on a skipped job")
			return "", "", nil
		}}, "rate limit low", nil
	}}
	summary, skipped, err := ingest.RunJob(context.Background(), j, t0)
	if err != nil || summary != "skipped" || skipped != "rate limit low" {
		t.Fatalf("RunJob = %q %q %v", summary, skipped, err)
	}
	if got, _ := f.store.Cursor(context.Background()); got != "" {
		t.Fatalf("a skipped job saved cursor %q", got)
	}
	if f.log.Gen() != 0 {
		t.Fatalf("a skipped job noted %d changes", f.log.Gen())
	}
}

func TestAFailedPrepareRunsNothing(t *testing.T) {
	boom := errors.New("boom")
	j := ingest.Job{Prepare: func(context.Context, time.Time) (ingest.Plan, string, error) {
		return ingest.Plan{}, "", boom
	}}
	if _, _, err := ingest.RunJob(context.Background(), j, t0); !errors.Is(err, boom) {
		t.Fatalf("RunJob err %v", err)
	}
}
