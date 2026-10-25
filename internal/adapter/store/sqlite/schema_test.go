package sqlite

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFreshStoreIsAtSchemaVersion(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	var v int
	if err := db.R().QueryRowContext(context.Background(), `PRAGMA user_version`).Scan(&v); err != nil || v != SchemaVersion {
		t.Fatalf("user_version %d %v", v, err)
	}
	if err := db.R().QueryRowContext(context.Background(), `PRAGMA auto_vacuum`).Scan(&v); err != nil || v != 2 {
		t.Fatalf("auto_vacuum %d %v", v, err)
	}
}

func TestOtherVersionsAreRefused(t *testing.T) {
	t.Parallel()
	for _, v := range []int{0, SchemaVersion + 1, 8} {
		db, path := openTemp(t)
		if _, err := execW(db, fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
			t.Fatal(err)
		}
		if _, err := execW(db, `UPDATE meta SET value = ? WHERE key = ?`, fmt.Sprint(v), compatibleKey); err != nil {
			t.Fatal(err)
		}
		db.Close()
		want := fmt.Sprintf("the store at %s is at schema version %d, newer than this release reads (%d)", path, v, SchemaVersion)
		if v == 0 {
			want = fmt.Sprintf("the store at %s is at schema version 0; this release reads versions 1 to %d only", path, SchemaVersion)
		}
		for name, err := range map[string]error{"Open": openErr(path), "Check": Check(path)} {
			if !errors.Is(err, ErrSchema) || err.Error() != want {
				t.Errorf("version %d, %s: %v", v, name, err)
			}
		}
	}
}

func TestANewerStoreThatDeclaresThisVersionCompatibleOpens(t *testing.T) {
	t.Parallel()
	db, path := openTemp(t)
	if _, err := execW(db, `PRAGMA user_version = 5`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := Check(path); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := openErr(path); err != nil {
		t.Fatalf("Open: %v", err)
	}
}

func TestIntakesOpenIndexIsUnique(t *testing.T) {
	t.Parallel()
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
