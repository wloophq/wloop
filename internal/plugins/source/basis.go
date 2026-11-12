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
	c, ok, err := drafterOf(ctx, b.Kind, string(u.Provider))
	if err != nil || !ok {
		return "", err
	}
	return c.Basis(ctx, Wire(u), kind)
}

// Within asks u's plugin whether target lies inside u.
func (b Bases) Within(ctx context.Context, u unit.Unit, target string) (bool, error) {
	c, ok, err := drafterOf(ctx, b.Kind, string(u.Provider))
	if err != nil || !ok {
		return false, err
	}
	return c.Within(ctx, Wire(u), target)
}

// drafterOf is the client of plugin id, and whether it is a Drafter.
func drafterOf(ctx context.Context, k *Kind, id string) (*src.Client, bool, error) {
	c, err := k.Client(id)
	if err != nil {
		return nil, false, err
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return nil, false, err
	}
	return c, slices.Contains(caps, src.CapabilityDrafter), nil
}
