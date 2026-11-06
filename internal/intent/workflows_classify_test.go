package intent

import (
	"strings"
	"testing"
)

const judgeOnCreate = `summary = "Judge a new task"
provider = "task"
when = 'size(task.candidates) > 0'

[trigger]
on = ["task_created"]

[[step]]
use = "classify"
with = "%s"
`

const proposeOnVerdict = `summary = "Act on the verdict"
provider = "task"
when = '%s'

[trigger]
on = ["classified"]

[[step]]
use = "task.verb"
verb = "next"
`

func TestAPredicateReadsAClassifyOutputTypedByItsSchema(t *testing.T) {
	schema := `{"type":"object","properties":{"same_as":{"type":"string"},"sure":{"type":"boolean"},"score":{"type":"number"}}}`
	for name, c := range map[string]struct{ with, when, problem string }{
		"a string and a bool":          {"task.candidates", `classify.same_work.same_as in task.candidates && classify.same_work.sure`, ""},
		"a field the schema lacks":     {"task.candidates", `classify.same_work.other != ""`, "unknown field classify.same_work.other"},
		"a number no fact holds":       {"task.candidates", `classify.same_work.score > 0`, "unknown field classify.same_work.score"},
		"the name as the manifest has": {"task.candidates", `classify.same-work.same_as != ""`, "when"},
		"with naming no list fact":     {"task.state", `true`, "step[0].with: task.state is not a list fact of task"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkflow(t, root, "same-work", strings.Replace(judgeOnCreate, "%s", c.with, 1))
			writeBeside(t, root, "same-work", map[string]string{"prompt.md": "Same?\n", "schema.json": schema})
			writeWorkflow(t, root, "act", strings.Replace(proposeOnVerdict, "%s", c.when, 1))
			l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
			if c.problem == "" {
				if len(problems) > 0 || len(l.Workflows) < 2 {
					t.Fatalf("%s", problems.Error())
				}
				return
			}
			if got := problems.Error(); !strings.Contains(got, c.problem) {
				t.Fatalf("want %q in\n%s", c.problem, got)
			}
		})
	}
}
