package api

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
