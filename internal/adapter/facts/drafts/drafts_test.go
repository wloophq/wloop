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
		want := workflow.Facts{FactFailed: c.failed, FactStale: c.stale, FactFailedKind: got[FactFailedKind], FactFailedRef: got[FactFailedRef],
			FactHeldStale: false, FactHeldKind: "", FactHeldRef: "", FactHeldBasis: ""}
		if !equal(got, want) {
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

func held(id draft.ID, origin draft.OriginKind, basis string) draft.Draft {
	return draft.Draft{ID: id, Unit: pr7, Kind: "review", Rev: 1, State: draft.Held, Basis: basis, Origin: draft.Origin{Kind: origin, TurnSeq: 3}}
}

func TestHeldDraftFactsNameTheDraftAPushCanMakeStale(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 10, 18, 12, m, 0, 0, time.UTC) }
	fresh := held(4, draft.OriginTurn, "c2")
	refused := held(5, draft.OriginSession, "c1")
	refused.Cause, refused.UpdatedAt = draft.CauseStale, at(1)
	newer := held(6, draft.OriginTurn, "c2")
	newer.Rev, newer.UpdatedAt = 2, at(5)
	noBasis := held(7, draft.OriginTurn, "")
	noBasis.Kind, noBasis.UpdatedAt = "thread_reply", at(9)
	for _, c := range []struct {
		name             string
		drafts           stored
		ref, kind, basis string
		stale            bool
	}{
		{"none", nil, "", "", "", false},
		{"a turn's fresh held review", stored{fresh}, "4:1", "review", "c2", false},
		{"a held review refused as stale", stored{refused}, "5:1", "review", "c1", true},
		{"the refused one before a newer fresh one", stored{newer, refused}, "5:1", "review", "c1", true},
		{"the newest of two fresh ones", stored{fresh, newer}, "6:2", "review", "c2", false},
		{"a held draft of no basis", stored{noBasis}, "", "", "", false},
		{"a person's held review", stored{held(8, draft.OriginUser, "c1")}, "", "", "", false},
		{"a workflow's held draft", stored{held(9, draft.OriginAuto, "c1")}, "", "", "", false},
	} {
		got, err := Provider{Drafts: c.drafts}.Facts(context.Background(), pr7)
		if err != nil {
			t.Fatal(err)
		}
		if got[FactHeldRef] != c.ref || got[FactHeldKind] != c.kind || got[FactHeldBasis] != c.basis || got[FactHeldStale] != c.stale {
			t.Errorf("%s: ref %v kind %v basis %v stale %v, want %q %q %q %v", c.name,
				got[FactHeldRef], got[FactHeldKind], got[FactHeldBasis], got[FactHeldStale], c.ref, c.kind, c.basis, c.stale)
		}
		if got[FactFailed] != false || got[FactStale] != false || got[FactFailedRef] != "" {
			t.Errorf("%s: a held draft set the failed facts: %v", c.name, got)
		}
	}
}
