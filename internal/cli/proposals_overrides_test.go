package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

// firstDayClosure runs the default workflows over the scenario's first tick and
// returns the closure the seeded close entry gets.
func firstDayClosure(t *testing.T, r *replay) draft.Draft {
	t.Helper()
	p := newWorkflowProposer(r)
	r.scenario(r.now)
	if err := p.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	ds, err := r.svc.List(context.Background(), outbox.Filter{Workflow: "slack-closure"})
	if err != nil || len(ds) != 1 {
		t.Fatalf("closures %+v, %v", ds, err)
	}
	return ds[0]
}

func TestAPortugueseClosureTemplateYieldsAPortugueseClosure(t *testing.T) {
	t.Parallel()
	r := newReplay(t)
	userProfile(t, r.te, map[string]string{"workflows/slack-closure/text.md.tmpl": "Encerrando:\n{{range .Items}}\n- {{.Line}}: {{or .Resolution \"resolvido\"}}{{end}}"})
	d := firstDayClosure(t, r)
	if want := "Encerrando:\n\n- Item d1 of the thread: done\n- Item d2 of the thread: resolvido"; d.Text != want {
		t.Fatalf("closure %q, want %q", d.Text, want)
	}
}

func TestAReactionClosureClosesWithACheckMark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "plugins", "slack", "profile", "workflows", "slack-closure", "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(string(b), `purpose = "close"`, "purpose = \"close\"\ndelivery = \"reaction\"\nreaction = \"white_check_mark\"\ntarget = \"item\"", 1)
	userProfile(t, r.te, map[string]string{"workflows/slack-closure/workflow.toml": manifest})
	r.slack.startedByMe(rpD)
	d := firstDayClosure(t, r)
	if d.Delivery != draft.Reaction || d.Reaction != "white_check_mark" || d.Target != "https://example.test/d1" || d.Text != "" {
		t.Fatalf("closure %+v", d)
	}
	if _, err := r.svc.Approve(ctx, d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, failed, err := r.svc.Send(ctx); err != nil || failed > 0 {
		t.Fatalf("send: %d failed, %v", failed, err)
	}
	if !r.slack.isClosed(rpD) {
		t.Fatal("the ✅ closed nothing")
	}
}

func TestTheConfigsDraftApprovalOverlaysTheWorkflows(t *testing.T) {
	t.Parallel()
	cfg, err := loadConfigTOML(t, `[workflows.slack-ask-owner.drafts.approval]
mode = "send"

[workflows.slack-closure.drafts.approval]
mode = "delay"
window = "2h"
`)
	if err != nil {
		t.Fatal(err)
	}
	rule := draft.Rule{When: draftWhen(func(map[string]any) bool { return true }), Approval: draft.Approval{Mode: draft.Wait}}
	manifest := &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{rule}}
	if p := draftApproval(cfg, workflow.Workflow{Name: "slack-closure", Slot: "slack", DraftApproval: manifest}, nil); p.Default != (draft.Approval{Mode: draft.Delay, Window: 2 * time.Hour}) || len(p.Rules) != 1 {
		t.Errorf("the config's mode and window keep the manifest's rules: %+v", p)
	}
	if a := draftApproval(cfg, workflow.Workflow{Name: "slack-ask-owner", Slot: "slack"}, nil).Default; a.Mode != draft.Send {
		t.Errorf("the config's block over no manifest block: %+v", a)
	}
	if p := draftApproval(cfg, workflow.Workflow{Name: "slack-stalled-nudge", Slot: "task"}, nil); p.Default.Mode != draft.Wait || len(p.Rules) != 0 {
		t.Errorf("a workflow the config leaves alone: %+v", p)
	}
	te := newTestEnv(t, nil)
	m := outboxManifests{loaded: te.loadedIntents, config: func() config.Config { return cfg }}
	if p, ok := m.WorkflowApproval("slack-closure", nil); !ok || p == nil || p.Default != (draft.Approval{Mode: draft.Delay, Window: 2 * time.Hour}) {
		t.Errorf("a turn the workflow starts does not take the config's approval: %+v", p)
	}
}

func TestAConfigOverlayOnAWorkflowWithoutABlockOverlaysTheIntents(t *testing.T) {
	t.Parallel()
	cfg, err := loadConfigTOML(t, `[[workflows.review-first-round.drafts.approval.rule]]
when = "pr.draft"
mode = "wait"

[workflows.review-follow-up.drafts.approval]
window = "30m"
`)
	if err != nil {
		t.Fatal(err)
	}
	te := newTestEnv(t, nil)
	m := outboxManifests{loaded: te.loadedIntents, config: func() config.Config { return cfg }}
	sends := draft.Fixed(draft.Approval{Mode: draft.Send})
	if p, _ := m.WorkflowApproval("review-first-round", sends); p == nil || p.Default.Mode != draft.Send || len(p.Rules) != 1 {
		t.Errorf("a rule-only overlay replaced the intent's mode: %+v", p)
	}
	delays := draft.Fixed(draft.Approval{Mode: draft.Delay, Window: time.Hour})
	if p, _ := m.WorkflowApproval("review-follow-up", delays); p == nil || p.Default != (draft.Approval{Mode: draft.Delay, Window: 30 * time.Minute}) {
		t.Errorf("a window overlay over the intent's delay: %+v", p)
	}
	if p, _ := m.WorkflowApproval("review-follow-up", sends); p == nil || p.Default.Mode != draft.Wait {
		t.Errorf("a window over an intent that sends holds every draft: %+v", p)
	}
	if p, _ := m.WorkflowApproval("nudge-silent-30d", sends); p != sends {
		t.Errorf("a workflow the config leaves alone takes the intent's block: %+v", p)
	}
}

func TestConfigWorkflowProblemsCheckTheMergedBlocks(t *testing.T) {
	t.Parallel()
	cfg, err := loadConfigTOML(t, `[workflows.nudge-silent-30d.approval]
window = "5m"

[workflows.review-first-round.approval]
window = "5m"

[workflows.review-first-round.drafts.approval]
mode = "delay"

[workflows.slack-closure.drafts.approval]
window = "30m"

[workflows.slack-ask-owner.drafts.approval]
rule = []

[workflows.address]
only_when = "brief.extra.nope == true"
`)
	if err != nil {
		t.Fatal(err)
	}
	te := newTestEnv(t, nil)
	var got []string
	for _, err := range configWorkflowProblems(cfg, te.loadedIntents()) {
		got = append(got, err.Error())
	}
	for _, want := range []string{"workflows.nudge-silent-30d.approval", "workflows.review-first-round.drafts.approval", "workflows.slack-closure.drafts.approval", "workflows.address.only_when"} {
		if !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, want) }) {
			t.Errorf("no problem for %s in %q", want, got)
		}
	}
	for _, clean := range []string{"workflows.review-first-round.approval", "workflows.slack-ask-owner"} {
		if slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, clean) }) {
			t.Errorf("%s merges cleanly, yet %q", clean, got)
		}
	}
}

type draftWhen func(map[string]any) bool

func (w draftWhen) Holds(facts map[string]any, _ time.Time) (bool, error) { return w(facts), nil }

func TestAnAskOwnerDraftTakesTheConfiguredWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	r.te.cfg.Workflows = map[string]config.WorkflowConfig{"slack-ask-owner": {Drafts: &config.WorkflowDrafts{Approval: &config.ApprovalConfig{Mode: "delay", Window: new(config.Days(time.Hour))}}}}
	p := newWorkflowProposer(r)
	r.scenario(r.now)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	ds, err := r.svc.List(ctx, outbox.Filter{Workflow: "slack-ask-owner"})
	if err != nil || len(ds) == 0 {
		t.Fatalf("ask-owner drafts %+v, %v", ds, err)
	}
	for _, d := range ds {
		if d.State != draft.Proposed || d.Approval.Mode != draft.Delay || !d.Deadline.Equal(d.CreatedAt.Add(time.Hour)) {
			t.Errorf("an ask-owner draft ignores the configured window: %+v", d)
		}
	}
}

func TestAUserWorkflowThatFailsToLoadKeepsItsDrafts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newReplay(t)
	b, err := os.ReadFile(filepath.Join("..", "..", "plugins", "slack", "profile", "workflows", "slack-ask-owner", "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join("..", "..", "plugins", "slack", "profile", "workflows", "slack-ask-owner", "text.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	dir := userProfile(t, r.te, map[string]string{"workflows/my-ask/workflow.toml": string(b), "workflows/my-ask/text.md.tmpl": string(text)})
	manifest := filepath.Join(dir, "workflows", "my-ask", "workflow.toml")
	off := false
	r.te.cfg.Workflows = map[string]config.WorkflowConfig{"slack-ask-owner": {Enabled: &off}}
	p := newWorkflowProposer(r)
	r.scenario(r.now)
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	mine := func() []draft.Draft {
		t.Helper()
		ds, err := r.svc.List(ctx, outbox.Filter{Workflow: "my-ask"})
		if err != nil {
			t.Fatal(err)
		}
		return ds
	}
	if ds := mine(); len(ds) == 0 || ds[0].State != draft.Proposed {
		t.Fatalf("my-ask proposed %+v; broken %v", ds, r.te.loadedIntents().BrokenWorkflows)
	}
	if err := os.WriteFile(manifest, append(b, []byte("\nwhen_typo = true\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	if _, broken := r.te.loadedIntents().BrokenWorkflows["my-ask"]; !broken {
		t.Fatal("the typo did not break my-ask")
	}
	for _, d := range mine() {
		if d.State != draft.Proposed {
			t.Fatalf("a workflow that failed to load lost its draft: %+v", d)
		}
	}
	if err := os.RemoveAll(filepath.Dir(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := p.pass(ctx); err != nil {
		t.Fatal(err)
	}
	for _, d := range mine() {
		if d.State != draft.Discarded || d.Reason != draft.WithdrawMoot {
			t.Fatalf("a removed workflow kept its draft: %+v", d)
		}
	}
}

// slackThanks is a user workflow on Slack units whose [drafts.approval]
// releases a draft in a DM.
const slackThanks = `summary = "Thank in a DM"
provider = "slack"
when = 'slack.live_task'

[[step]]
use = "draft.propose"
purpose = "thanks"
items = "all"

[drafts.approval]
mode = "wait"
[[drafts.approval.rule]]
when = 'slack.conversation == "dm"'
mode = "send"
`

func loadConfigTOML(t *testing.T, body string) (config.Config, error) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config.LoadFile(file, Defaults())
}

func TestTheConfigsDraftApprovalReplacesTheManifestsRules(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	userProfile(t, te, map[string]string{"workflows/slack-thanks/workflow.toml": slackThanks, "workflows/slack-thanks/text.md.tmpl": "Thanks!\n"})
	l := te.loadedIntents()
	i := slices.IndexFunc(l.Workflows, func(x workflow.Workflow) bool { return x.Name == "slack-thanks" })
	if i < 0 {
		t.Fatalf("not loaded: %v", l.Problems.Error())
	}
	w := l.Workflows[i]
	cfg, err := loadConfigTOML(t, `[workflows.slack-thanks.drafts.approval]
mode = "wait"

[[workflows.slack-thanks.drafts.approval.rule]]
when = "slack.channel == \"C0TEAM\" && draft.purpose == \"thanks\""
mode = "delay"
window = "1h"
`)
	if err != nil {
		t.Fatal(err)
	}
	pol := draftApproval(cfg, w, nil)
	d := draft.Draft{Delivery: draft.Message, Purpose: "thanks", Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: w.Name}}
	for name, c := range map[string]struct {
		facts map[string]any
		want  draft.Approval
	}{
		"the manifest's rule is gone": {map[string]any{"slack.conversation": "dm"}, draft.Approval{Mode: draft.Wait}},
		"the config's rule applies":   {map[string]any{"slack.channel": "C0TEAM"}, draft.Approval{Mode: draft.Delay, Window: time.Hour}},
	} {
		if got, err := pol.Decide(d, draft.Subject{Slot: "slack", Facts: c.facts}, time.Time{}); err != nil || got.Approval != c.want {
			t.Errorf("%s: %+v %v", name, got, err)
		}
	}
	if got, _ := draftApproval(Defaults(), w, nil).Decide(d, draft.Subject{Slot: "slack", Facts: map[string]any{"slack.conversation": "dm"}}, time.Time{}); got.Approval.Mode != draft.Send {
		t.Errorf("without the config, the manifest's rule: %+v", got)
	}
	m := outboxManifests{loaded: te.loadedIntents, config: func() config.Config { return cfg }}
	if p, ok := m.WorkflowApproval("slack-thanks", nil); !ok || p == nil || len(p.Rules) != 1 || p.Rules[0].Approval.Mode != draft.Delay {
		t.Errorf("a turn's draft of the workflow does not take the config's: %+v", p)
	}
}

func TestAConfigRuleOverAnotherSubjectIsRefusedAndHolds(t *testing.T) {
	t.Parallel()
	cfg, err := loadConfigTOML(t, `[workflows.slack-thanks.drafts.approval]
mode = "send"

[[workflows.slack-thanks.drafts.approval.rule]]
when = "pr.author == \"dependabot[bot]\""
mode = "send"
`)
	if err != nil {
		t.Fatalf("a rule over one subject's facts loads: %v", err)
	}
	w := workflow.Workflow{Name: "slack-thanks", Slot: "slack"}
	pol := draftApproval(cfg, w, nil)
	if got, err := pol.Decide(draft.Draft{Delivery: draft.Message}, draft.Subject{Slot: "slack"}, time.Time{}); err != nil || got.Approval.Mode != draft.Wait || len(pol.Rules) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	l := *newTestEnv(t, nil).loadedIntents()
	l.Workflows = []workflow.Workflow{w, {Name: "other", Slot: "pr"}}
	problems := configWorkflowProblems(cfg, &l)
	if len(problems) != 1 || !strings.Contains(problems[0].Error(), "workflows.slack-thanks.drafts.approval") || !strings.Contains(problems[0].Error(), "pr.author") {
		t.Fatalf("problems %v", problems)
	}
}

func TestTheConfigRefusesAnInvalidDraftApprovalRule(t *testing.T) {
	t.Parallel()
	for name, rule := range map[string]string{
		"a bad predicate":         `when = "draft.delivery =="` + "\nmode = \"send\"",
		"a window with mode none": `when = 'draft.delivery == "message"'` + "\nmode = \"send\"\nwindow = \"1h\"",
		"a brief fact":            `when = 'brief.risk == "low"'` + "\nmode = \"send\"",
		"no when":                 `mode = "send"`,
		"an untrusted fact":       `when = "slack.started_by_me"` + "\nmode = \"send\"",
		"two subjects":            `when = 'pr.author == "x" && slack.conversation == "dm"'` + "\nmode = \"send\"",
	} {
		_, err := loadConfigTOML(t, "[workflows.slack-thanks.drafts.approval]\nmode = \"wait\"\n\n[[workflows.slack-thanks.drafts.approval.rule]]\n"+rule)
		if err == nil || !strings.Contains(err.Error(), "workflows.slack-thanks.drafts.approval.rule[0]") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestARuleThatNeverReleasesACommentIsReportedAtLoad(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	never := strings.Replace(slackThanks, `when = 'slack.conversation == "dm"'`, `when = 'draft.delivery in ["comment"]'`, 1)
	dir := userProfile(t, te, map[string]string{"workflows/slack-thanks/workflow.toml": never, "workflows/slack-thanks/text.md.tmpl": "Thanks!\n"})
	if code := te.run("profile", "check", dir); code != 0 || !strings.Contains(te.err.String(), "warning: ") ||
		!strings.Contains(te.err.String(), "never releases a comment draft") {
		t.Fatalf("code %d\n%s", code, te.err.String())
	}
	cfg, err := loadConfigTOML(t, `[workflows.slack-thanks.drafts.approval]
mode = "wait"

[[workflows.slack-thanks.drafts.approval.rule]]
when = "draft.delivery != \"reaction\""
mode = "send"
`)
	if err != nil {
		t.Fatal(err)
	}
	var logged []string
	te.wireWorkflows(&serveWiring{Cfg: cfg, Log: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }})
	if len(logged) != 2 || !slices.ContainsFunc(logged, func(l string) bool {
		return strings.HasPrefix(l, "config: workflows.slack-thanks.drafts.approval.rule[0].when") && strings.Contains(l, `draft.delivery == "comment"`)
	}) || !slices.ContainsFunc(logged, func(l string) bool { return strings.HasPrefix(l, "profile: ") && strings.Contains(l, "workflow.toml") }) {
		t.Fatalf("logged %q", logged)
	}
}
