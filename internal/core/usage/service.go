package usage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/usage"
)

type Service struct {
	Store     Store
	ReaderFor func(kind string) Reader
	Clock     port.Clock
	// Log gets the first reader error of each run; nil drops it.
	Log func(format string, args ...any)
}

var (
	errUnavailable = errors.New("no usage to read")
	errNotYet      = errors.New("the agent session may still arrive")
)

// Run reads the usage of up to Batch pending turns. A turn whose reader
// fails stays pending for the next run, and is unavailable once it failed
// MaxAttempts times.
func (s Service) Run(ctx context.Context) (string, error) {
	now := s.Clock.Now()
	pending, err := s.Store.Pending(ctx, now.Add(-Window), Batch)
	if err != nil {
		return "", err
	}
	var read, unavailable, retried int
	logged := false
	for _, t := range pending {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rows, err := s.read(ctx, t, now)
		source := SourceTranscript
		switch {
		case errors.Is(err, errNotYet):
			continue
		case errors.Is(err, errUnavailable), errors.Is(err, ErrNoTranscript):
			source, rows = SourceUnavailable, nil
		case err != nil:
			if !logged && s.Log != nil {
				s.Log("usage: turn %d of %s: %v", t.Seq, t.PR, err)
				logged = true
			}
			if t.Attempts+1 < MaxAttempts {
				if err := s.Store.Attempted(ctx, t.Seq); err != nil {
					return "", err
				}
				retried++
				continue
			}
			source, rows = SourceUnavailable, nil
		}
		ev := port.Event{Unit: t.PR.Unit(), Kind: KindTurnUsage, Key: strconv.FormatInt(t.Seq, 10), Data: source, At: now}
		if err := s.Store.SaveUsage(ctx, t.Seq, rows, source, now, ev); err != nil {
			return "", err
		}
		if source == SourceTranscript {
			read++
		} else {
			unavailable++
		}
	}
	return fmt.Sprintf("usage: %d read, %d unavailable, %d retried", read, unavailable, retried), nil
}

func (s Service) read(ctx context.Context, t Turn, now time.Time) ([]usage.Model, error) {
	var r Reader
	if s.ReaderFor != nil {
		r = s.ReaderFor(t.Kind)
	}
	switch {
	case r == nil:
		return nil, errUnavailable
	case t.AgentSession == "" && now.Sub(t.To) > Late:
		return nil, errUnavailable
	case t.AgentSession == "":
		return nil, errNotYet
	}
	return r.Usage(ctx, t)
}
