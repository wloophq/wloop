package workflow_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestKeyValueIgnoresFactOrderAndChangesWithAValue(t *testing.T) {
	u := unit.Unit{Provider: unit.GitHub, Key: "o/r#12"}
	facts := workflow.Facts{"pr.fingerprint": "abc", "pr.state": "open", "pr.age": 3 * time.Hour}
	w := workflow.Workflow{Name: "review", Slot: "pr", Trigger: workflow.Trigger{Kind: workflow.TriggerSweep}, Key: []string{"pr.state", "pr.fingerprint"}}
	reordered := w
	reordered.Key = []string{"pr.fingerprint", "pr.state"}

	k := workflow.NewKey(w, u, facts, time.Time{})
	if got := workflow.NewKey(reordered, u, facts, time.Time{}); got != k {
		t.Errorf("reordered key facts: %v, want %v", got, k)
	}
	if len(k.Value) != 16 || k.Workflow != "review" || k.Unit != u {
		t.Errorf("key = %+v", k)
	}
	changed := workflow.Facts{"pr.fingerprint": "abd", "pr.state": "open"}
	if workflow.NewKey(w, u, changed, time.Time{}).Value == k.Value {
		t.Error("a changed key fact kept the value")
	}
	if workflow.NewKey(w, u, workflow.Facts{"pr.fingerprint": "abc", "pr.state": "open", "pr.age": time.Hour}, time.Time{}) != k {
		t.Error("a fact outside the key changed the value")
	}

	byDefault := workflow.Workflow{Name: "review", Slot: "pr"}
	if workflow.NewKey(byDefault, u, facts, time.Time{}).Value == workflow.NewKey(byDefault, u, changed, time.Time{}).Value {
		t.Error("an empty key does not read the slot's fingerprint")
	}

	sched := workflow.Workflow{Name: "daily", Slot: "pr", Trigger: workflow.Trigger{Kind: workflow.TriggerSchedule}}
	monday := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	if workflow.NewKey(sched, u, facts, monday) == workflow.NewKey(sched, u, facts, monday.AddDate(0, 0, 1)) {
		t.Error("a schedule key ignores the firing time")
	}
	if workflow.NewKey(w, u, facts, monday) != k {
		t.Error("a sweep key reads the firing time")
	}

	id := k.ID()
	parts := strings.Split(id, "/")
	if len(parts) != 4 || parts[0] != "review" || parts[1] != "github" || parts[3] != k.Value {
		t.Fatalf("ID = %q", id)
	}
	if strings.ContainsAny(parts[2], "/#") {
		t.Errorf("ID segment %q keeps / or #", parts[2])
	}
}

func TestVisibleWhenAStepIsVisible(t *testing.T) {
	w := workflow.Workflow{Steps: []workflow.Step{{Use: "session.close", Reach: workflow.ReachLocal}}}
	if w.Visible() {
		t.Error("a local workflow is visible")
	}
	w.Steps = append(w.Steps, workflow.Step{Use: "turn.start", Reach: workflow.ReachVisible})
	if !w.Visible() {
		t.Error("a workflow with a visible step is not visible")
	}
}

type always struct{}

func (always) Match(workflow.Vars) (bool, error) { return true, nil }
func (always) Reads() []string                   { return nil }

func TestAnApprovalReadsItsWindowOnlyWithDelay(t *testing.T) {
	for _, c := range []struct {
		a  workflow.Approval
		ok bool
	}{
		{workflow.Approval{Mode: workflow.ApprovalRun}, true},
		{workflow.Approval{Mode: workflow.ApprovalWait}, true},
		{workflow.Approval{Mode: workflow.ApprovalDelay, Window: time.Minute}, true},
		{workflow.Approval{Mode: workflow.ApprovalDelay}, false},
		{workflow.Approval{Mode: workflow.ApprovalRun, Window: time.Minute}, false},
		{workflow.Approval{Mode: workflow.ApprovalWait, Window: time.Minute}, false},
		{workflow.Approval{Mode: "send"}, false},
		{workflow.Approval{Mode: workflow.ApprovalRun, Rules: []workflow.ApprovalRule{{When: always{}, Mode: workflow.ApprovalWait}}}, true},
		{workflow.Approval{Mode: workflow.ApprovalRun, Rules: []workflow.ApprovalRule{{When: always{}, Mode: workflow.ApprovalWait, Window: time.Second}}}, false},
		{workflow.Approval{Mode: workflow.ApprovalRun, Rules: []workflow.ApprovalRule{{Mode: workflow.ApprovalWait}}}, false},
	} {
		if err := c.a.Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c.a, err)
		}
	}
}

func TestOnlyRunAndDelayRelease(t *testing.T) {
	if !workflow.ApprovalRun.Releases() || !workflow.ApprovalDelay.Releases() || workflow.ApprovalWait.Releases() {
		t.Error("run and delay release, wait holds")
	}
}
