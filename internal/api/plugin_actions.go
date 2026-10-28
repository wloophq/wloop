package api

import (
	"encoding/json"
	"net/url"
)

// PluginAction runs an action of a running plugin that names no unit:
// POST takes a PluginActionRequest and answers the plugin's JSON.
const PluginAction = "/api/plugins/{id}/actions/{action}"

// PluginActionPath is the path of plugin id's action.
func PluginActionPath(id, action string) string {
	return "/api/plugins/" + url.PathEscape(id) + "/actions/" + url.PathEscape(action)
}

// PluginActionRequest is an action of a plugin and its arguments, whose
// shape the plugin owns; the route sets Action.
type PluginActionRequest struct {
	Action string          `json:"-"`
	Args   json.RawMessage `json:"args,omitempty"`
}
