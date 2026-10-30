package slack

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/socket"
)

// EventWindow is how long the stream remembers an event_id: Slack redelivers
// events whose ack it missed, even on a live connection.
const EventWindow = 10 * time.Minute

// stream turns socket envelopes into deltas. socket.Conn acked each
// envelope before it reaches handle; this is the only dedupe by event_id.
type stream struct {
	now     func() time.Time
	team    func() string
	metrics *Metrics
	log     func(format string, args ...any)
	deliver func(ctx context.Context, d dslack.Delta)

	parseErrors atomic.Int64

	mu     sync.Mutex
	seen   map[string]time.Time
	pruned time.Time
}

func (s *stream) handle(ctx context.Context, env socket.Envelope) {
	if env.Type != "events_api" || s.repeated(env.EventID) {
		return
	}
	s.metrics.Received()
	d, ok, err := dslack.FromEvent(s.team(), env.Payload, dslack.ViaSocket, s.now())
	if err != nil {
		s.parseErrors.Add(1)
		s.logf("slack stream: envelope %s: %v", env.ID, err)
		return
	}
	if ok {
		s.deliver(ctx, d)
	}
}

// repeated says whether id was seen in the last EventWindow, and remembers it.
func (s *stream) repeated(id string) bool {
	if id == "" {
		return false
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	if now.Sub(s.pruned) >= time.Minute {
		for k, at := range s.seen {
			if now.Sub(at) >= EventWindow {
				delete(s.seen, k)
			}
		}
		s.pruned = now
	}
	if at, ok := s.seen[id]; ok && now.Sub(at) < EventWindow {
		return true
	}
	s.seen[id] = now
	return false
}

func (s *stream) logf(format string, args ...any) {
	if s.log != nil {
		s.log(format, args...)
	}
}
