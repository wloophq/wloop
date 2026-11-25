package cli

import (
	"context"
	"testing"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// TestAnEndedConversationWithNoItemClosesOnItsOwn: a conversation no item came from closes with no message and
// no approval once it ended, whoever started it, and its task ends; one that has not ended, or holds an item,
// stays.
func TestAnEndedConversationWithNoItemClosesOnItsOwn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	p := newWorkflowProposer(r)
	quiet, live, held := rpThread(40), rpThread(41), rpThread(42)
	for i, u := range []unit.Unit{quiet, live, held} {
		id := []string{"t-quiet", "t-live", "t-held"}[i]
		if err := r.db.Tasks().Apply(ctx, []task.Change{{Op: task.OpCreate, Task: task.Task{ID: task.ID(id), State: "next",
			Category: task.CategoryReady, Title: id, Origin: task.OriginUser, Units: []unit.Unit{u}, Created: r.now, Updated: r.now}}}); err != nil {
			t.Fatal(err)
		}
	}
	r.slack.setFact(quiet, "slack.ended", true)
	r.slack.setFact(held, "slack.ended", true)
	r.slack.setFact(held, "slack.items_resolved", 1)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.slack.isClosed(quiet) || r.taskOf("t-quiet").Open() {
		t.Fatal("the ended conversation with no item is not closed, or its task is still open")
	}
	if r.slack.isClosed(live) || r.slack.isClosed(held) {
		t.Fatal("closed a conversation that has not ended, or one an item came from")
	}
	if ds, err := r.svc.List(ctx, outbox.Filter{Workflow: "slack-ended-empty"}); err != nil || len(ds) != 0 {
		t.Fatalf("drafts %+v, %v; want none", ds, err)
	}
}
