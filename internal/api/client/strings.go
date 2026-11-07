package client

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
)

// Strings are the client strings in the daemon's interface language.
func (c *Client) Strings(ctx context.Context) (api.Strings, error) {
	panic("not implemented: D4")
}
