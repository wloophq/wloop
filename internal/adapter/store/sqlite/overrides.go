package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// hostOverrideColumns are the overrides columns the host owns, in the order
// of port.Overrides.
const hostOverrideColumns = `next_guidance, hold`

// Override is u's host overrides by name, the unset ones absent.
func (db *DB) Override(ctx context.Context, u unit.Unit) (map[string]string, error) {
	return readOverride(ctx, db.R(), u)
}

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func readOverride(ctx context.Context, q rowQuerier, u unit.Unit) (map[string]string, error) {
	var guidance string
	var hold bool
	err := q.QueryRowContext(ctx, `SELECT `+hostOverrideColumns+` FROM overrides WHERE provider = ? AND unit_key = ?`,
		string(u.Provider), u.Key).Scan(&guidance, &hold)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{port.OverrideNextGuidance: guidance, port.OverrideHold: flag(hold)}
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
		_, err = tx.ExecContext(ctx, `INSERT INTO overrides (provider, unit_key, `+hostOverrideColumns+`) VALUES (?,?,?,?)
			ON CONFLICT(provider, unit_key) DO UPDATE SET next_guidance=excluded.next_guidance, hold=excluded.hold`,
			string(u.Provider), u.Key, after[port.OverrideNextGuidance], after[port.OverrideHold] != "")
		if err == nil {
			tx.Touch(port.TopicOverrides, u)
		}
		return err
	})
	return written && err == nil, err
}

// normalOverride is v as the store reads it back: a flag "true" or "".
func normalOverride(name, v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if name == port.OverrideHold {
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
