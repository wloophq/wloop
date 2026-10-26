package intent

import (
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/profiles"
)

const removeWorkspace = `summary = "Remove a workspace"
provider = "workspace"
when = '!workspace.main'

[[step]]
use = "workspace.remove"

[approval]
mode = "wait"
[[approval.rule]]
when = 'size(workspace.pendency) == 0'
`

func TestReleasingRunRuleRefusesUntrustedFacts(t *testing.T) {
	for mode, refused := range map[string]bool{`"run"`: true, "\"delay\"\nwindow = \"5m\"": true, `"wait"`: false} {
		root := t.TempDir()
		writeWorkflow(t, root, "remove", removeWorkspace+"mode = "+mode+"\n")
		l, problems := Load(userLayer(root), nil, testBase(), nil)
		got := problems.Error()
		if refused != strings.Contains(got, "approval.rule[0].when: reads workspace.pendency, which a rule that runs") {
			t.Errorf("mode %s: %s", mode, got)
		}
		if !refused && (len(l.Workflows) != 1 || l.Workflows[0].Approval.Rules[0].Mode != workflow.ApprovalWait) {
			t.Errorf("mode %s: %+v", mode, l.Workflows)
		}
	}
}

func TestARunRuleNeedsAWhenAndAMode(t *testing.T) {
	for block, want := range map[string]string{
		"[[approval.rule]]\nmode = \"wait\"\n":                                                "approval.rule[0].when: is required",
		"[[approval.rule]]\nwhen = 'workspace.dirty > 0'\n":                                   "approval.rule[0].mode: is required",
		"[[approval.rule]]\nwhen = 'workspace.dirty > 0'\nmode = \"send\"\n":                  `approval.rule[0].mode: "send" is not one of "run", "delay", "wait"`,
		"[[approval.rule]]\nwhen = 'workspace.dirty > 0'\nmode = \"delay\"\n":                 "approval.rule[0].window: is required with mode delay",
		"[[approval.rule]]\nwhen = 'workspace.dirty > 0'\nmode = \"wait\"\nwindow = \"1m\"\n": "approval.rule[0].window: is read only with mode delay",
		"[approval]\nmode = \"delay\"\n":                                                      "approval.window: is required with mode delay",
		"[[approval.rule]]\nwhen = 'draft.delivery == \"comment\"'\nmode = \"run\"\n":         "approval.rule[0].when: 1: unknown field draft.delivery",
	} {
		root := t.TempDir()
		writeWorkflow(t, root, "remove", strings.SplitAfter(removeWorkspace, "[[step]]\nuse = \"workspace.remove\"\n")[0]+"\n"+block)
		if _, problems := Load(userLayer(root), nil, testBase(), nil); !strings.Contains(problems.Error(), want) {
			t.Errorf("%s: want %q in\n%s", block, want, problems.Error())
		}
	}
}

type holds bool

func (h holds) Match(workflow.Vars) (bool, error) { return bool(h), nil }
func (holds) Reads() []string                     { return nil }

func TestConfigOverlaysFieldByField(t *testing.T) {
	min, hour := time.Minute, time.Hour
	env := builtin.WorkflowEnv("workspace")
	manifest := workflow.Approval{Mode: workflow.ApprovalDelay, Window: min, Rules: []workflow.ApprovalRule{{When: holds(true), Mode: workflow.ApprovalWait}}}
	for name, c := range map[string]struct {
		o     ApprovalOverlay
		mode  workflow.ApprovalMode
		win   time.Duration
		rules int
		err   string
	}{
		"nothing set keeps the manifest's":                 {ApprovalOverlay{}, workflow.ApprovalDelay, min, 1, ""},
		"a window keeps the mode and the rules":            {ApprovalOverlay{Window: &hour}, workflow.ApprovalDelay, hour, 1, ""},
		"a mode keeps the rules":                           {ApprovalOverlay{Mode: "wait"}, workflow.ApprovalWait, 0, 1, ""},
		"an empty rule list clears them":                   {ApprovalOverlay{Rules: []RuleOverlay{}}, workflow.ApprovalDelay, min, 0, ""},
		"rules replace them":                               {ApprovalOverlay{Rules: []RuleOverlay{{When: "workspace.dirty > 0", Mode: "wait"}, {When: "true", Mode: "run"}}}, workflow.ApprovalDelay, min, 2, ""},
		"a window left off delay fails":                    {ApprovalOverlay{Mode: "run", Window: &hour}, "", 0, 0, "window"},
		"a releasing rule reading an untrusted fact fails": {ApprovalOverlay{Rules: []RuleOverlay{{When: "size(workspace.pendency) == 0", Mode: "run"}}}, "", 0, 0, "workspace.pendency"},
		"a rule that does not compile fails":               {ApprovalOverlay{Rules: []RuleOverlay{{When: "pr.mine", Mode: "wait"}}}, "", 0, 0, "rule[0].when"},
	} {
		got, err := OverlayRunApproval(manifest, c.o, env)
		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("run, %s: %+v, %v", name, got, err)
			}
		case err != nil || got.Mode != c.mode || got.Window != c.win || len(got.Rules) != c.rules:
			t.Errorf("run, %s: %+v, %v", name, got, err)
		}
	}
	if len(manifest.Rules) != 1 || manifest.Mode != workflow.ApprovalDelay {
		t.Fatalf("the overlay changed the manifest's approval: %+v", manifest)
	}

	denv := builtin.ApprovalEnv(true, "slack")
	base, err := OverlayDraftApproval(nil, ApprovalOverlay{Mode: "delay", Window: &min, Rules: []RuleOverlay{{When: `slack.conversation == "dm"`, Mode: "send"}}}, denv)
	if err != nil || base.Default != (draft.Approval{Mode: draft.Delay, Window: min}) || len(base.Rules) != 1 {
		t.Fatalf("over no manifest block: %+v, %v", base, err)
	}
	for name, c := range map[string]struct {
		o     ApprovalOverlay
		want  draft.Approval
		rules int
		err   string
	}{
		"a mode keeps the rules":         {ApprovalOverlay{Mode: string(draft.DryRun)}, draft.Approval{Mode: draft.DryRun}, 1, ""},
		"a window keeps the mode":        {ApprovalOverlay{Window: &hour}, draft.Approval{Mode: draft.Delay, Window: hour}, 1, ""},
		"an empty rule list clears them": {ApprovalOverlay{Rules: []RuleOverlay{}}, draft.Approval{Mode: draft.Delay, Window: min}, 0, ""},
		"a window left off delay fails":  {ApprovalOverlay{Mode: "send", Window: &hour}, draft.Approval{}, 0, "window"},
		"an untrusted fact fails":        {ApprovalOverlay{Rules: []RuleOverlay{{When: `slack.conversation == "dm"`, Mode: "wait"}, {When: `brief.kind == "request"`, Mode: "wait"}}}, draft.Approval{}, 0, "brief.kind"},
	} {
		got, err := OverlayDraftApproval(&base, c.o, denv)
		switch {
		case c.err != "":
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("drafts, %s: %+v, %v", name, got, err)
			}
		case err != nil || got.Default != c.want || len(got.Rules) != c.rules:
			t.Errorf("drafts, %s: %+v, %v", name, got, err)
		}
	}
}

func TestBundledWorkflowsFollowTheGrammar(t *testing.T) {
	sub, err := fs.Sub(profiles.Default, "default")
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := fs.ReadDir(sub, "workflows")
	if err != nil || len(dirs) == 0 {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		b, err := fs.ReadFile(sub, path.Join("workflows", d.Name(), "workflow.toml"))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := toml.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		for _, p := range builtin.Check(builtin.SurfaceWorkflow, doc) {
			t.Errorf("%s: %s: %s", d.Name(), p.Path, p.Msg)
		}
		steps, _ := doc["step"].([]any)
		for i, raw := range steps {
			args := map[string]any{}
			for k, v := range raw.(map[string]any) {
				if k != "use" {
					args[k] = v
				}
			}
			for _, p := range builtin.Check(builtin.SurfaceStep, args) {
				t.Errorf("%s: step[%d].%s: %s", d.Name(), i, p.Path, p.Msg)
			}
		}
	}
}

func TestEveryStepArgumentHasAGrammarRow(t *testing.T) {
	for _, s := range builtin.Steps {
		for _, a := range s.Args {
			if r, ok := builtin.StepArg(s.Name, a); !ok || r.Doc == "" {
				t.Errorf("%s: %s has no grammar row", s.Name, a)
			}
		}
	}
	for _, r := range builtin.Grammar {
		if r.In != builtin.SurfaceStep {
			continue
		}
		i := slicesIndex(builtin.Steps, r.Step)
		if i < 0 || !strings.Contains(" "+strings.Join(builtin.Steps[i].Args, " ")+" ", " "+r.Path+" ") {
			t.Errorf("row %s of %s names no argument of the step", r.Path, r.Step)
		}
	}
}

func slicesIndex(steps []builtin.StepDef, name string) int {
	for i, s := range steps {
		if s.Name == name {
			return i
		}
	}
	return -1
}
