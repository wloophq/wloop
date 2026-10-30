package api

import (
	"encoding/json"
	"slices"

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

// The capabilities a plugin serves in Plugin.Capabilities.
const (
	// CapabilityCheckouts: a session on a unit runs in a checkout of the
	// unit's own repository.
	CapabilityCheckouts = "checkouts"
	// CapabilityBriefs: the brief queue briefs the plugin's units.
	CapabilityBriefs = "briefs"
	// CapabilityPublishes: the plugin posts approved drafts.
	CapabilityPublishes = "publishes"
	// CapabilityReacts: the plugin posts reactions.
	CapabilityReacts = "reacts"
	// CapabilityItems: the plugin's rows derive items.
	CapabilityItems = "items"
	// CapabilityDetail: the cockpit shows a detail of the plugin's units,
	// which a client opens in place of the unit's link.
	CapabilityDetail = "detail"
)

// Can says whether the plugin is enabled and serves capability c.
func (p Plugin) Can(c string) bool { return p.Enabled && slices.Contains(p.Capabilities, c) }

// PluginCan says whether the plugin of provider id is enabled and serves
// capability c.
func PluginCan(ps []Plugin, id unit.ProviderID, c string) bool {
	return slices.ContainsFunc(ps, func(p Plugin) bool { return p.ID == id && p.Can(c) })
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
	Facets        []Facet        `json:"facets,omitempty"`        // qualifiers over row and item facts
	Views         []SavedView    `json:"views,omitempty"`         // listed under the plugin in navigation
	Notifications []Notification `json:"notifications,omitempty"` // templates keyed by item kind or row kind
	Keys          []KeyBinding   `json:"keys,omitempty"`          // checked with keys.Check when the catalog loads
	// Key is the chord that jumps to the plugin's views, checked with
	// keys.CheckView; "" for none.
	Key     string           `json:"key,omitempty"`
	Web     *WebView         `json:"web,omitempty"` // tier c
	Menubar []MenubarSection `json:"menubar,omitempty"`
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
