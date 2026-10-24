// Package slack is the Slack source: ingestion, rows, cards and the
// plugin's capabilities.
package slack

import (
	"sync"
	"time"

	dslack "github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/slack/socket"
)

// Metrics are the Slack source's in-memory counters, safe for concurrent
// use; the zero value is ready.
type Metrics struct {
	mu  sync.Mutex
	now func() time.Time

	received, admitted, missed window
	byVia                      map[dslack.Via]int
	rateLimited                map[string]int

	socket    socketSnapshot
	hellos    int
	downSince time.Time
	seen      bool

	lastCatchUp *catchUpSnapshot
	backfill    backfillSnapshot

	warnOrder []string
	warn      map[string]func() []string
}

// window counts events of the last hour in one-minute buckets.
type window struct {
	counts  [60]int
	minutes [60]int64
}

func (w *window) add(at time.Time, n int) {
	m := at.Unix() / 60
	i := m % 60
	if w.minutes[i] != m {
		w.minutes[i], w.counts[i] = m, 0
	}
	w.counts[i] += n
}

func (w *window) sum(at time.Time) int {
	m := at.Unix() / 60
	total := 0
	for i, bm := range w.minutes {
		if bm > m-60 && bm <= m {
			total += w.counts[i]
		}
	}
	return total
}

type socketSnapshot struct {
	State          string    `json:"state"` // connected, connecting, down
	NumConnections int       `json:"num_connections"`
	ConnectedAt    time.Time `json:"connected_at,omitzero"`
	LastDisconnect string    `json:"last_disconnect,omitempty"`
	Reconnects     int       `json:"reconnects"`
}

type eventsSnapshot struct {
	Received int `json:"received"`
	Admitted int `json:"admitted"`
}

type catchUpSnapshot struct {
	At     time.Time `json:"at"`
	Reason string    `json:"reason"`
	Deltas int       `json:"deltas"`
}

type backfillSnapshot struct {
	Done          bool `json:"done"`
	Conversations int  `json:"conversations"`
	Threads       int  `json:"threads"`
	Deltas        int  `json:"deltas"`
}

// snapshot is what health reads: the detail's counters, the time the
// socket went down and the warnings of every Warn source.
type snapshot struct {
	Socket               socketSnapshot   `json:"socket"`
	EventsPerHour        eventsSnapshot   `json:"events_per_hour"`
	DeltasByVia          map[string]int   `json:"deltas_by_via"`
	MissedWhileConnected int              `json:"missed_while_connected"`
	RateLimited          map[string]int   `json:"rate_limited"`
	LastCatchUp          *catchUpSnapshot `json:"last_catchup,omitempty"`
	Backfill             backfillSnapshot `json:"backfill"`
	DownSince            time.Time        `json:"-"`
	Warnings             []string         `json:"-"`
}

var vias = []dslack.Via{dslack.ViaSocket, dslack.ViaCatchUp, dslack.ViaProbe, dslack.ViaSearch, dslack.ViaFollow}

func (m *Metrics) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *Metrics) Received() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received.add(m.clock(), 1)
}

func (m *Metrics) Admitted(v dslack.Via) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.admitted.add(m.clock(), 1)
	if m.byVia == nil {
		m.byVia = map[dslack.Via]int{}
	}
	m.byVia[v]++
}

func (m *Metrics) Socket(s socket.State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at := s.At
	if at.IsZero() {
		at = m.clock()
	}
	m.seen = true
	switch s.Kind {
	case "hello":
		m.hellos++
		m.socket.State = "connected"
		m.socket.NumConnections = s.NumConnections
		m.socket.ConnectedAt = at
		m.downSince = time.Time{}
		return
	case "disconnect":
		m.socket.LastDisconnect = s.Reason
		m.socket.State = "connecting"
	case "dropped":
		m.socket.LastDisconnect = "dropped"
		m.socket.State = "down"
	case "overflow":
		m.socket.LastDisconnect = "overflow"
		return
	default:
		if m.socket.State != "connected" {
			m.socket.State = "connecting"
		}
	}
	if m.downSince.IsZero() {
		m.downSince = at
	}
}

func (m *Metrics) CatchUp(reason string, deltas, missedWhileConnected int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	m.lastCatchUp = &catchUpSnapshot{At: now, Reason: reason, Deltas: deltas}
	m.missed.add(now, missedWhileConnected)
}

// RateLimited is wired to slack.Client.OnRateLimited.
func (m *Metrics) RateLimited(method string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rateLimited == nil {
		m.rateLimited = map[string]int{}
	}
	m.rateLimited[method]++
}

func (m *Metrics) Backfill(conversations, threads, deltas int, done bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backfill = backfillSnapshot{Done: done, Conversations: conversations, Threads: threads, Deltas: deltas}
}

// Warn registers fn as the warnings of source, e.g. the extraction breaker; a second call replaces it.
func (m *Metrics) Warn(source string, fn func() []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.warn == nil {
		m.warn = map[string]func() []string{}
	}
	if _, ok := m.warn[source]; !ok {
		m.warnOrder = append(m.warnOrder, source)
	}
	m.warn[source] = fn
}

func (m *Metrics) snapshot() snapshot {
	m.mu.Lock()
	now := m.clock()
	s := snapshot{
		Socket:               m.socket,
		EventsPerHour:        eventsSnapshot{Received: m.received.sum(now), Admitted: m.admitted.sum(now)},
		DeltasByVia:          make(map[string]int, len(vias)),
		MissedWhileConnected: m.missed.sum(now),
		RateLimited:          make(map[string]int, len(m.rateLimited)),
		Backfill:             m.backfill,
		DownSince:            m.downSince,
	}
	if !m.seen {
		s.Socket.State = "down"
	}
	s.Socket.Reconnects = max(m.hellos-1, 0)
	for _, v := range vias {
		s.DeltasByVia[string(v)] = m.byVia[v]
	}
	for k, n := range m.rateLimited {
		s.RateLimited[k] = n
	}
	if m.lastCatchUp != nil {
		c := *m.lastCatchUp
		s.LastCatchUp = &c
	}
	fns := make([]func() []string, 0, len(m.warnOrder))
	for _, src := range m.warnOrder {
		fns = append(fns, m.warn[src])
	}
	m.mu.Unlock()
	for _, fn := range fns {
		s.Warnings = append(s.Warnings, fn()...)
	}
	return s
}
