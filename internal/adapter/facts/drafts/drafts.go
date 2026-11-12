// Package factdrafts reports the facts of a unit's drafts that failed to send, which the unit's workflows
// read in their when; nothing restarts on its own.
package factdrafts

import (
	"context"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

const (
	// FactFailed says an agent's draft of the unit failed to send and waits on a person or a new round.
	FactFailed = "draft.failed"
	// FactStale says one of those failed because the unit moved past the basis it was written against.
	FactStale = "draft.stale"
	// FactFailedKind is the kind of the newest of those.
	FactFailedKind = "draft.failed_kind"
	// FactFailedRef is the newest of those as <id>:<rev>.
	FactFailedRef = "draft.failed_ref"
)

// Drafts lists drafts, as outbox.Service does.
type Drafts interface {
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
}

// Provider is the draft facts of a unit, merged into the facts its own provider reports.
type Provider struct {
	Drafts Drafts
}

// Facts are u's draft facts: whether a draft a turn or a session proposed on u failed, whether one
// failed stale, and the kind and <id>:<rev> of the newest that failed. A workflow's or a person's draft
// that failed is the outbox's to show, not a round's.
func (p Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	failed, err := p.Drafts.List(ctx, outbox.Filter{Unit: &u, States: []draft.State{draft.Failed}})
	if err != nil {
		return nil, err
	}
	out := workflow.Facts{FactFailed: false, FactStale: false, FactFailedKind: "", FactFailedRef: ""}
	var newest *draft.Draft
	for i, d := range failed {
		if d.State != draft.Failed || d.Unit != u || d.Origin.Kind != draft.OriginTurn && d.Origin.Kind != draft.OriginSession {
			continue
		}
		out[FactFailed] = true
		if stale(d) {
			out[FactStale] = true
		}
		if newest == nil || newer(d, *newest) {
			newest = &failed[i]
		}
	}
	if newest != nil {
		out[FactFailedKind] = string(newest.Kind)
		out[FactFailedRef] = strconv.FormatInt(int64(newest.ID), 10) + ":" + strconv.FormatInt(newest.Rev, 10)
	}
	return out, nil
}

// StalePrefix starts the failure the outbox stores for a draft whose send it refused as stale, a failure
// that wraps outbox.ErrStale; a publisher's free text never counts.
const StalePrefix = "stale:"

// newer says whether a failed after b: it was updated later, or at once with a higher id.
func newer(a, b draft.Draft) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}

func stale(d draft.Draft) bool { return strings.HasPrefix(d.Error, StalePrefix) }
