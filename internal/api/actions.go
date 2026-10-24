package api

// Actions is the route of the action specs.
const Actions = "/api/actions"

// ActionsResponse is the answer of GET /api/actions; a row's specs_version
// names the Version its actions follow.
type ActionsResponse struct {
	Version string       `json:"version"`
	Specs   []ActionSpec `json:"specs"`
	// Plan are the actions a plan item may offer.
	Plan []ActionSpec `json:"plan"`
}

// ActionSpec is an action the daemon may offer on a row, as GET /api/actions serves it.
type ActionSpec struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Group    string     `json:"group"`         // round | turn | control | workspace | brief | intake
	Key      string     `json:"key,omitempty"` // TUI key
	Surfaces []string   `json:"surfaces"`      // row, detail, tui, menubar, notification, cli, explore
	Input    *InputSpec `json:"input,omitempty"`
	Intake   bool       `json:"intake,omitempty"`
	Toggle   string     `json:"toggle,omitempty"` // paired id sharing the key: hold<->release
	Source   string     `json:"source"`           // "builtin" or the manifest path
	Scope    string     `json:"scope,omitempty"`  // "unit" (default), "item" or "draft"
	Verb     string     `json:"verb,omitempty"`   // GTD verb it fulfills: "next", "waiting", "delegate", "done", "dismiss", "approve", "snooze"; scope draft also "edit", "retry"
	Plugin   string     `json:"plugin,omitempty"` // provider id, from the intent's Def.Provider; "" is core
}

type InputSpec struct {
	Kind        string `json:"kind"` // text | duration | answer
	Required    bool   `json:"required,omitempty"`
	Label       string `json:"label,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Default     string `json:"default,omitempty"`
	UntilState  bool   `json:"until_state,omitempty"`
	Max         int    `json:"max,omitempty"`
}

// ActionState is one action as offered on a row now.
type ActionState struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	Hint    string `json:"hint,omitempty"`
	Label   string `json:"label,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	Confirm string `json:"confirm,omitempty"`
	Basis   string `json:"basis,omitempty"`
	Note    string `json:"note,omitempty"`
}
