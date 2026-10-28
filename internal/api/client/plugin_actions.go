package client

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
)

// PluginAction runs plugin id's action with args and decodes the plugin's
// answer into out; a nil out discards it.
func (c *Client) PluginAction(ctx context.Context, id, action string, args, out any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	var res json.RawMessage
	if err := c.do(ctx, actionTimeout, http.MethodPost, api.PluginActionPath(id, action), api.PluginActionRequest{Args: raw}, &res); err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(res, out)
}
