package row

import (
	"time"

	"github.com/wloophq/wloop/plugins/github/pr"
)

// Payload is the source part of a row's payload, in the keys the clients
// read; the host adds its own: the mute, the brief, the intake and the
// turns.
type Payload struct {
	Author    string     `json:"author"`
	Repo      string     `json:"repo"`
	Head      string     `json:"head"`
	Approved  bool       `json:"approved"`
	Reasons   []string   `json:"reasons"`
	Workspace Workspace  `json:"workspace"`
	Override  Override   `json:"override,omitzero"`
	Facts     *Facts     `json:"facts,omitempty"`
	Viewer    string     `json:"viewer"`
	LastHuman *LastHuman `json:"last_human,omitempty"`
}

// Workspace is the workspace a row shows.
type Workspace struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"display_name"`
	Status      string   `json:"status"`
	IsReview    bool     `json:"is_review"`
	AgentStates []string `json:"agent_states"`
	Comment     string   `json:"comment"`
	IsMain      bool     `json:"is_main"`
}

// WorkspaceOf is w as a row shows it.
func WorkspaceOf(w pr.Workspace) Workspace {
	return Workspace{ID: w.ID, DisplayName: w.DisplayName, Status: w.Status, IsReview: w.IsReview, AgentStates: nonNil(w.AgentStates),
		Comment: w.Comment, IsMain: w.IsMain}
}

// Override is what the user set on the pull request that the plugin keeps.
type Override struct {
	Adopted bool `json:"adopted,omitempty"`
	Tracked bool `json:"tracked,omitempty"`
}

// Facts are the pull request's facts a row shows.
type Facts struct {
	CreatedAt    time.Time `json:"created_at,omitzero"`
	ChangedFiles int       `json:"changed_files"`
	Checks       Checks    `json:"checks"`
}

type Checks struct {
	State   string   `json:"state"`
	Failing []string `json:"failing,omitempty"`
}

// LastHuman is the newest human event on the pull request not by the
// viewer.
type LastHuman struct {
	Who  string    `json:"who"`
	What string    `json:"what"` // the event kind: opened, push, review, comment
	At   time.Time `json:"at"`
	Link string    `json:"link"`
}
