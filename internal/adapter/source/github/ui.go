package ghsource

import (
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// UI is the GitHub plugin's declared contribution: a saved view per served
// view, in their order, and the menu bar's In loop section; the Inbox and
// Next hold what needs the user.
func UI() *api.PluginUI {
	ui := &api.PluginUI{Key: "g g", Menubar: []api.MenubarSection{{Label: "In loop", View: api.ViewLoop}}}
	for _, v := range prrow.ViewSpecs() {
		ui.Views = append(ui.Views, api.SavedView{ID: v.ID, Label: v.Label, View: v.ID})
	}
	return ui
}
