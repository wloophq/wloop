package source

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
)

// NotifyTimeout bounds one plugin's Notify, so a plugin that hangs delays
// the others' by no more.
const NotifyTimeout = 10 * time.Second

// Notify tells each running plugin of the host changes its manifest's
// notify subscribes to, from the changes after from until ctx ends, one
// note at a time. A plugin hears of its own units only: a note naming
// units, none of them its, is not sent to it, and a note of every unit, or
// of none, is sent with no unit.
func (k *Kind) Notify(ctx context.Context, changes port.Changes, from port.Generation) {
	topics := HostTopics(plugin.Topics...)
	var mu sync.Mutex
	var queue []port.Change
	poke := make(chan struct{}, 1)
	cancel := changes.Subscribe(func(c port.Change) {
		if !c.Has(topics) {
			return
		}
		mu.Lock()
		queue = append(queue, c)
		mu.Unlock()
		select {
		case poke <- struct{}{}:
		default:
		}
	})
	defer cancel()
	missed := changes.Since(from, topics)
	if !missed.Empty() {
		k.notify(ctx, missed)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-poke:
		}
		mu.Lock()
		next := queue
		queue = nil
		mu.Unlock()
		for _, c := range next {
			if c.Gen > missed.Gen {
				c.Topics &= topics
				k.notify(ctx, c)
			}
		}
	}
}

func (k *Kind) notify(ctx context.Context, c port.Change) {
	for _, s := range k.subscribers(WireTopics(c.Topics)) {
		var units []plugin.Unit
		if !c.All {
			for _, u := range c.Units {
				if u.Provider == unit.ProviderID(s.id) {
					units = append(units, Wire(u))
				}
			}
			if len(c.Units) > 0 && len(units) == 0 {
				continue
			}
		}
		nctx, cancel := context.WithTimeout(ctx, NotifyTimeout)
		err := s.client.Notify(nctx, units, s.topics)
		cancel()
		if err != nil && ctx.Err() == nil && k.Logf != nil {
			k.Logf("plugin %s: notify %v: %v", s.id, s.topics, err)
		}
	}
}
