package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// hostOverrideColumns are the overrides columns the host owns, in the order
// of port.Overrides.
const hostOverrideColumns = `muted_until, mute_until_state, muted_kind, muted_waiting_on, next_guidance, hold`

// Override is u's host overrides by name, the unset ones absent.
func (db *DB) Override(ctx context.Context, u unit.Unit) (map[string]string, error) {
	return readOverride(ctx, db.R(), u)
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func readOverride(ctx context.Context, q rowQuerier, u unit.Unit) (map[string]string, error) {
	var until, kind, waitingOn, guidance string
	var untilState, hold bool
	err := q.QueryRowContext(ctx, `SELECT `+hostOverrideColumns+` FROM overrides WHERE provider = ? AND unit_key = ?`,
		string(u.Provider), u.Key).Scan(&until, &untilState, &kind, &waitingOn, &guidance, &hold)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if until != "" {
		t, err := time.Parse(event.TimeLayout, until)
		if err != nil {
			return nil, fmt.Errorf("%s: muted_until %q: %w", u.Label(), until, err)
		}
		until = t.UTC().Format(time.RFC3339Nano)
	}
	out := map[string]string{port.OverrideMutedUntil: until, port.OverrideMuteUntilStateChange: flag(untilState),
		port.OverrideMutedKind: kind, port.OverrideMutedWaitingOn: waitingOn, port.OverrideNextGuidance: guidance, port.OverrideHold: flag(hold)}
	maps.DeleteFunc(out, func(_, v string) bool { return v == "" })
	return out, nil
}

func flag(b bool) string {
	if b {
		return port.OverrideTrue
	}
	return ""
}

// SetOverride sets u's host overrides of set, in one transaction, only
// while each of ifMatch holds its value ("" for unset); a value "" clears
// one. Written is false when ifMatch did not hold.
func (db *DB) SetOverride(ctx context.Context, u unit.Unit, set, ifMatch map[string]string) (written bool, err error) {
	for _, n := range slices.Concat(slices.Collect(maps.Keys(set)), slices.Collect(maps.Keys(ifMatch))) {
		if !slices.Contains(port.Overrides, n) {
			return false, fmt.Errorf("no host override %q; one of %v", n, port.Overrides)
		}
	}
	next := map[string]string{}
	for n, v := range set {
		if v, err = normalOverride(n, v); err != nil {
			return false, err
		}
		next[n] = v
	}
	_, err = db.Write(ctx, func(tx *Tx) error {
		cur, err := readOverride(ctx, tx, u)
		if err != nil {
			return err
		}
		for n, want := range ifMatch {
			if want, err = normalOverride(n, want); err != nil {
				return err
			}
			if cur[n] != want {
				return nil
			}
		}
		written = true
		after := maps.Clone(cur)
		for n, v := range next {
			after[n] = v
		}
		maps.DeleteFunc(after, func(_, v string) bool { return v == "" })
		if maps.Equal(after, cur) {
			return nil
		}
		until := ""
		if v := after[port.OverrideMutedUntil]; v != "" {
			t, _ := time.Parse(time.RFC3339Nano, v)
			until = t.UTC().Format(event.TimeLayout)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO overrides (provider, unit_key, `+hostOverrideColumns+`) VALUES (?,?,?,?,?,?,?,?)
			ON CONFLICT(provider, unit_key) DO UPDATE SET muted_until=excluded.muted_until, mute_until_state=excluded.mute_until_state,
			muted_kind=excluded.muted_kind, muted_waiting_on=excluded.muted_waiting_on, next_guidance=excluded.next_guidance, hold=excluded.hold`,
			string(u.Provider), u.Key, until, after[port.OverrideMuteUntilStateChange] != "", after[port.OverrideMutedKind],
			after[port.OverrideMutedWaitingOn], after[port.OverrideNextGuidance], after[port.OverrideHold] != "")
		if err == nil {
			tx.Touch(port.TopicOverrides, u)
		}
		return err
	})
	return written && err == nil, err
}

// normalOverride is v as the store reads it back: a time in RFC 3339 UTC, a
// flag "true" or "".
func normalOverride(name, v string) (string, error) {
	if v == "" {
		return "", nil
	}
	switch name {
	case port.OverrideMutedUntil:
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return "", fmt.Errorf("override %s: %w", name, err)
		}
		return t.UTC().Format(time.RFC3339Nano), nil
	case port.OverrideMuteUntilStateChange, port.OverrideHold:
		if v != port.OverrideTrue {
			return "", fmt.Errorf("override %s is %q or empty, not %q", name, port.OverrideTrue, v)
		}
	}
	return v, nil
}

// Held are the units of provider p the user holds.
func (db *DB) Held(ctx context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	rows, err := db.R().QueryContext(ctx, `SELECT unit_key FROM overrides WHERE provider = ? AND hold = 1 ORDER BY unit_key`, string(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []unit.Unit
	for rows.Next() {
		u := unit.Unit{Provider: p}
		if err := rows.Scan(&u.Key); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
