package api

// ProbeOwner and ProbeRepo name the forge unit of every probe, keyed
// <owner>/<repo>#<n>: one that exists nowhere, so no inventory holds it and
// no view has a role for it.
const (
	ProbeOwner = "loop-e2e"
	ProbeRepo  = "probe"
)

type ProbeRequest struct {
	// WorkspaceID is the workspace's id in its manager, or its worktree path.
	WorkspaceID string `json:"workspace_id"`
	// Home and Model are the agent's; "" is its default.
	Home  string `json:"home,omitempty"`
	Model string `json:"model,omitempty"`
}

// ProbeResult names the probe's pull request, the id of its turn and the
// session the turn was sent to.
type ProbeResult struct {
	PR      string `json:"pr"`
	Turn    string `json:"turn"`
	Session string `json:"session"`
}
