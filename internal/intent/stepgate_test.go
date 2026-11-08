package intent_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

const stepWorkflow = `summary = "Case"
provider = "task"
when = 'task.state == "next"'

[[step]]
use = "gate.open"
name = "review-diff"

[gates.withdraw]
when = 'task.state != "next"'
reason = "moved on"
`

func TestAGateWithdrawLoadsWithItsReasonAndNeedsAGateOpenStep(t *testing.T) {
	l, problems := loadProfile(t, "", map[string]string{"workflows/my-step/workflow.toml": stepWorkflow})
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == "my-step" })
	if i < 0 {
		t.Fatal("my-step did not load")
	}
	gw := l.Workflows[i].GateWithdraw
	if gw == nil || gw.Reason != "moved on" || strings.Join(gw.When.Reads(), ",") != "task.state" {
		t.Fatalf("gates.withdraw %+v", gw)
	}
	for _, c := range []struct{ from, to, want string }{
		{"when = 'task.state != \"next\"'\n", "", "gates.withdraw.when: is required"},
		{"use = \"gate.open\"\nname = \"review-diff\"", `use = "notify.self"`, "gates.withdraw: withdraws gates the workflow opened; it has no gate.open step"},
		{`task.state != "next"`, `task.nope`, "task.nope"},
	} {
		_, problems := loadProfile(t, "", map[string]string{"workflows/my-step/workflow.toml": strings.Replace(stepWorkflow, c.from, c.to, 1)})
		if !strings.Contains(problems.Error(), c.want) {
			t.Errorf("%s: %s, want %q", c.to, problems.Error(), c.want)
		}
	}
}

func TestTheReviewDiffStepReadsInPortuguese(t *testing.T) {
	l, problems := loadProfile(t, "pt-BR", nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == "start-review-diff" })
	if i < 0 || l.Workflows[i].GateWithdraw == nil || !strings.HasPrefix(l.Workflows[i].GateWithdraw.Reason, "o pull request foi mesclado") {
		t.Fatalf("start-review-diff's withdrawal in pt-BR: %+v", l.Workflows[i].GateWithdraw)
	}
}
