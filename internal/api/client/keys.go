package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/wloophq/wloop/internal/api"
)

// Keys is the keyboard map of surface: web, tui or menubar.
func (c *Client) Keys(ctx context.Context, surface string) (api.KeysResponse, error) {
	var out api.KeysResponse
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.Keys+"?"+url.Values{"surface": {surface}}.Encode(), nil, &out)
	return out, err
}
