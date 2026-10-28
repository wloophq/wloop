package source

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
)

// heldNotify is a plugin whose Notify waits until its ctx ends, when hang
// is set, and records how it ended and when.
type heldNotify struct {
	hang  bool
	heard atomic.Int32
	ended error
	took  time.Duration
}

func (h *heldNotify) Notify(ctx context.Context, _ []plugin.Unit, _ []plugin.Topic) error {
	h.heard.Add(1)
	if !h.hang {
		return nil
	}
	start := time.Now()
	<-ctx.Done()
	h.ended, h.took = ctx.Err(), time.Since(start)
	return ctx.Err()
}

func TestAHangingNotifyIsCutAtTheBoundAndDelaysNoOther(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const bound = 50 * time.Millisecond
		hung, other := &heldNotify{hang: true}, &heldNotify{}
		k := &Kind{NotifyTimeout: bound, running: map[string]*attached{
			"acme": {notified: hung, notify: []plugin.Topic{plugin.TopicRounds}},
			"beta": {notified: other, notify: []plugin.Topic{plugin.TopicRounds}},
		}}
		done := make(chan struct{})
		go func() {
			defer close(done)
			k.notify(t.Context(), port.Change{Topics: port.TopicPause})
		}()
		synctest.Wait()
		if other.heard.Load() != 0 {
			t.Fatal("beta heard before acme's bound ended")
		}
		<-done
		if !errors.Is(hung.ended, context.DeadlineExceeded) || hung.took != bound {
			t.Errorf("acme's Notify ended %v after %v, want the deadline at %v", hung.ended, hung.took, bound)
		}
		if other.heard.Load() != 1 {
			t.Errorf("beta heard %d notifications, want 1", other.heard.Load())
		}
	})
}

func TestTheNotifyBoundDefaults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		hung := &heldNotify{hang: true}
		k := &Kind{running: map[string]*attached{"acme": {notified: hung, notify: []plugin.Topic{plugin.TopicTurns}}}}
		k.notify(t.Context(), port.Change{Topics: port.TopicTurns, Units: []unit.Unit{{Provider: "acme", Key: "one"}}})
		if hung.took != DefaultNotifyTimeout {
			t.Errorf("acme's Notify cut after %v, want %v", hung.took, DefaultNotifyTimeout)
		}
	})
}
