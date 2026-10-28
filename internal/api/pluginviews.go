package api

import (
	"fmt"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// PluginView serves one of an enabled plugin's views, its Presenter.View:
// GET -> the view's content, its query the request's query parameters as
// a JSON object of strings; POST with the query as the body -> the same.
const PluginView = "/api/plugins/{id}/views/{view}"

// PluginViewPath is the route of plugin id's view.
func PluginViewPath(id unit.ProviderID, view string) string {
	return "/api/plugins/" + string(id) + "/views/" + view
}

// The views the daemon asks of a unit's plugin, by the name its View
// serves them under, and what each answers.
const (
	// ViewUnitRow answers a UnitRowQuery with the Row of a unit the read
	// model does not list, its Unit as its source names it; ErrNotFound for
	// a unit the plugin has no row of.
	ViewUnitRow = "unit.row"
	// ViewUnitDetail answers a UnitQuery with the PluginDetail of a unit.
	ViewUnitDetail = "unit.detail"
	// ViewUnitChecks answers a UnitQuery with the unit's []CheckDetail.
	ViewUnitChecks = "unit.checks"
	// ViewTimings answers a TimingsQuery with a TimingsReport.
	ViewTimings = "timings"
)

// UnitQuery names the unit a view is of.
type UnitQuery struct {
	Unit unit.Unit `json:"unit"`
}

// UnitRowQuery asks for a unit's row; Action and Inputs are the action a
// request asks for, Unmuted derives it without the user's mute and pending
// round, and Kept asks only for a unit the plugin keeps, reading nothing
// from its source.
type UnitRowQuery struct {
	Unit    unit.Unit      `json:"unit"`
	Action  string         `json:"action,omitempty"`
	Inputs  map[string]any `json:"inputs,omitempty"`
	Unmuted bool           `json:"unmuted,omitempty"`
	Kept    bool           `json:"kept,omitempty"`
}

// PluginDetail is what a unit's plugin adds to its detail: the timeline and
// the brief.
type PluginDetail struct {
	Events []Event    `json:"events"`
	Brief  *BriefView `json:"brief,omitempty"`
}

// TimingsQuery asks for the timings of the Days days before now.
type TimingsQuery struct {
	Days int `json:"days"`
}

// PluginActionArgs are the Args of an action the daemon sends a unit's
// plugin: the person's request, the unit's row without the user's mute and
// pending round, and, for an intent's prepare, the call's inputs so far.
type PluginActionArgs struct {
	Request ActionRequest  `json:"request"`
	Unmuted *Row           `json:"unmuted,omitempty"`
	Inputs  map[string]any `json:"inputs,omitempty"`
}

// PluginActionResult is what a plugin's Action answers the daemon: the
// lines it reports and, for a prepare, the call's inputs.
type PluginActionResult struct {
	Output []string       `json:"output,omitempty"`
	Inputs map[string]any `json:"inputs,omitempty"`
}

// PluginError is a plugin's refusal of a request, with the HTTP status the
// daemon answers it with.
type PluginError struct {
	Status int
	Msg    string
}

func (e *PluginError) Error() string { return e.Msg }

// Refuse is the refusal msg answered with status.
func Refuse(status int, format string, args ...any) error {
	return &PluginError{Status: status, Msg: fmt.Sprintf(format, args...)}
}
