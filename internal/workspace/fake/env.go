package fake

const (
	HandleVar   = "FAKE_TERMINAL"
	PaneVar     = "FAKE_PANE"
	WorktreeVar = "FAKE_WORKTREE"
)

// SessionEnv reads the identity the fake manager gives a terminal's session.
type SessionEnv struct{}

func (SessionEnv) Handle(getenv func(string) string) string { return getenv(HandleVar) }

func (SessionEnv) Pane(getenv func(string) string) string { return getenv(PaneVar) }

func (SessionEnv) WorktreeID(getenv func(string) string) string { return getenv(WorktreeVar) }
