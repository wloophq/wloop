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
	l, problems := loadIntentWith(t, "\n[draft_approval]\nmode = \"window\"\nwindow = \"10m\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	if d.DraftApproval == nil || d.DraftApproval.Default != (draft.Approval{Mode: draft.Window, Window: 10 * time.Minute}) || len(d.DraftApproval.Rules) != 0 {
		t.Fatalf("intent: %+v", d.DraftApproval)
	}

	l, problems = loadWorkflowWith(t, "\n[draft_approval]\nmode = \"none\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if w := l.Workflows[0]; w.DraftApproval == nil || w.DraftApproval.Default != (draft.Approval{Mode: draft.None}) {
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
		{"window without a window", "mode = \"window\"", "draft_approval.window", "is required with mode window"},
		{"a window on another mode", "mode = \"none\"\nwindow = \"5m\"", "draft_approval.window", "only with mode window"},
		{"a zero window", "mode = \"window\"\nwindow = \"0s\"", "draft_approval.window", "longer than zero"},
		{"a bad duration", "mode = \"window\"\nwindow = \"soon\"", "draft_approval.window", "not a duration"},
		{"an unknown key", "mode = \"none\"\nafter = \"5m\"", "draft_approval.after", "unknown key"},
		{"a rule with no when", "mode = \"always\"\n[[draft_approval.rule]]\nmode = \"none\"", "draft_approval.rule[0].when", "is required"},
		{"a rule with no mode", "mode = \"always\"\n[[draft_approval.rule]]\nwhen = 'draft.delivery == \"message\"'", "draft_approval.rule[0].mode", "is required"},
		{"a rule's window without mode window", "mode = \"always\"\n[[draft_approval.rule]]\nwhen = 'draft.delivery == \"message\"'\nmode = \"none\"\nwindow = \"5m\"", "draft_approval.rule[0].window", "only with mode window"},
		{"a rule's bad predicate", "mode = \"always\"\n[[draft_approval.rule]]\nwhen = 'draft.delivery =='\nmode = \"none\"", "draft_approval.rule[0].when", ""},
		{"a rule reading a withdraw fact", "mode = \"always\"\n[[draft_approval.rule]]\nwhen = 'draft.items_held > 0'\nmode = \"none\"", "draft_approval.rule[0].when", "draft.items_held"},
		{"a rule reading a brief", "mode = \"always\"\n[[draft_approval.rule]]\nwhen = 'brief.risk == \"low\"'\nmode = \"none\"", "draft_approval.rule[0].when", "brief.risk"},
		{"a rule reading marks", "mode = \"always\"\n[[draft_approval.rule]]\nwhen = 'handled(\"x\", draft.delivery)'\nmode = \"none\"", "draft_approval.rule[0].when", "marks"},
	} {
		block := "\n[draft_approval]\n" + c.block + "\n"
		for loader, load := range map[string]func(*testing.T, string) (*Loaded, Problems){"intent": loadIntentWith, "workflow": loadWorkflowWith} {
			t.Run(loader+"/"+c.name, func(t *testing.T) {
				l, problems := load(t, block)
				if len(l.Workflows) > 0 || l.Registry != nil && func() bool { _, ok := l.Registry.Get("ask-author"); return ok }() {
					t.Errorf("the manifest loaded")
				}
				for _, p := range problems {
					if p.Field == c.field && strings.Contains(p.Msg, c.msg) && strings.HasSuffix(p.File, "toml") {
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
mode = "always"
[[draft_approval.rule]]
when = 'draft.delivery == "comment" && pr.author == "dependabot[bot]"'
mode = "none"
[[draft_approval.rule]]
when = 'pr.repo == "acme/docs"'
mode = "window"
window = "10m"
`
	l, problems := loadIntentWith(t, rules)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	p := d.DraftApproval
	if p == nil || p.Default.Mode != draft.Always || len(p.Rules) != 2 {
		t.Fatalf("%+v", p)
	}
	if r := p.Rules[0]; !r.NamesComment || r.Approval.Mode != draft.None {
		t.Errorf("rule 0: %+v", r)
	}
	if r := p.Rules[1]; r.NamesComment || r.Approval != (draft.Approval{Mode: draft.Window, Window: 10 * time.Minute}) {
		t.Errorf("rule 1: %+v", r)
	}
	comment := draft.Draft{Delivery: draft.CommentDelivery, Origin: draft.Origin{Kind: draft.OriginTurn, Intent: "ask-author"}}
	for name, c := range map[string]struct {
		d     draft.Draft
		facts map[string]any
		want  draft.ApprovalMode
	}{
		"a bot's pull request":            {comment, map[string]any{"pr.author": "dependabot[bot]"}, draft.None},
		"a comment on the docs":           {comment, map[string]any{"pr.repo": "acme/docs"}, draft.Always},
		"a message on the docs":           {draft.Draft{Delivery: draft.Message}, map[string]any{"pr.repo": "acme/docs"}, draft.Window},
		"a comment on another repository": {comment, map[string]any{"pr.repo": "acme/widgets"}, draft.Always},
	} {
		got, err := p.Decide(c.d, draft.Subject{Slot: "pr", Facts: c.facts}, time.Time{})
		if err != nil || got.Approval.Mode != c.want {
			t.Errorf("%s: %+v %v, want %s", name, got, err, c.want)
		}
	}
}

func TestAnIntentsRuleMayNotReadThePurposeItsAgentNames(t *testing.T) {
	rule := "\n[draft_approval]\nmode = \"always\"\n[[draft_approval.rule]]\nwhen = 'draft.purpose == \"thanks\"'\nmode = \"none\"\n"
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
		_, problems := c.load(t, "\n[draft_approval]\nmode = \"always\"\n[[draft_approval.rule]]\nwhen = '"+c.when+"'\nmode = \"none\"\n")
		if len(problems) != 1 || problems[0].Field != "draft_approval.rule[0].when" || !strings.Contains(problems[0].Msg, c.fact) ||
			!strings.Contains(problems[0].Msg, "no draft approval rule may") || !strings.HasSuffix(problems[0].File, ".toml") {
			t.Errorf("%s: %s", loader, problems.Error())
		}
	}
}

func TestAnIntentsRuleMayNotReadTheEffectsItsAgentNames(t *testing.T) {
	rule := "\n[draft_approval]\nmode = \"always\"\n[[draft_approval.rule]]\nwhen = '\"pr.nudged\" in draft.effects && draft.delivery == \"comment\"'\nmode = \"none\"\n"
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
		l, problems := loadIntentWith(t, "\n[draft_approval]\nmode = \"always\"\n[[draft_approval.rule]]\nwhen = '"+when+"'\nmode = \"none\"\n")
		if len(problems) > 0 {
			t.Fatalf("%s: %s", when, problems.Error())
		}
		got := len(l.Warnings) == 1 && l.Warnings[0].Field == "draft_approval.rule[0].when" && strings.Contains(l.Warnings[0].Msg, `draft.delivery == "comment"`)
		if got != warns || !warns && len(l.Warnings) != 0 {
			t.Errorf("%s: warnings %+v", when, l.Warnings)
		}
	}
}
