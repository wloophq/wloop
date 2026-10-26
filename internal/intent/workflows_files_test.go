package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBeside(t *testing.T, root, workflow string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, "workflows", workflow, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const classifyOnOpen = `summary = "Classify a pull request"
provider = "github"
when = "true"

[trigger]
on = ["pr.opened"]

[[step]]
use = "classify"
`

func TestClassifyAndNotifyReadFiles(t *testing.T) {
	for name, c := range map[string]struct {
		manifest string
		files    map[string]string
		problem  string
	}{
		"classify's default files": {classifyOnOpen, map[string]string{"prompt.md": "What is it?\n", "schema.json": `{"type":"object"}`}, ""},
		"classify's named files": {classifyOnOpen + "prompt = \"ask.md\"\nschema = \"answer.json\"\n",
			map[string]string{"ask.md": "What is it?\n", "answer.json": `{"type":"object"}`}, ""},
		"classify without its prompt": {classifyOnOpen, map[string]string{"schema.json": `{"type":"object"}`}, "step[0].prompt: prompt.md: no such file beside the manifest"},
		"classify's schema outside the subset": {classifyOnOpen, map[string]string{"prompt.md": "What is it?\n",
			"schema.json": `{"type":"object","properties":{"kind":{"type":"string","oneOf":[]}}}`}, `keyword "oneOf"`},
		"classify's prompt outside its directory": {classifyOnOpen + "prompt = \"../ask.md\"\n", map[string]string{"schema.json": `{"type":"object"}`},
			`step[0].prompt: "../ask.md" is not a file of the workflow's directory`},
		"notify's default text": {"summary = \"Note\"\nprovider = \"github\"\nwhen = \"true\"\n\n[[step]]\nuse = \"notify.self\"\n",
			map[string]string{"text.md.tmpl": "merged\n"}, ""},
		"notify without its text": {"summary = \"Note\"\nprovider = \"github\"\nwhen = \"true\"\n\n[[step]]\nuse = \"notify.self\"\n",
			nil, "step[0].text: text.md.tmpl: no such file beside the manifest"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeWorkflow(t, root, "triage", c.manifest)
			writeBeside(t, root, "triage", c.files)
			l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
			if c.problem == "" {
				if len(problems) > 0 || len(l.Workflows) == 0 {
					t.Fatalf("%s", problems.Error())
				}
				return
			}
			if got := l.BrokenWorkflows["triage"].Error(); !strings.Contains(got, c.problem) {
				t.Fatalf("want %q in\n%s", c.problem, got)
			}
		})
	}
}

func TestEffectsIsAList(t *testing.T) {
	const closeEntry = `summary = "Close"
provider = "entry"
when = 'entry.items_resolved > 0'

[[step]]
use = "draft.propose"
purpose = "close"
`
	for effects, problem := range map[string]string{
		`["unit.closed"]`:                 "",
		`["unit.closed", "entry.nudged"]`: "",
		`"unit.closed"`:                   "step[0].effects: must be a list of strings",
		`"unit.closed,entry.nudged"`:      "step[0].effects: must be a list of strings",
		`["unit.close"]`:                  `step[0].effects[0]: "unit.close" is not one of`,
	} {
		root := t.TempDir()
		writeWorkflow(t, root, "close", closeEntry+"effects = "+effects+"\n")
		l, problems := Load(userLayer(root), nil, testBase(), nil)
		if problem == "" {
			if len(problems) > 0 {
				t.Errorf("%s: %s", effects, problems.Error())
				continue
			}
			if got, _ := l.Workflows[0].Steps[0].Args["effects"].([]string); len(got) == 0 {
				t.Errorf("%s: args %v", effects, l.Workflows[0].Steps[0].Args)
			}
			continue
		}
		if !strings.Contains(problems.Error(), problem) {
			t.Errorf("%s: want %q in\n%s", effects, problem, problems.Error())
		}
	}
}

func TestNoTriggerMeansEverySweep(t *testing.T) {
	root := t.TempDir()
	writeWorkflow(t, root, "close-quiet", quietSession)
	writeWorkflow(t, root, "every", strings.Replace(quietSession, "[[step]]", "[trigger]\nevery = \"sweep\"\n\n[[step]]", 1))
	writeWorkflow(t, root, "empty", strings.Replace(quietSession, "[[step]]", "[trigger]\n\n[[step]]", 1))
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(l.Workflows) != 1 || l.Workflows[0].Trigger.Kind != "sweep" {
		t.Fatalf("%+v", l.Workflows)
	}
	got := problems.Error()
	for _, want := range []string{"every/workflow.toml:6:9: trigger.every: unknown key", "empty/workflow.toml:5:2: trigger: needs exactly one of on or schedule"} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in\n%s", want, got)
		}
	}
}
