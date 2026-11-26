package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// TestAnEndedConversationWithNoItemClosesOnItsOwn: a conversation no item came from closes with no message and
// no approval once it ended, whoever started it; one that has not ended, holds an item, or that an open task of
// yours holds, a snoozed one included, stays.
func TestAnEndedConversationWithNoItemClosesOnItsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	p := newWorkflowProposer(r)
	quiet, live, held, mine := rpThread(40), rpThread(41), rpThread(42), rpThread(43)
	for _, c := range []struct {
		id       string
		state    task.State
		category task.Category
		u        unit.Unit
	}{
		{"t-quiet", "done", task.CategoryDone, quiet},
		{"t-live", "done", task.CategoryDone, live},
		{"t-held", "done", task.CategoryDone, held},
		{"t-mine", "snoozed", task.CategoryDeferred, mine},
	} {
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: task.ID(c.id), State: c.state,
			Category: c.category, Title: c.id, Origin: task.OriginUser, Units: []unit.Unit{c.u}, Created: r.now, Updated: r.now}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range []unit.Unit{quiet, held, mine} {
		r.slack.setFact(u, "slack.ended", true)
	}
	r.slack.setFact(held, "slack.items_resolved", 1)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.slack.isClosed(quiet) {
		t.Fatal("the ended conversation with no item and no open task is not closed")
	}
	if r.slack.isClosed(live) || r.slack.isClosed(held) {
		t.Fatal("closed a conversation that has not ended, or one an item came from")
	}
	if r.slack.isClosed(mine) || !r.taskOf("t-mine").Open() {
		t.Fatal("closed a conversation your snoozed task holds, or ended that task")
	}
	if ds, err := r.svc.List(ctx, outbox.Filter{Workflow: "slack-ended-empty"}); err != nil || len(ds) != 0 {
		t.Fatalf("drafts %+v, %v; want none", ds, err)
	}
}
