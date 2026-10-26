package api

import (
	"encoding/json"

	"github.com/wloophq/wloop/internal/domain/unit"
)

type Plugin struct {
	ID      unit.ProviderID `json:"id"`
	Name    string          `json:"name"` // the service as a person reads it, such as "GitHub"
	Enabled bool            `json:"enabled"`
	// Reacts: the plugin's publisher posts reactions (drafts of kind react); only when enabled.
	Reacts bool      `json:"reacts,omitempty"`
	UI     *PluginUI `json:"ui,omitempty"` // only when enabled
	// Capabilities are what the plugin can do, so a client asks what a
	// provider does, never which it is: checkouts, briefs, publishes, reacts,
	// items; only when enabled.
	Capabilities []string `json:"capabilities,omitempty"`
}

// Plugins is the route of every compiled-in plugin: GET -> []Plugin.
const Plugins = "/api/plugins"

// PluginAssets serves an enabled plugin's WebAssets; 404 while it is disabled.
const PluginAssets = "/plugins/{id}/{path...}"

type SourceHealth struct {
	Provider unit.ProviderID `json:"provider"`
	Warnings []string        `json:"warnings"`
	Detail   json.RawMessage `json:"detail,omitempty"`
}

type PluginUI struct {
	Facets        []Facet          `json:"facets,omitempty"`        // qualifiers over row and item facts
	Views         []SavedView      `json:"views,omitempty"`         // listed under the plugin in navigation
	Notifications []Notification   `json:"notifications,omitempty"` // templates keyed by item kind or row kind
	Keys          []KeyBinding     `json:"keys,omitempty"`          // checked with keys.Check when the catalog loads
	Web           *WebView         `json:"web,omitempty"`           // tier c
	TUIViews      []string         `json:"tui_views,omitempty"`     // ids of compiled tui.PluginView
	Menubar       []MenubarSection `json:"menubar,omitempty"`
}

// Facet: Field is "row.*", "item.*" or "<provider>.*".
type Facet struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Field  string   `json:"field"`
	Values []string `json:"values,omitempty"`
}

// SavedView: View is a ViewSpec id served by the generic rows (rows or custom).
type SavedView struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	View  string `json:"view"`
	Group string `json:"group,omitempty"`
}

// Notification: On is "item:<kind>" or "row:<kind>"; Body a text/template over the row or item.
type Notification struct {
	ID    string `json:"id"`
	On    string `json:"on"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

// KeyBinding: Surface is web, tui or menubar.
type KeyBinding struct {
	Key     string `json:"key"`
	Action  string `json:"action"`
	Surface string `json:"surface,omitempty"`
}

// WebView: Module is served at /plugins/<id>/<Module>.
type WebView struct {
	Module string   `json:"module"`
	Views  []string `json:"views"`
}

type MenubarSection struct {
	Label string `json:"label"`
	View  string `json:"view"`
}
