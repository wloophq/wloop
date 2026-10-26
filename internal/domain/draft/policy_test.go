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
	none   = Approval{Mode: None}
	always = Approval{Mode: Always}
	window = Approval{Mode: Window, Window: 10 * time.Minute}
)

func TestADefaultThatReleasesNeverReleasesAComment(t *testing.T) {
	for _, def := range []Approval{none, window} {
		p := Policy{Default: def}
		got, err := p.Decide(Draft{Delivery: CommentDelivery}, nil, time.Time{})
		if err != nil || got.Approval != always || got.CommentReleased {
			t.Errorf("%s: %+v %v", def.Mode, got, err)
		}
		if got, _ := p.Decide(Draft{Delivery: Message}, nil, time.Time{}); got.Approval != def {
			t.Errorf("%s on a message: %+v", def.Mode, got)
		}
	}
	if got, _ := (Policy{Default: Approval{Mode: Observe}}).Decide(Draft{Delivery: CommentDelivery}, nil, time.Time{}); got.Approval.Mode != Observe {
		t.Errorf("observe on a comment: %+v", got)
	}
}

func TestARuleReleasesACommentOnlyWhenItNamesCommentDelivery(t *testing.T) {
	bot := factIs{"pr.author", "dependabot"}
	facts := map[string]any{"pr.author": "dependabot"}
	comment := Draft{Delivery: CommentDelivery}
	unnamed := Policy{Default: always, Rules: []Rule{{When: bot, Approval: none}}}
	if got, _ := unnamed.Decide(comment, facts, time.Time{}); got.Approval != always || got.Rule != -1 {
		t.Errorf("a rule that does not name comment delivery: %+v", got)
	}
	named := Policy{Default: always, Rules: []Rule{{When: bot, Approval: window, NamesComment: true}}}
	if got, _ := named.Decide(comment, facts, time.Time{}); got.Approval != window || got.Rule != 0 || !got.CommentReleased {
		t.Errorf("a rule that names comment delivery: %+v", got)
	}
	held := Policy{Default: none, Rules: []Rule{{When: bot, Approval: always}}}
	if got, _ := held.Decide(comment, facts, time.Time{}); got.Approval != always || got.Rule != 0 || got.CommentReleased {
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
		if got, err := p.Decide(msg, c.facts, time.Time{}); err != nil || got.Approval != c.want || got.Rule != c.rule {
			t.Errorf("%v: %+v %v, want %s by %d", c.facts, got, err, c.want.Mode, c.rule)
		}
	}
}

func TestARuleThatFailsHoldsTheDraft(t *testing.T) {
	p := Policy{Default: none, Rules: []Rule{{When: failing{}, Approval: none}}}
	if got, err := p.Decide(Draft{Delivery: Message}, nil, time.Time{}); err == nil || got.Approval != always {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAPurposeIsAFactOnlyWhenTheLoopNamedIt(t *testing.T) {
	release := Policy{Default: always, Rules: []Rule{{When: factIs{"draft.purpose", "thanks"}, Approval: none}}}
	for _, o := range []Origin{{Kind: OriginTurn, Intent: "reply"}, {Kind: OriginSession}, {Kind: OriginUser}} {
		d := Draft{Delivery: Message, Purpose: "thanks", Origin: o}
		if got, _ := release.Decide(d, nil, time.Time{}); got.Approval != always {
			t.Errorf("a %s draft labeled thanks: %+v", o.Kind, got)
		}
	}
	d := Draft{Delivery: Message, Purpose: "thanks", Origin: Origin{Kind: OriginAuto, Workflow: "slack-thanks"}}
	if got, _ := release.Decide(d, nil, time.Time{}); got.Approval != none {
		t.Errorf("a workflow's draft: %+v", got)
	}
}

func TestTheUnitsFactsNeverOverrideTheDraftsOwn(t *testing.T) {
	p := Policy{Default: always, Rules: []Rule{{When: factIs{"draft.delivery", "message"}, Approval: none, NamesComment: false}}}
	if got, _ := p.Decide(Draft{Delivery: Reaction}, map[string]any{"draft.delivery": "message"}, time.Time{}); got.Approval != always {
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
