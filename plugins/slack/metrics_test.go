package slack

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/socket"
)

func clockAt(t0 time.Time) (*Metrics, *time.Time) {
	now := t0
	return &Metrics{now: func() time.Time { return now }}, &now
}

func TestMetricsCountsTheLastHour(t *testing.T) {
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	m, now := clockAt(t0)
	m.Received()
	m.Received()
	m.Admitted(dslack.ViaSocket)
	*now = t0.Add(30 * time.Minute)
	m.Received()
	m.Admitted(dslack.ViaCatchUp)
	m.CatchUp("reconnect", 4, 2)
	s := m.snapshot()
	if s.EventsPerHour.Received != 3 || s.EventsPerHour.Admitted != 2 || s.MissedWhileConnected != 2 {
		t.Fatalf("at 30 min: %+v", s)
	}
	*now = t0.Add(61 * time.Minute)
	s = m.snapshot()
	if s.EventsPerHour.Received != 1 || s.EventsPerHour.Admitted != 1 || s.MissedWhileConnected != 2 {
		t.Errorf("at 61 min, the first minute's counts are still in: %+v", s.EventsPerHour)
	}
	*now = t0.Add(2 * time.Hour)
	s = m.snapshot()
	if s.EventsPerHour.Received != 0 || s.MissedWhileConnected != 0 {
		t.Errorf("after two hours: %+v %d", s.EventsPerHour, s.MissedWhileConnected)
	}
	if s.DeltasByVia["socket"] != 1 || s.DeltasByVia["catchup"] != 1 || s.DeltasByVia["follow"] != 0 {
		t.Errorf("deltas by via cover the process life: %v", s.DeltasByVia)
	}
	if s.LastCatchUp == nil || s.LastCatchUp.Reason != "reconnect" || s.LastCatchUp.Deltas != 4 || !s.LastCatchUp.At.Equal(t0.Add(30*time.Minute)) {
		t.Errorf("last catch-up %+v", s.LastCatchUp)
	}
}

func TestMetricsFollowsTheSocket(t *testing.T) {
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	m, _ := clockAt(t0)
	if s := m.snapshot(); s.Socket.State != "down" {
		t.Errorf("before any state: %q", s.Socket.State)
	}
	m.Socket(socket.State{Kind: "dialing", At: t0})
	if s := m.snapshot(); s.Socket.State != "connecting" || !s.DownSince.Equal(t0) {
		t.Errorf("dialing: %+v since %v", s.Socket, s.DownSince)
	}
	m.Socket(socket.State{Kind: "hello", NumConnections: 1, At: t0.Add(time.Second)})
	s := m.snapshot()
	if s.Socket.State != "connected" || s.Socket.NumConnections != 1 || !s.Socket.ConnectedAt.Equal(t0.Add(time.Second)) || !s.DownSince.IsZero() || s.Socket.Reconnects != 0 {
		t.Errorf("hello: %+v since %v", s.Socket, s.DownSince)
	}
	m.Socket(socket.State{Kind: "disconnect", Reason: "refresh_requested", At: t0.Add(time.Hour)})
	m.Socket(socket.State{Kind: "hello", NumConnections: 2, At: t0.Add(time.Hour + time.Second)})
	m.Socket(socket.State{Kind: "dropped", Reason: "read: EOF", At: t0.Add(2 * time.Hour)})
	s = m.snapshot()
	if s.Socket.State != "down" || s.Socket.LastDisconnect != "dropped" || s.Socket.Reconnects != 1 || !s.DownSince.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("dropped: %+v since %v", s.Socket, s.DownSince)
	}
	m.Socket(socket.State{Kind: "overflow", At: t0.Add(3 * time.Hour)})
	if s := m.snapshot(); s.Socket.State != "down" || s.Socket.LastDisconnect != "overflow" {
		t.Errorf("overflow: %+v", s.Socket)
	}
}

func TestMetricsRateLimitsBackfillAndWarnings(t *testing.T) {
	m := &Metrics{}
	m.RateLimited("conversations.history")
	m.RateLimited("conversations.history")
	m.RateLimited("search.messages")
	m.Backfill(3, 10, 42, false)
	m.Backfill(4, 12, 50, true)
	m.Warn("extraction", func() []string { return []string{"the extraction breaker is open"} })
	m.Warn("other", func() []string { return nil })
	m.Warn("extraction", func() []string { return []string{"replaced"} })
	s := m.snapshot()
	if s.RateLimited["conversations.history"] != 2 || s.RateLimited["search.messages"] != 1 {
		t.Errorf("rate limited %v", s.RateLimited)
	}
	if s.Backfill != (backfillSnapshot{Done: true, Conversations: 4, Threads: 12, Deltas: 50}) {
		t.Errorf("backfill %+v", s.Backfill)
	}
	if !slices.Equal(s.Warnings, []string{"replaced"}) {
		t.Errorf("warnings %v", s.Warnings)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"socket"`, `"events_per_hour"`, `"deltas_by_via"`, `"missed_while_connected"`, `"rate_limited"`, `"backfill"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("no %s in %s", key, raw)
		}
	}
}

func TestMetricsIsSafeForConcurrentUse(t *testing.T) {
	m := &Metrics{}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				m.Received()
				m.Admitted(dslack.ViaSocket)
				m.RateLimited("chat.postMessage")
				m.Socket(socket.State{Kind: "hello", NumConnections: i})
				m.CatchUp("start", 1, 0)
				_ = m.snapshot()
			}
		}()
	}
	wg.Wait()
	s := m.snapshot()
	if s.EventsPerHour.Received != 1600 || s.DeltasByVia["socket"] != 1600 || s.RateLimited["chat.postMessage"] != 1600 {
		t.Errorf("%+v %v %v", s.EventsPerHour, s.DeltasByVia, s.RateLimited)
	}
}
