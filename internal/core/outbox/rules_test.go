package outbox

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// factIs holds when the fact name is value.
type factIs struct {
	name  string
	value any
}

func (f factIs) Holds(facts map[string]any, _ time.Time) (bool, error) {
	return facts[f.name] == f.value, nil
}

// commentRule is a rule whose when names comment delivery, as the loader
// marks one that tests draft.delivery == "comment".
func commentRule(when draft.Condition, a draft.Approval) draft.Rule {
	return draft.Rule{When: when, Approval: a, NamesComment: true, Subjects: []string{"pr"}}
}

var (
	slackSubject = []string{"slack"}
	noneA        = draft.Approval{Mode: draft.None}
	window5      = draft.Approval{Mode: draft.Window, Window: 5 * time.Minute}
	bot          = factIs{"pr.author", "dependabot[bot]"}
)

func botFacts(context.Context, unit.Unit, string) (draft.Subject, error) {
	return draft.Subject{Slot: "pr", Facts: map[string]any{"pr.author": "dependabot[bot]", "pr.repo": "acme/docs"}}, nil
}

// subjectOf reads facts as the subject of slot.
func subjectOf(slot string, facts map[string]any) Facts {
	return func(context.Context, unit.Unit, string) (draft.Subject, error) {
		return draft.Subject{Slot: slot, Facts: facts}, nil
	}
}

func comment(text string) Proposal {
	return Proposal{Unit: pull, Purpose: "review", Delivery: draft.CommentDelivery, Text: text,
		Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review"}}
}

func TestACommentDraftUnderACatchAllNoneWaits(t *testing.T) {
	e := newEnv()
	withPolicy(e, draft.Fixed(noneA))
	d := e.propose(comment("looks good"))
	if d.State != draft.Proposed || d.Approval.Mode != draft.Always || d.Would.Policy != draft.WouldHold {
		t.Fatalf("%+v", d)
	}
	e.svc.Send(ctx)
	if len(e.fx.published) != 0 {
		t.Fatalf("published %v", e.fx.published)
	}
}

func TestARuleThatNamesCommentDeliveryReleasesAComment(t *testing.T) {
	t.Run("none sends at once", func(t *testing.T) {
		e := newEnv()
		e.svc.Facts = botFacts
		withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Always}, Rules: []draft.Rule{commentRule(bot, noneA)}})
		d := e.propose(comment("looks good"))
		if d.State != draft.Approved || d.Approval != noneA || d.Would.Policy != draft.WouldSend {
			t.Fatalf("%+v", d)
		}
		if sent, _, err := e.svc.Send(ctx); sent != 1 || err != nil {
			t.Fatalf("sent %d %v", sent, err)
		}
	})
	t.Run("window sets the deadline", func(t *testing.T) {
		e := newEnv()
		e.svc.Facts = botFacts
		withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Always}, Rules: []draft.Rule{commentRule(bot, window5)}})
		d := e.propose(comment("looks good"))
		if d.State != draft.Proposed || d.Approval != window5 || !d.Deadline.Equal(t0.Add(5*time.Minute)) {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("a rule that does not name it is skipped", func(t *testing.T) {
		e := newEnv()
		e.svc.Facts = botFacts
		withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Always}, Rules: []draft.Rule{{When: bot, Approval: noneA, Subjects: []string{"pr"}}}})
		if d := e.propose(comment("looks good")); d.State != draft.Proposed || d.Approval.Mode != draft.Always {
			t.Fatalf("%+v", d)
		}
	})
}

func TestRulesPickTheModeByTheUnitsFacts(t *testing.T) {
	pol := &draft.Policy{Default: draft.Approval{Mode: draft.Always}, Rules: []draft.Rule{
		{When: factIs{"slack.conversation", "dm"}, Approval: noneA, Subjects: slackSubject},
		{When: factIs{"slack.channel", "C0TEAM"}, Approval: window5, Subjects: slackSubject},
		{When: factIs{"slack.channel", "C0TEAM"}, Approval: noneA, Subjects: slackSubject},
	}}
	for name, c := range map[string]struct {
		facts map[string]any
		want  draft.Approval
	}{
		"the first rule":         {map[string]any{"slack.conversation": "dm", "slack.channel": "C0TEAM"}, noneA},
		"the first that matches": {map[string]any{"slack.conversation": "channel", "slack.channel": "C0TEAM"}, window5},
		"else the mode":          {map[string]any{"slack.conversation": "channel"}, draft.Approval{Mode: draft.Always}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv()
			e.svc.Facts = subjectOf("slack", c.facts)
			withPolicy(e, pol)
			if d := e.propose(reply("x")); d.Approval != c.want {
				t.Fatalf("%+v, want %+v", d.Approval, c.want)
			}
		})
	}
	t.Run("a proposal's own facts are read instead of the unit's", func(t *testing.T) {
		e := newEnv()
		e.svc.Facts = func(context.Context, unit.Unit, string) (draft.Subject, error) {
			t.Fatal("read the unit's facts")
			return draft.Subject{}, nil
		}
		p := reply("x")
		p.Policy, p.Subject = pol, &draft.Subject{Slot: "slack", Facts: map[string]any{"slack.conversation": "dm"}}
		if d := e.propose(p); d.Approval != noneA {
			t.Fatalf("%+v", d.Approval)
		}
	})
	t.Run("facts that cannot be read hold the draft", func(t *testing.T) {
		e := newEnv()
		e.svc.Facts = func(context.Context, unit.Unit, string) (draft.Subject, error) { return draft.Subject{}, errBoom }
		withPolicy(e, &draft.Policy{Default: noneA, Rules: pol.Rules})
		if d := e.propose(reply("x")); d.Approval.Mode != draft.Always {
			t.Fatalf("%+v", d.Approval)
		}
		if !slices.ContainsFunc(e.logs, func(l string) bool { return strings.Contains(l, "always") && strings.Contains(l, "boom") }) {
			t.Fatalf("logs %q", e.logs)
		}
	})
}

func TestAnAgentsPurposeNeverMatchesAPurposeRule(t *testing.T) {
	pol := &draft.Policy{Default: draft.Approval{Mode: draft.Always}, Rules: []draft.Rule{{When: factIs{"draft.purpose", "thanks"}, Approval: noneA}}}
	for _, kind := range []draft.OriginKind{draft.OriginTurn, draft.OriginSession} {
		e := newEnv()
		withPolicy(e, pol)
		p := reply("thanks!")
		p.Kind, p.Purpose, p.Delivery, p.Origin.Kind = "", "thanks", draft.Message, kind
		if d := e.propose(p); d.State != draft.Proposed || d.Approval.Mode != draft.Always {
			t.Fatalf("a %s draft labeled thanks: %+v", kind, d)
		}
	}
	e := newEnv()
	p := Proposal{Unit: thread, Purpose: "thanks", Text: "thanks!", Policy: pol,
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "slack-thanks", Rule: "slack-thanks", Key: "k"}}
	if d := e.propose(p); d.State != draft.Approved {
		t.Fatalf("a workflow's draft: %+v", d)
	}
}

func TestARuleNeverReleasesADraftAimedOutsideItsUnit(t *testing.T) {
	e := newEnv()
	withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Always}, Rules: []draft.Rule{{When: factIs{"draft.delivery", "message"}, Approval: noneA}}})
	p := reply("x")
	p.Target = "#general"
	if d := e.propose(p); d.State != draft.Proposed || d.Approval.Mode != draft.Always || d.Would.Reason != "targets outside its unit" {
		t.Fatalf("%+v", d)
	}
}

func TestARuleThatObservesRecordsAComment(t *testing.T) {
	e := newEnv()
	withPolicy(e, &draft.Policy{Default: noneA, Rules: []draft.Rule{{When: factIs{"draft.delivery", "comment"}, Approval: draft.Approval{Mode: draft.Observe}}}})
	if d := e.propose(comment("x")); d.State != draft.Discarded || d.Reason != string(draft.Observe) {
		t.Fatalf("%+v", d)
	}
}
