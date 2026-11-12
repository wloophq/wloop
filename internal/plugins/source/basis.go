package source

import (
	"context"
	"slices"

	src "github.com/wloophq/wloop/pkg/plugin/source"
)

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
