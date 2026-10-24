package proposals

import (
	"context"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
)

// Watch runs a pass and a stamp at once, then a pass debounce after a TopicEntries or TopicItems note, a stamp
// debounce after a TopicDrafts note, and a pass every period for staleness, until ctx ends.
func (j *Job) Watch(ctx context.Context, changes port.Changes, debounce, every time.Duration) {
	var mu sync.Mutex
	pass, stamp := true, true
	kick := make(chan struct{}, 1)
	if changes != nil {
		cancel := changes.Subscribe(func(c port.Change) {
			p, s := c.Has(port.TopicEntries|port.TopicItems), c.Has(port.TopicDrafts)
			if !p && !s {
				return
			}
			mu.Lock()
			pass, stamp = pass || p, stamp || s
			mu.Unlock()
			select {
			case kick <- struct{}{}:
			default:
			}
		})
		defer cancel()
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		mu.Lock()
		doPass, doStamp := pass, stamp
		pass, stamp = false, false
		mu.Unlock()
		if doStamp {
			if err := j.Stamp(ctx); err != nil {
				j.logf("proposals: stamping sent nudges: %v", err)
			}
		}
		if doPass {
			sum, err := j.Run(ctx)
			if err != nil {
				j.logf("proposals: %v", err)
			}
			if sum.Proposed+sum.Discarded > 0 {
				j.logf("proposals: %s", sum)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			mu.Lock()
			pass = true
			mu.Unlock()
		case <-kick:
			wait := time.NewTimer(debounce)
			select {
			case <-ctx.Done():
				wait.Stop()
				return
			case <-wait.C:
			}
		}
	}
}
