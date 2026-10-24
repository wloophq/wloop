package gtd_test

import (
	"context"
	"testing"
	"time"

	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestAUserScheduleEndingUnderAMuteStaysScheduled(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU}); err != nil {
		t.Fatal(err)
	}
	id := gtd.DerivedID(gtd.KindAction, unitU, "a")
	if _, err := r.svc.Do(ctx, id, coregtd.Request{Verb: gtd.VerbSchedule, Args: map[string]string{"until": t0.Add(time.Hour).Format(time.RFC3339)}}); err != nil {
		t.Fatal(err)
	}
	muteEnd := t0.Add(72 * time.Hour)
	r.facts.facts[unitU] = gtd.UnitFacts{Unit: unitU, Mute: &gtd.Mute{Until: muteEnd}}
	r.clock.t = t0.Add(2 * time.Hour)
	r.store.Applies = 0
	if err := r.svc.Sync(ctx, nil); err != nil {
		t.Fatal(err)
	}
	e := r.entry(t, id)
	if e.State != gtd.Scheduled || !e.Snooze.Mute || !e.Snooze.Until.Equal(muteEnd) || e.Snooze.Return != gtd.Inbox {
		t.Fatalf("woke under the mute: %+v", e)
	}
	if r.store.Applies != 1 {
		t.Fatalf("%d applies, want the wake and the mute in one", r.store.Applies)
	}
}

func TestActivityMovesOnlyItsUnitsEntries(t *testing.T) {
	ctx := context.Background()
	r := newRig()
	r.items.byUnit[unitU] = []items.Item{owed(unitU, "a")}
	r.facts.facts[unitU] = gtd.UnitFacts{Unit: unitU, Mute: &gtd.Mute{UntilStateChange: true}}
	r.facts.facts[unitV] = gtd.UnitFacts{Unit: unitV, LastHuman: t0.Add(time.Hour)}
	if err := r.svc.Sync(ctx, []unit.Unit{unitU, unitV}); err != nil {
		t.Fatal(err)
	}
	if e := r.entry(t, gtd.DerivedID(gtd.KindAction, unitU, "a")); e.State != gtd.Scheduled || e.Changed {
		t.Fatalf("activity on V moved U's entry: %+v", e)
	}
}
