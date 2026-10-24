package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFreshStoreIsVersion1(t *testing.T) {
	db, _ := openTemp(t)
	var v int
	if err := db.R().QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&v); err != nil || v != 1 || SchemaVersion != 1 {
		t.Fatalf("user_version %d %v", v, err)
	}
	if err := db.R().QueryRowContext(context.Background(), `PRAGMA auto_vacuum`).Scan(&v); err != nil || v != 2 {
		t.Fatalf("auto_vacuum %d %v", v, err)
	}
}

func TestOtherVersionsAreRefused(t *testing.T) {
	for _, v := range []int{0, 2, 8} {
		db, path := openTemp(t)
		if _, err := execW(db, fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
			t.Fatal(err)
		}
		db.Close()
		for name, err := range map[string]error{"Open": openErr(path), "Check": Check(path)} {
			want := fmt.Sprintf("the store at %s is at schema version %d; this release reads version 1 only", path, v)
			if !errors.Is(err, ErrSchema) || err.Error() != want {
				t.Errorf("version %d, %s: %v", v, name, err)
			}
		}
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
