package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/views/viewstest"
	"github.com/wloophq/wloop/internal/domain/event"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

func TestStoreContract(t *testing.T) {
	turnstest.RunStore(t, func(t *testing.T) turnstest.Repository { return newStore(t) })
}

func TestRegistryContract(t *testing.T) {
	turnstest.RunRegistry(t, func(t *testing.T) turns.Registry { return newStore(t) })
}

func TestStateContract(t *testing.T) {
	viewstest.RunState(t, func(t *testing.T) views.State { return newStore(t) }, func(t *testing.T, s views.State) []port.Event { return storedEvents(t, s.(*Store)) })
}

func storedEvents(t *testing.T, s *Store) []port.Event {
	t.Helper()
	rows, err := s.db.R().QueryContext(context.Background(), `SELECT provider, unit_key, kind, key, at, data FROM events ORDER BY at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []port.Event
	for rows.Next() {
		var ev port.Event
		var at string
		if err := rows.Scan(&ev.Unit.Provider, &ev.Unit.Key, &ev.Kind, &ev.Key, &at, &ev.Data); err != nil {
			t.Fatal(err)
		}
		if ev.At, err = time.Parse(event.TimeLayout, at); err != nil {
			t.Fatal(err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func eventRows(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.R().QueryContext(context.Background(), `SELECT provider, unit_key, kind, key, at, data FROM events ORDER BY at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u unit.Unit
		var kind, key, at, data string
		if err := rows.Scan(&u.Provider, &u.Key, &kind, &key, &at, &data); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join([]string{u.String(), kind, key, at, data}, "|"))
	}
	return out
}

func TestSavesWriteTheirEvents(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	b := turn.Book{Unit: ref.Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Sent, SentAt: at}}}
	sentEv := port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "r1", At: at}
	answered := port.Event{Unit: ref.Unit(), Kind: turns.KindAnswered, Data: "yes", At: at}
	if _, err := s.Save(ctx, b, sentEv, answered, sentEv, answered); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(ctx, turn.Session{ID: "term_1"}, port.Event{Kind: turns.KindSessionLaunched, Key: "term_1", At: at}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"github:acme/api#7|turn_sent|r1|2026-10-17T12:00:00.000000000Z|",
		"github:acme/api#7|answered||2026-10-17T12:00:00.000000000Z|yes",
		"session:term_1|session_launched|term_1|2026-10-17T12:00:00.000000000Z|",
		"github:acme/api#7|answered||2026-10-17T12:00:00.000000001Z|yes",
	}
	if got := eventRows(t, s); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s", strings.Join(got, "\n"))
	}
}

func TestAFailedSaveWritesNoEvent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	b := turn.Book{Unit: ref.Unit(), Turns: []turn.Turn{{Seq: 42, Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Sent, SentAt: at}}}
	if _, err := s.Save(ctx, b, port.Event{Unit: ref.Unit(), Kind: turns.KindTurnSent, Key: "r1", At: at}); err == nil {
		t.Fatal("saved a turn the store does not have")
	}
	if got := eventRows(t, s); len(got) != 0 {
		t.Fatalf("events of a failed save: %v", got)
	}
}

func TestATurnWithoutInputsStoresAnEmptyObjectAndReadsBackNil(t *testing.T) {
	s := newStore(t)
	ref := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	b, err := s.Save(context.Background(), turn.Book{Unit: ref.Unit(), Turns: []turn.Turn{{Intent: turn.Intent(dintent.Review), ID: "r1", State: turn.Sent, SentAt: time.Now()}}})
	if err != nil || b.Turns[0].Inputs != nil {
		t.Fatalf("%+v %v", b.Turns, err)
	}
	var raw string
	if err := s.db.R().QueryRowContext(context.Background(), `SELECT inputs FROM turns`).Scan(&raw); err != nil || raw != "{}" {
		t.Fatalf("stored %q %v", raw, err)
	}
}
