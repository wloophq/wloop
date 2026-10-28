package inventory

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// fakeBroker keeps the events a store records in memory, as the host's
// broker would.
type fakeBroker struct {
	mu       sync.Mutex
	recorded [][]src.Event
	kept     []src.Event
}

func (b *fakeBroker) RecordEvents(_ context.Context, evs []src.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recorded = append(b.recorded, evs)
	for _, e := range evs {
		if !slices.ContainsFunc(b.kept, func(k src.Event) bool {
			return k.Unit == e.Unit && k.Kind == e.Kind && k.Key == e.Key && (e.Key != "" || k.At.Equal(e.At))
		}) {
			b.kept = append(b.kept, e)
		}
	}
	return nil
}

func (b *fakeBroker) Events(_ context.Context, q src.EventQuery) ([]src.Event, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []src.Event
	for _, e := range b.kept {
		if slices.Contains(q.Units, e.Unit) && (len(q.Kinds) == 0 || slices.Contains(q.Kinds, e.Kind)) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (*fakeBroker) SetMarks(context.Context, plugin.Unit, map[string]string) error { return nil }

func (*fakeBroker) PutBrief(context.Context, src.Brief) error { return nil }

func TestSaveSnapshotsReportsEventsThroughTheBroker(t *testing.T) {
	s := open(t)
	b := &fakeBroker{}
	s.UseHost(src.Host{Recorder: b, History: b})
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pr := dinv.PR{Ref: ref, Title: "one", Author: "ana", State: "OPEN", Reasons: []string{"author"}}
	push := dinv.Event{PR: ref, Kind: dinv.KindPush, Key: "abc", Actor: "ana", At: at}
	merged := dinv.Event{PR: ref, Kind: dinv.KindMerged, At: at.Add(time.Hour)}
	added, err := s.SaveSnapshots(ctx, []Snapshot{{PR: pr, Events: []dinv.Event{push, merged}}}, at)
	if err != nil {
		t.Fatal(err)
	}
	if added[ref] != 2 {
		t.Errorf("added %v, want 2 on %s", added, ref)
	}
	var kinds []string
	for _, e := range b.kept {
		kinds = append(kinds, e.Kind)
	}
	slices.Sort(kinds)
	if want := []string{dinv.KindFollowed, dinv.KindMerged, dinv.KindPush}; !slices.Equal(kinds, want) {
		t.Errorf("the broker got %v, want %v", kinds, want)
	}
	var stored int
	if err := s.db.R().QueryRowContext(ctx, `SELECT count(*) FROM events`).Scan(&stored); err != nil || stored != 0 {
		t.Errorf("the store wrote %d events itself (%v), want none", stored, err)
	}
	if got, ok, err := s.PR(ctx, ref); err != nil || !ok || got.Title != "one" {
		t.Fatalf("stored %+v %v %v", got, ok, err)
	}

	filled := merged
	filled.Actor, filled.Data = "bruno", "def"
	calls := len(b.recorded)
	added, err = s.SaveSnapshots(ctx, []Snapshot{{PR: pr, Events: []dinv.Event{push, merged}, Merges: []dinv.Event{filled}}}, at.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("a second save added %v, want none", added)
	}
	if got := b.recorded[calls:]; len(got) != 1 || len(got[0]) != 1 || got[0][0].Actor != "bruno" {
		t.Errorf("a second save recorded %+v, want only the merge that fills", got)
	}
}
