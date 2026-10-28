package cli

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/outbox/outboxtest"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
)

// withDraftGates makes o hold its drafts on the gates of db, stamped by now. The gates name no task: a test's
// drafts serve tasks a fake keeps, which db does not hold.
func withDraftGates(db *sqlite.DB, now func() time.Time, o *outbox.Service) *outbox.Service {
	st := db.Gates(now)
	return outboxtest.GatesOn(taskless{st}, st, now).Bind(o)
}

type taskless struct{ sqlite.GateStore }

func (s taskless) Open(ctx context.Context, g gate.Gate) (gate.Gate, error) {
	g.Task = ""
	return s.GateStore.Open(ctx, g)
}

func (s taskless) RecordSent(ctx context.Context, g gate.Gate) (gate.Gate, error) {
	g.Task = ""
	return s.GateStore.RecordSent(ctx, g)
}

// decideDraft resolves draft id's open gate with intent as a person, through o's gates: the daemon's service for
// an outbox serve built, the bound one for a test's.
func decideDraftAs(ctx context.Context, o *outbox.Service, id draft.ID, intent string) error {
	d, err := o.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	g, ok, err := o.GateOf(ctx, d)
	if err != nil || !ok {
		return fmt.Errorf("draft %d has no gate: %w", id, err)
	}
	if intent == "approve" {
		if _, err := o.Prepare(ctx, id, d.Rev); err != nil {
			return err
		}
	}
	switch gs := o.Gates.(type) {
	case draftGates:
		return gs.e.newGates(gs.db, nil).Resolve(ctx, g.ID, gates.Resolution{Intent: intent, By: task.ActorPerson})
	case *outboxtest.Gates:
		return gs.Decide(ctx, g.ID, intent)
	}
	return fmt.Errorf("outbox gates %T resolve nothing", o.Gates)
}

func approveDraft(ctx context.Context, o *outbox.Service, id draft.ID) error {
	return decideDraftAs(ctx, o, id, "approve")
}

func discardDraft(ctx context.Context, o *outbox.Service, id draft.ID) error {
	return decideDraftAs(ctx, o, id, "discard")
}

// decided is how d's gate decided it as it opened.
func decided(t *testing.T, o *outbox.Service, d draft.Draft) draft.Approval {
	t.Helper()
	g, ok, err := o.GateOf(context.Background(), d)
	if err != nil || !ok {
		t.Fatalf("draft %d has no gate: %v", d.ID, err)
	}
	return outboxtest.Decided(g)
}

// seedTask stores a task id, the task a turn's draft serves, so its gate is on it.
func seedTask(t *testing.T, db *sqlite.DB, id task.ID) {
	t.Helper()
	e := task.Task{ID: id, Kind: task.KindAction, State: "next", Category: task.CategoryReady, Title: "a task", Origin: task.OriginUser,
		Created: testNow, Updated: testNow}
	if err := db.Tasks().Apply(context.Background(), []task.Change{{Op: task.OpCreate, Task: e}}); err != nil {
		t.Fatal(err)
	}
}
