package proposals

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
)

// Watch runs a pass at once, then a pass debounce after a TopicEntries or
// TopicItems note, and a pass every period for staleness, until ctx ends.
func (j *Job) Watch(ctx context.Context, changes port.Changes, debounce, every time.Duration) {
	kick := make(chan struct{}, 1)
	if changes != nil {
		cancel := changes.Subscribe(func(c port.Change) {
			if !c.Has(port.TopicEntries | port.TopicItems) {
				return
			}
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
		sum, err := j.Run(ctx)
		if err != nil {
			j.logf("proposals: %v", err)
		}
		if sum.Proposed+sum.Discarded > 0 {
			j.logf("proposals: %s", sum)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
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
