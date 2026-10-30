package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestASlackOnlyDaemonBriefsSlackUnitsOnDemand(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg = withoutWindows(te.cfg)
	off := false
	te.cfg.GitHub.Enabled = &off
	te.cfg.Brief.Enabled = true
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), storeFile), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	runSlackPlugin(t, te, db)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	te.serveDeps(ctx, db, te.cfg, t.Logf, &lock.Mutex{}, &daemon.AppState{})
	steps, ok := te.Env.engine.Steps.(*stepSet)
	if !ok || steps.d.Briefs == nil {
		t.Fatalf("the engine's steps have no briefs: %T", te.Env.engine.Steps)
	}
	rd, err := steps.d.Briefs.Peek(ctx, slackThread2, true, nil)
	if err != nil || rd.Cause == intake.CauseOff {
		t.Fatalf("a Slack first turn: %+v %v", rd, err)
	}
}
