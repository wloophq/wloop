package client

import (
	"context"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
)

// Plugins is every compiled-in plugin the daemon serves, enabled or not.
func (c *Client) Plugins(ctx context.Context) ([]api.Plugin, error) {
	var ps []api.Plugin
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.Plugins, nil, &ps)
	return ps, err
}
