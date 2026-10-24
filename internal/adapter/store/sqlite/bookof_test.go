package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/meter/metertest"
)

func plan(t *testing.T, s *Store, q string, args ...any) string {
	t.Helper()
	rows, err := s.db.R().QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, "\n")
}

func TestBookOfPlans(t *testing.T) {
	s := newStore(t)
	for _, c := range []struct {
		name, q string
		args    []any
		want    []string
	}{
		{"turn", bookOfTurn, []any{"r"}, []string{"turns_turn_id"}},
		{"handle", bookOfHandle, []any{"h", "h"}, []string{"turns_session", "turns_sent_to"}},
		{"both", bookOfBoth, []any{"r", "h", "h"}, []string{"turns_turn_id"}},
	} {
		got := plan(t, s, c.q, c.args...)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s plan lacks %s:\n%s", c.name, w, got)
			}
		}
		for _, f := range []string{"SCAN turns", "TEMP B-TREE"} {
			if strings.Contains(got, f) {
				t.Errorf("%s plan has %s:\n%s", c.name, f, got)
			}
		}
	}
}

func TestBookOfSameAsBefore(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	r7 := pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	r8 := pr.Ref{Owner: "acme", Repo: "api", Number: 8}
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	started := turn.Turn{Intent: turn.Intent(dintent.Address), ID: "a", State: turn.Started, SentAt: at, Session: "h"}
	sentTo := turn.Turn{Intent: turn.Intent(dintent.Nudge), ID: "b", State: turn.Sent, SentAt: at, SentTo: "h"}
	other := turn.Turn{Intent: turn.Intent(dintent.Review), ID: "c", State: turn.Sent, SentAt: at, SentTo: "x"}
	ran := turn.Turn{Intent: turn.Intent(dintent.Address), ID: "d", State: turn.Started, SentAt: at, Session: "y"}
	for _, b := range []turn.Book{
		{Unit: r7.Unit(), Turns: []turn.Turn{started}},
		{Unit: r8.Unit(), Turns: []turn.Turn{sentTo, other, ran}},
	} {
		if _, err := s.Save(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []turns.Key{{Turn: "a"}, {Turn: "b"}, {Turn: "c"}, {Session: "h"}, {Session: "x"}, {Session: "y"}, {Turn: "d", Session: "y"}, {Turn: "a", Session: "h"}, {Turn: "c", Session: "h"}, {Turn: "nope"}, {Session: "nope"}} {
		var want unit.Unit
		err := s.db.R().QueryRowContext(ctx, `SELECT provider, unit_key FROM turns
			WHERE (?1 = '' OR turn_id = ?1) AND (?2 = '' OR session = ?2 OR sent_to = ?2)
			ORDER BY seq DESC LIMIT 1`, k.Turn, k.Session).Scan(&want.Provider, &want.Key)
		found := err == nil
		b, ok, err := s.BookOf(ctx, k)
		if err != nil || ok != found || (ok && b.Unit != want) {
			t.Errorf("BookOf(%+v) = %s %v %v, want %s %v", k, b.Unit, ok, err, want, found)
		}
	}
}

func TestUnitsOfOneQuery(t *testing.T) {
	s := newStore(t)
	handles := make([]string, 159)
	for i := range handles {
		handles[i] = fmt.Sprintf("term_%d", i)
	}
	ctx, sc := metertest.Start(t, context.Background())
	got, err := s.UnitsOf(ctx, handles)
	if err != nil || len(got) != 0 {
		t.Fatalf("UnitsOf = %v %v", got, err)
	}
	if n := count(sc, meter.SQL, "", metertest.Calls); n != 1 {
		t.Fatalf("%d statements\n%s\nwant 1", n, metertest.Report(sc.Tally()))
	}
}
