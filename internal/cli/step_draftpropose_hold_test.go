package cli

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

func TestAHoldNamesADurationOfTheConfig(t *testing.T) {
	t.Parallel()
	cfg := Defaults()
	cfg.Drafts.RepeatAfter = config.Days(36 * time.Hour)
	for v, want := range map[string]time.Duration{"config.drafts.repeat_after": 36 * time.Hour, "2d": 48 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := holdDuration(v, cfg); err != nil || got != want {
			t.Errorf("%s: %v, %v; want %v", v, got, err, want)
		}
	}
	for _, v := range []string{"config.gtd.stale_waiting_days", "config.nope", "soon", "-1h"} {
		if _, err := holdDuration(v, cfg); err == nil {
			t.Errorf("%s read as a duration", v)
		}
	}
}

// TestARunLeavingAPersonOnATimedHoldIsNotDone: the run proposes the people
// who are free and stays pending while another's settled draft holds them,
// so their nudge follows once the hold ends, as the job's did.
func TestARunLeavingAPersonOnATimedHoldIsNotDone(t *testing.T) {
	t.Parallel()
	r := newDraftRig(t, draftWF("nudges", "purpose", "nudge", "items", "owed", "per", "person", "hold", "24h"), "{{.Person.Key}}")
	r.entries.m["e1"] = gtd.Entry{ID: "e1", Kind: gtd.KindWaiting, State: gtd.Waiting, Units: []unit.Unit{draftThread},
		Items:   []gtd.ItemRef{{Unit: draftThread, Item: "i1"}, {Unit: draftThread, Item: "i2"}},
		Waiting: &gtd.WaitingOn{On: []items.Person{ana, bob}}}
	r.items.set(draftThread, openItem("i1", "The deck", r.now.Add(-96*time.Hour), ana), openItem("i2", "The budget", r.now.Add(-96*time.Hour), bob))
	runErr := func() error {
		_, err := r.s.Run(context.Background(), r.wf.Steps[0], draftEntry, workflow.Why{Workflow: r.wf.Name}, automation.RunOpts{})
		return err
	}
	if err := runErr(); err != nil {
		t.Fatalf("both free: %v", err)
	}
	for _, d := range r.drafts() {
		if d.Origin.Key == "e1:"+bob.Key {
			if _, err := r.svc.Discard(context.Background(), d.ID, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	var deferred automation.Deferred
	if err := runErr(); !errors.As(err, &deferred) {
		t.Fatalf("bob discarded within the hold, ana open: %v", err)
	}
	r.now = r.now.Add(25 * time.Hour)
	if err := runErr(); err != nil {
		t.Fatalf("past bob's hold he is proposed and only ana's open draft holds: %v", err)
	}
}
