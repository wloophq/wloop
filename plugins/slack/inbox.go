package slack

import (
	"context"
	"slices"
	"sync"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
)

// Debounce is how long the inbox gathers an object's deltas before one Touch.
const Debounce = 2 * time.Second

// touchAttempts bounds how often the inbox retries an object whose Touch fails.
const touchAttempts = 3

type entry struct {
	seq   uint64
	delta dslack.Delta
	kind  dslack.ConversationKind
}

// inbox holds admitted deltas by object until their debounce ends, then
// touches every object due at once; a failed Touch keeps them for a retry.
type inbox struct {
	debounce time.Duration
	touch    func(ctx context.Context, objects []string) error
	log      func(format string, args ...any)

	mu      sync.Mutex
	seq     uint64
	pending map[string][]entry
	due     map[string]time.Time
	fails   map[string]int
	wake    chan struct{}
}

func newInbox(debounce time.Duration, touch func(context.Context, []string) error, log func(string, ...any)) *inbox {
	if debounce <= 0 {
		debounce = Debounce
	}
	return &inbox{debounce: debounce, touch: touch, log: log, pending: map[string][]entry{}, due: map[string]time.Time{},
		fails: map[string]int{}, wake: make(chan struct{}, 1)}
}

func (b *inbox) add(object string, kind dslack.ConversationKind, d dslack.Delta) {
	b.mu.Lock()
	b.seq++
	b.pending[object] = append(b.pending[object], entry{seq: b.seq, delta: d, kind: kind})
	if _, ok := b.due[object]; !ok {
		b.due[object] = time.Now().Add(b.debounce)
	}
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// peek is a copy of the objects' waiting deltas.
func (b *inbox) peek(objects []string) map[string][]entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string][]entry, len(objects))
	for _, o := range objects {
		if es := b.pending[o]; len(es) > 0 {
			out[o] = slices.Clone(es)
		}
	}
	return out
}

// message is the latest waiting message at ts in conv.
func (b *inbox) message(conv, ts string) (dslack.Message, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var found dslack.Message
	var seq uint64
	for _, es := range b.pending {
		for _, e := range es {
			d := e.delta
			if d.Conversation == conv && d.TS == ts && d.Message != nil && e.seq > seq {
				found, seq = *d.Message, e.seq
			}
		}
	}
	return found, seq > 0
}

// run touches due objects until ctx ends.
func (b *inbox) run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		next := b.next()
		timer.Stop()
		if !next.IsZero() {
			timer.Reset(time.Until(next))
		}
		var fire <-chan time.Time
		if !next.IsZero() {
			fire = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
			continue
		case <-fire:
		}
		objects, marks := b.takeDue(time.Now())
		if len(objects) == 0 {
			continue
		}
		err := b.touch(ctx, objects)
		if ctx.Err() != nil {
			return
		}
		b.settle(objects, marks, err)
	}
}

func (b *inbox) next() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	var next time.Time
	for _, at := range b.due {
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}
	return next
}

// takeDue are the objects due at now, sorted, with the last seq of each.
func (b *inbox) takeDue(now time.Time) ([]string, map[string]uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var objects []string
	marks := map[string]uint64{}
	for o, at := range b.due {
		if at.After(now) {
			continue
		}
		delete(b.due, o)
		objects = append(objects, o)
		if es := b.pending[o]; len(es) > 0 {
			marks[o] = es[len(es)-1].seq
		}
	}
	slices.Sort(objects)
	return objects, marks
}

// settle drops what a successful Touch covered; a failed one is retried
// after the debounce, at most touchAttempts times.
func (b *inbox) settle(objects []string, marks map[string]uint64, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, o := range objects {
		if err != nil {
			b.fails[o]++
			if b.fails[o] < touchAttempts {
				b.due[o] = time.Now().Add(b.debounce)
				continue
			}
			b.logf("slack inbox: dropping %d deltas of %s after %d failed passes: %v", len(b.pending[o]), o, b.fails[o], err)
		}
		delete(b.fails, o)
		es := slices.DeleteFunc(b.pending[o], func(e entry) bool { return e.seq <= marks[o] })
		if len(es) == 0 {
			delete(b.pending, o)
			continue
		}
		b.pending[o] = es
		if _, ok := b.due[o]; !ok {
			b.due[o] = time.Now().Add(b.debounce)
		}
	}
}

func (b *inbox) logf(format string, args ...any) {
	if b.log != nil {
		b.log(format, args...)
	}
}
