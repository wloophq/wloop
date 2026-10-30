package intent

import (
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

const onDone = `summary = "React to done"
provider = "task"
when = 'trigger.event.kind == "verb_done"'
key = ["trigger.event.id"]

[trigger]
on = ["verb_done"]

[[step]]
use = "notify.self"
`

func TestAWorkflowLoadsWithAVerbTrigger(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "on-done", onDone)
	writeBeside(t, root, "on-done", map[string]string{"text.md.tmpl": "done\n"})
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) > 0 || len(l.Workflows) == 0 {
		t.Fatalf("%s", problems.Error())
	}
}

func TestAVerbTriggerIsChecked(t *testing.T) {
	for name, c := range map[string]struct{ manifest, problem string }{
		"an unknown verb":      {strings.Replace(onDone, `on = ["verb_done"]`, `on = ["verb_dne"]`, 1), `trigger.on[0]: names no event kind; did you mean "verb_done"?`},
		"an unknown actor":     {strings.Replace(onDone, `on = ["verb_done"]`, `on = ["verb_done"]`+"\nactors = [\"persn\"]", 1), `"persn" is not one of`},
		"another provider":     {strings.Replace(onDone, `provider = "task"`, `provider = "github"`, 1), `trigger.on[0]: verb_done is an event of task units; it needs provider = "task"`},
		"an event key, sweeps": {strings.Replace(onDone, "[trigger]\non = [\"verb_done\"]\n", "", 1), `key[0]: trigger.event.id needs [trigger] on`},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkflow(t, root, "on-done", c.manifest)
			writeBeside(t, root, "on-done", map[string]string{"text.md.tmpl": "done\n"})
			l, _ := Load(userLayer(root), bundledFS(), testBase(), nil)
			if got := l.BrokenWorkflows["on-done"].Error(); !strings.Contains(got, c.problem) {
				t.Errorf("want %q in\n%s", c.problem, got)
			}
		})
	}
}

func TestATriggerDepthAboveFiveIsAProblem(t *testing.T) {
	for depth, problem := range map[string]string{"6": "trigger.depth: must be 1 to 5", "0": "trigger.depth: must be 1 to 5", "5": ""} {
		root := t.TempDir()
		writeWorkflow(t, root, "on-done", strings.Replace(onDone, `on = ["verb_done"]`, `on = ["verb_done"]`+"\ndepth = "+depth+"\nactors = [\"person\"]", 1))
		writeBeside(t, root, "on-done", map[string]string{"text.md.tmpl": "done\n"})
		l, _ := Load(userLayer(root), bundledFS(), testBase(), nil)
		got := l.BrokenWorkflows["on-done"].Error()
		if problem == "" {
			if got != "" {
				t.Errorf("depth %s: %s", depth, got)
			}
			i := slices.IndexFunc(l.Workflows, func(w workflow.Workflow) bool { return w.Name == "on-done" })
			if i < 0 || l.Workflows[i].Trigger.Depth != 5 || !slices.Equal(l.Workflows[i].Trigger.Actors, []string{"person"}) {
				t.Errorf("depth %s loads as %+v", depth, l.Workflows)
			}
			continue
		}
		if !strings.Contains(got, problem) {
			t.Errorf("depth %s: want %q in\n%s", depth, problem, got)
		}
	}
}

func TestATaskVerbStepNamesAVerbAWorkflowMayRun(t *testing.T) {
	for verb, problem := range map[string]string{
		"someday": "step[0].verb: someday has no by.workflow in tasks.toml: no workflow may run it",
		"dnoe":    `step[0].verb: names no verb of tasks.toml; did you mean "done"?`,
		"done":    "",
	} {
		got := userWorkflowProblems(t, "mine", taskWorkflow(`task.state == "inbox"`, "use = \"task.verb\"\nverb = \""+verb+"\""))
		if problem == "" && strings.TrimSpace(got) != "" || !strings.Contains(got, problem) {
			t.Errorf("%s: want %q in\n%s", verb, problem, got)
		}
	}
	got := userWorkflowProblems(t, "mine", taskWorkflow(`task.state == "inbox"`, "use = \"task.verb\"\nverb = \"schedule\"\nset = \"x\""))
	if !strings.Contains(got, "step[0].set: is not an argument of schedule") || !strings.Contains(got, "schedule needs until or on_activity") {
		t.Errorf("schedule's arguments come from its handler:\n%s", got)
	}
}
