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
)

// WindowRequest asks the native window to act; Route is a cockpit route, a hash without "#".
type WindowRequest struct {
	Action string `json:"action"`
	Route  string `json:"route,omitempty"`
}

// WindowResult says whether a window client took the request.
type WindowResult struct {
	Delivered bool `json:"delivered"`
}
