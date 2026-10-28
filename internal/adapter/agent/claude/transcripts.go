package claude

import (
	"context"
	"errors"
	"fmt"

	"github.com/wloophq/wloop/internal/core/turns"
)

// Transcripts pages a Claude Code session's transcript.
type Transcripts struct{}

var _ turns.Transcripts = Transcripts{}

func (Transcripts) Page(context.Context, string, int, int) ([]turns.TranscriptLine, int, error) {
	return nil, 0, fmt.Errorf("not implemented: U1: %w", errors.ErrUnsupported)
}
