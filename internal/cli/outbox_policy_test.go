package cli

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

const followUp = `summary = "Follow up a review"
provider = "github"
when = 'pr.review.action == "follow-up"'
key = ["pr.fingerprint"]

[[step]]
use = "turn.start"
intent = "review"
stage = "follow-up"

[[gates.approval.rule]]
when = 'gate.kind == "plan"'
mode = "run"
`

const askQuick = `label = "Ask quick"
summary = "Ask one quick question"
workspace = "review"
session = { strategy = "new", title = "quick" }

[headline]
resumed = "quick round on {{.pr.slug}}"
[[headline.new]]
line = "Ask {{.pr.slug}} one quick question."

[record]
states = ["done"]

[gates.approval]
mode = "send"
`

type policyBooks map[unit.Unit]dturn.Book

func (b policyBooks) Books(ctx context.Context, units ...unit.Unit) (map[unit.Unit]dturn.Book, error) {
	return b, nil
}

func TestBundledManifestsDefaultToAlways(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	l := te.loadedIntents()
	if len(l.Problems) > 0 {
		t.Fatal(l.Problems.Error())
	}
	u := pr.Ref(r7).Unit()
	if a, err := te.outboxPolicies().For(context.Background(), u, draft.Origin{Kind: draft.OriginSession}); err != nil || a.Default.Mode != draft.Wait || len(a.Rules) != 0 {
		t.Fatalf("a session draft without a turn: %+v, %v", a, err)
	}
	defs := l.Registry.All()
	if len(defs) == 0 || len(l.Workflows) == 0 {
		t.Fatalf("%d intents, %d workflows", len(defs), len(l.Workflows))
	}
	book := dturn.Book{Unit: u}
	var origins []draft.Origin
	for _, d := range defs {
		if p := d.DraftApproval; p != nil && (p.Default.Mode != draft.Wait || len(p.Rules) != 0) {
			t.Errorf("intent %s declares %+v", d.Name, *p)
		}
		seq := int64(len(book.Turns) + 1)
		book.Turns = append(book.Turns, dturn.Turn{Seq: seq, Intent: dturn.Intent(d.Name)})
		origins = append(origins, draft.Origin{Kind: draft.OriginTurn, TurnSeq: seq, Intent: string(d.Name)})
	}
	for _, w := range l.Workflows {
		if p := w.DraftApproval; p != nil && (p.Default.Mode != draft.Wait || len(p.Rules) != 0) {
			t.Errorf("workflow %s declares %+v", w.Name, *p)
		}
		seq := int64(len(book.Turns) + 1)
		book.Turns = append(book.Turns, dturn.Turn{Seq: seq, Intent: "review", Workflow: w.Name})
		origins = append(origins, draft.Origin{Kind: draft.OriginTurn, TurnSeq: seq, Intent: "review", Workflow: w.Name})
	}
	pol := outbox.Resolver{Manifests: outboxManifests{loaded: te.loadedIntents, books: policyBooks{u: book}}}
	for _, o := range origins {
		if a, err := pol.For(context.Background(), u, o); err != nil || a.Default.Mode != draft.Wait || len(a.Rules) != 0 {
			t.Errorf("%+v resolves to %+v, %v", o, a, err)
		}
	}
}

func TestWorkflowRunApprovalIsNotDraftApproval(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{
		"workflows/follow-up-run/workflow.toml":   followUp,
		"workflows/follow-up-timed/workflow.toml": followUp + "\n[gates.approval]\nmode = \"delay\"\nwindow = \"5m\"\n",
		"intents/ask-quick/intent.toml":           askQuick,
		"intents/ask-quick/instructions.md.tmpl":  "Ask.\n",
	})
	l := te.loadedIntents()
	if len(l.Problems) > 0 {
		t.Fatal(l.Problems.Error())
	}
	u := pr.Ref(r7).Unit()
	book := policyBooks{u: {Unit: u, Turns: []dturn.Turn{
		{Seq: 3, Intent: "review", Workflow: "follow-up-run"},
		{Seq: 4, Intent: "review", Workflow: "follow-up-timed"},
		{Seq: 5, Intent: "ask-quick"},
	}}}
	pol := outbox.Resolver{Manifests: outboxManifests{loaded: te.loadedIntents, books: book}}
	for _, c := range []struct {
		name string
		o    draft.Origin
		want draft.Approval
	}{
		{"a run approval is not a draft approval", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 3, Intent: "review", Workflow: "follow-up-run"}, draft.Approval{Mode: draft.Wait}},
		{"a session draft of that workflow's turn", draft.Origin{Kind: draft.OriginSession, TurnSeq: 3}, draft.Approval{Mode: draft.Wait}},
		{"a workflow's draft approval", draft.Origin{Kind: draft.OriginTurn, TurnSeq: 4, Intent: "review", Workflow: "follow-up-timed"}, draft.Approval{Mode: draft.Delay, Window: 5 * time.Minute}},
		{"a session draft reads its turn's workflow", draft.Origin{Kind: draft.OriginSession, TurnSeq: 4}, draft.Approval{Mode: draft.Delay, Window: 5 * time.Minute}},
		{"a session draft reads its turn's intent", draft.Origin{Kind: draft.OriginSession, TurnSeq: 5}, draft.Approval{Mode: draft.Send}},
	} {
		a, err := pol.For(context.Background(), u, c.o)
		if err != nil || a.Default != c.want || len(a.Rules) != 0 {
			t.Errorf("%s: %+v, %v", c.name, a, err)
		}
	}
	if _, err := pol.For(context.Background(), u, draft.Origin{Kind: draft.OriginSession, TurnSeq: 99}); err == nil {
		t.Error("an unknown turn resolved without an error")
	}
	if _, err := pol.For(context.Background(), u, draft.Origin{Kind: draft.OriginTurn, Intent: "review", Workflow: "deleted"}); err == nil {
		t.Error("an unknown workflow resolved without an error")
	}
}
