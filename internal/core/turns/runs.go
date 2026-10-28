package turns

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
)

// Run is one turn on a task as its timeline shows it: the workspace manager,
// workspace path and worktree it ran in, its agent session, the session's
// live state and the command that resumes it. A headless call has no
// Manager, Workspace or Session.
type Run struct {
	Turn          turn.Turn
	Manager       string
	Workspace     string
	Worktree      string
	Session       string
	State         string
	Resume        string
	HasTranscript bool
}

// TranscriptLine is one message of an agent session's transcript.
type TranscriptLine struct {
	Role string
	Text string
	At   time.Time
}

// Transcripts pages an agent session's transcript.
type Transcripts interface {
	// Page is at most limit lines from line from (0-based), oldest first,
	// and the transcript's line count.
	Page(ctx context.Context, agentSession string, from, limit int) ([]TranscriptLine, int, error)
}
