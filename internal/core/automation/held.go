package automation

import (
	"sync"
	"time"
)

// eventHold is how long an event whose unit cannot be decided holds its
// workflow's cursor before it is passed.
const eventHold = time.Hour

// heldEvents remembers, in memory, per workflow, when each held event first
// failed and up to which event id a sweep already decided, so the events
// re-read behind a held one are not decided twice. A restart forgets both:
// the hour starts over and the re-read events fall back on their keys.
type heldEvents struct {
	mu      sync.Mutex
	failing map[string]map[int64]time.Time
	upTo    map[string]int64
}

// decided says whether an earlier sweep decided event id of wf.
func (h *heldEvents) decided(wf string, id int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, failing := h.failing[wf][id]
	return !failing && id <= h.upTo[wf]
}

// decide marks event id of wf decided.
func (h *heldEvents) decide(wf string, id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.failing[wf], id)
	if h.upTo == nil {
		h.upTo = map[string]int64{}
	}
	h.upTo[wf] = max(h.upTo[wf], id)
}

// expired notes that event id of wf failed at now and says whether it has
// held the cursor for eventHold since its first failure.
func (h *heldEvents) expired(wf string, id int64, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failing == nil {
		h.failing = map[string]map[int64]time.Time{}
	}
	if h.failing[wf] == nil {
		h.failing[wf] = map[int64]time.Time{}
	}
	first, ok := h.failing[wf][id]
	if !ok {
		h.failing[wf][id] = now
		return false
	}
	return now.Sub(first) >= eventHold
}
