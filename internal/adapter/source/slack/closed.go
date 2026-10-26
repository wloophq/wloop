package slack

import (
	"context"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// onPosted are the Slack effects of a sent draft: unit.closed marks the
// unit's closure, which no extraction pass clears.
func (p *plugin) onPosted() map[string]outbox.Effect {
	if p.deps.DB == nil {
		return nil
	}
	x := p.deps.DB.Extraction()
	return map[string]outbox.Effect{
		builtin.EffectUnitClosed: func(ctx context.Context, d draft.Draft) error {
			at := d.Evidence.At
			if at.IsZero() {
				at = d.UpdatedAt
			}
			return x.MarkClosed(ctx, d.Unit, at)
		},
	}
}
