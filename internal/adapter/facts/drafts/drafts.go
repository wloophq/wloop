// Package factdrafts reports the facts of a unit's drafts that failed to send or wait for the user, which the
// unit's workflows read in their when; nothing restarts on its own.
package factdrafts

import (
	"context"
	"strconv"

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
	// FactHeldRef is a held draft an intent's turn wrote on the unit that states a basis, as <id>:<rev>: the
	// newest refused as stale, else the newest.
	FactHeldRef = "draft.held_ref"
	// FactHeldKind is that draft's kind.
	FactHeldKind = "draft.held_kind"
	// FactHeldBasis is the basis that draft was written against.
	FactHeldBasis = "draft.held_basis"
	// FactHeldStale says that draft was refused as stale.
	FactHeldStale = "draft.held_stale"
)

// States are the states of the drafts Provider lists.
var States = []draft.State{draft.Failed, draft.Held}

// Drafts lists drafts, as outbox.Service does.
type Drafts interface {
	List(ctx context.Context, f outbox.Filter) ([]draft.Draft, error)
}

// Provider is the draft facts of a unit, merged into the facts its own provider reports.
type Provider struct {
	Drafts Drafts
}

// Facts are u's draft facts: whether a draft a turn or a session proposed on u failed, whether one
// failed stale, and the kind and <id>:<rev> of the newest that failed; and the held draft an intent's turn
// wrote that a move of u can make stale, with its basis and whether it was refused as stale. A workflow's or
// a person's draft is the outbox's to show, not a round's, and a held draft of the user's own session is
// theirs to revise. Nothing here reads the unit's live basis: a workflow compares the held basis with the
// unit's own facts.
func (p Provider) Facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	ds, err := p.Drafts.List(ctx, outbox.Filter{Unit: &u, States: States})
	if err != nil {
		return nil, err
	}
	out := workflow.Facts{FactFailed: false, FactStale: false, FactFailedKind: "", FactFailedRef: "",
		FactHeldRef: "", FactHeldKind: "", FactHeldBasis: "", FactHeldStale: false}
	var failed, held *draft.Draft
	for i, d := range ds {
		if d.Unit != u || !d.Origin.ByAgent() {
			continue
		}
		switch {
		case d.State == draft.Failed:
			out[FactFailed] = true
			if d.Cause == draft.CauseStale {
				out[FactStale] = true
			}
			if failed == nil || newer(d, *failed) {
				failed = &ds[i]
			}
		case d.State == draft.Held && d.Basis != "" && d.Origin.Kind == draft.OriginTurn:
			if held == nil || heldFirst(d, *held) {
				held = &ds[i]
			}
		}
	}
	if failed != nil {
		out[FactFailedKind] = string(failed.Kind)
		out[FactFailedRef] = ref(*failed)
	}
	if held != nil {
		out[FactHeldRef], out[FactHeldKind], out[FactHeldBasis] = ref(*held), string(held.Kind), held.Basis
		out[FactHeldStale] = held.Cause == draft.CauseStale
	}
	return out, nil
}

func ref(d draft.Draft) string {
	return strconv.FormatInt(int64(d.ID), 10) + ":" + strconv.FormatInt(d.Rev, 10)
}

// heldFirst says whether held draft a goes before b: refused as stale when b is not, else newer.
func heldFirst(a, b draft.Draft) bool {
	if as, bs := a.Cause == draft.CauseStale, b.Cause == draft.CauseStale; as != bs {
		return as
	}
	return newer(a, b)
}

// newer says whether a changed after b: it was updated later, or at once with a higher id.
func newer(a, b draft.Draft) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}
