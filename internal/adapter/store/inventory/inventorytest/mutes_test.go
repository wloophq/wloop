package inventorytest

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestUnitMutesReadEveryProvidersMutes(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	until := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if err := s.SetOverride(ctx, dinv.Override{PR: ref, MutedUntil: until}); err != nil {
		t.Fatal(err)
	}
	thread := unit.Unit{Provider: "acme", Key: "one"}
	if _, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO overrides (provider, unit_key, mute_until_state, muted_kind, muted_waiting_on) VALUES ('acme','one',1,'waiting','me')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	other := unit.Unit{Provider: "acme", Key: "two"}
	got, err := s.UnitMutes(ctx, []unit.Unit{ref.Unit(), thread, other})
	if err != nil {
		t.Fatal(err)
	}
	if m := got[ref.Unit()]; !m.Until.Equal(until) || m.UntilStateChange {
		t.Errorf("the pull request's mute %+v", m)
	}
	if m := got[thread]; !m.UntilStateChange || m.Kind != "waiting" || m.WaitingOn != "me" {
		t.Errorf("the thread's mute %+v", m)
	}
	if _, ok := got[other]; ok || len(got) != 2 {
		t.Errorf("mutes %+v", got)
	}
}
