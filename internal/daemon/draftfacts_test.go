package daemon

import (
	"cmp"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestDraftViewFacts(t *testing.T) {
	created := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		draft     draft.Draft
		publisher unit.ProviderID
		where     string
		people    []string
		want      workflow.Facts
	}{
		{
			name: "a review round's turn draft",
			draft: draft.Draft{Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#12"}, Kind: "thread_reply", Delivery: "comment", State: "held", CreatedAt: created,
				Origin: draft.Origin{Kind: draft.OriginTurn, Intent: "review", Workflow: "review-follow-up"}},
			where:  "acme/api",
			people: []string{"@alice"},
			want: workflow.Facts{"draft.proposer": "intent:review", "draft.workflow": "review-follow-up", "draft.kind": "thread_reply",
				"draft.delivery": "comment", "draft.provider": "github", "draft.state": "held", "draft.where": "acme/api",
				"draft.people": []string{"@alice"}, "draft.created_at": created},
		},
		{
			name: "an auto workflow's draft",
			draft: draft.Draft{Unit: unit.Unit{Provider: "slack", Key: "C1:1.2"}, Kind: "message", Delivery: "message", State: "queued", CreatedAt: created,
				Origin: draft.Origin{Kind: draft.OriginAuto, Workflow: "slack-stalled-nudge"}},
			want: workflow.Facts{"draft.proposer": "workflow:slack-stalled-nudge", "draft.workflow": "slack-stalled-nudge", "draft.kind": "message",
				"draft.delivery": "message", "draft.provider": "slack", "draft.state": "queued", "draft.where": "",
				"draft.people": []string{}, "draft.created_at": created},
		},
		{
			name: "a rule's draft",
			draft: draft.Draft{Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#12"}, Kind: "nudge", Delivery: "comment", State: "held", CreatedAt: created,
				Origin: draft.Origin{Kind: draft.OriginAuto, Rule: "github-nudge"}},
			want: workflow.Facts{"draft.proposer": "rule:github-nudge", "draft.workflow": "", "draft.kind": "nudge",
				"draft.delivery": "comment", "draft.provider": "github", "draft.state": "held", "draft.where": "",
				"draft.people": []string{}, "draft.created_at": created},
		},
		{
			name: "a draft on a task its target's plugin posts",
			draft: draft.Draft{Unit: unit.Unit{Provider: unit.Task, Key: "e1"}, Target: "#general", Kind: "ask_owner", Delivery: "message", State: "held", CreatedAt: created,
				Origin: draft.Origin{Kind: draft.OriginUser}},
			publisher: "slack",
			want: workflow.Facts{"draft.proposer": "user", "draft.workflow": "", "draft.kind": "ask_owner",
				"draft.delivery": "message", "draft.provider": "slack", "draft.state": "held", "draft.where": "",
				"draft.people": []string{}, "draft.created_at": created},
		},
		{
			name: "a user's draft",
			draft: draft.Draft{Unit: unit.Unit{Provider: "slack", Key: "C1:1.2"}, Kind: "message", Delivery: "message", State: "failed", CreatedAt: created,
				Origin: draft.Origin{Kind: draft.OriginUser}},
			want: workflow.Facts{"draft.proposer": "user", "draft.workflow": "", "draft.kind": "message",
				"draft.delivery": "message", "draft.provider": "slack", "draft.state": "failed", "draft.where": "",
				"draft.people": []string{}, "draft.created_at": created},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := draftViewFacts(tc.draft, cmp.Or(tc.publisher, tc.draft.Unit.Provider), tc.where, tc.people)
			if len(got) != len(tc.want) {
				t.Fatalf("facts = %v, want %v", got, tc.want)
			}
			for k, w := range tc.want {
				if !equalFact(got[k], w) {
					t.Errorf("%s = %#v, want %#v", k, got[k], w)
				}
			}
		})
	}
}

func equalFact(a, b any) bool {
	as, ok := a.([]string)
	bs, bok := b.([]string)
	if ok && bok {
		if len(as) != len(bs) {
			return false
		}
		for i := range as {
			if as[i] != bs[i] {
				return false
			}
		}
		return true
	}
	if at, ok := a.(time.Time); ok {
		bt, _ := b.(time.Time)
		return at.Equal(bt)
	}
	return a == b
}
