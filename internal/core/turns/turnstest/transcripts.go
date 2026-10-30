package turnstest

import (
	"context"
	"sync"

	"github.com/wloophq/wloop/internal/core/turns"
)

// Transcripts is an in-memory turns.Transcripts: Lines by agent session.
type Transcripts struct {
	mu    sync.Mutex
	Lines map[string][]turns.TranscriptLine
}

var _ turns.Transcripts = (*Transcripts)(nil)

func (t *Transcripts) Page(_ context.Context, agentSession string, from, limit int) ([]turns.TranscriptLine, int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	all := t.Lines[agentSession]
	from = min(max(from, 0), len(all))
	end := len(all)
	if limit > 0 {
		end = min(from+limit, len(all))
	}
	return append([]turns.TranscriptLine{}, all[from:end]...), len(all), nil
}
