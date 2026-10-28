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
	"github.com/wloophq/wloop/internal/core/port"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/tasks/taskstest"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var entryAt = time.Date(2026, 10, 20, 13, 0, 0, 0, time.UTC)

func openEntries(t *testing.T) (coretasks.Store, *change.Log) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "entries.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.Entries(), log
}

func applyEntries(t *testing.T, s coretasks.Store, es ...task.Entry) {
	t.Helper()
	var cs []task.Change
	for _, e := range es {
		cs = append(cs, task.Change{Op: task.OpUpdate, Entry: e})
	}
	if err := s.Apply(context.Background(), cs); err != nil {
		t.Fatal(err)
	}
}

func capture(id task.ID, s task.State, min int) task.Entry {
	at := entryAt.Add(time.Duration(min) * time.Minute)
	return task.Entry{ID: id, Kind: task.KindCapture, State: s, Title: string(id), Origin: task.OriginUser, Created: at, Updated: at}
}

func TestEntryStoreContract(t *testing.T) {
	t.Parallel()
	taskstest.Contract(t, func(t *testing.T) coretasks.Store { s, _ := openEntries(t); return s })
}

func TestEntriesRoundTrip(t *testing.T) {
	t.Parallel()
	s, _ := openEntries(t)
	want := taskstest.Full("e-00000000000000aa")
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
	e := capture("e-1", task.Inbox, 0)
	e.Units = []unit.Unit{u}
	from := log.Gen()
	ev := port.Event{Unit: u, Kind: "entry_test", Key: "k", At: entryAt}
	if err := s.Apply(ctx, []task.Change{{Op: task.OpCreate, Entry: e}}, ev); err != nil {
		t.Fatal(err)
	}
	got := log.Since(from, ^port.Topic(0))
	if log.Gen()-from != 1 || !got.Has(port.TopicEntries) || !slices.Equal(got.Units, []unit.Unit{u}) {
		t.Fatalf("%d notes: %+v", log.Gen()-from, got)
	}
	from = log.Gen()
	bad := []task.Change{{Op: task.OpCreate, Entry: capture("e-2", task.Inbox, 1)}, {Op: task.OpCreate, Entry: task.Entry{}}}
	if err := s.Apply(ctx, bad); err == nil {
		t.Fatal("an entry without an id was written")
	}
	if _, err := s.Entry(ctx, "e-2"); !errors.Is(err, coretasks.ErrNoEntry) || log.Gen() != from {
		t.Fatalf("a failed apply wrote or noted: %v, %d notes", err, log.Gen()-from)
	}
}

func TestEmptyApplyNotesNothing(t *testing.T) {
	t.Parallel()
	s, log := openEntries(t)
	ctx := context.Background()
	e := capture("e-1", task.Inbox, 0)
	applyEntries(t, s, e)
	before, err := s.EntriesSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{})
	if err != nil {
		t.Fatal(err)
	}
	from := log.Gen()
	e.Updated = e.Updated.Add(time.Hour)
	applyEntries(t, s, e)
	applyEntries(t, s)
	after, err := s.EntriesSince(ctx, task.Query{View: task.ViewInbox}, coretasks.Since{})
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
	a, b, c := capture("e-a", task.Next, 0), capture("e-b", task.Next, 1), capture("e-c", task.Inbox, 2)
	p := capture("e-p", task.Next, 3)
	p.Kind, p.Sequential = task.KindProject, true
	s1, s2 := capture("e-s1", task.Next, 4), capture("e-s2", task.Next, 5)
	s1.Project, s1.Position, s2.Project, s2.Position = p.ID, 1, p.ID, 2
	applyEntries(t, s, a, b, c, p, s1, s2)
	q := task.Query{View: task.ViewNext}
	first, err := s.EntriesSince(ctx, q, coretasks.Since{})
	if err != nil || !first.Full || !slices.Equal(entryIDs(first.Entries), []task.ID{"e-a", "e-b", "e-p", "e-s1"}) {
		t.Fatalf("first page %v %v", entryIDs(first.Entries), err)
	}
	a.Title, b.State, s1.State = "a2", task.Waiting, task.Done
	applyEntries(t, s, a, b, s1)
	page, err := s.EntriesSince(ctx, q, coretasks.Since{Epoch: first.Epoch, Rev: first.Rev})
	if err != nil {
		t.Fatal(err)
	}
	if page.Full || !slices.Equal(entryIDs(page.Entries), []task.ID{"e-a", "e-p", "e-s2"}) || !slices.Equal(page.Removed, []task.ID{"e-b", "e-s1"}) {
		t.Fatalf("page entries %v removed %v full %v", entryIDs(page.Entries), page.Removed, page.Full)
	}
	if page.Counters.Views[task.ViewNext] != 3 || page.Counters.Views[task.ViewWaiting] != 1 {
		t.Fatalf("counters %+v", page.Counters.Views)
	}
	other, err := s.EntriesSince(ctx, q, coretasks.Since{Epoch: "another", Rev: page.Rev})
	if err != nil || !other.Full || len(other.Entries) != 3 {
		t.Fatalf("another epoch %+v %v", other, err)
	}
	ahead, err := s.EntriesSince(ctx, q, coretasks.Since{Epoch: page.Epoch, Rev: page.Rev + 5})
	if err != nil || !ahead.Full {
		t.Fatalf("a position ahead %+v %v", ahead, err)
	}
}

func entryIDs(es []task.Entry) []task.ID {
	out := []task.ID{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestVerbEventsWriteWithOrWithoutAChange(t *testing.T) {
	t.Parallel()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "entries.db"), change.NewLog(change.DefaultWindow, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	e := capture("e-1", task.Next, 0)
	applyEntries(t, db.Entries(), e)
	done := e
	done.State = task.Done
	ev := port.Event{Unit: task.UnitOf(e.ID), Kind: task.VerbEventKind(task.VerbDone), Data: "next", At: entryAt}
	if err := db.Entries().Apply(ctx, []task.Change{{Op: task.OpUpdate, Entry: done}}, ev); err != nil {
		t.Fatal(err)
	}
	if err := db.Entries().Apply(ctx, nil, ev); err != nil {
		t.Fatal(err)
	}
	got, err := db.Events().Since(ctx, 0, task.VerbEventKinds)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Unit != task.UnitOf(e.ID) || got[0].Data != "next" || got[1].Kind != "verb_done" {
		t.Fatalf("events %+v", got)
	}
}
