package factdrafts

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var pr7 = unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}

type stored []draft.Draft

func (s stored) List(_ context.Context, f outbox.Filter) ([]draft.Draft, error) {
	var out []draft.Draft
	for _, d := range s {
		if (f.Unit == nil || d.Unit == *f.Unit) && (len(f.States) == 0 || slices.Contains(f.States, d.State)) {
			out = append(out, d)
		}
	}
	return out, nil
}

func failed(id draft.ID, origin draft.OriginKind, failure string) draft.Draft {
	return draft.Draft{ID: id, Unit: pr7, State: draft.Failed, Origin: draft.Origin{Kind: origin, TurnSeq: 3}, Error: failure}
}

func failedStale(id draft.ID, origin draft.OriginKind) draft.Draft {
	d := failed(id, origin, "the draft is stale: head moved from c1 to c2")
	d.Cause = draft.CauseStale
	return d
}

func TestFailedSendIsUnitFact(t *testing.T) {
	unprocessable := "422 Unprocessable Entity: pull_request_review_thread.line must be part of the diff"
	for _, c := range []struct {
		name          string
		drafts        stored
		failed, stale bool
	}{
		{"none", nil, false, false},
		{"a turn's draft failed", stored{failed(1, draft.OriginTurn, unprocessable)}, true, false},
		{"a session's draft failed stale", stored{failedStale(1, draft.OriginSession)}, true, true},
		{"a failure whose text only says stale", stored{failed(1, draft.OriginTurn, outbox.ErrStale.Error())}, true, false},
		{"a publisher's text that says stale", stored{failed(1, draft.OriginTurn, "stale: 422: comment body")}, true, false},
		{"a workflow's nudge failed", stored{failed(1, draft.OriginAuto, unprocessable)}, false, false},
		{"a person's draft failed stale", stored{failedStale(1, draft.OriginUser)}, false, false},
		{"a held and a sent draft", stored{{ID: 1, Unit: pr7, State: draft.Held, Origin: draft.Origin{Kind: draft.OriginTurn}}, {ID: 2, Unit: pr7, State: draft.Sent, Origin: draft.Origin{Kind: draft.OriginTurn}}}, false, false},
		{"another unit's", stored{func() draft.Draft {
			d := failedStale(1, draft.OriginTurn)
			d.Unit.Key = "acme/api#8"
			return d
		}()}, false, false},
	} {
		got, err := Provider{Drafts: c.drafts}.Facts(context.Background(), pr7)
		if err != nil {
			t.Fatal(err)
		}
		if want := (workflow.Facts{FactFailed: c.failed, FactStale: c.stale, FactFailedKind: got[FactFailedKind], FactFailedRef: got[FactFailedRef]}); !equal(got, want) {
			t.Errorf("%s: %v, want %v", c.name, got, want)
		}
	}
}

func TestFailedKindAndRefNameTheNewestFailure(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 18, 12, m, 0, 0, time.UTC) }
	review := failed(7, draft.OriginTurn, "422")
	review.Kind, review.Rev, review.UpdatedAt = "review", 3, at(5)
	reply := failed(8, draft.OriginSession, "422")
	reply.Kind, reply.Rev, reply.UpdatedAt = "thread_reply", 1, at(2)
	nudge := failed(9, draft.OriginAuto, "422")
	nudge.Kind, nudge.Rev, nudge.UpdatedAt = "comment", 1, at(9)
	for _, c := range []struct {
		name      string
		drafts    stored
		kind, ref string
	}{
		{"none", nil, "", ""},
		{"the newest of a turn's and a session's", stored{reply, review, nudge}, "review", "7:3"},
		{"a reply alone", stored{reply}, "thread_reply", "8:1"},
	} {
		got, err := Provider{Drafts: c.drafts}.Facts(context.Background(), pr7)
		if err != nil {
			t.Fatal(err)
		}
		if got[FactFailedKind] != c.kind || got[FactFailedRef] != c.ref {
			t.Errorf("%s: kind %v ref %v, want %q %q", c.name, got[FactFailedKind], got[FactFailedRef], c.kind, c.ref)
		}
	}
	again := review
	again.Rev, again.UpdatedAt = 4, at(7)
	got, _ := Provider{Drafts: stored{again}}.Facts(context.Background(), pr7)
	if got[FactFailedRef] != "7:4" {
		t.Fatalf("a revised draft that failed again: %v", got[FactFailedRef])
	}
}

func equal(a, b workflow.Facts) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
