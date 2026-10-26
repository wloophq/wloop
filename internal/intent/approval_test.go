package intent

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/builtin"
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
		"[[approval.rule]]\nwhen = 'workspace.dirty > 0'\nmode = \"send\"\n":                  `approval.rule[0].mode: "send" is not one of "run", "delay" or "wait"`,
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

func TestEveryStepArgumentNamesAStep(t *testing.T) {
	for _, r := range builtin.Grammar {
		if r.In != builtin.SurfaceStep {
			continue
		}
		if !slices.ContainsFunc(builtin.Steps, func(s builtin.StepDef) bool { return s.Name == r.Step }) || r.Doc == "" {
			t.Errorf("row %s names no step, or has no doc: %q", r.Path, r.Step)
		}
	}
}
