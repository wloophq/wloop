package outbox

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// commenting posts every draft that is not a reaction as a comment.
type commenting struct{ *fakeEffects }

func (commenting) Delivers(k draft.Kind) draft.Delivery {
	if k == draft.React {
		return draft.Reaction
	}
	return draft.CommentDelivery
}

func withCommentingPulls(e *env) {
	e.svc.Effects = func(p unit.ProviderID) (port.Effects, error) {
		if p == pull.Provider {
			return commenting{e.fx}, nil
		}
		return e.fx, nil
	}
}

func prReply(text string) Proposal {
	return Proposal{Unit: pull, Kind: draft.Reply, Text: text, Origin: draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review"}}
}

func TestAMessageItsPluginPostsAsACommentWaitsUnderACatchAllRelease(t *testing.T) {
	for _, mode := range []draft.Approval{noneA, window5} {
		e := newEnv()
		withCommentingPulls(e)
		withPolicy(e, draft.Fixed(mode))
		d := e.propose(prReply("LGTM, merging"))
		if d.Delivery != draft.CommentDelivery || d.Kind != draft.Comment || d.State != draft.Held ||
			e.approval(d.ID).Mode != draft.Wait {
			t.Fatalf("%s: %+v", mode.Mode, d)
		}
	}
}

func TestARuleReleasingMessagesNeverReleasesWhatItsPluginPostsAsAComment(t *testing.T) {
	e := newEnv()
	withCommentingPulls(e)
	withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{{When: factIs{"draft.delivery", "message"}, Approval: noneA}}})
	if d := e.propose(prReply("ok")); d.State != draft.Held || e.approval(d.ID).Mode != draft.Wait {
		t.Fatalf("%+v", d)
	}
	nudge := Proposal{Unit: pull, Purpose: "nudge", Text: "any news?", Policy: draft.Fixed(noneA),
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: "k"}}
	if d := e.propose(nudge); d.Delivery != draft.CommentDelivery || d.Purpose != "nudge" || d.State != draft.Held {
		t.Fatalf("a plugin rule's nudge: %+v", d)
	}
}

func TestADraftWhosePublisherChangedItsDeliveryFailsOnItsWindow(t *testing.T) {
	e := newEnv()
	nudge := Proposal{Unit: pull, Purpose: "nudge", Text: "any news?", Policy: draft.Fixed(window5),
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: "k"}}
	d := e.propose(nudge)
	if d.Delivery != draft.Message || e.approval(d.ID) != window5 {
		t.Fatalf("%+v %+v", d, e.approval(d.ID))
	}
	withCommentingPulls(e)
	ds, err := e.svc.List(ctx, Filter{})
	if err != nil || len(ds) != 1 || ds[0].Delivery != draft.CommentDelivery || ds[0].Kind != draft.Comment {
		t.Fatalf("listed %+v %v", ds, err)
	}
	if _, err := e.svc.Propose(ctx, nudge); err == nil {
		t.Fatal("the stored nudge does not hold the same one")
	}
	e.clock.now = t0.Add(time.Hour)
	if err := e.tick(); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Failed || !strings.Contains(got.Error, "comment") {
		t.Fatalf("a window released a comment its rule never named: %+v", got)
	}
	if sent, _, _ := e.svc.Send(ctx); sent != 0 {
		t.Fatalf("sent %d", sent)
	}
}

func TestARuleOverTheUnitsFactsWithNoFactsReaderWaits(t *testing.T) {
	e := newEnv()
	withPolicy(e, &draft.Policy{Default: noneA, Rules: []draft.Rule{{When: factIs{"slack.conversation", "dm"}, Approval: noneA, Subjects: slackSubject}}})
	if d := e.propose(reply("x")); e.approval(d.ID).Mode != draft.Wait {
		t.Fatalf("%+v", e.approval(d.ID))
	}
	if !slices.ContainsFunc(e.logs, func(l string) bool { return strings.Contains(l, "resolved to wait") }) {
		t.Fatalf("logs %q", e.logs)
	}
	e = newEnv()
	withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{{When: factIs{"draft.delivery", "message"}, Approval: noneA}}})
	if d := e.propose(reply("x")); e.approval(d.ID) != noneA {
		t.Fatalf("a rule over the draft's own facts: %+v", e.approval(d.ID))
	}
}

func TestARuleOverAnotherProvidersFactsFallsThrough(t *testing.T) {
	e := newEnv()
	e.svc.Facts = subjectOf("pr", map[string]any{"pr.author": "someone"})
	withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{
		{When: notFact{"slack.conversation", "channel"}, Approval: noneA, Subjects: slackSubject},
	}})
	if d := e.propose(prReply("x")); e.approval(d.ID).Mode != draft.Wait {
		t.Fatalf("%+v", e.approval(d.ID))
	}
}

func TestATurnDraftReadsTheFactsOfTheWorkflowThatStartedIt(t *testing.T) {
	var scopes []string
	e := newEnv()
	e.svc.Facts = func(_ context.Context, _ unit.Unit, workflow string) (draft.Subject, error) {
		scopes = append(scopes, workflow)
		return draft.Subject{Slot: "slack", Facts: map[string]any{"slack.conversation": "dm"}}, nil
	}
	m := manifests()
	rule := &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{{When: factIs{"slack.conversation", "dm"}, Approval: noneA, Subjects: slackSubject}}}
	m.intents["reply"], m.workflows["plain"] = rule, nil
	e.svc.Policies = Resolver{Manifests: m}
	for _, seq := range []int64{13, 4} {
		p := reply("x")
		p.Origin.TurnSeq, p.Origin.Key = seq, fmt.Sprint(seq)
		if d := e.propose(p); e.approval(d.ID) != noneA {
			t.Fatalf("turn %d: %+v", seq, e.approval(d.ID))
		}
	}
	if !slices.Equal(scopes, []string{"plain", ""}) {
		t.Fatalf("scopes %q", scopes)
	}
}

func TestAWindowDraftOfABrokenWorkflowFailsForAPersonToRetry(t *testing.T) {
	e := newEnv()
	broken := []string{"my-followups"}
	e.svc.Broken = func() []string { return broken }
	p := Proposal{Unit: thread, Purpose: "nudge", Text: "any news?", Policy: draft.Fixed(window5),
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "my-followups", Rule: "my-followups", Key: "k"}}
	d := e.propose(p)
	e.clock.now = t0.Add(time.Hour)
	if err := e.tick(); err != nil {
		t.Fatal(err)
	}
	if got := e.get(d.ID); got.State != draft.Failed || !strings.Contains(got.Error, "my-followups") {
		t.Fatalf("%+v", got)
	}
	broken = nil
	if got, err := e.svc.Retry(ctx, d.ID, false); err != nil || got.State != draft.Queued {
		t.Fatalf("once it loads, a person retries it: %+v %v", got, err)
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
