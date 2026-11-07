package client

import (
	"context"

	"github.com/wloophq/wloop/internal/api"
)

// Window relays req to the native window, if one listens.
func (c *Client) Window(ctx context.Context, req api.WindowRequest) (api.WindowResult, error) {
	panic("not implemented: A2")
}
