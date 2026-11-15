package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/core/lock"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestTheServedStarterPreparesThroughTheDaemonItWasBuiltWith(t *testing.T) {
	t.Parallel()
	d := ServeForTest(t, TestOptions{})
	d.e.linkStates.Store(nil)
	_, err := d.e.Turns.Starter.Preparer.PrepareTurn(context.Background(), "nudge", unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, "", nil)
	if err == nil || !strings.Contains(err.Error(), "the GitHub plugin does not run") {
		t.Fatalf("prepare = %v, want the daemon's answer for a nudge no running plugin prepares", err)
	}
}

// failingRun fails every turn it runs.
type failingRun struct{}

func (failingRun) Run(context.Context, turns.Turn) (turns.Outcome, error) {
	return turns.Outcome{}, errors.New("ran")
}

// nudgeOnATask is a nudge on a unit no pull request reads ahead.
var nudgeOnATask = turns.Request{Unit: unit.Unit{Provider: "tasks", Key: "t1"}, Intent: dturn.Intent(dintent.Nudge), Trigger: dturn.Automated}

func TestAStartBeforeServeBuildsItsDaemonIsTriedAgain(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	te.serveDeps(context.Background(), te.db, te.cfg, t.Logf, &lock.Mutex{}, &daemon.AppState{})
	if _, err := te.Turns.Starter.Start(context.Background(), nudgeOnATask, failingRun{}); !errors.Is(err, turns.ErrTransient) {
		t.Fatalf("start = %v, want transient before the daemon that prepares it exists", err)
	}
}

func TestAStartWhileServeBuildsItsServersReadsThemSafely(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	mu, app := &lock.Mutex{}, &daemon.AppState{}
	ctx := context.Background()
	te.newDaemon(te.serveDeps(ctx, te.db, te.cfg, t.Logf, mu, app))
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 20 {
			if _, err := te.Turns.Starter.Start(ctx, nudgeOnATask, failingRun{}); err == nil {
				t.Error("a nudge on a task started")
			}
		}
	})
	te.newDaemon(te.serveDeps(ctx, te.db, te.cfg, t.Logf, mu, app))
	wg.Wait()
}
