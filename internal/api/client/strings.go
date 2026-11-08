package client

import (
	"context"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
)

// Strings are the client strings in the daemon's interface language.
func (c *Client) Strings(ctx context.Context) (api.Strings, error) {
	var out api.Strings
	err := c.do(ctx, ReadTimeout, http.MethodGet, api.StringsPath, nil, &out)
	return out, err
}
