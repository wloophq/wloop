package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
)

const (
	pingEvery = 30 * time.Second
	// healthQuiet folds a burst of health notes, such as a job's meta
	// writes and its end, into one message.
	healthQuiet = 2 * time.Second
)

// notifierClient is the subscriber that posts the notifications.
const notifierClient = "app"

type hub struct {
	mu     sync.Mutex
	subs   map[*subscriber]struct{}
	closed bool
	// pos is where the rows were at the last row change published.
	pos api.Since
	// feed serializes row publishes, so each starts where the previous one
	// left pos.
	feed sync.Mutex
}

// subscriber holds the changes published since its stream last wrote,
// merged into one.
type subscriber struct {
	client  string
	signal  chan struct{}
	mu      sync.Mutex
	pending *api.Change
}

func newHub() *hub { return &hub{subs: map[*subscriber]struct{}{}} }

// subscribe returns a subscriber whose signal holds at most one pending
// wake and is closed when the hub shuts down.
func (h *hub) subscribe(client string) *subscriber {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub := &subscriber{client: client, signal: make(chan struct{}, 1)}
	if h.closed {
		close(sub.signal)
		return sub
	}
	h.subs[sub] = struct{}{}
	return sub
}

func (h *hub) has(client string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		if sub.client == client {
			return true
		}
	}
	return false
}

func (h *hub) unsubscribe(sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[sub]; ok {
		delete(h.subs, sub)
		close(sub.signal)
	}
}

func (h *hub) position() api.Since {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pos
}

func (h *hub) moveTo(pos api.Since) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pos = pos
}

func (h *hub) publish(c api.Change) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !c.Health && len(c.Notices) == 0 {
		h.pos = api.Since{Epoch: c.Epoch, Rev: c.Rev}
	}
	for sub := range h.subs {
		sub.add(c)
	}
}

func (h *hub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for sub := range h.subs {
		delete(h.subs, sub)
		close(sub.signal)
	}
}

func (sub *subscriber) add(c api.Change) {
	sub.mu.Lock()
	if sub.pending == nil {
		c.Units, c.Notices = slices.Clone(c.Units), slices.Clone(c.Notices)
		sub.pending = &c
	} else {
		sub.pending.Merge(c)
	}
	sub.mu.Unlock()
	select {
	case sub.signal <- struct{}{}:
	default:
	}
}

func (sub *subscriber) take() (api.Change, bool) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.pending == nil {
		return api.Change{}, false
	}
	c := *sub.pending
	sub.pending = nil
	return c, true
}

// publishRows publishes what moved in the rows since the last row change
// published; the first call only learns where the rows are.
func (s *Server) publishRows(ctx context.Context) {
	s.hub.feed.Lock()
	defer s.hub.feed.Unlock()
	pos := s.hub.position()
	c, err := s.readModel().Since(ctx, pos)
	if err != nil {
		s.logf("events: %v", err)
		return
	}
	if pos == (api.Since{}) || (!c.All && len(c.Units) == 0) {
		s.hub.moveTo(api.Since{Epoch: c.Epoch, Rev: c.Rev})
		return
	}
	s.hub.publish(c)
}

// feedRows keeps the read model current and publishes each revision it
// moves to, until ctx ends.
func (s *Server) feedRows(ctx context.Context) {
	publish := func() {
		ctx, scope := meter.Start(ctx, meter.Loop("feed"))
		defer scope.End()
		s.publishRows(ctx)
	}
	publish()
	s.readModel().Run(ctx, func(c api.Change) {
		publish()
		s.fanOutRows(c)
	})
}

// feedHealth publishes one health message per burst of health notes, until
// ctx ends.
func (s *Server) feedHealth(ctx context.Context) {
	if s.deps.Changes == nil {
		return
	}
	poke, cancel := change.Poke(s.deps.Changes, port.HealthInputs)
	defer cancel()
	var due <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-poke:
			if due == nil {
				due = time.After(healthQuiet)
			}
		case <-due:
			due = nil
			pos := s.hub.position()
			s.hub.publish(api.Change{Type: "changed", Epoch: pos.Epoch, Rev: pos.Rev, Health: true})
		}
	}
}

// Notices are the notifications the workflows raise for the user.
type Notices interface {
	// Follow calls fn with each notice raised until stop is called.
	Follow(fn func(api.Notice)) (stop func())
}

// notify streams n to the subscribers at the rows' current position.
func (s *Server) notify(n api.Notice) {
	if !n.Unit.IsZero() && n.Label == "" {
		n.Label = s.unitLabel(n.Unit)
	}
	pos := s.hub.position()
	s.hub.publish(api.Change{Type: "notify", Epoch: pos.Epoch, Rev: pos.Rev, Notices: []api.Notice{n}})
}

func (s *Server) note(t port.Topic, units ...unit.Unit) {
	if s.deps.Changes != nil {
		s.deps.Changes.Note(t, units...)
	}
}

// resumeFrom is the position a reconnecting client gives in Last-Event-ID
// or ?since=; one that does not parse is of no epoch this daemon has.
func resumeFrom(r *http.Request) api.Since {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("since")
	}
	since, err := api.ParseSince(raw)
	if err != nil {
		return api.Since{Epoch: raw}
	}
	return since
}

func eventMessage(c api.Change) (string, error) {
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	if c.Epoch == "" {
		return "data: " + string(data) + "\n\n", nil
	}
	return fmt.Sprintf("id: %s\ndata: %s\n\n", api.Since{Epoch: c.Epoch, Rev: c.Rev}, data), nil
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	sub := s.hub.subscribe(r.URL.Query().Get("client"))
	defer s.hub.unsubscribe(sub)
	if s.hub.position() == (api.Since{}) {
		s.publishRows(r.Context())
	}
	first := ": connected\n\n"
	if since := resumeFrom(r); since != (api.Since{}) {
		c, err := s.readModel().Since(r.Context(), since)
		if err != nil {
			s.logf("events: %v", err)
			pos := s.hub.position()
			c = api.Change{Type: "changed", Epoch: pos.Epoch, Rev: pos.Rev, All: true}
		}
		if c.All || len(c.Units) > 0 {
			if msg, err := eventMessage(c); err == nil {
				first += msg
			}
		}
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	if _, err := fmt.Fprint(w, first); err != nil {
		return
	}
	flusher.Flush()
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	for {
		var msg string
		select {
		case <-r.Context().Done():
			return
		case _, open := <-sub.signal:
			if !open {
				return
			}
			c, ok := sub.take()
			if !ok {
				continue
			}
			var err error
			if msg, err = eventMessage(c); err != nil {
				s.logf("events: %v", err)
				continue
			}
		case <-ping.C:
			msg = ": ping\n\n"
		}
		if _, err := fmt.Fprint(w, msg); err != nil { //nolint:gosec // G705: a text/event-stream of JSON events, not HTML
			return
		}
		flusher.Flush()
	}
}
