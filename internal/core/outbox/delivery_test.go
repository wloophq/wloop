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

func (commenting) Delivers(d draft.Delivery) draft.Delivery {
	if d == draft.Reaction {
		return d
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
		if d.Delivery != draft.CommentDelivery || d.Kind != draft.Comment || d.State != draft.Proposed ||
			d.Approval.Mode != draft.Wait || d.Would.Policy != draft.WouldWait {
			t.Fatalf("%s: %+v", mode.Mode, d)
		}
	}
}

func TestARuleReleasingMessagesNeverReleasesWhatItsPluginPostsAsAComment(t *testing.T) {
	e := newEnv()
	withCommentingPulls(e)
	withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{{When: factIs{"draft.delivery", "message"}, Approval: noneA}}})
	if d := e.propose(prReply("ok")); d.State != draft.Proposed || d.Approval.Mode != draft.Wait {
		t.Fatalf("%+v", d)
	}
	nudge := Proposal{Unit: pull, Purpose: "nudge", Text: "any news?", Policy: draft.Fixed(noneA),
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: "k"}}
	if d := e.propose(nudge); d.Delivery != draft.CommentDelivery || d.Purpose != "nudge" || d.State != draft.Proposed {
		t.Fatalf("a plugin rule's nudge: %+v", d)
	}
}

func TestADraftStoredWithAnotherDeliveryReadsAsItsPluginPostsIt(t *testing.T) {
	e := newEnv()
	old := draft.Draft{Unit: pull, Kind: draft.Nudge, Purpose: "nudge", Delivery: draft.Message, Text: "any news?", State: draft.Proposed,
		Approval: window5, Deadline: t0.Add(5 * time.Minute), Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: "k"}}
	if _, err := e.store.Insert(ctx, old); err != nil {
		t.Fatal(err)
	}
	withCommentingPulls(e)
	ds, err := e.svc.List(ctx, Filter{})
	if err != nil || len(ds) != 1 || ds[0].Delivery != draft.CommentDelivery || ds[0].Kind != draft.Comment {
		t.Fatalf("listed %+v %v", ds, err)
	}
	again := Proposal{Unit: pull, Purpose: "nudge", Text: "any news?", Policy: draft.Fixed(window5),
		Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge", Key: "k"}}
	if _, err := e.svc.Propose(ctx, again); err == nil {
		t.Fatal("the stored nudge does not hold the same one")
	}
	e.clock.now = t0.Add(time.Hour)
	if n, err := e.svc.Tick(ctx); n != 0 || err != nil {
		t.Fatalf("tick approved %d %v", n, err)
	}
	if _, ok, _ := e.svc.NextDeadline(ctx); ok {
		t.Fatal("its deadline still wakes the sender")
	}
}

func TestARuleOverTheUnitsFactsWithNoFactsReaderWaits(t *testing.T) {
	e := newEnv()
	withPolicy(e, &draft.Policy{Default: noneA, Rules: []draft.Rule{{When: factIs{"slack.conversation", "dm"}, Approval: noneA, Subjects: slackSubject}}})
	if d := e.propose(reply("x")); d.Approval.Mode != draft.Wait {
		t.Fatalf("%+v", d.Approval)
	}
	if !slices.ContainsFunc(e.logs, func(l string) bool { return strings.Contains(l, "resolved to wait") }) {
		t.Fatalf("logs %q", e.logs)
	}
	e = newEnv()
	withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{{When: factIs{"draft.delivery", "message"}, Approval: noneA}}})
	if d := e.propose(reply("x")); d.Approval != noneA {
		t.Fatalf("a rule over the draft's own facts: %+v", d.Approval)
	}
}

func TestARuleOverAnotherProvidersFactsFallsThrough(t *testing.T) {
	e := newEnv()
	e.svc.Facts = subjectOf("pr", map[string]any{"pr.author": "someone"})
	withPolicy(e, &draft.Policy{Default: draft.Approval{Mode: draft.Wait}, Rules: []draft.Rule{
		{When: notFact{"slack.conversation", "channel"}, Approval: noneA, Subjects: slackSubject},
	}})
	if d := e.propose(prReply("x")); d.Approval.Mode != draft.Wait {
		t.Fatalf("%+v", d.Approval)
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
		if d := e.propose(p); d.Approval != noneA {
			t.Fatalf("turn %d: %+v", seq, d.Approval)
		}
	}
	if !slices.Equal(scopes, []string{"plain", ""}) {
		t.Fatalf("scopes %q", scopes)
	}
}

func TestAWindowDraftOfABrokenWorkflowWaitsUntilItLoads(t *testing.T) {
	e := newEnv()
	broken := []string{"my-followups"}
	e.svc.Broken = func() []string { return broken }
	p := Proposal{Unit: thread, Purpose: "nudge", Text: "any news?", Policy: draft.Fixed(window5),
		Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "my-followups", Rule: "my-followups", Key: "k"}}
	d := e.propose(p)
	e.clock.now = t0.Add(time.Hour)
	if n, err := e.svc.Tick(ctx); n != 0 || err != nil {
		t.Fatalf("tick approved %d %v", n, err)
	}
	if got := e.get(d.ID); got.State != draft.Proposed {
		t.Fatalf("%+v", got)
	}
	if _, ok, _ := e.svc.NextDeadline(ctx); ok {
		t.Fatal("its deadline still wakes the sender")
	}
	broken = nil
	if n, err := e.svc.Tick(ctx); n != 1 || err != nil {
		t.Fatalf("once it loads: approved %d %v", n, err)
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
