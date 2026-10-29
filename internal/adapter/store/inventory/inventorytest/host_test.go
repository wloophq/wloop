package inventorytest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
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

// failOnce is a broker whose first RecordEvents fails.
type failOnce struct {
	*fakeBroker
	failed bool
}

func (b *failOnce) RecordEvents(ctx context.Context, evs []src.Event) error {
	if !b.failed {
		b.failed = true
		return errors.New("broker down")
	}
	return b.fakeBroker.RecordEvents(ctx, evs)
}

func keptKinds(b *fakeBroker) map[string]int {
	out := map[string]int{}
	for _, e := range b.kept {
		out[e.Kind]++
	}
	return out
}

func TestSaveSnapshotsRepairsAFailedBrokerCall(t *testing.T) {
	s := open(t)
	b := &failOnce{fakeBroker: &fakeBroker{}}
	s.UseHost(src.Host{Recorder: b, History: b})
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pr := dinv.PR{Ref: ref, Title: "one", Author: "ana", State: "OPEN", Reasons: []string{"author"}}
	push := dinv.Event{PR: ref, Kind: dinv.KindPush, Key: "abc", Actor: "ana", At: at}
	if _, err := s.SaveSnapshots(ctx, []Snapshot{{PR: pr, Events: []dinv.Event{push}}}, at); err == nil {
		t.Fatal("want the broker error")
	}
	if _, ok, err := s.PR(ctx, ref); err != nil || ok {
		t.Fatalf("a save whose events the host refused stored the pull request (%v)", err)
	}
	if _, err := s.SaveSnapshots(ctx, []Snapshot{{PR: pr, Events: []dinv.Event{push}}}, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := keptKinds(b.fakeBroker); got[dinv.KindPush] != 1 || got[dinv.KindFollowed] != 1 {
		t.Errorf("after the retry the host holds %v, want one push and one followed", got)
	}
}

// failPRWrites makes every write of a pull request row fail until the
// returned func runs.
func failPRWrites(t *testing.T, s *Store) func() {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`CREATE TRIGGER fail_pr_insert BEFORE INSERT ON pull_requests BEGIN SELECT RAISE(ABORT, 'disk full'); END`,
		`CREATE TRIGGER fail_pr_update BEFORE UPDATE ON pull_requests BEGIN SELECT RAISE(ABORT, 'disk full'); END`,
	} {
		if _, err := s.db.Write(ctx, func(tx *sqlite.Tx) error { _, err := tx.ExecContext(ctx, q); return err }); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		if _, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
			_, err := tx.ExecContext(ctx, `DROP TRIGGER fail_pr_insert; DROP TRIGGER fail_pr_update`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAWriteThatFailsAfterTheBrokerLeavesEachEventOnce(t *testing.T) {
	s := open(t)
	b := &fakeBroker{}
	s.UseHost(src.Host{Recorder: b, History: b})
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	pr := dinv.PR{Ref: ref, Title: "one", Author: "ana", State: "OPEN", Reasons: []string{"author"}, UpdatedAt: at}
	push := dinv.Event{PR: ref, Kind: dinv.KindPush, Key: "abc", Actor: "ana", At: at}
	heal := failPRWrites(t, s)
	if _, err := s.SaveSnapshots(ctx, []Snapshot{{PR: pr, Events: []dinv.Event{push}}}, at); err == nil {
		t.Fatal("want the write to fail")
	}
	if _, ok, err := s.PR(ctx, ref); err != nil || ok {
		t.Fatalf("the failed write stored the pull request (%v), so the next ingest would skip it", err)
	}
	heal()
	added, err := s.SaveSnapshots(ctx, []Snapshot{{PR: pr, Events: []dinv.Event{push}}}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Errorf("the retry added %v, want none: the host took them before the write failed", added)
	}
	if got := keptKinds(b); got[dinv.KindPush] != 1 || got[dinv.KindFollowed] != 1 {
		t.Errorf("after the retry the host holds %v, want one push and one followed", got)
	}
	if got, ok, err := s.PR(ctx, ref); err != nil || !ok || !got.UpdatedAt.Equal(at) {
		t.Errorf("the retry stored %+v %v %v", got, ok, err)
	}
}

func TestSetReasonsRecordsItsFlipOnceAcrossAFailedWrite(t *testing.T) {
	s := open(t)
	b := &fakeBroker{}
	s.UseHost(src.Host{Recorder: b, History: b})
	ctx := context.Background()
	upsert(t, s, dinv.PR{Ref: ref, Title: "one", State: "OPEN"})
	heal := failPRWrites(t, s)
	if err := s.SetReasons(ctx, ref, []string{"author"}); err == nil {
		t.Fatal("want the write to fail")
	}
	heal()
	if err := s.SetReasons(ctx, ref, []string{"author"}); err != nil {
		t.Fatal(err)
	}
	if got := keptKinds(b); got[dinv.KindFollowed] != 1 {
		t.Errorf("the host holds %v, want one followed", got)
	}
	if err := s.SetReasons(ctx, ref, nil); err != nil {
		t.Fatal(err)
	}
	if got := keptKinds(b); got[dinv.KindFollowed] != 1 || got[dinv.KindUnfollowed] != 1 {
		t.Errorf("after unfollowing the host holds %v, want one followed and one unfollowed", got)
	}
}

func TestTheOverrideWriteSharesNoTransactionWithPullRequests(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	upsert(t, s, dinv.PR{Ref: ref, Title: "one", State: "OPEN"})
	heal := failPRWrites(t, s)
	defer heal()
	until := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if err := s.SetOverride(ctx, dinv.Override{PR: ref, MutedUntil: until, Tracked: true}); err == nil {
		t.Fatal("want the pull request's reasons to fail")
	}
	host, err := s.db.Override(ctx, ref.Unit())
	if err != nil {
		t.Fatal(err)
	}
	if host[port.OverrideMutedUntil] != until.Format(time.RFC3339Nano) {
		t.Errorf("the host's columns %v went down with the pull request's write", host)
	}
	if o, err := s.Override(ctx, ref); err != nil || o.Tracked {
		t.Errorf("the plugin's columns %+v %v outlived the failed reasons write", o, err)
	}
}
