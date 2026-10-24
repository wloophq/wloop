// Package usage reads the tokens each ended turn spent, through the reader
// of the turn's agent, and stores them once.
package usage

import (
	"context"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/usage"
)

const (
	Window            = 30 * 24 * time.Hour
	Late              = time.Hour
	Batch             = 50
	MaxAttempts       = 5
	SourceTranscript  = "transcript"
	SourceUnavailable = "unavailable"
	KindTurnUsage     = "turn_usage" // Key = turn seq, Data = source
)

// Turn is an ended turn whose usage is not read yet, with its agent session's
// paths and the window its messages fall in.
type Turn struct {
	Seq                                      int64
	PR                                       pr.Ref
	Kind                                     string
	AgentSession, Transcript, Home, Worktree string
	From, To                                 time.Time
	Attempts                                 int
}

type Reader interface {
	Usage(ctx context.Context, t Turn) ([]usage.Model, error)
}

var ErrNoTranscript = errors.New("the transcript is gone")

type Store interface {
	Pending(ctx context.Context, endedSince time.Time, limit int) ([]Turn, error)
	SaveUsage(ctx context.Context, seq int64, rows []usage.Model, source string, at time.Time, evs ...port.Event) error
	Attempted(ctx context.Context, seq int64) error
}
