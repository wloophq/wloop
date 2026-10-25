package daemon

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/automation/automationtest"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func (p enginePlan) Explain(ctx context.Context, u unit.Unit) (Explained, error) {
	return Explain(ctx, p.Engine, u)
}

func TestThePlanOfAUnitSaysWhyEachWorkflowGivesNoItem(t *testing.T) {
	t.Parallel()
	r := newPlanRig(t)
	r.units.Put(u7, workflow.Facts{"pr.repo": "acme/api", "pr.fingerprint": "h2", "pr.first.due": false, "pr.first.reason": "the author is a bot"})
	base := r.eng.Workflows
	r.eng.Workflows = func() []workflow.Workflow {
		return append(base(),
			workflow.Workflow{Name: "review-first-round", Provider: unit.GitHub, Slot: "pr", Mode: workflow.ModeApply,
				Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{r.steps.Step("session.close")},
				When: automationtest.MatchFunc(func(v workflow.Vars) bool { return v.Facts["pr.first.due"] == true }, "pr.first.due")},
			workflow.Workflow{Name: "after-merge", Provider: unit.GitHub, Slot: "pr", Mode: workflow.ModeApply,
				Trigger: workflow.Trigger{Kind: workflow.TriggerEvent, On: []string{"merged", "closed"}}, Steps: []workflow.Step{r.steps.Step("session.close")}})
	}
	gen := r.log.Gen()
	res := r.plan(t, "?unit="+url.QueryEscape("github:ACME/api#7"))
	if res.Unit != "github:acme/api#7" || len(res.Items) != 1 || res.Items[0].Workflow != "review-follow-up" {
		t.Fatalf("unit %q items %+v", res.Unit, res.Items)
	}
	want := []api.UnitWorkflow{
		{Workflow: "after-merge", Trigger: "event", Outcome: "waits", Reason: "decides when a merged or closed event arrives"},
		{Workflow: "review-first-round", Trigger: "sweep", Outcome: "not matched",
			Facts: []api.Fact{{Name: "pr.first.due", Value: "false"}, {Name: "pr.first.reason", Value: "the author is a bot"}}},
	}
	if len(res.Workflows) != len(want) {
		t.Fatalf("workflows %+v", res.Workflows)
	}
	for i := range want {
		got := res.Workflows[i]
		if got.Workflow != want[i].Workflow || got.Outcome != want[i].Outcome || got.Reason != want[i].Reason || len(got.Facts) != len(want[i].Facts) {
			t.Fatalf("workflow %d: %+v, want %+v", i, got, want[i])
		}
		for j := range want[i].Facts {
			if got.Facts[j] != want[i].Facts[j] {
				t.Fatalf("workflow %d fact %d: %+v, want %+v", i, j, got.Facts[j], want[i].Facts[j])
			}
		}
	}
	if r.log.Gen() != gen {
		t.Fatalf("explaining a unit noted a change")
	}

	gone := r.plan(t, "?unit="+url.QueryEscape("github:acme/api#99"))
	if len(gone.Errors) != 1 || !strings.Contains(gone.Errors[0], "not tracked, or gone") || len(gone.Workflows) != 0 {
		t.Fatalf("%+v", gone)
	}
}

type failingMarks struct{}

func (failingMarks) Marks(context.Context, unit.Unit) (map[string]string, error) {
	return nil, errors.New("marks unreadable")
}

type readsHead struct{ workflow.Predicate }

func (readsHead) MarkNames() []string { return []string{"w/head"} }

func TestThePlanOfAUnitReadsMarksOnlyForAWorkflowThatCallsHandled(t *testing.T) {
	t.Parallel()
	r := newPlanRig(t)
	r.units.Put(u7, workflow.Facts{"pr.repo": "acme/api", "pr.fingerprint": "h2", "pr.first.due": false})
	r.eng.Marks = failingMarks{}
	never := automationtest.MatchFunc(func(v workflow.Vars) bool { return v.Facts["pr.first.due"] == true }, "pr.first.due")
	r.eng.Workflows = func() []workflow.Workflow {
		wf := func(name string, when workflow.Predicate) workflow.Workflow {
			return workflow.Workflow{Name: name, Provider: unit.GitHub, Slot: "pr", Mode: workflow.ModeApply,
				Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Steps: []workflow.Step{r.steps.Step("session.close")}, When: when}
		}
		return []workflow.Workflow{wf("plain", never), wf("marked", readsHead{never})}
	}
	res := r.plan(t, "?unit="+url.QueryEscape("github:acme/api#7"))
	got := map[string]api.UnitWorkflow{}
	for _, w := range res.Workflows {
		got[w.Workflow] = w
	}
	if got["plain"].Outcome != "not matched" {
		t.Errorf("a workflow without handled(): %+v", got["plain"])
	}
	if got["marked"].Outcome != "error" || !strings.Contains(got["marked"].Reason, "marks unreadable") {
		t.Errorf("a workflow with handled(): %+v", got["marked"])
	}
}
