package api

// WindowPath takes a WindowRequest and relays it to the events client named WindowClient, answering a
// WindowResult.
const WindowPath = "/api/window"

// WindowClient is the events client the window controller subscribes as.
const WindowClient = "window"

// The actions of a WindowRequest.
const (
	WindowOpen      = "open"  // show the main window at Route, hide the panel
	WindowPanel     = "panel" // show the panel
	WindowHidePanel = "hide-panel"
	WindowDetach    = "detach"   // show the item Route names in a window of its own
	WindowExternal  = "external" // open URL, a web address, in the default browser
)

// WindowActions are the actions a WindowRequest may name.
var WindowActions = []string{WindowOpen, WindowPanel, WindowHidePanel, WindowDetach, WindowExternal}

// WindowRequest asks the native window to act; Route is a cockpit route, a hash without "#".
type WindowRequest struct {
	Action string `json:"action"`
	Route  string `json:"route,omitempty"`
	// URL is the http or https address an external request opens.
	URL string `json:"url,omitempty"`
}

// WindowResult says whether a window client took the request.
type WindowResult struct {
	Delivered bool `json:"delivered"`
}
