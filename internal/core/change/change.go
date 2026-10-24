// Package change is the daemon's change log: a generation that every
// committed write and every in-memory input owner advances, with the topics
// and units each change touched. It lives in one process and is never
// persisted.
package change

import (
	"crypto/rand"
	"encoding/hex"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// DefaultWindow is how many notes a Log keeps: about an hour of a busy daemon.
const DefaultWindow = 4096

type note struct {
	topics port.Topic
	units  []unit.Unit
	all    bool
}

type subscriber struct {
	fn      func(port.Change)
	dropped atomic.Bool
}

// Log is an in-memory, bounded change log. It satisfies port.Changes.
type Log struct {
	logf  func(format string, args ...any)
	epoch string

	// dispatch serializes notes end to end, so subscribers see generation order.
	dispatch sync.Mutex

	mu   sync.Mutex
	gen  port.Generation
	ring []note
	subs []*subscriber
}

var _ port.Changes = (*Log)(nil)

// NewLog returns an empty log keeping the last window notes; logf reports a
// dropped subscriber and may be nil.
func NewLog(window int, logf func(format string, args ...any)) *Log {
	if window < 1 {
		window = 1
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return &Log{logf: logf, epoch: hex.EncodeToString(b[:]), ring: make([]note, window)}
}

// Note records one committed change. No units is a unit-less change, not All.
func (l *Log) Note(t port.Topic, units ...unit.Unit) port.Change {
	return l.append(note{topics: t, units: normalize(units)})
}

// NoteAll records a change to every unit under t.
func (l *Log) NoteAll(t port.Topic) port.Change {
	return l.append(note{topics: t, all: true})
}

func (l *Log) append(n note) port.Change {
	l.dispatch.Lock()
	defer l.dispatch.Unlock()

	l.mu.Lock()
	l.gen++
	l.ring[int(l.gen%port.Generation(len(l.ring)))] = n
	c := port.Change{Gen: l.gen, Epoch: l.epoch, Topics: n.topics, Units: n.units, All: n.all}
	subs := l.subs
	l.mu.Unlock()

	for _, s := range subs {
		l.call(s, c)
	}
	return cloneChange(c)
}

func (l *Log) call(s *subscriber, c port.Change) {
	if s.dropped.Load() {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			l.logf("change: subscriber panicked at generation %d, unsubscribed: %v", c.Gen, r)
			l.remove(s)
		}
	}()
	s.fn(cloneChange(c))
}

func (l *Log) Gen() port.Generation {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gen
}

func (l *Log) Epoch() string { return l.epoch }

// Since merges the notes after from whose topics meet the filter. A from
// older than the window, or ahead of Gen, returns All with Topics = topics.
func (l *Log) Since(from port.Generation, topics port.Topic) port.Change {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := port.Change{Gen: l.gen, Epoch: l.epoch}
	size := port.Generation(len(l.ring))
	if from > l.gen || (l.gen > size && from < l.gen-size) {
		out.Topics, out.All = topics, true
		return out
	}
	var units []unit.Unit
	for g := from + 1; g <= l.gen; g++ {
		n := l.ring[int(g%size)]
		if n.topics&topics == 0 {
			continue
		}
		out.Topics |= n.topics & topics
		if n.all {
			out.All = true
		}
		if !out.All {
			units = append(units, n.units...)
		}
	}
	if !out.All {
		out.Units = normalize(units)
	}
	return out
}

func (l *Log) Subscribe(fn func(port.Change)) (cancel func()) {
	s := &subscriber{fn: fn}
	l.mu.Lock()
	l.subs = append(slices.Clip(l.subs), s)
	l.mu.Unlock()
	return func() { l.remove(s) }
}

func (l *Log) remove(s *subscriber) {
	if s.dropped.Swap(true) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// A fresh slice: an append in flight may still range over the old one.
	l.subs = slices.DeleteFunc(slices.Clone(l.subs), func(x *subscriber) bool { return x == s })
}

// Poke adapts a subscription to a channel holding at most one pending poke,
// sent for every note whose topics meet the filter.
func Poke(c port.Changes, topics port.Topic) (poke <-chan struct{}, cancel func()) {
	ch := make(chan struct{}, 1)
	cancel = c.Subscribe(func(n port.Change) {
		if !n.Has(topics) {
			return
		}
		select {
		case ch <- struct{}{}:
		default:
		}
	})
	return ch, cancel
}

func normalize(units []unit.Unit) []unit.Unit {
	var out []unit.Unit
	for _, u := range units {
		if !u.IsZero() {
			out = append(out, u)
		}
	}
	slices.SortFunc(out, func(a, b unit.Unit) int {
		switch {
		case a.Less(b):
			return -1
		case b.Less(a):
			return 1
		}
		return 0
	})
	return slices.Compact(out)
}

func cloneChange(c port.Change) port.Change {
	c.Units = slices.Clone(c.Units)
	return c
}
