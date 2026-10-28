package slack

import (
	"sync"
	"time"

	"github.com/wloophq/wloop/plugins/slack/socket"
)

// Catch-up reasons, as health reports them in last_catchup.
const (
	ReasonStart     = "start"
	ReasonReconnect = "reconnect"
	ReasonOverflow  = "overflow"
	ReasonClockJump = "clock_jump"
	ReasonDown      = "down_5m"
	ReasonDropped   = "touch_failed"
)

const (
	// GapTick is how often the gap detector compares the clocks.
	GapTick = 30 * time.Second
	// ClockJump is how far the wall clock must outrun the monotonic one to
	// count as a wake from sleep: on macOS the monotonic clock stops asleep.
	ClockJump = 20 * time.Minute
	// DownGap is how long the socket may stay down before a catch-up runs
	// without waiting for the reconnect; Slack redelivers for about 6 min.
	DownGap = 5 * time.Minute
	// deliveryLag is how late the socket may deliver a message and still
	// not count as missed while connected.
	deliveryLag = 30 * time.Second
	keptPeriods = 64
)

type period struct{ from, to time.Time }

// gaps marks a gap on a reconnect, an overflow, a wake from sleep and a
// socket down longer than DownGap; the poller takes the pending reason.
type gaps struct {
	wall func() time.Time
	mono func() time.Duration

	mu        sync.Mutex
	hellos    int
	up        bool
	downAt    time.Time
	downFired bool
	lastWall  time.Time
	lastMono  time.Duration
	ticked    bool
	periods   []period
	pending   string
	wake      chan struct{}
}

func newGaps() *gaps {
	start := time.Now()
	return &gaps{
		wall: func() time.Time { return time.Now().Round(0) },
		mono: func() time.Duration { return time.Since(start) },
		wake: make(chan struct{}, 1),
	}
}

// reset starts a stream: the socket is down until its first hello.
func (g *gaps) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hellos, g.up, g.downAt, g.downFired, g.ticked = 0, false, g.wall(), false, false
}

func (g *gaps) state(s socket.State) {
	at := s.At
	if at.IsZero() {
		at = g.wall()
	}
	g.mu.Lock()
	switch s.Kind {
	case socket.StateHello:
		g.hellos++
		if !g.up {
			g.periods = append(g.periods, period{from: at})
			if len(g.periods) > keptPeriods {
				g.periods = g.periods[len(g.periods)-keptPeriods:]
			}
		}
		g.up, g.downFired = true, false
		reconnect := g.hellos > 1
		g.mu.Unlock()
		if reconnect {
			g.mark(ReasonReconnect)
		}
		return
	case socket.StateDisconnect, socket.StateDropped:
		if g.up {
			g.periods[len(g.periods)-1].to = at
			g.up, g.downAt, g.downFired = false, at, false
		}
	case socket.StateOverflow:
		g.mu.Unlock()
		g.mark(ReasonOverflow)
		return
	}
	g.mu.Unlock()
}

// tick marks a clock jump, or a socket down longer than DownGap.
func (g *gaps) tick() {
	wall, mono := g.wall(), g.mono()
	g.mu.Lock()
	jumped := g.ticked && wall.Sub(g.lastWall)-(mono-g.lastMono) > ClockJump
	g.lastWall, g.lastMono, g.ticked = wall, mono, true
	down := !g.up && !g.downFired && !g.downAt.IsZero() && wall.Sub(g.downAt) > DownGap
	if down {
		g.downFired = true
	}
	g.mu.Unlock()
	switch {
	case jumped:
		g.mark(ReasonClockJump)
	case down:
		g.mark(ReasonDown)
	}
}

// mark asks for a catch-up; a reason already pending is kept.
func (g *gaps) mark(reason string) {
	g.mu.Lock()
	if g.pending == "" {
		g.pending = reason
	}
	g.mu.Unlock()
	select {
	case g.wake <- struct{}{}:
	default:
	}
}

func (g *gaps) take() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	r := g.pending
	g.pending = ""
	return r
}

// connectedAt says whether the socket was up at t, long enough before it
// went down for a message posted at t to arrive.
func (g *gaps) connectedAt(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range g.periods {
		if t.Before(p.from) {
			continue
		}
		end := p.to
		if end.IsZero() {
			end = g.wall()
		}
		if t.Before(end.Add(-deliveryLag)) {
			return true
		}
	}
	return false
}
