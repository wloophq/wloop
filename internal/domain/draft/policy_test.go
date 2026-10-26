package draft

import (
	"errors"
	"testing"
	"time"
)

// factIs holds when facts[name] is value.
type factIs struct {
	name  string
	value any
}

func (f factIs) Holds(facts map[string]any, _ time.Time) (bool, error) {
	return facts[f.name] == f.value, nil
}

type failing struct{}

func (failing) Holds(map[string]any, time.Time) (bool, error) { return false, errors.New("boom") }

var (
	none   = Approval{Mode: Send}
	always = Approval{Mode: Wait}
	window = Approval{Mode: Delay, Window: 10 * time.Minute}
)

func TestADefaultThatReleasesNeverReleasesAComment(t *testing.T) {
	for _, def := range []Approval{none, window} {
		p := Policy{Default: def}
		got, err := p.Decide(Draft{Delivery: CommentDelivery}, Subject{Facts: nil}, time.Time{})
		if err != nil || got.Approval != always || got.CommentReleased {
			t.Errorf("%s: %+v %v", def.Mode, got, err)
		}
		if got, _ := p.Decide(Draft{Delivery: Message}, Subject{Facts: nil}, time.Time{}); got.Approval != def {
			t.Errorf("%s on a message: %+v", def.Mode, got)
		}
	}
	if got, _ := (Policy{Default: Approval{Mode: DryRun}}).Decide(Draft{Delivery: CommentDelivery}, Subject{Facts: nil}, time.Time{}); got.Approval.Mode != DryRun {
		t.Errorf("observe on a comment: %+v", got)
	}
}

func TestARuleReleasesACommentOnlyWhenItNamesCommentDelivery(t *testing.T) {
	bot := factIs{"pr.author", "dependabot"}
	facts := map[string]any{"pr.author": "dependabot"}
	comment := Draft{Delivery: CommentDelivery}
	unnamed := Policy{Default: always, Rules: []Rule{{When: bot, Approval: none}}}
	if got, _ := unnamed.Decide(comment, Subject{Facts: facts}, time.Time{}); got.Approval != always || got.Rule != -1 {
		t.Errorf("a rule that does not name comment delivery: %+v", got)
	}
	named := Policy{Default: always, Rules: []Rule{{When: bot, Approval: window, NamesComment: true}}}
	if got, _ := named.Decide(comment, Subject{Facts: facts}, time.Time{}); got.Approval != window || got.Rule != 0 || !got.CommentReleased {
		t.Errorf("a rule that names comment delivery: %+v", got)
	}
	held := Policy{Default: none, Rules: []Rule{{When: bot, Approval: always}}}
	if got, _ := held.Decide(comment, Subject{Facts: facts}, time.Time{}); got.Approval != always || got.Rule != 0 || got.CommentReleased {
		t.Errorf("a rule that holds applies to a comment: %+v", got)
	}
}

func TestTheFirstMatchingRuleWinsElseTheDefault(t *testing.T) {
	p := Policy{Default: always, Rules: []Rule{
		{When: factIs{"slack.conversation", "dm"}, Approval: none},
		{When: factIs{"slack.started_by_me", true}, Approval: window},
		{When: factIs{"slack.started_by_me", true}, Approval: none},
	}}
	msg := Draft{Delivery: Message}
	for _, c := range []struct {
		facts map[string]any
		want  Approval
		rule  int
	}{
		{map[string]any{"slack.conversation": "dm", "slack.started_by_me": true}, none, 0},
		{map[string]any{"slack.conversation": "channel", "slack.started_by_me": true}, window, 1},
		{map[string]any{"slack.conversation": "channel"}, always, -1},
	} {
		if got, err := p.Decide(msg, Subject{Facts: c.facts}, time.Time{}); err != nil || got.Approval != c.want || got.Rule != c.rule {
			t.Errorf("%v: %+v %v, want %s by %d", c.facts, got, err, c.want.Mode, c.rule)
		}
	}
}

func TestARuleThatFailsHoldsTheDraft(t *testing.T) {
	p := Policy{Default: none, Rules: []Rule{{When: failing{}, Approval: none}}}
	if got, err := p.Decide(Draft{Delivery: Message}, Subject{Facts: nil}, time.Time{}); err == nil || got.Approval != always {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAPurposeIsAFactOnlyWhenTheLoopNamedIt(t *testing.T) {
	release := Policy{Default: always, Rules: []Rule{{When: factIs{"draft.purpose", "thanks"}, Approval: none}}}
	for _, o := range []Origin{{Kind: OriginTurn, Intent: "reply"}, {Kind: OriginSession}, {Kind: OriginUser}} {
		d := Draft{Delivery: Message, Purpose: "thanks", Origin: o}
		if got, _ := release.Decide(d, Subject{Facts: nil}, time.Time{}); got.Approval != always {
			t.Errorf("a %s draft labeled thanks: %+v", o.Kind, got)
		}
	}
	d := Draft{Delivery: Message, Purpose: "thanks", Origin: Origin{Kind: OriginAuto, Workflow: "slack-thanks"}}
	if got, _ := release.Decide(d, Subject{Facts: nil}, time.Time{}); got.Approval != none {
		t.Errorf("a workflow's draft: %+v", got)
	}
}

func TestTheUnitsFactsNeverOverrideTheDraftsOwn(t *testing.T) {
	p := Policy{Default: always, Rules: []Rule{{When: factIs{"draft.delivery", "message"}, Approval: none, NamesComment: false}}}
	if got, _ := p.Decide(Draft{Delivery: Reaction}, Subject{Facts: map[string]any{"draft.delivery": "message"}}, time.Time{}); got.Approval != always {
		t.Fatalf("%+v", got)
	}
}

func TestProposerNamesWhatProposed(t *testing.T) {
	for o, want := range map[Origin]string{
		{Kind: OriginAuto, Workflow: "slack-closure", Rule: "slack-closure"}: "workflow:slack-closure",
		{Kind: OriginAuto, Rule: "github-nudge"}:                             "rule:github-nudge",
		{Kind: OriginTurn, Intent: "review", Workflow: "auto-review"}:        "intent:review",
		{Kind: OriginSession}: "session",
		{Kind: OriginUser}:    "user",
	} {
		if got := o.Proposer(); got != want {
			t.Errorf("%+v: %q, want %q", o, got, want)
		}
	}
}

func TestARuleOverAnotherSubjectsFactsDoesNotApply(t *testing.T) {
	p := Policy{Default: always, Rules: []Rule{
		{When: notFact{"slack.conversation", "channel"}, Approval: none, Subjects: []string{"slack"}},
		{When: factIs{"draft.delivery", "message"}, Approval: window},
	}}
	pr := Subject{Slot: "pr", Facts: map[string]any{"pr.author": "someone"}}
	if got, _ := p.Decide(Draft{Delivery: Message}, pr, time.Time{}); got.Approval != window || got.Rule != 1 {
		t.Fatalf("a slack rule on a pull request: %+v", got)
	}
	dm := Subject{Slot: "slack", Facts: map[string]any{"slack.conversation": "dm"}}
	if got, _ := p.Decide(Draft{Delivery: Message}, dm, time.Time{}); got.Approval != none || got.Rule != 0 {
		t.Fatalf("on its own subject: %+v", got)
	}
	if !p.ReadsSubject() || (Policy{Rules: p.Rules[1:]}).ReadsSubject() {
		t.Error("ReadsSubject")
	}
}

func TestEffectsAreAFactOnlyWhenTheLoopNamedThem(t *testing.T) {
	for _, o := range []Origin{{Kind: OriginTurn, Intent: "nudge"}, {Kind: OriginSession}, {Kind: OriginUser}} {
		if got := (Draft{Effects: []string{"pr.nudged"}, Origin: o}).RuleFacts()["draft.effects"]; len(got.([]string)) != 0 {
			t.Errorf("a %s draft: %v", o.Kind, got)
		}
	}
	auto := Draft{Effects: []string{"pr.nudged"}, Origin: Origin{Kind: OriginAuto, Workflow: "nudges"}}
	if got := auto.RuleFacts()["draft.effects"].([]string); len(got) != 1 {
		t.Errorf("a workflow's draft: %v", got)
	}
}

// notFact holds when facts[name] is not value, a missing fact included.
type notFact struct {
	name  string
	value any
}

func (f notFact) Holds(facts map[string]any, _ time.Time) (bool, error) {
	return facts[f.name] != f.value, nil
}

func TestRuleFactsJoinNoFactsAndKeepTheCallersMap(t *testing.T) {
	d := Draft{Delivery: "reply"}
	if got := withRuleFacts(d, nil); len(got) != 4 || got["draft.delivery"] != "reply" {
		t.Fatalf("without facts: %v", got)
	}
	facts := map[string]any{"pr.draft": true}
	got := withRuleFacts(d, facts)
	if len(got) != 5 || got["pr.draft"] != true || len(facts) != 1 {
		t.Fatalf("facts %v, joined %v", facts, got)
	}
}

func TestAnEditTakesItsPolicyLikeAMessage(t *testing.T) {
	edit := Draft{Delivery: EditDelivery}
	for _, def := range []Approval{none, window, always} {
		if got, err := (Policy{Default: def}).Decide(edit, Subject{}, time.Time{}); err != nil || got.Approval != def {
			t.Errorf("%s on an edit: %+v %v", def.Mode, got, err)
		}
	}
	isEdit := factIs{"draft.delivery", "edit"}
	if got, _ := (Policy{Default: always, Rules: []Rule{{When: isEdit, Approval: none}}}).Decide(edit, Subject{}, time.Time{}); got.Approval != none || got.Rule != 0 {
		t.Errorf("a rule over draft.delivery: %+v", got)
	}
}
