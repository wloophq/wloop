package sqlite

import (
	"slices"
	"testing"
)

func TestV7WakesTurnsSnoozesAndMutesIntoWakes(t *testing.T) {
	t.Parallel()
	const (
		at      = "2026-10-01T09:00:00.000000000Z"
		until   = "2026-11-02T08:00:00.000000000Z"
		muteEnd = "2026-11-03T08:00:00.000000000Z"
	)
	path := v6Store(t,
		`INSERT INTO entries (id, kind, state, title, snooze_until, snooze_on_activity, snooze_return, snooze_mute, origin, created, updated, rev) VALUES
			('e-00000000000000a1', 'capture', 'scheduled', 'until a date', '`+until+`', 0, 'next', 0, 'user', '`+at+`', '`+at+`', 1),
			('e-00000000000000a2', 'capture', 'scheduled', 'until activity', '', 1, 'waiting', 0, 'user', '`+at+`', '`+at+`', 2),
			('e-00000000000000a3', 'capture', 'next', 'a mute that ended', '', 0, '', 1, 'user', '`+at+`', '`+at+`', 3),
			('e-00000000000000a4', 'capture', 'scheduled', 'muted until its state changes', '', 1, 'next', 1, 'user', '`+at+`', '`+at+`', 4),
			('e-00000000000000a5', 'capture', 'next', 'muted, not deferred yet', '', 0, '', 0, 'user', '`+at+`', '`+at+`', 5),
			('e-00000000000000a6', 'capture', 'done', 'muted and done', '', 0, '', 0, 'user', '`+at+`', '`+at+`', 6)`,
		`INSERT INTO entry_units (entry_id, provider, unit_key, ord) VALUES
			('e-00000000000000a4', 'github', 'acme/api#7', 0), ('e-00000000000000a5', 'github', 'acme/api#8', 0),
			('e-00000000000000a6', 'github', 'acme/api#10', 0)`,
		`INSERT INTO overrides (provider, unit_key, muted_until, mute_until_state, muted_kind, muted_waiting_on, hold) VALUES
			('github', 'acme/api#7', '', 1, 'review', 'me', 0), ('github', 'acme/api#8', '`+muteEnd+`', 0, '', '', 1),
			('github', 'acme/api#9', '', 1, 'review', 'me', 0), ('github', 'acme/api#10', '`+muteEnd+`', 0, '', '', 0)`,
	)
	migrateV6(t, path)
	raw := rawDB(t, path)
	got := strings1(t, raw, `SELECT task_id || ' ' || until || ' ' || on_kinds || ' ' || actors || ' ' || cond || ' ' || expires || ' ' || return_state
		FROM wakes ORDER BY task_id`)
	want := []string{
		"t-00000000000000a1 " + until + ` [] []   next`,
		`t-00000000000000a2  ["activity"] []   waiting`,
		`t-00000000000000a4  ["activity"] []   next`,
		"t-00000000000000a5 " + muteEnd + ` [] []   next`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("wakes\n%q\nwant\n%q", got, want)
	}
	if got := strings1(t, raw, `SELECT id || ' ' || state || ' ' || category FROM tasks WHERE id IN ('t-00000000000000a3', 't-00000000000000a5', 't-00000000000000a6') ORDER BY id`); !slices.Equal(got,
		[]string{"t-00000000000000a3 next ready", "t-00000000000000a5 scheduled deferred", "t-00000000000000a6 done done"}) {
		t.Fatalf("tasks %q", got)
	}
	for _, table := range []string{"tasks", "overrides"} {
		if cols := strings1(t, raw, `SELECT name FROM pragma_table_info(?) WHERE name LIKE 'snooze%' OR name LIKE 'mute%' OR name LIKE 'muted%'`, table); len(cols) != 0 {
			t.Errorf("%s keeps %q", table, cols)
		}
	}
	if got := strings1(t, raw, `SELECT unit_key || ' ' || hold FROM overrides ORDER BY unit_key`); !slices.Contains(got, "acme/api#8 1") {
		t.Fatalf("the overrides lost their other columns: %q", got)
	}
}
