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
// shape the plugin owns; the route sets Action, and Actor from the
// request's session header.
type PluginActionRequest struct {
	Action string          `json:"-"`
	Actor  string          `json:"-"`
	Args   json.RawMessage `json:"args,omitempty"`
}

// PluginRun runs a command line of a running plugin: POST takes a
// PluginRunRequest and streams PluginRunChunk lines, one JSON object a
// line, as the command writes, the last one its exit.
const PluginRun = "/api/plugins/{id}/run"

// PluginRunPath is the path of plugin id's command lines.
func PluginRunPath(id string) string { return "/api/plugins/" + url.PathEscape(id) + "/run" }

// PluginRunRequest is a command line of a plugin: the words after its id.
type PluginRunRequest struct {
	Args []string `json:"args"`
}

// PluginRunChunk is one line of a plugin command's stream: what it wrote
// to its standard output or error, or how it ended.
type PluginRunChunk struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Exit   *int   `json:"exit,omitempty"`
	Error  string `json:"error,omitempty"`
}
