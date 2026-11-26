package outbox

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Recent is optional: Bases that also serve a read of a unit's basis made a while ago, to a reader that
// takes one, as Staleness does for a draft's card. Live still reads now.
type Recent interface {
	Recent(ctx context.Context, publisher unit.ProviderID, u unit.Unit, kind string) (string, error)
}

// RecentBases are Bases whose Recent shares one read per publisher, unit and kind among every caller within
// Fresh, and starts a new one once Fresh passed. A caller takes the answer of the last read that gave one,
// failed or not, at once, even while a newer read is on its way, for at most Fresh and ReadTimeout after that
// read started; it waits, only until its own context ends, only for a key no read answered within that time. A read runs past the caller that started it, bounded by
// ReadTimeout, so the next caller finds its answer.
type RecentBases struct {
	Bases Bases
	Clock port.Clock
	// Fresh is how long a read serves; ReadTimeout bounds one read.
	Fresh, ReadTimeout time.Duration
	// Reads runs the reads that outlive their caller; its owner waits on it before what Bases reach goes away.
	// Nil reads in the caller.
	Reads *sync.WaitGroup
	// Log reports a read that failed for another reason than a deadline; nil reports none.
	Log func(format string, args ...any)

	mu    sync.Mutex
	reads map[string]*basisReads
}

var (
	_ Bases  = (*RecentBases)(nil)
	_ Recent = (*RecentBases)(nil)
)

// basisRead is one live read: done closes once live or err is set, at when it started.
type basisRead struct {
	done chan struct{}
	live string
	err  error
	at   time.Time
}

func (r *basisRead) answered() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// basisReads are a key's newest read and the newest one that answered.
type basisReads struct{ newest, answered *basisRead }

func (b *RecentBases) Live(ctx context.Context, publisher unit.ProviderID, u unit.Unit, kind string) (string, error) {
	return b.Bases.Live(ctx, publisher, u, kind)
}

func (b *RecentBases) Recent(ctx context.Context, publisher unit.ProviderID, u unit.Unit, kind string) (string, error) {
	r, answer := b.read(publisher, u, kind)
	if answer != nil {
		return answer.live, answer.err
	}
	select {
	case <-r.done:
		return r.live, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// read is the key's newest read, started now when none is on its way or fresh, and the newest that answered,
// nil when none has.
func (b *RecentBases) read(publisher unit.ProviderID, u unit.Unit, kind string) (newest, answer *basisRead) {
	r, answer, started := b.shared(string(publisher)+"\x00"+u.String()+"\x00"+kind, b.Clock.Now())
	if !started {
		return r, answer
	}
	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), b.ReadTimeout)
		defer cancel()
		r.live, r.err = b.Bases.Live(ctx, publisher, u, kind)
		if r.err != nil && !errors.Is(r.err, context.DeadlineExceeded) && !errors.Is(r.err, context.Canceled) && b.Log != nil {
			b.Log("drafts: the live basis of %s: %v", u, r.err)
		}
		close(r.done)
	}
	if b.Reads == nil {
		run()
		return r, r
	}
	b.Reads.Go(run)
	return r, answer
}

// shared is the read of key on its way or fresh at now, else a new one, started true, that its caller runs, with
// the newest read of key that answered. Every other key whose newest read answered and serves no caller any more,
// Fresh and ReadTimeout after it started, is let go.
func (b *RecentBases) shared(key string, now time.Time) (r, answer *basisRead, started bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reads == nil {
		b.reads = map[string]*basisReads{}
	}
	for k, rs := range b.reads {
		if k != key && rs.newest.answered() && now.Sub(rs.newest.at) >= b.Fresh+b.ReadTimeout {
			delete(b.reads, k)
		}
	}
	rs := b.reads[key]
	if rs == nil {
		rs = &basisReads{}
		b.reads[key] = rs
	}
	if rs.newest != nil && rs.newest.answered() {
		rs.answered = rs.newest
	}
	if rs.answered != nil && now.Sub(rs.answered.at) >= b.Fresh+b.ReadTimeout {
		rs.answered = nil
	}
	if rs.newest != nil && (!rs.newest.answered() || now.Sub(rs.newest.at) < b.Fresh) {
		return rs.newest, rs.answered, false
	}
	rs.newest = &basisRead{done: make(chan struct{}), at: now}
	return rs.newest, rs.answered, true
}
