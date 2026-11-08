package cli

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// startedTask is a task the user captured, a start turn ran on, and the pull request it opened attached.
func (p *prTaskRig) startedTask(t *testing.T, pr unit.Unit) task.ID {
	t.Helper()
	ctx := context.Background()
	e, err := p.r.te.tasks.Capture(ctx, "ship the export", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	start := dturn.Book{Unit: task.UnitOf(e.ID), Turns: []dturn.Turn{{Intent: "start", ID: "s1", Trigger: dturn.ByHand, State: dturn.Done, SentAt: p.r.now}}}
	if _, err := sqlite.New(p.r.db).Save(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err := p.r.te.tasks.Attach(ctx, e.ID, pr); err != nil {
		t.Fatal(err)
	}
	return e.ID
}

func (p *prTaskRig) reviewDiff(t *testing.T, id task.ID) []gate.Gate {
	t.Helper()
	gs, err := p.r.db.Gates(p.r.te.Now).Where(context.Background(), gates.Query{Name: "review-diff", Task: id,
		Statuses: []gate.Status{gate.Open, gate.Superseded, gate.Approved, gate.Dismissed}})
	if err != nil {
		t.Fatal(err)
	}
	return gs
}

func TestAReviewDiffStepLeavesOnceItsPRMergesAndTheTaskCloses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := newPRTaskRig(t)
	p.r.te.newGates(p.r.db, t.Logf)
	p.eng.Gates = draftGates{e: p.r.te.Env, db: p.r.db, logf: t.Logf}
	p.prs.more = map[unit.Unit]workflow.Facts{
		ptPR1: {"pr.slug": "acme/api#1", "pr.changed_files": int64(3)},
		ptPR2: {"pr.slug": "acme/api#2", "pr.changed_files": int64(1)},
	}
	merged := p.startedTask(t, ptPR1)
	open := p.startedTask(t, ptPR2)
	p.pass(t)
	for id, want := range map[task.ID]string{merged: "Review the diff of acme/api#1, 3 files changed", open: "Review the diff of acme/api#2, 1 file changed"} {
		if gs := p.reviewDiff(t, id); len(gs) != 1 || gs[0].Status != gate.Open || gs[0].Text != want {
			t.Fatalf("%s: the review-diff steps %+v, want one open that reads %q", id, gs, want)
		}
	}

	p.r.now = p.r.now.Add(time.Minute)
	p.prs.end(ptPR1)
	p.pass(t)
	gs := p.reviewDiff(t, merged)
	if len(gs) != 1 || gs[0].Status != gate.Superseded || gs[0].By != task.ActorWorkflow {
		t.Fatalf("the merged pull request's step %+v, want withdrawn by the workflow", gs)
	}
	evs, err := p.r.db.Events().Since(ctx, 0, []string{gate.EventWithdrawn})
	if err != nil || len(evs) != 1 || evs[0].Unit != task.UnitOf(merged) || evs[0].Data == "" {
		t.Fatalf("the withdrawal's recorded reason %+v %v", evs, err)
	}
	if e := p.r.taskOf(merged); e.Category != task.CategoryDone {
		t.Errorf("the task whose pull request merged is %s, want done by task-results-ended", e.Category)
	}
	if gs := p.reviewDiff(t, open); len(gs) != 1 || gs[0].Status != gate.Open {
		t.Errorf("the open pull request's step %+v, want kept", gs)
	}
	if e := p.r.taskOf(open); !e.Open() {
		t.Errorf("the open pull request's task closed: %s", e.Category)
	}
	if gs := p.reviewDiff(t, merged); len(gs) != 1 {
		t.Errorf("the step came back on the merged pull request: %+v", gs)
	}
}
