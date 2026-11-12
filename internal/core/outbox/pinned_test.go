package outbox

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
)

// pinnedGates are Gates whose question decides the drafts of the gates in decided.
type pinnedGates struct {
	Gates
	decided map[string]bool
}

func (p pinnedGates) Decides(_ context.Context, drafted gate.Gate) error {
	if p.decided[drafted.Subject] {
		return gates.ErrRefused
	}
	return nil
}

func TestARetryOfADraftAQuestionDecidesIsRefused(t *testing.T) {
	e := newEnv()
	failed := e.propose(reply("fails"))
	e.approve(failed.ID, nil)
	e.fx.failWith = errBoom
	e.svc.Send(ctx)
	e.fx.failWith = nil
	pg := pinnedGates{e.svc.Gates, map[string]bool{strconv.FormatInt(int64(failed.ID), 10): true}}
	e.svc.Gates = pg
	if _, err := e.svc.Retry(ctx, failed.ID, false); !errors.Is(err, gates.ErrRefused) || e.get(failed.ID).State != draft.Failed {
		t.Fatalf("a retry of a draft a question decides: %v, draft %s", err, e.get(failed.ID).State)
	}
	pg.decided = nil
	e.svc.Gates = pg
	if got, err := e.svc.Retry(ctx, failed.ID, false); err != nil || got.State != draft.Queued {
		t.Fatalf("a retry once no question decides it: %+v %v", got, err)
	}
}
