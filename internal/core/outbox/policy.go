package outbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Manifests reads the [drafts.approval] blocks of the loaded intents and
// workflows, and the turns book.
type Manifests interface {
	// IntentApproval is the block of intent name, nil when it declares none; known is false for an unknown intent.
	IntentApproval(name string) (p *draft.Policy, known bool)
	// WorkflowApproval is the policy of the drafts of a turn workflow name
	// started, of an intent whose block is intent: the workflow's block, else
	// intent, with what the configuration lays over it; nil when none
	// applies. known is false for an unknown workflow.
	WorkflowApproval(name string, intent *draft.Policy) (p *draft.Policy, known bool)
	// Turn is the intent of turn seq in u's book and the workflow that started it; ok is false when the book has no such turn.
	Turn(ctx context.Context, u unit.Unit, seq int64) (intent, workflow string, ok bool, err error)
}

// ErrUnknownOrigin is a draft's proposing intent, workflow or turn that the manifests or the book do not know.
var ErrUnknownOrigin = errors.New("unknown proposer")

// Resolver is the Policies of the manifests, for turn and session drafts only:
// the proposing turn's workflow's policy laid over its intent's block, else
// wait. The
// intent and workflow come from the turn in the book; an Origin that names
// others than the book's resolves to wait. Every other origin resolves to
// wait, so a rule's policy reaches a draft only through Proposal.Policy.
type Resolver struct {
	Manifests Manifests
}

var (
	waiting = draft.Approval{Mode: draft.Wait}
	waitPol = draft.Policy{Default: waiting}
)

func (r Resolver) For(ctx context.Context, u unit.Unit, o draft.Origin) (draft.Policy, error) {
	switch {
	case o.Kind != draft.OriginTurn && o.Kind != draft.OriginSession:
		if o.Intent != "" || o.Workflow != "" {
			return waitPol, fmt.Errorf("a %s draft names a manifest: %w", o.Kind, ErrUnknownOrigin)
		}
		return waitPol, nil
	case o.TurnSeq <= 0:
		if o.Intent != "" || o.Workflow != "" {
			return waitPol, fmt.Errorf("a %s draft with no turn names a manifest: %w", o.Kind, ErrUnknownOrigin)
		}
		return waitPol, nil
	}
	intent, workflow, ok, err := r.Manifests.Turn(ctx, u, o.TurnSeq)
	switch {
	case err != nil:
		return waitPol, fmt.Errorf("reading turn %d of %s: %w", o.TurnSeq, u, err)
	case !ok:
		return waitPol, fmt.Errorf("turn %d of %s: %w", o.TurnSeq, u, ErrUnknownOrigin)
	case o.Intent != "" && o.Intent != intent, o.Workflow != "" && o.Workflow != workflow:
		return waitPol, fmt.Errorf("turn %d of %s is not the draft's intent and workflow: %w", o.TurnSeq, u, ErrUnknownOrigin)
	}
	p, known := r.Manifests.IntentApproval(intent)
	if !known {
		return waitPol, fmt.Errorf("intent %s: %w", intent, ErrUnknownOrigin)
	}
	if workflow != "" {
		if p, known = r.Manifests.WorkflowApproval(workflow, p); !known {
			return waitPol, fmt.Errorf("workflow %s: %w", workflow, ErrUnknownOrigin)
		}
	}
	out := waitPol
	if p != nil {
		out = *p
	}
	out.Scope = workflow
	return out, nil
}
