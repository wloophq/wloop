package orca

const (
	HandleVar   = "ORCA_TERMINAL_HANDLE"
	PaneVar     = "ORCA_PANE_KEY"
	WorktreeVar = "ORCA_WORKTREE_ID"
)

// SessionEnv reads the identity Orca gives a terminal's session.
type SessionEnv struct{}

func (SessionEnv) Handle(getenv func(string) string) string { return getenv(HandleVar) }

func (SessionEnv) Pane(getenv func(string) string) string { return getenv(PaneVar) }

func (SessionEnv) WorktreeID(getenv func(string) string) string { return getenv(WorktreeVar) }
