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

// The draft approval table of each manifest.
const (
	intentDrafts   = "drafts.approval"
	workflowDrafts = "drafts.approval"
)

// drafts is block written at the draft approval table at, its rules
// "[[rule]]" written at at.rule.
func drafts(at, block string) string {
	return "\n[" + at + "]\n" + strings.ReplaceAll(block, "[[rule]]", "[["+at+".rule]]") + "\n"
}

func TestManifestDraftApproval(t *testing.T) {
	l, problems := loadIntentWith(t, drafts(intentDrafts, "mode = \"delay\"\nwindow = \"10m\""))
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	if d.DraftApproval == nil || d.DraftApproval.Default != (draft.Approval{Mode: draft.Delay, Window: 10 * time.Minute}) || len(d.DraftApproval.Rules) != 0 {
		t.Fatalf("intent: %+v", d.DraftApproval)
	}

	l, problems = loadWorkflowWith(t, drafts(workflowDrafts, "mode = \"send\""))
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
		{"unknown mode", "mode = \"later\"", "@.mode", "not one of"},
		{"no mode", "window = \"5m\"", "@.mode", "is required"},
		{"window without a window", "mode = \"delay\"", "@.window", "is required with mode delay"},
		{"a window on another mode", "mode = \"send\"\nwindow = \"5m\"", "@.window", "only with mode delay"},
		{"a zero window", "mode = \"delay\"\nwindow = \"0s\"", "@.window", "longer than zero"},
		{"a bad duration", "mode = \"delay\"\nwindow = \"soon\"", "@.window", "not a duration"},
		{"an unknown key", "mode = \"send\"\nafter = \"5m\"", "@.after", "unknown key"},
		{"a rule with no when", "mode = \"wait\"\n[[rule]]\nmode = \"send\"", "@.rule[0].when", "is required"},
		{"a rule with no mode", "mode = \"wait\"\n[[rule]]\nwhen = 'draft.delivery == \"message\"'", "@.rule[0].mode", "is required"},
		{"a rule's window without mode delay", "mode = \"wait\"\n[[rule]]\nwhen = 'draft.delivery == \"message\"'\nmode = \"send\"\nwindow = \"5m\"", "@.rule[0].window", "only with mode delay"},
		{"a rule's bad predicate", "mode = \"wait\"\n[[rule]]\nwhen = 'draft.delivery =='\nmode = \"send\"", "@.rule[0].when", ""},
		{"a rule reading a withdraw fact", "mode = \"wait\"\n[[rule]]\nwhen = 'draft.items_owed > 0'\nmode = \"send\"", "@.rule[0].when", "draft.items_owed"},
		{"a rule reading a brief", "mode = \"wait\"\n[[rule]]\nwhen = 'brief.risk == \"low\"'\nmode = \"send\"", "@.rule[0].when", "brief.risk"},
		{"a rule reading marks", "mode = \"wait\"\n[[rule]]\nwhen = 'handled(\"x\", draft.delivery)'\nmode = \"send\"", "@.rule[0].when", "marks"},
	} {
		for loader, m := range map[string]struct {
			load func(*testing.T, string) (*Loaded, Problems)
			at   string
		}{"intent": {loadIntentWith, intentDrafts}, "workflow": {loadWorkflowWith, workflowDrafts}} {
			t.Run(loader+"/"+c.name, func(t *testing.T) {
				l, problems := m.load(t, drafts(m.at, c.block))
				field := strings.Replace(c.field, "@", m.at, 1)
				if len(l.Workflows) > 0 || l.Registry != nil && func() bool { _, ok := l.Registry.Get("ask-author"); return ok }() {
					t.Errorf("the manifest loaded")
				}
				for _, p := range problems {
					if p.Field == field && strings.Contains(p.Msg, c.msg) && strings.HasSuffix(p.File, "toml") {
						return
					}
				}
				t.Fatalf("want %s: %s, got\n%s", field, c.msg, problems.Error())
			})
		}
	}
}

func TestManifestDraftApprovalRules(t *testing.T) {
	rules := drafts(intentDrafts, `mode = "wait"
[[rule]]
when = 'draft.delivery == "comment" && pr.author == "dependabot[bot]"'
mode = "send"
[[rule]]
when = 'pr.repo == "acme/docs"'
mode = "delay"
window = "10m"`)
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
	rule := "mode = \"wait\"\n[[rule]]\nwhen = 'draft.purpose == \"thanks\"'\nmode = \"send\""
	_, problems := loadIntentWith(t, drafts(intentDrafts, rule))
	if len(problems) != 1 || problems[0].Field != intentDrafts+".rule[0].when" || !strings.Contains(problems[0].Msg, "agents name") ||
		!strings.HasSuffix(problems[0].File, "intent.toml") {
		t.Fatalf("%s", problems.Error())
	}
	l, problems := loadWorkflowWith(t, drafts(workflowDrafts, rule))
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
		load           func(*testing.T, string) (*Loaded, Problems)
		at, when, fact string
	}{
		"intent":   {loadIntentWith, intentDrafts, `pr.waits_on_user && draft.delivery == "comment"`, "pr.waits_on_user"},
		"workflow": {loadWorkflowWith, workflowDrafts, `session.agent_state == "idle"`, "session.agent_state"},
	} {
		_, problems := c.load(t, drafts(c.at, "mode = \"wait\"\n[[rule]]\nwhen = '"+c.when+"'\nmode = \"send\""))
		if len(problems) != 1 || problems[0].Field != c.at+".rule[0].when" || !strings.Contains(problems[0].Msg, c.fact) ||
			!strings.Contains(problems[0].Msg, "no draft approval rule may") || !strings.HasSuffix(problems[0].File, ".toml") {
			t.Errorf("%s: %s", loader, problems.Error())
		}
	}
}

func TestAnIntentsRuleMayNotReadTheEffectsItsAgentNames(t *testing.T) {
	rule := "mode = \"wait\"\n[[rule]]\nwhen = '\"pr.nudged\" in draft.effects && draft.delivery == \"comment\"'\nmode = \"send\""
	_, problems := loadIntentWith(t, drafts(intentDrafts, rule))
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
		l, problems := loadIntentWith(t, drafts(intentDrafts, "mode = \"wait\"\n[[rule]]\nwhen = '"+when+"'\nmode = \"send\""))
		if len(problems) > 0 {
			t.Fatalf("%s: %s", when, problems.Error())
		}
		got := len(l.Warnings) == 1 && l.Warnings[0].Field == intentDrafts+".rule[0].when" && strings.Contains(l.Warnings[0].Msg, `draft.delivery == "comment"`)
		if got != warns || !warns && len(l.Warnings) != 0 {
			t.Errorf("%s: warnings %+v", when, l.Warnings)
		}
	}
}
