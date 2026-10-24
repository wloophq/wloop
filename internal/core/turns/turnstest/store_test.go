package turnstest

import (
	"context"
	"fmt"
	"testing"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/turn"
)

func TestFakeStore(t *testing.T) {
	RunStore(t, func(*testing.T) Repository { return NewStore() })
}

func TestFakeRegistry(t *testing.T) {
	RunRegistry(t, func(*testing.T) turns.Registry { return NewRegistry() })
}

type recordingTB struct {
	testing.TB
	errs []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func TestStrictFakesWantEvents(t *testing.T) {
	ctx := context.Background()
	rec := &recordingTB{}
	store, reg := NewStore(Strict(rec)), NewRegistry(Strict(rec))
	book := turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "r1", turn.Sent, 0)}}
	session := turn.Session{ID: "term_1"}
	stop := turn.Stop{Session: "term_1", PromptID: "p1", At: at0}

	store.Put(book)
	reg.Put(session)
	if len(rec.errs) != 0 {
		t.Fatalf("a seed failed the test: %v", rec.errs)
	}
	e := port.Event{Unit: r7, Kind: turns.KindTurnSent, Key: "r1", At: at0}
	if _, err := store.Save(ctx, book, e); err != nil {
		t.Fatal(err)
	}
	if err := reg.SaveSession(ctx, session, e); err != nil {
		t.Fatal(err)
	}
	if err := reg.LogStop(ctx, stop, e); err != nil {
		t.Fatal(err)
	}
	if len(rec.errs) != 0 {
		t.Fatalf("a save with events failed the test: %v", rec.errs)
	}

	store.Save(ctx, book)
	reg.SaveSession(ctx, session)
	reg.LogStop(ctx, stop)
	want := []string{
		"turnstest: Store.Save without an event",
		"turnstest: Registry.SaveSession without an event",
		"turnstest: Registry.LogStop without an event",
	}
	if fmt.Sprint(rec.errs) != fmt.Sprint(want) {
		t.Fatalf("errors %q, want %q", rec.errs, want)
	}
}

func TestFakesKeepTheirEvents(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	b := turn.Book{Unit: r7, Turns: []turn.Turn{sent(turn.Intent(dintent.Review), "r1", turn.Sent, 0)}}
	keyed := port.Event{Unit: r7, Kind: turns.KindTurnSent, Key: "r1", At: at0}
	unkeyed := port.Event{Unit: r7, Kind: turns.KindAnswered, Data: "yes", At: at0}
	if _, err := store.Save(ctx, b, keyed, unkeyed, keyed, unkeyed); err != nil {
		t.Fatal(err)
	}
	got := store.Events()
	if len(got) != 3 || got[0] != keyed || got[1] != unkeyed || got[2].At != at0.Add(1) {
		t.Fatalf("events %+v", got)
	}
}
