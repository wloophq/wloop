package source

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Bases reads a unit's live basis from the plugin of its provider, through
// that plugin's BasisReader; a plugin with none reads "".
type Bases struct {
	Kind *Kind
}

var _ outbox.Bases = Bases{}

// Live asks u's plugin now, never a cache; a plugin that is not running
// fails, so no draft is taken as fresh unread.
func (b Bases) Live(ctx context.Context, u unit.Unit, kind string) (string, error) {
	c, err := b.Kind.Client(string(u.Provider))
	if err != nil {
		return "", err
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return "", err
	}
	if !slices.Contains(caps, src.CapabilityBasis) {
		return "", nil
	}
	return c.Basis(ctx, Wire(u), kind)
}
