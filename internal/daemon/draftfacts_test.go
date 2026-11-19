package daemon

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestDraftViewFacts(t *testing.T) {
	created := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		draft  api.Draft
		where  string
		people []string
		want   workflow.Facts
	}{
		{
			name: "a review round's turn draft",
			draft: api.Draft{Unit: "github/acme/api#12", Kind: "thread_reply", Delivery: "comment", State: "held", CreatedAt: created,
				Origin: api.DraftOrigin{Kind: "turn", Intent: "review", Workflow: "review-follow-up"}},
			where:  "acme/api",
			people: []string{"@alice"},
			want: workflow.Facts{"draft.proposer": "intent:review", "draft.workflow": "review-follow-up", "draft.kind": "thread_reply",
				"draft.delivery": "comment", "draft.provider": "github", "draft.state": "held", "draft.where": "acme/api",
				"draft.people": []string{"@alice"}, "draft.created_at": created},
		},
		{
			name: "an auto workflow's draft",
			draft: api.Draft{Unit: "slack/C1:1.2", Kind: "message", Delivery: "message", State: "queued", CreatedAt: created,
				Origin: api.DraftOrigin{Kind: "auto", Workflow: "slack-stalled-nudge"}},
			want: workflow.Facts{"draft.proposer": "workflow:slack-stalled-nudge", "draft.workflow": "slack-stalled-nudge", "draft.kind": "message",
				"draft.delivery": "message", "draft.provider": "slack", "draft.state": "queued", "draft.where": "",
				"draft.people": []string{}, "draft.created_at": created},
		},
		{
			name: "a rule's draft",
			draft: api.Draft{Unit: "github/acme/api#12", Kind: "nudge", Delivery: "comment", State: "held", CreatedAt: created,
				Origin: api.DraftOrigin{Kind: "auto", Rule: "github-nudge"}},
			want: workflow.Facts{"draft.proposer": "rule:github-nudge", "draft.workflow": "", "draft.kind": "nudge",
				"draft.delivery": "comment", "draft.provider": "github", "draft.state": "held", "draft.where": "",
				"draft.people": []string{}, "draft.created_at": created},
		},
		{
			name: "a user's draft",
			draft: api.Draft{Unit: "slack/C1:1.2", Kind: "message", Delivery: "message", State: "failed", CreatedAt: created,
				Origin: api.DraftOrigin{Kind: "user"}},
			want: workflow.Facts{"draft.proposer": "user", "draft.workflow": "", "draft.kind": "message",
				"draft.delivery": "message", "draft.provider": "slack", "draft.state": "failed", "draft.where": "",
				"draft.people": []string{}, "draft.created_at": created},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := draftViewFacts(tc.draft, tc.where, tc.people)
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
