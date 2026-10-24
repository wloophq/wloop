package cli

import (
	"context"
	"sync"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/daemon"
)

// stepEvents records each event a step publishes on its unit, then passes
// it on.
type stepEvents struct {
	rec interface {
		RecordEvents(context.Context, ...port.Event) error
	}
	next port.Publisher
	log  func(format string, args ...any)
}

func (p stepEvents) Publish(evs ...port.Event) {
	if err := p.rec.RecordEvents(context.Background(), evs...); err != nil && p.log != nil {
		p.log("recording the events of a step: %v", err)
	}
	if p.next != nil {
		p.next.Publish(evs...)
	}
}

// noticeFeed turns each notify.self event into a notice for whoever
// follows it, the daemon's event stream.
type noticeFeed struct {
	mu   sync.Mutex
	next int
	fns  map[int]func(api.Notice)
}

var _ daemon.Notices = (*noticeFeed)(nil)

func (f *noticeFeed) Follow(fn func(api.Notice)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fns == nil {
		f.fns = map[int]func(api.Notice){}
	}
	id := f.next
	f.next++
	f.fns[id] = fn
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		delete(f.fns, id)
	}
}

func (f *noticeFeed) Publish(evs ...port.Event) {
	f.mu.Lock()
	fns := make([]func(api.Notice), 0, len(f.fns))
	for _, fn := range f.fns {
		fns = append(fns, fn)
	}
	f.mu.Unlock()
	for _, ev := range evs {
		if ev.Kind != kindNotifySelf {
			continue
		}
		n := api.Notice{ID: ev.Key, Workflow: notifyWorkflow(ev.Key), Unit: ev.Unit, Text: ev.Data}
		for _, fn := range fns {
			fn(n)
		}
	}
}
