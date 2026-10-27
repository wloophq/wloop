package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"time"
)

// v6Answers gives each answer the message it answers and a read state. An answered message of a turn or an
// agent post, in the order they closed, takes the newest answer of its unit and turn, not typed in the
// terminal and not taken yet, stored no later than it closed: a message closes right after its answer. An
// answer no message takes keeps message 0. Every answer but a queued one reads as read: the loop typed it,
// resumed with it or refused it before answers were read through the Inbox.
func v6Answers(ctx context.Context, tx *sql.Tx) error {
	for _, q := range []string{
		`ALTER TABLE turn_answers ADD COLUMN message_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE turn_answers ADD COLUMN read_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE turn_answers ADD COLUMN noticed_at TEXT NOT NULL DEFAULT ''`,
		`UPDATE turn_answers SET read_at = at WHERE route != 'queued'`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	type key struct {
		provider, unit string
		seq            int64
	}
	type row struct {
		id int64
		at time.Time
	}
	answers := map[key][]row{}
	err := eachRow(ctx, tx, `SELECT provider, unit_key, turn_seq, id, at FROM turn_answers WHERE route != 'direct'`, nil, func(rows *sql.Rows) error {
		var k key
		var r row
		var at string
		if err := rows.Scan(&k.provider, &k.unit, &k.seq, &r.id, &at); err != nil {
			return err
		}
		r.at = parseTime(at)
		answers[k] = append(answers[k], r)
		return nil
	})
	if err != nil {
		return err
	}
	type msg struct {
		k      key
		id     int64
		closed time.Time
	}
	var msgs []msg
	err = eachRow(ctx, tx, `SELECT provider, unit_key, turn_seq, id, closed_at FROM messages
		WHERE state = 'answered' AND origin_kind IN ('turn', 'agent') AND turn_seq != 0`, nil, func(rows *sql.Rows) error {
		var m msg
		var closed string
		if err := rows.Scan(&m.k.provider, &m.k.unit, &m.k.seq, &m.id, &closed); err != nil {
			return err
		}
		m.closed = parseTime(closed)
		msgs = append(msgs, m)
		return nil
	})
	if err != nil {
		return err
	}
	slices.SortStableFunc(msgs, func(a, b msg) int {
		if c := a.closed.Compare(b.closed); c != 0 {
			return c
		}
		return int(a.id - b.id)
	})
	for _, m := range msgs {
		rs := answers[m.k]
		best := -1
		for i, r := range rs {
			if !r.at.After(m.closed) && (best < 0 || !r.at.Before(rs[best].at)) {
				best = i
			}
		}
		if best < 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE turn_answers SET message_id = ? WHERE id = ?`, m.id, rs[best].id); err != nil {
			return err
		}
		answers[m.k] = slices.Delete(rs, best, best+1)
	}
	return nil
}
