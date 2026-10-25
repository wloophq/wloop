package cli

import (
	"context"
	"errors"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
)

var errEffectNotImplemented = errors.New("the draft effect is not implemented")

func effectNotImplemented(context.Context, draft.Draft) error { return errEffectNotImplemented }

// appliedByPublisher is an effect its provider's publisher applies as it
// posts, so applying it after the send does nothing.
func appliedByPublisher(context.Context, draft.Draft) error { return nil }

// draftEffects are the draft effects the daemon applies once a draft is
// sent, by name; a plugin's own come from its Module.OnPosted.
func draftEffects() map[string]outbox.Effect {
	return map[string]outbox.Effect{
		"entry.nudged": effectNotImplemented,
		"unit.closed":  effectNotImplemented,
		"pr.nudged":    appliedByPublisher,
	}
}
