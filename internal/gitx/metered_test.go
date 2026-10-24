package gitx

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func TestGitMeteredOp(t *testing.T) {
	dir, _, first, second := realRepo(t)
	ctx, sc := metertest.Start(t, context.Background())
	if _, err := New().IsAncestor(ctx, dir, first, second); err != nil {
		t.Fatal(err)
	}
	if n := metertest.Count(sc, metertest.Limit{Res: meter.Git, Op: "merge-base", Field: metertest.Spawns}); n != 1 {
		t.Errorf("merge-base spawns = %d, want 1: %v", n, sc.Tally())
	}
}

type blockingRunner struct{}

func (blockingRunner) Run(ctx context.Context, _ string, _ []string, _ ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, &Error{Msg: "killed", Code: -1}
}

func TestCanceledGitIsNotError(t *testing.T) {
	ctx, sc := metertest.Start(t, context.Background())
	canceled, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	if _, err := (Metered{Next: blockingRunner{}}).Run(canceled, t.TempDir(), nil, "fetch", "-q", "origin"); err == nil {
		t.Fatal("want the run's error")
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.Git, Op: "fetch", Field: metertest.Calls}); got != 1 {
		t.Errorf("fetch calls = %d, want 1", got)
	}
	if got := metertest.Count(sc, metertest.Limit{Res: meter.Git, Field: metertest.Errors}); got != 0 {
		t.Errorf("a canceled run counted %d errors, want 0", got)
	}
}
