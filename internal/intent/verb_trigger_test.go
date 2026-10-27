package intent

import (
	"strings"
	"testing"
)

const onDone = `summary = "React to done"
provider = "entry"
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
		"an unknown verb":      {strings.Replace(onDone, `on = ["verb_done"]`, `on = ["verb_dne"]`, 1), `"verb_dne" is not one of`},
		"another provider":     {strings.Replace(onDone, `provider = "entry"`, `provider = "github"`, 1), `trigger.on[0]: verb_done is an event of entry units; it needs provider = "entry"`},
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
