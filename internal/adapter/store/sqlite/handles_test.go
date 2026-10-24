package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/turn"
)

var handlesT0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func handleRows(t *testing.T, s *Store, id string) int {
	t.Helper()
	var n int
	if err := s.db.R().QueryRowContext(context.Background(), `SELECT count(*) FROM session_handles WHERE session_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantSessionOf(t *testing.T, s *Store, manager, handle, want string) {
	t.Helper()
	id, ok, err := s.SessionOf(context.Background(), manager, handle)
	if err != nil || ok != (want != "") || id != want {
		t.Fatalf("SessionOf(%s, %s) = %q %v %v, want %q", manager, handle, id, ok, err, want)
	}
}

func openHandle(t *testing.T, s *Store, id string) turn.Session {
	t.Helper()
	got, ok, err := s.Session(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("Session(%s) = %v %v", id, ok, err)
	}
	return got
}

func TestBindHandleOpensAndReplaces(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.SaveSession(ctx, turn.Session{ID: "s1", Manager: "orca"}); err != nil {
		t.Fatal(err)
	}
	if got := openHandle(t, s, "s1"); got.Handle != "" || got.State() != turn.SessionDormant {
		t.Fatalf("a session saved with no handle: %+v", got)
	}
	wantSessionOf(t, s, "orca", "h1", "")

	if err := s.BindHandle(ctx, "s1", "orca", "h1", "", handlesT0); err != nil {
		t.Fatal(err)
	}
	if got := openHandle(t, s, "s1"); got.Handle != "h1" || got.State() != turn.SessionActive {
		t.Fatalf("after binding h1: %+v", got)
	}
	wantSessionOf(t, s, "orca", "h1", "s1")
	wantSessionOf(t, s, "desktop", "h1", "")

	if err := s.BindHandle(ctx, "s1", "orca", "h1", "rt-1", handlesT0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n := handleRows(t, s, "s1"); n != 1 {
		t.Fatalf("binding the open handle again added a row: %d rows", n)
	}
	var runtime string
	if err := s.db.R().QueryRowContext(ctx, `SELECT runtime FROM session_handles WHERE session_id = 's1' AND to_at = ''`).Scan(&runtime); err != nil || runtime != "rt-1" {
		t.Fatalf("runtime = %q %v, want the one the rebind brought", runtime, err)
	}

	if err := s.BindHandle(ctx, "s1", "orca", "h2", "", handlesT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := openHandle(t, s, "s1"); got.Handle != "h2" {
		t.Fatalf("after binding h2: %+v", got)
	}
	var to string
	if err := s.db.R().QueryRowContext(ctx, `SELECT to_at FROM session_handles WHERE session_id = 's1' AND handle = 'h1'`).Scan(&to); err != nil || to != formatTime(handlesT0.Add(time.Hour)) {
		t.Fatalf("h1 ended at %q %v, want when h2 was bound", to, err)
	}
	wantSessionOf(t, s, "orca", "h1", "s1")
	wantSessionOf(t, s, "orca", "h2", "s1")
}

func TestSessionOfPrefersTheOpenThenTheNewestHandle(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	for _, id := range []string{"old", "new"} {
		if err := s.SaveSession(ctx, turn.Session{ID: id, Manager: "orca"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.BindHandle(ctx, "old", "orca", "h", "", handlesT0); err != nil {
		t.Fatal(err)
	}
	if err := s.EndHandle(ctx, "old", handlesT0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.BindHandle(ctx, "new", "orca", "h", "", handlesT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	wantSessionOf(t, s, "orca", "h", "new")
	if err := s.EndHandle(ctx, "new", handlesT0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	wantSessionOf(t, s, "orca", "h", "new")

	// An older session that took the handle again, open, wins over a newer closed one.
	if err := s.BindHandle(ctx, "old", "orca", "h", "", handlesT0.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantSessionOf(t, s, "orca", "h", "old")
}

func TestEndHandleMakesTheSessionDormant(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if err := s.SaveSession(ctx, turn.Session{ID: "s1", Manager: "orca", Handle: "h1"}); err != nil {
		t.Fatal(err)
	}
	if got := openHandle(t, s, "s1"); got.Handle != "" {
		t.Fatalf("SaveSession bound a handle; only BindHandle does: %+v", got)
	}
	if err := s.BindHandle(ctx, "s1", "orca", "h1", "", handlesT0.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.EndHandle(ctx, "s1", handlesT0); err != nil {
		t.Fatal(err)
	}
	got := openHandle(t, s, "s1")
	if got.Handle != "" || got.State() != turn.SessionDormant {
		t.Fatalf("after EndHandle: %+v", got)
	}
	all, err := s.Sessions(ctx)
	if err != nil || len(all) != 1 || all[0].Handle != "" {
		t.Fatalf("Sessions lists a dormant session as %+v %v", all, err)
	}
	if err := s.EndHandle(ctx, "s1", handlesT0.Add(time.Minute)); err != nil {
		t.Fatalf("ending a dormant session's handle again: %v", err)
	}
	if err := s.EndHandle(ctx, "nobody", handlesT0); err != nil {
		t.Fatalf("ending an unknown session's handle: %v", err)
	}
	wantSessionOf(t, s, "orca", "h1", "s1")
}

func TestSessionOfHandleIsAmbiguousAcrossManagers(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	if _, _, ok, err := s.SessionOfHandle(ctx, "h"); ok || err != nil {
		t.Fatalf("an unknown handle: %v %v", ok, err)
	}
	if err := s.BindHandle(ctx, "s1", "orca", "h", "", handlesT0); err != nil {
		t.Fatal(err)
	}
	if err := s.BindHandle(ctx, "s2", "orca", "h2", "", handlesT0); err != nil {
		t.Fatal(err)
	}
	if id, m, ok, err := s.SessionOfHandle(ctx, "h"); err != nil || !ok || id != "s1" || m != "orca" {
		t.Fatalf("one manager's handle: %q %q %v %v", id, m, ok, err)
	}
	if err := s.EndHandle(ctx, "s1", handlesT0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.BindHandle(ctx, "s3", "orca", "h", "", handlesT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, _, ok, err := s.SessionOfHandle(ctx, "h"); err != nil || !ok || id != "s3" {
		t.Fatalf("a handle one manager gave out twice: %q %v %v, want the open one", id, ok, err)
	}
	if err := s.BindHandle(ctx, "s4", "desktop", "h", "", handlesT0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if id, m, ok, err := s.SessionOfHandle(ctx, "h"); err != nil || ok {
		t.Fatalf("a handle two managers gave out: %q %q %v %v, want not ok", id, m, ok, err)
	}
	wantSessionOf(t, s, "desktop", "h", "s4")
	wantSessionOf(t, s, "orca", "h", "s3")
}

func TestOneOpenHandlePerSession(t *testing.T) {
	s := newStore(t)
	insert := func(handle, from string) error {
		_, err := s.db.Write(context.Background(), func(tx *Tx) error {
			_, err := tx.ExecContext(context.Background(), `INSERT INTO session_handles (session_id, manager, handle, from_at) VALUES ('s1', 'orca', ?, ?)`, handle, from)
			return err
		})
		return err
	}
	if err := insert("h1", "a"); err != nil {
		t.Fatal(err)
	}
	if err := insert("h2", "b"); err == nil {
		t.Fatal("the store took a second open handle for one session")
	}
}
