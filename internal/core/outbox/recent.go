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
// Fresh: the read in flight, else the last one while it is fresh, else a new one. A read runs past the
// caller that started it, bounded by ReadTimeout, so the next caller finds its answer; a caller waits for it
// only until its own context ends.
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
	reads map[string]*basisRead
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

func (b *RecentBases) Live(ctx context.Context, publisher unit.ProviderID, u unit.Unit, kind string) (string, error) {
	return b.Bases.Live(ctx, publisher, u, kind)
}

func (b *RecentBases) Recent(ctx context.Context, publisher unit.ProviderID, u unit.Unit, kind string) (string, error) {
	r := b.read(publisher, u, kind)
	select {
	case <-r.done:
		return r.live, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b *RecentBases) read(publisher unit.ProviderID, u unit.Unit, kind string) *basisRead {
	r, started := b.shared(string(publisher)+"\x00"+u.String()+"\x00"+kind, b.Clock.Now())
	if !started {
		return r
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
	} else {
		b.Reads.Go(run)
	}
	return r
}

// shared is the read of key in flight or fresh at now, else a new one, started true, that its caller runs.
func (b *RecentBases) shared(key string, now time.Time) (r *basisRead, started bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r, ok := b.reads[key]; ok && now.Sub(r.at) < b.Fresh {
		return r, false
	}
	if b.reads == nil {
		b.reads = map[string]*basisRead{}
	}
	r = &basisRead{done: make(chan struct{}), at: now}
	b.reads[key] = r
	return r, true
}
