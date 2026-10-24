package sqlite

import (
	"context"
	"strings"
	"testing"
)

func TestFreshStoreIsIncrementalVacuum(t *testing.T) {
	db, _ := openTemp(t)
	var v int
	if err := db.R().QueryRowContext(context.Background(), `PRAGMA auto_vacuum`).Scan(&v); err != nil || v != 2 {
		t.Fatalf("auto_vacuum %d %v", v, err)
	}
}

func TestIntakesOpenIndexIsUnique(t *testing.T) {
	db, _ := openTemp(t)
	insert := func(state string) error {
		_, err := execW(db, `INSERT INTO intakes (provider, unit_key, intent, source, request, state, accepted_at) VALUES ('github', 'acme/api#7', 'review', 'cli', '{}', ?, '2026-10-18T12:00:00.000000000Z')`, state)
		return err
	}
	if err := insert("accepted"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"accepted", "briefing", "starting"} {
		if err := insert(state); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
			t.Fatalf("a second open intake in state %s: %v", state, err)
		}
	}
	for _, state := range []string{"started", "failed", "canceled"} {
		if err := insert(state); err != nil {
			t.Fatalf("a closed intake in state %s: %v", state, err)
		}
	}
}
