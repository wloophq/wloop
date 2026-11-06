package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// mergeProposals are the open merge gates on id and the task each proposes merging with.
func (r *profileRig) mergeProposals(t *testing.T, id task.ID) []string {
	t.Helper()
	gs, err := r.db.Gates(func() time.Time { return testNow }).OfTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range gs {
		if p, ok := coretasks.ProposedVerb(g); ok && g.Status == gate.Open && p.Verb == task.VerbMerge {
			out = append(out, p.Args[task.ArgTask])
		}
	}
	return out
}

func TestTheTaskVerbStepPassesTheOtherTaskFromAFact(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newProfileRig(t, "")
	r.put(t, "t-ask", "next", task.CategoryReady)
	r.put(t, "t-pr", "next", task.CategoryReady)
	r.te.newTasks(r.db, r.te.cfg, nil)
	units := automationtest.NewUnits()
	u := task.UnitOf("t-ask")
	units.Put(u, workflow.Facts{"task.link_target_tasks": []string{}})
	s := newStepSet(r.te.Env, stepDeps{Units: units, Rounds: r.te.Rounds})
	st, why := wfStep("task.verb", "verb", "merge", "task", "task.link_target_tasks"), workflow.Why{Workflow: "task-fold"}
	if refusal, err := s.Check(ctx, st, u, why, automation.CheckOpts{}); err != nil || !strings.Contains(refusal, "task.link_target_tasks names no task") {
		t.Fatalf("check while the fact names none: %q %v", refusal, err)
	}
	units.Set(u, "task.link_target_tasks", []string{"t-pr", "t-other"})
	if refusal, err := s.Check(ctx, st, u, why, automation.CheckOpts{}); err != nil || refusal != "" {
		t.Fatalf("check: %q %v", refusal, err)
	}
	if _, err := s.Run(ctx, st, u, why, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := r.mergeProposals(t, "t-ask"); len(got) != 1 || got[0] != "t-pr" {
		t.Fatalf("merge proposals %v, want one with t-pr, the fact's first", got)
	}
}

func TestTheTaskVerbStepPassesTheOtherTaskFromAClassifyOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newProfileRig(t, "")
	r.put(t, "t-ask", "next", task.CategoryReady)
	r.put(t, "t-dm", "next", task.CategoryReady)
	r.te.newTasks(r.db, r.te.cfg, nil)
	units := automationtest.NewUnits()
	u := task.UnitOf("t-ask")
	units.Put(u, workflow.Facts{"classify.task-correlate.same_as": "t-dm"})
	s := newStepSet(r.te.Env, stepDeps{Units: units, Rounds: r.te.Rounds})
	st, why := wfStep("task.verb", "verb", "merge", "task", "classify.task-correlate.same_as"), workflow.Why{Workflow: "task-correlate"}
	if _, err := s.Run(ctx, st, u, why, automation.RunOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := r.mergeProposals(t, "t-ask"); len(got) != 1 || got[0] != "t-dm" {
		t.Fatalf("merge proposals %v, want one with t-dm", got)
	}
	units.Set(u, "classify.task-correlate.same_as", "")
	if refusal, err := s.Check(ctx, st, u, why, automation.CheckOpts{}); err != nil || refusal == "" {
		t.Fatalf("check while the output names none: %q %v", refusal, err)
	}
}

func TestASlackItemLinkingAPullRequestWithATaskProposesItsMerge(t *testing.T) {
	t.Skip("T4 C8")
}

func TestAnApprovedFoldMergesTheSlackTaskIntoThePullRequestsTask(t *testing.T) {
	t.Skip("T4 C8")
}

func TestASlackTaskCreatedBeforeTheLinkIsFoldedTheSame(t *testing.T) {
	t.Skip("T4 C8")
}

func TestAFoldKeepsTheSlackTaskWhenItAloneWasClarified(t *testing.T) {
	t.Skip("T4 C5")
}

func TestAFoldNeverMergesWithoutTheGate(t *testing.T) {
	t.Skip("T4 C9")
}

func TestAFoldGateEndsWhenEitherTaskClosesOrIsMergedElsewhere(t *testing.T) {
	t.Skip("T4 C9")
}

func TestARefusedFoldIsNotProposedAgainUntilEitherTaskChanges(t *testing.T) {
	t.Skip("T4 C11")
}
