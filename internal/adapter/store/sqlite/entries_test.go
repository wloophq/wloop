package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/change"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/gtd/gtdtest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var entryAt = time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)

func openEntries(t *testing.T) (coregtd.Store, *change.Log) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "entries.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.Entries(), log
}

func applyEntries(t *testing.T, s coregtd.Store, es ...gtd.Entry) {
	t.Helper()
	var cs []gtd.Change
	for _, e := range es {
		cs = append(cs, gtd.Change{Op: gtd.OpUpdate, Entry: e})
	}
	if err := s.Apply(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
}

func capture(id gtd.ID, s gtd.State, min int) gtd.Entry {
	at := entryAt.Add(time.Duration(min) * time.Minute)
	return gtd.Entry{ID: id, Kind: gtd.KindCapture, State: s, Title: string(id), Origin: gtd.OriginUser, Created: at, Updated: at}
}

func TestEntryStoreContract(t *testing.T) {
	t.Parallel()
	gtdtest.Contract(t, func(t *testing.T) coregtd.Store { s, _ := openEntries(t); return s })
}

func TestEntriesRoundTrip(t *testing.T) {
	t.Parallel()
	s, _ := openEntries(t)
	want := gtdtest.Full("e-00000000000000aa")
	applyEntries(t, s, want)
	got, err := s.Entry(context.Background(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Same(want) || !got.Updated.Equal(want.Updated) || !slices.Equal(got.Contexts, want.Contexts) || !slices.Equal(got.Units, want.Units) {
		t.Fatalf("read back\n%+v\nwant\n%+v", got, want)
	}
	if got.Waiting.On[0].Aliases[0] != want.Waiting.On[0].Aliases[0] {
		t.Fatalf("waiting %+v", got.Waiting)
	}
}

func TestApplyNotesEntries(t *testing.T) {
	t.Parallel()
	s, log := openEntries(t)
	ctx := context.Background()
	u := unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}
	e := capture("e-1", gtd.Inbox, 0)
	e.Units = []unit.Unit{u}
	from := log.Gen()
	ev := port.Event{Unit: u, Kind: "entry_test", Key: "k", At: entryAt}
	if err := s.Apply(ctx, []gtd.Change{{Op: gtd.OpCreate, Entry: e}}, ev); err != nil {
		t.Fatal(err)
	}
	got := log.Since(from, ^port.Topic(0))
	if log.Gen()-from != 1 || !got.Has(port.TopicEntries) || !slices.Equal(got.Units, []unit.Unit{u}) {
		t.Fatalf("%d notes: %+v", log.Gen()-from, got)
	}
	from = log.Gen()
	bad := []gtd.Change{{Op: gtd.OpCreate, Entry: capture("e-2", gtd.Inbox, 1)}, {Op: gtd.OpCreate, Entry: gtd.Entry{}}}
	if err := s.Apply(ctx, bad); err == nil {
		t.Fatal("an entry without an id was written")
	}
	if _, err := s.Entry(ctx, "e-2"); !errors.Is(err, coregtd.ErrNoEntry) || log.Gen() != from {
		t.Fatalf("a failed apply wrote or noted: %v, %d notes", err, log.Gen()-from)
	}
}

func TestEmptyApplyNotesNothing(t *testing.T) {
	t.Parallel()
	s, log := openEntries(t)
	ctx := context.Background()
	e := capture("e-1", gtd.Inbox, 0)
	applyEntries(t, s, e)
	before, err := s.EntriesSince(ctx, gtd.Query{View: gtd.ViewInbox}, coregtd.Since{})
	if err != nil {
		t.Fatal(err)
	}
	from := log.Gen()
	e.Updated = e.Updated.Add(time.Hour)
	applyEntries(t, s, e)
	applyEntries(t, s)
	after, err := s.EntriesSince(ctx, gtd.Query{View: gtd.ViewInbox}, coregtd.Since{})
	if err != nil {
		t.Fatal(err)
	}
	if log.Gen() != from || after.Rev != before.Rev || after.Epoch != before.Epoch {
		t.Fatalf("%d notes, rev %d -> %d", log.Gen()-from, before.Rev, after.Rev)
	}
}

func TestEntriesSinceStore(t *testing.T) {
	t.Parallel()
	s, _ := openEntries(t)
	ctx := context.Background()
	a, b, c := capture("e-a", gtd.Next, 0), capture("e-b", gtd.Next, 1), capture("e-c", gtd.Inbox, 2)
	p := capture("e-p", gtd.Next, 3)
	p.Kind, p.Sequential = gtd.KindProject, true
	s1, s2 := capture("e-s1", gtd.Next, 4), capture("e-s2", gtd.Next, 5)
	s1.Project, s1.Position, s2.Project, s2.Position = p.ID, 1, p.ID, 2
	applyEntries(t, s, a, b, c, p, s1, s2)
	q := gtd.Query{View: gtd.ViewNext}
	first, err := s.EntriesSince(ctx, q, coregtd.Since{})
	if err != nil || !first.Full || !slices.Equal(entryIDs(first.Entries), []gtd.ID{"e-a", "e-b", "e-p", "e-s1"}) {
		t.Fatalf("first page %v %v", entryIDs(first.Entries), err)
	}
	a.Title, b.State, s1.State = "a2", gtd.Waiting, gtd.Done
	applyEntries(t, s, a, b, s1)
	page, err := s.EntriesSince(ctx, q, coregtd.Since{Epoch: first.Epoch, Rev: first.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if page.Full || !slices.Equal(entryIDs(page.Entries), []gtd.ID{"e-a", "e-p", "e-s2"}) || !slices.Equal(page.Removed, []gtd.ID{"e-b", "e-s1"}) {
		t.Fatalf("page entries %v removed %v full %v", entryIDs(page.Entries), page.Removed, page.Full)
	}
	if page.Counters.Views[gtd.ViewNext] != 3 || page.Counters.Views[gtd.ViewWaiting] != 1 {
		t.Fatalf("counters %+v", page.Counters.Views)
	}
	other, err := s.EntriesSince(ctx, q, coregtd.Since{Epoch: "another", Rev: page.Rev})
	if err != nil || !other.Full || len(other.Entries) != 3 {
		t.Fatalf("another epoch %+v %v", other, err)
	}
	ahead, err := s.EntriesSince(ctx, q, coregtd.Since{Epoch: page.Epoch, Rev: page.Rev + 5})
	if err != nil || !ahead.Full {
		t.Fatalf("a position ahead %+v %v", ahead, err)
	}
}

func entryIDs(es []gtd.Entry) []gtd.ID {
	out := []gtd.ID{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}
