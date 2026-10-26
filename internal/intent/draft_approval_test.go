package intent

import (
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
)

func loadIntentWith(t *testing.T, extra string) (*Loaded, Problems) {
	t.Helper()
	root := t.TempDir()
	extra = strings.ReplaceAll(extra, "[draft_approval", "[drafts.approval")
	writeIntent(t, root, "ask-author", minimal+extra, "Ask.\n")
	return Load(userLayer(root), nil, testBase(), nil)
}

func loadWorkflowWith(t *testing.T, extra string) (*Loaded, Problems) {
	t.Helper()
	root := t.TempDir()
	writeWorkflow(t, root, "close-quiet", quietSession+extra)
	return Load(userLayer(root), nil, testBase(), nil)
}

func TestManifestDraftApproval(t *testing.T) {
	l, problems := loadIntentWith(t, "\n[draft_approval]\nmode = \"delay\"\nwindow = \"10m\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	if d.DraftApproval == nil || d.DraftApproval.Default != (draft.Approval{Mode: draft.Delay, Window: 10 * time.Minute}) || len(d.DraftApproval.Rules) != 0 {
		t.Fatalf("intent: %+v", d.DraftApproval)
	}

	l, problems = loadWorkflowWith(t, "\n[draft_approval]\nmode = \"send\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if w := l.Workflows[0]; w.DraftApproval == nil || w.DraftApproval.Default != (draft.Approval{Mode: draft.Send}) {
		t.Fatalf("workflow: %+v", w.DraftApproval)
	}

	l, _ = loadIntentWith(t, "")
	if d, _ := l.Registry.Get("ask-author"); d.DraftApproval != nil {
		t.Fatalf("intent without a block: %+v", d.DraftApproval)
	}
	l, _ = loadWorkflowWith(t, "")
	if w := l.Workflows[0]; w.DraftApproval != nil {
		t.Fatalf("workflow without a block: %+v", w.DraftApproval)
	}
}

func TestManifestDraftApprovalRefusals(t *testing.T) {
	for _, c := range []struct{ name, block, field, msg string }{
		{"unknown mode", "mode = \"later\"", "draft_approval.mode", "not one of"},
		{"no mode", "window = \"5m\"", "draft_approval.mode", "is required"},
		{"window without a window", "mode = \"delay\"", "draft_approval.window", "is required with mode delay"},
		{"a window on another mode", "mode = \"send\"\nwindow = \"5m\"", "draft_approval.window", "only with mode delay"},
		{"a zero window", "mode = \"delay\"\nwindow = \"0s\"", "draft_approval.window", "longer than zero"},
		{"a bad duration", "mode = \"delay\"\nwindow = \"soon\"", "draft_approval.window", "not a duration"},
		{"an unknown key", "mode = \"send\"\nafter = \"5m\"", "draft_approval.after", "unknown key"},
		{"a rule with no when", "mode = \"wait\"\n[[draft_approval.rule]]\nmode = \"send\"", "draft_approval.rule[0].when", "is required"},
		{"a rule with no mode", "mode = \"wait\"\n[[draft_approval.rule]]\nwhen = 'draft.delivery == \"message\"'", "draft_approval.rule[0].mode", "is required"},
		{"a rule's window without mode delay", "mode = \"wait\"\n[[draft_approval.rule]]\nwhen = 'draft.delivery == \"message\"'\nmode = \"send\"\nwindow = \"5m\"", "draft_approval.rule[0].window", "only with mode delay"},
		{"a rule's bad predicate", "mode = \"wait\"\n[[draft_approval.rule]]\nwhen = 'draft.delivery =='\nmode = \"send\"", "draft_approval.rule[0].when", ""},
		{"a rule reading a withdraw fact", "mode = \"wait\"\n[[draft_approval.rule]]\nwhen = 'draft.items_owed > 0'\nmode = \"send\"", "draft_approval.rule[0].when", "draft.items_owed"},
		{"a rule reading a brief", "mode = \"wait\"\n[[draft_approval.rule]]\nwhen = 'brief.risk == \"low\"'\nmode = \"send\"", "draft_approval.rule[0].when", "brief.risk"},
		{"a rule reading marks", "mode = \"wait\"\n[[draft_approval.rule]]\nwhen = 'handled(\"x\", draft.delivery)'\nmode = \"send\"", "draft_approval.rule[0].when", "marks"},
	} {
		block := "\n[draft_approval]\n" + c.block + "\n"
		for loader, load := range map[string]func(*testing.T, string) (*Loaded, Problems){"intent": loadIntentWith, "workflow": loadWorkflowWith} {
			t.Run(loader+"/"+c.name, func(t *testing.T) {
				l, problems := load(t, block)
				if len(l.Workflows) > 0 || l.Registry != nil && func() bool { _, ok := l.Registry.Get("ask-author"); return ok }() {
					t.Errorf("the manifest loaded")
				}
				for _, p := range problems {
					field := p.Field == c.field || loader == "intent" && p.Field == strings.Replace(c.field, "draft_approval", "drafts.approval", 1)
					if field && strings.Contains(p.Msg, c.msg) && strings.HasSuffix(p.File, "toml") {
						return
					}
				}
				t.Fatalf("want %s: %s, got\n%s", c.field, c.msg, problems.Error())
			})
		}
	}
}

func TestManifestDraftApprovalRules(t *testing.T) {
	rules := `
[draft_approval]
mode = "wait"
[[draft_approval.rule]]
when = 'draft.delivery == "comment" && pr.author == "dependabot[bot]"'
mode = "send"
[[draft_approval.rule]]
when = 'pr.repo == "acme/docs"'
mode = "delay"
window = "10m"
`
	l, problems := loadIntentWith(t, rules)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	p := d.DraftApproval
	if p == nil || p.Default.Mode != draft.Wait || len(p.Rules) != 2 {
		t.Fatalf("%+v", p)
	}
	if r := p.Rules[0]; !r.NamesComment || r.Approval.Mode != draft.Send {
		t.Errorf("rule 0: %+v", r)
	}
	if r := p.Rules[1]; r.NamesComment || r.Approval != (draft.Approval{Mode: draft.Delay, Window: 10 * time.Minute}) {
		t.Errorf("rule 1: %+v", r)
	}
	comment := draft.Draft{Delivery: draft.CommentDelivery, Origin: draft.Origin{Kind: draft.OriginTurn, Intent: "ask-author"}}
	for name, c := range map[string]struct {
		d     draft.Draft
		facts map[string]any
		want  draft.ApprovalMode
	}{
		"a bot's pull request":            {comment, map[string]any{"pr.author": "dependabot[bot]"}, draft.Send},
		"a comment on the docs":           {comment, map[string]any{"pr.repo": "acme/docs"}, draft.Wait},
		"a message on the docs":           {draft.Draft{Delivery: draft.Message}, map[string]any{"pr.repo": "acme/docs"}, draft.Delay},
		"a comment on another repository": {comment, map[string]any{"pr.repo": "acme/widgets"}, draft.Wait},
	} {
		got, err := p.Decide(c.d, draft.Subject{Slot: "pr", Facts: c.facts}, time.Time{})
		if err != nil || got.Approval.Mode != c.want {
			t.Errorf("%s: %+v %v, want %s", name, got, err, c.want)
		}
	}
}

func TestAnIntentsRuleMayNotReadThePurposeItsAgentNames(t *testing.T) {
	rule := "\n[draft_approval]\nmode = \"wait\"\n[[draft_approval.rule]]\nwhen = 'draft.purpose == \"thanks\"'\nmode = \"send\"\n"
	_, problems := loadIntentWith(t, rule)
	if len(problems) != 1 || problems[0].Field != "draft_approval.rule[0].when" || !strings.Contains(problems[0].Msg, "agents name") ||
		!strings.HasSuffix(problems[0].File, "intent.toml") {
		t.Fatalf("%s", problems.Error())
	}
	l, problems := loadWorkflowWith(t, rule)
	if len(problems) > 0 {
		t.Fatalf("a workflow's rule: %s", problems.Error())
	}
	if p := l.Workflows[0].DraftApproval; p == nil || len(p.Rules) != 1 {
		t.Fatalf("%+v", p)
	}
}

func TestAutoSendIsAnUnknownKey(t *testing.T) {
	for _, key := range []string{"auto_send = false\n", "auto_send = true\n"} {
		for loader, write := range map[string]func(root string){
			"intent":   func(root string) { writeIntent(t, root, "ask-author", key+minimal, "Ask.\n") },
			"workflow": func(root string) { writeWorkflow(t, root, "close-quiet", key+quietSession) },
		} {
			root := t.TempDir()
			write(root)
			_, problems := Load(userLayer(root), nil, testBase(), nil)
			if len(problems) == 0 || problems[0].Field != "auto_send" || !strings.Contains(problems[0].Msg, "unknown key") {
				t.Errorf("%s %s: %s", loader, strings.TrimSpace(key), problems.Error())
			}
		}
	}
}

func TestARuleReadingAnUntrustedFactIsRefused(t *testing.T) {
	for loader, c := range map[string]struct {
		load       func(*testing.T, string) (*Loaded, Problems)
		when, fact string
	}{
		"intent":   {loadIntentWith, `pr.waits_on_user && draft.delivery == "comment"`, "pr.waits_on_user"},
		"workflow": {loadWorkflowWith, `session.agent_state == "idle"`, "session.agent_state"},
	} {
		_, problems := c.load(t, "\n[draft_approval]\nmode = \"wait\"\n[[draft_approval.rule]]\nwhen = '"+c.when+"'\nmode = \"send\"\n")
		if len(problems) != 1 || problems[0].Field != "draft_approval.rule[0].when" || !strings.Contains(problems[0].Msg, c.fact) ||
			!strings.Contains(problems[0].Msg, "no draft approval rule may") || !strings.HasSuffix(problems[0].File, ".toml") {
			t.Errorf("%s: %s", loader, problems.Error())
		}
	}
}

func TestAnIntentsRuleMayNotReadTheEffectsItsAgentNames(t *testing.T) {
	rule := "\n[draft_approval]\nmode = \"wait\"\n[[draft_approval.rule]]\nwhen = '\"pr.nudged\" in draft.effects && draft.delivery == \"comment\"'\nmode = \"send\"\n"
	_, problems := loadIntentWith(t, rule)
	if len(problems) != 1 || !strings.Contains(problems[0].Msg, "draft.effects") || !strings.Contains(problems[0].Msg, "agents name") {
		t.Fatalf("%s", problems.Error())
	}
}

func TestAReleasingRuleThatNeverNamesCommentLoadsWithAWarning(t *testing.T) {
	for when, warns := range map[string]bool{
		`draft.delivery in ["comment"] && pr.author == "dependabot[bot]"`: true,
		`draft.delivery == "comment" && pr.author == "dependabot[bot]"`:   false,
		`draft.delivery == "message"`:                                     false,
		`pr.author == "dependabot[bot]"`:                                  false,
	} {
		l, problems := loadIntentWith(t, "\n[draft_approval]\nmode = \"wait\"\n[[draft_approval.rule]]\nwhen = '"+when+"'\nmode = \"send\"\n")
		if len(problems) > 0 {
			t.Fatalf("%s: %s", when, problems.Error())
		}
		got := len(l.Warnings) == 1 && l.Warnings[0].Field == "draft_approval.rule[0].when" && strings.Contains(l.Warnings[0].Msg, `draft.delivery == "comment"`)
		if got != warns || !warns && len(l.Warnings) != 0 {
			t.Errorf("%s: warnings %+v", when, l.Warnings)
		}
	}
}
