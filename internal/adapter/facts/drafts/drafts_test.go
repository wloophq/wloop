package factdrafts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

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

func failed(id draft.ID, origin draft.OriginKind, err error) draft.Draft {
	return draft.Draft{ID: id, Unit: pr7, State: draft.Failed, Origin: draft.Origin{Kind: origin, TurnSeq: 3}, Error: err.Error()}
}

func TestFailedSendIsUnitFact(t *testing.T) {
	unprocessable := errors.New("422 Unprocessable Entity: pull_request_review_thread.line must be part of the diff")
	for _, c := range []struct {
		name          string
		drafts        stored
		failed, stale bool
	}{
		{"none", nil, false, false},
		{"a turn's draft failed", stored{failed(1, draft.OriginTurn, unprocessable)}, true, false},
		{"a session's draft failed stale", stored{failed(1, draft.OriginSession, fmt.Errorf("%w: head moved", outbox.ErrStale))}, true, true},
		{"a workflow's nudge failed", stored{failed(1, draft.OriginAuto, unprocessable)}, false, false},
		{"a person's draft failed stale", stored{failed(1, draft.OriginUser, outbox.ErrStale)}, false, false},
		{"a held and a sent draft", stored{{ID: 1, Unit: pr7, State: draft.Held, Origin: draft.Origin{Kind: draft.OriginTurn}}, {ID: 2, Unit: pr7, State: draft.Sent, Origin: draft.Origin{Kind: draft.OriginTurn}}}, false, false},
		{"another unit's", stored{func() draft.Draft {
			d := failed(1, draft.OriginTurn, outbox.ErrStale)
			d.Unit.Key = "acme/api#8"
			return d
		}()}, false, false},
	} {
		got, err := Provider{Drafts: c.drafts}.Facts(context.Background(), pr7)
		if err != nil {
			t.Fatal(err)
		}
		if want := (workflow.Facts{FactFailed: c.failed, FactStale: c.stale}); !equal(got, want) {
			t.Errorf("%s: %v, want %v", c.name, got, want)
		}
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
