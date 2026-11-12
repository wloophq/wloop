package e2e

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

// TestASentDraftReachesTheFeed sends a draft the person approved after the approval answered, as the daemon's
// sender does, and the events an open cockpit follows carry that change: its unit and the counters, so a view and
// an open detail reload and show the draft sent: its unit and the gates moved.
func TestASentDraftReachesTheFeed(t *testing.T) {
	r := newDraftsRig(t)
	r.review()
	d := r.propose("COMMENT", "Two nits, nothing blocking.", "c2", true)
	r.end(doneOn("COMMENT", d))
	events, err := r.c.Events(r.ctx, "feed-test", api.Since{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.approve(d.ID, d.Rev); err != nil {
		t.Fatalf("approve: %v", err)
	}
	r.wantState(d.ID, "queued")
	drain := time.After(2 * time.Second)
	for drained := false; !drained; {
		select {
		case <-events:
		case <-drain:
			drained = true
		}
	}
	r.send()
	r.wantState(d.ID, "sent")
	deadline := time.After(10 * time.Second)
	for {
		select {
		case c := <-events:
			if c.Type == "changed" && c.Gates && (c.All || slices.Contains(c.Units, draftsPR)) {
				e, err := r.c.Task(r.ctx, fmt.Sprintf("draft:%d", d.ID))
				if err != nil || e.State != "done" {
					t.Fatalf("the draft's task after the change: %+v %v, want done", e.State, err)
				}
				return
			}
		case <-deadline:
			t.Fatal(fmt.Sprintf("no change event carried draft %d's send to the open views", d.ID))
		}
	}
}
