package client

import (
	"context"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
)

// Window relays req to the native window, if one listens.
func (c *Client) Window(ctx context.Context, req api.WindowRequest) (api.WindowResult, error) {
	var res api.WindowResult
	err := c.do(ctx, pingTimeout, http.MethodPost, api.WindowPath, req, &res)
	return res, err
}
