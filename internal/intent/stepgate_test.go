package intent_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
)

const stepWorkflow = `summary = "Case"
provider = "task"
when = 'task.state == "next"'

[[step]]
use = "gate.open"
name = "review-diff"

[gates.withdraw]
when = 'task.turns > 2'
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
	if gw == nil || gw.Reason != "moved on" || strings.Join(gw.When.Reads(), ",") != "task.turns" {
		t.Fatalf("gates.withdraw %+v", gw)
	}
	for _, c := range []struct{ from, to, want string }{
		{"when = 'task.turns > 2'\n", "", "gates.withdraw.when: is required"},
		{"use = \"gate.open\"\nname = \"review-diff\"", `use = "notify.self"`, "gates.withdraw: withdraws gates the workflow opened; it has no gate.open step"},
		{`task.turns > 2`, `task.nope`, "task.nope"},
		{`task.turns > 2`, `task.title == "done"`, "gates.withdraw.when: reads task.title"},
	} {
		_, problems := loadProfile(t, "", map[string]string{"workflows/my-step/workflow.toml": strings.Replace(stepWorkflow, c.from, c.to, 1)})
		if !strings.Contains(problems.Error(), c.want) {
			t.Errorf("%s: %s, want %q", c.to, problems.Error(), c.want)
		}
	}
}

func TestAStepGatesTextReadsTheWorkflowFacts(t *testing.T) {
	facts := func(changed any) map[string]any {
		return intentload.StepFacts(func(n string) any {
			return map[string]any{"pr.slug": "acme/api#7", "pr.changed_files": changed}[n]
		})
	}
	for _, c := range []struct {
		locale  string
		changed any
		want    string
	}{
		{"", int64(3), "Review the diff of acme/api#7, 3 files changed"},
		{"", int64(1), "Review the diff of acme/api#7, 1 file changed"},
		{"", nil, "Review the diff of acme/api#7"},
		{"pt-BR", int64(3), "Revisar o diff de acme/api#7, 3 arquivos alterados"},
		{"pt-BR", int64(1), "Revisar o diff de acme/api#7, 1 arquivo alterado"},
	} {
		l, problems := loadProfile(t, c.locale, nil)
		if len(problems) > 0 {
			t.Fatal(problems.Error())
		}
		def, _ := l.Gates.Gate("review-diff")
		if got, err := def.Words(facts(c.changed)); err != nil || got != c.want {
			t.Errorf("%s, %v files: %q %v, want %q", c.locale, c.changed, got, err, c.want)
		}
	}
	_, problems := loadProfile(t, "", map[string]string{"gates.toml": "[gates.review-diff]\ntext = \"{{.pr.nope}}\"\n"})
	if !strings.Contains(problems.Error(), "gates.review-diff.text") {
		t.Fatalf("a step gate's text naming no fact: %v", problems)
	}
}

func TestTheReviewDiffStepReadsInPortuguese(t *testing.T) {
	l, problems := loadProfile(t, "pt-BR", nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	def, _ := l.Gates.Gate("review-diff")
	if len(def.Intents) != 2 || def.Intents[0].Summary != "Marcar o diff como revisado; não publica nada." || def.Intents[1].Summary == "" {
		t.Fatalf("review-diff's answers in pt-BR: %+v", def.Intents)
	}
	i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == "start-review-diff" })
	if i < 0 || l.Workflows[i].GateWithdraw == nil || !strings.HasPrefix(l.Workflows[i].GateWithdraw.Reason, "o pull request foi mesclado") {
		t.Fatalf("start-review-diff's withdrawal in pt-BR: %+v", l.Workflows[i].GateWithdraw)
	}
}
