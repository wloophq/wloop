package intent

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
)

func loadGatesWith(t *testing.T, user string) (GatesDef, string) {
	t.Helper()
	b, err := os.ReadFile("../../profiles/default/" + GatesFile)
	if err != nil {
		t.Fatal(err)
	}
	var layers []profile.Layer
	if user != "" {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, GatesFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		layers = []profile.Layer{{Name: "user", Dir: dir}}
	}
	d, problems := loadGates(layers, fstest.MapFS{GatesFile: {Data: b}})
	return d, problems.Error()
}

func TestGatesTomlDeclaresKindsAndIntents(t *testing.T) {
	t.Parallel()
	d, problems := loadGatesWith(t, "")
	if problems != "" {
		t.Fatal(problems)
	}
	for _, k := range []gate.Kind{gate.KindDraft, gate.KindPlan, gate.KindQuestion, gate.KindPost, gate.KindProposal, gate.KindTrust, gate.KindStep} {
		if g, ok := d.FirstOf(k); !ok || g.Kind != k || g.Label == "" || len(g.Intents) == 0 {
			t.Errorf("first gate of %s: %+v %v", k, g, ok)
		}
	}
	draft, _ := d.Gate("draft")
	if len(draft.Intents) != 2 || draft.Intents[0].Name != "approve" || draft.Intents[0].Status != gate.Approved ||
		draft.Intents[1].Name != "discard" || draft.Intents[1].Status != gate.Rejected {
		t.Errorf("draft intents %+v", draft.Intents)
	}
	q, _ := d.Gate("question")
	if !q.Intents[0].Answer || q.Intents[1].Answer {
		t.Errorf("question intents %+v", q.Intents)
	}

	d, problems = loadGatesWith(t, `[gates.draft]
label = "Send it?"

[gates.draft.intents.later]
label = "Later"
status = "dismissed"

[gates.ship]
kind = "step"
label = "Ship it?"

[gates.ship.intents.go]
label = "Go"
status = "approved"
`)
	if problems != "" {
		t.Fatal(problems)
	}
	draft, _ = d.Gate("draft")
	if draft.Label != "Send it?" || len(draft.Intents) != 3 || draft.Intents[2].Name != "later" || draft.Kind != gate.KindDraft {
		t.Errorf("draft over a user layer %+v", draft)
	}
	if first, _ := d.FirstOf(gate.KindStep); first.Name != "review-diff" {
		t.Errorf("a user's gate comes after the bundled ones of its kind: %s", first.Name)
	}
	if _, ok := d.Gate("ship"); !ok {
		t.Error("the user's gate is not declared")
	}

	for user, want := range map[string]string{
		"[gates.wish]\nkind = \"wish\"\n":                       "gates.wish.kind",
		"[gates.wish]\nlabel = \"Wish\"\n":                      "gates.wish.kind: is required",
		"[gates.draft.intents.maybe]\nlabel = \"Maybe\"\n":      "gates.draft.intents.maybe.status: is required",
		"[gates.draft.intents.maybe]\nstatus = \"open\"\n":      "gates.draft.intents.maybe.status",
		"[gates.wish]\nkind = \"step\"\n[gates.wish.intents]\n": "gates.wish.intents: is required",
	} {
		_, problems := loadGatesWith(t, user)
		if !strings.Contains(problems, want) {
			t.Errorf("%q: problems %q, want %q", user, problems, want)
		}
	}
}

func TestAVerbsGateNamesADeclaredGate(t *testing.T) {
	t.Parallel()
	gates, problems := loadGatesWith(t, "")
	if problems != "" {
		t.Fatal(problems)
	}
	bundled := fstest.MapFS{TasksFile: {Data: []byte(defaultTasks(t))}}
	for gateName, want := range map[string]bool{"proposal": true, "nowhere": false} {
		dir := t.TempDir()
		user := "[verbs.done]\ngate = \"" + gateName + "\"\n[verbs.done.by]\nagent = \"gate\"\n"
		if err := os.WriteFile(filepath.Join(dir, TasksFile), []byte(user), 0o600); err != nil {
			t.Fatal(err)
		}
		_, problems := loadTasks([]profile.Layer{{Name: "user", Dir: dir}}, bundled, &gates)
		if got := !strings.Contains(problems.Error(), "verbs.done.gate: names no gate"); got != want {
			t.Errorf("gate %s: problems %q", gateName, problems.Error())
		}
	}
}

func TestEveryOldApprovalKeyNamesGatesApproval(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		intent, workflow, path, near string
	}{
		"an intent's drafts.approval":        {intent: minimal + "\n[drafts.approval]\nmode = \"send\"\n", path: "drafts.approval", near: "gates.approval"},
		"an intent's drafts.approval rule":   {intent: minimal + "\n[[drafts.approval.rule]]\nwhen = 'true'\nmode = \"wait\"\n", path: "drafts.approval", near: "gates.approval"},
		"a workflow's approval":              {workflow: quietSession + "\n[approval]\nmode = \"delay\"\nwindow = \"1m\"\n", path: "approval", near: "gates.approval"},
		"a workflow's approval rule":         {workflow: quietSession + "\n[[approval.rule]]\nwhen = 'true'\nmode = \"wait\"\n", path: "approval", near: "gates.approval"},
		"a workflow's drafts.approval":       {workflow: quietSession + "\n[drafts.approval]\nmode = \"send\"\n", path: "drafts.approval", near: "gates.approval"},
		"a workflow's drafts.approval rule":  {workflow: quietSession + "\n[[drafts.approval.rule]]\nwhen = 'true'\nmode = \"wait\"\n", path: "drafts.approval", near: "gates.approval"},
		"a workflow's drafts.approval.mode ": {workflow: quietSession + "\n[drafts]\napproval = { mode = \"send\" }\n", path: "drafts.approval", near: "gates.approval"},
	} {
		root := t.TempDir()
		if c.intent != "" {
			writeIntent(t, root, "ask-author", c.intent, "Ask.\n")
		} else {
			writeWorkflow(t, root, "close-quiet", c.workflow)
		}
		_, problems := Load(userLayer(root), nil, testBase(), nil)
		want := fmt.Sprintf("%s: renamed; did you mean %q?", c.path, c.near)
		if !strings.Contains(problems.Error(), want) {
			t.Errorf("%s: %s, want %q", name, problems.Error(), want)
		}
	}
	for raw, near := range map[string]map[string]string{
		"[workflows.x.approval]\nmode = \"run\"\n":                        {"workflows.x.approval": "workflows.x.gates.approval"},
		"[[workflows.x.approval.rule]]\nwhen = 'true'\nmode = \"wait\"\n": {"workflows.x.approval": "workflows.x.gates.approval"},
		"[workflows.x.drafts.approval]\nmode = \"send\"\n":                {"workflows.x.drafts.approval": "workflows.x.gates.approval"},
		"[github.drafts.approval]\nmode = \"wait\"\n":                     {"github.drafts.approval": "plugins.github.gates.approval"},
		"[slack.drafts.approval]\nmode = \"wait\"\n":                      {"slack.drafts.approval": "plugins.slack.gates.approval"},
		"[plugins.github.drafts.approval]\nmode = \"wait\"\n":             {"plugins.github.drafts.approval": "plugins.github.gates.approval"},
	} {
		file := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := config.LoadFile(file, config.Defaults())
		var unknown *config.UnknownKeysError
		if !errors.As(err, &unknown) || !maps.Equal(unknown.Near, near) {
			t.Errorf("%s: %v, want %v", raw, err, near)
		}
	}
}

func TestEveryGateKindHasAProfileGate(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../profiles/default/" + GatesFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, problems := loadGates(nil, fstest.MapFS{GatesFile: {Data: b}}); len(problems) > 0 {
		t.Fatalf("the bundled gates.toml: %s", problems.Error())
	}
	src := string(b)
	start := strings.Index(src, "[gates.trust]")
	end := strings.Index(src, "[gates.review-diff]")
	_, problems := loadGates(nil, fstest.MapFS{GatesFile: {Data: []byte(src[:start] + src[end:])}})
	if !strings.Contains(problems.Error(), "declares no gate of kind trust: the loop opens one by its kind") {
		t.Fatalf("a profile with no trust gate: %s", problems.Error())
	}
}

func TestGateIntentsKeepTheirDeclaredOrderAcrossLayers(t *testing.T) {
	t.Parallel()
	d, problems := loadGatesWith(t, `[gates.plan.intents.later]
label = "Later"
status = "dismissed"

[gates.plan.intents.run_now]
label = "Go"

[gates.plan.intents.again]
label = "Again"
status = "approved"
`)
	if problems != "" {
		t.Fatal(problems)
	}
	g, _ := d.Gate("plan")
	var names []string
	for _, in := range g.Intents {
		names = append(names, in.Name)
	}
	if want := []string{"run_now", "skip", "guide", "later", "again"}; !slices.Equal(names, want) || g.Intents[0].Label != "Go" {
		t.Fatalf("intents %v (%+v), want %v", names, g.Intents, want)
	}
}

func TestAWorkflowWithNoApprovalStillRunsAtOnce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWorkflow(t, root, "close-quiet", quietSession)
	loaded, broken, _, problems, _ := loadWorkflows(userLayer(root), bundledDefault(t), nil, nil, bundledTasks(t), bundledGates(t))
	if len(problems) > 0 || len(broken) > 0 {
		t.Fatal(problems.Error())
	}
	i := slices.IndexFunc(loaded, func(w workflow.Workflow) bool { return w.Name == "close-quiet" })
	w := loaded[i]
	if w.Approval.Mode != workflow.ApprovalRun || len(w.Approval.Rules) != 0 {
		t.Errorf("its plan gates: %+v, want run", w.Approval)
	}
	if w.DraftApproval != nil {
		t.Errorf("its drafts: %+v, want gates.toml's default", w.DraftApproval)
	}
	if got := DefaultDraftApproval(bundledGates(t)); got.Mode != draft.Wait {
		t.Errorf("a draft gate's default %+v, want wait", got)
	}
	for _, k := range GateKinds {
		if w.Gates[k].Mode != workflow.ApprovalWait {
			t.Errorf("its %s gates: %+v, want wait", k, w.Gates[k])
		}
	}
	d, problems2 := loadGatesWith(t, "[approval]\nmode = \"send\"\n")
	if problems2 != "" {
		t.Fatal(problems2)
	}
	if m, _, _ := d.DefaultFor(gate.KindPlan); m != gate.ModeRun {
		t.Errorf("a user's mode keeps the bundled rules: plan %s", m)
	}
	if m, _, _ := d.DefaultFor(gate.KindQuestion); m != gate.ModeSend {
		t.Errorf("a user's mode replaces the bundled mode: question %s", m)
	}
}

func TestDryRunOnAPlanGateIsRefused(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ manifest, want string }{
		"pinned to plan": {quietSession + "\n[[gates.approval.rule]]\nwhen = 'gate.kind == \"plan\"'\nmode = \"dry_run\"\n",
			"gates.approval.rule[0].when: pins gate.kind to plan with mode dry_run, which applies to draft gates only"},
		"pinned to plan among others": {quietSession + "\n[[gates.approval.rule]]\nwhen = 'gate.kind == \"plan\" && session.managed'\nmode = \"dry_run\"\n",
			"pins gate.kind to plan with mode dry_run"},
	} {
		root := t.TempDir()
		writeWorkflow(t, root, "close-quiet", c.manifest)
		if _, problems := Load(userLayer(root), nil, testBase(), nil); !strings.Contains(problems.Error(), c.want) {
			t.Errorf("%s: %s, want %q", name, problems.Error(), c.want)
		}
	}
	root := t.TempDir()
	writeWorkflow(t, root, "close-quiet", quietSession+"\n[[gates.approval.rule]]\nwhen = 'session.managed'\nmode = \"dry_run\"\n")
	l, problems := Load(userLayer(root), nil, testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	w := l.Workflows[0]
	if len(w.Approval.Rules) != 0 || w.DraftApproval == nil || len(w.DraftApproval.Rules) != 1 || w.DraftApproval.Rules[0].Approval.Mode != draft.DryRun {
		t.Fatalf("an unpinned dry_run holds draft gates only: plan %+v, drafts %+v", w.Approval, w.DraftApproval)
	}
	if _, problems := loadGatesWith(t, "[[approval.rule]]\nwhen = 'gate.kind == \"trust\"'\nmode = \"dry_run\"\n"); !strings.Contains(problems, "approval.rule[0].mode: is dry_run, which a trust gate never is") {
		t.Fatalf("gates.toml: %s", problems)
	}
}

func TestGateFactsComeFromTheGate(t *testing.T) {
	t.Parallel()
	f := GateFacts(gate.Gate{ID: 4, Kind: gate.KindQuestion, Name: "question", Status: gate.Answered}, "")
	for k, v := range map[string]any{"gate.kind": "question", "gate.name": "question", "gate.status": "answered", "gate.delivery": ""} {
		if f[k] != v {
			t.Errorf("%s = %v, want %v", k, f[k], v)
		}
	}
	env := gateEnv()
	for name := range env {
		if _, ok := f[name]; !ok {
			t.Errorf("GateFacts lacks %s, which the gate slot declares", name)
		}
	}
	p, err := expr.CompileWorkflow(`gate.kind == "question" && gate.status == "answered" && gate.name == "question"`, env)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := p.Match(workflow.Vars{Facts: f}); err != nil || !ok {
		t.Fatalf("a view's predicate over the gate's facts = %v, %v; want true", ok, err)
	}
	d, problems := loadGatesWith(t, "")
	if problems != "" {
		t.Fatal(problems)
	}
	if m, _, at, err := d.Approval.Decide(GateFacts(gate.Gate{Kind: gate.KindPlan, Name: "plan"}, "")); err != nil || m != gate.ModeRun || at != "gates.toml approval.rule[0]" {
		t.Fatalf("gates.toml decides a plan gate by its facts: %s %s %v", m, at, err)
	}
}

func TestAGateNamesWhatItsClosingDoesToATaskHeldForGates(t *testing.T) {
	t.Parallel()
	d, problems := loadGatesWith(t, "")
	if problems != "" {
		t.Fatal(problems)
	}
	dg, _ := d.Gate("draft")
	want := map[gate.Status]task.Category{gate.Approved: task.CategoryDone, gate.Rejected: task.CategoryDropped, gate.Superseded: task.CategoryDropped}
	if !maps.Equal(dg.EndsTask, want) {
		t.Fatalf("the bundled draft gate ends a held task as %v, want %v", dg.EndsTask, want)
	}
	if plan, _ := d.Gate("plan"); plan.EndsTask != nil {
		t.Errorf("the plan gate ends a held task as %v, want it left open", plan.EndsTask)
	}
	d, problems = loadGatesWith(t, "[gates.draft]\nends_task = { rejected = \"done\" }\n")
	if problems != "" {
		t.Fatal(problems)
	}
	dg, _ = d.Gate("draft")
	want = map[gate.Status]task.Category{gate.Approved: task.CategoryDone, gate.Rejected: task.CategoryDone, gate.Superseded: task.CategoryDropped}
	if !maps.Equal(dg.EndsTask, want) {
		t.Errorf("a user layer's ends_task gives %v, want %v", dg.EndsTask, want)
	}
	if _, problems := loadGatesWith(t, "[gates.draft]\nends_task = { opened = \"done\", approved = \"later\" }\n"); !strings.Contains(problems, "ends_task.opened") || !strings.Contains(problems, "ends_task.approved") {
		t.Errorf("an unknown status or category loads: %q", problems)
	}
}
