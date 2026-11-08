package slack

import (
	"context"
	"errors"
	"sync"
	"time"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/host"
	"github.com/wloophq/wloop/plugins/slack/store"
)

// waker asks the host to run the extraction when it has work: when the quiet time of a change that landed ends,
// and at once for a brief a first turn wants. The host keeps the earliest wake, so a wake no earlier than one it
// holds is not sent.
type waker struct {
	host  host.Broker
	now   func() time.Time
	quiet func() time.Duration
	log   func(format string, args ...any)
	kick  chan struct{}

	mu sync.Mutex
	// want is the earliest wake not sent yet; held the earliest the host holds, until it passes.
	want, held time.Time
}

func newWaker(p *plugin) *waker {
	w := &waker{host: p.host, now: p.now, quiet: p.all.Slack.Extraction.QuietFor, log: p.logf, kick: make(chan struct{}, 1)}
	p.db.OnChange(func(c store.Change) {
		if c.Landed {
			w.at(w.now().Add(w.quiet()))
		}
	})
	return w
}

// at asks for the extraction at t, unless a wake no later is pending or held.
func (w *waker) at(t time.Time) {
	w.mu.Lock()
	held := w.held.After(w.now()) && !t.Before(w.held)
	pending := !w.want.IsZero() && !t.Before(w.want)
	if !held && !pending {
		w.want = t
	}
	w.mu.Unlock()
	if !held && !pending {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
}

// flush sends the pending wake; a wake the host refuses is left to the job's interval.
func (w *waker) flush(ctx context.Context) {
	w.mu.Lock()
	t := w.want
	w.want = time.Time{}
	w.mu.Unlock()
	if t.IsZero() || w.host.Host.Jobs == nil {
		return
	}
	if err := w.host.Host.Jobs.WakeJob(ctx, JobExtract, t); err != nil {
		if !errors.Is(err, src.ErrUnsupported) {
			w.log("slack extraction: wake at %s: %v", t.Format(time.TimeOnly), err)
		}
		return
	}
	w.mu.Lock()
	if !w.held.After(w.now()) || t.Before(w.held) {
		w.held = t
	}
	w.mu.Unlock()
}

// run sends each wake asked until ctx ends.
func (w *waker) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
			w.flush(ctx)
		}
	}
}
