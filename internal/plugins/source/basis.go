package source

import (
	"context"
	"slices"

	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Bases reads a unit's live basis, and whether a target lies inside it,
// from the plugin of its provider, through that plugin's Drafter; a
// plugin with none reads "" and no target inside.
type Bases struct {
	Kind *Kind
}

var (
	_ outbox.Bases = Bases{}
	_ port.Scoper  = Bases{}
)

// Live asks u's plugin now, never a cache; a plugin that is not running
// fails, so no draft is taken as fresh unread.
func (b Bases) Live(ctx context.Context, u unit.Unit, kind string) (string, error) {
	c, ok, err := b.drafter(ctx, u)
	if err != nil || !ok {
		return "", err
	}
	return c.Basis(ctx, Wire(u), kind)
}

// Within asks u's plugin whether target lies inside u.
func (b Bases) Within(ctx context.Context, u unit.Unit, target string) (bool, error) {
	c, ok, err := b.drafter(ctx, u)
	if err != nil || !ok {
		return false, err
	}
	return c.Within(ctx, Wire(u), target)
}

// drafter is the client of u's plugin, and whether it is a Drafter.
func (b Bases) drafter(ctx context.Context, u unit.Unit) (*src.Client, bool, error) {
	c, err := b.Kind.Client(string(u.Provider))
	if err != nil {
		return nil, false, err
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return nil, false, err
	}
	return c, slices.Contains(caps, src.CapabilityDrafter), nil
}
