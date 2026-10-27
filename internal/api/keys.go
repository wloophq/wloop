package api

// Keys serves the keyboard map of a surface: GET takes the query parameter
// surface (web, tui or menubar).
const Keys = "/api/keys"

// KeysResponse is the answer of GET /api/keys: the core bindings of the
// surface, then every served action key.
type KeysResponse struct {
	Bindings []KeyView `json:"bindings"`
}

// KeyView is one key of a surface. Owner is "core", a plugin id or
// "intent:<name>"; Action is a core action or an action spec's id.
type KeyView struct {
	Key     string `json:"key"`
	Action  string `json:"action"`
	Context string `json:"context"`
	Owner   string `json:"owner"`
	Help    string `json:"help"`
	// Summary is what an action's key does, from its spec or, for a core
	// verb's key, the verb's summary; "" for any other core key.
	Summary string `json:"summary,omitempty"`
}
