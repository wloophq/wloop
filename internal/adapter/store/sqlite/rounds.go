package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
)

// The kinds of the event a pause or a resume leaves.
const (
	KindPaused  = "paused"
	KindResumed = "resumed"
)

// Rounds is the round state's repository.
type Rounds struct{ db *DB }

func NewRounds(db *DB) *Rounds { return &Rounds{db: db} }

func (r *Rounds) Load(ctx context.Context) (*round.Store, error) {
	st := round.New()
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT unit_key, fingerprint, sent_at, round_at, checks_head, agent_home, first_round_skipped, conflict_head FROM round_state WHERE provider = 'github'`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key, sent, roundAt string
			var p round.PR
			if err := rows.Scan(&key, &p.Fingerprint, &sent, &roundAt, &p.ChecksHead, &p.AgentHome, &p.FirstRoundSkipped, &p.ConflictHead); err != nil {
				rows.Close()
				return err
			}
			p.SentAt, p.RoundAt = parseTime(sent), parseTime(roundAt)
			st.PRs[key] = p
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		return loadDays(ctx, tx, st)
	})
	if err != nil {
		return nil, err
	}
	return st, nil
}

func loadDays(ctx context.Context, tx *sql.Tx, st *round.Store) error {
	day := func(k string) round.Day { return st.Days[k] }
	if err := each(ctx, tx, `SELECT day, total FROM round_days`, func(rows *sql.Rows) error {
		var k string
		var total int
		if err := rows.Scan(&k, &total); err != nil {
			return err
		}
		d := day(k)
		d.Total = total
		st.Days[k] = d
		return nil
	}); err != nil {
		return err
	}
	if err := each(ctx, tx, `SELECT day, unit_key, rounds FROM round_day_prs WHERE provider = 'github'`, func(rows *sql.Rows) error {
		var k, key string
		var n int
		if err := rows.Scan(&k, &key, &n); err != nil {
			return err
		}
		d := day(k)
		if d.PerPR == nil {
			d.PerPR = map[string]int{}
		}
		d.PerPR[key] = n
		st.Days[k] = d
		return nil
	}); err != nil {
		return err
	}
	return each(ctx, tx, `SELECT day, kind, note FROM round_day_notes ORDER BY rowid`, func(rows *sql.Rows) error {
		var k, kind, note string
		if err := rows.Scan(&k, &kind, &note); err != nil {
			return err
		}
		d := day(k)
		if kind == round.NoteCap {
			d.CapLogged = append(d.CapLogged, note)
		} else {
			d.Noted = append(d.Noted, note)
		}
		st.Days[k] = d
		return nil
	})
}

func each(ctx context.Context, tx *sql.Tx, query string, fn func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Save writes only the fields st's setters changed, adds its rounds to the
// day's counters and drops the days before the newest one it counts.
func (r *Rounds) Save(ctx context.Context, st *round.Store) error {
	ch := st.Changes()
	_, err := r.db.Write(ctx, func(tx *Tx) error {
		for key, f := range ch.PRs {
			ref, err := ParseKey(key)
			if err != nil {
				return err
			}
			if err := saveFields(ctx, tx, ref, st.PRs[key], f); err != nil {
				return err
			}
			tx.Touch(port.TopicRounds, ref.Unit())
		}
		newest := ""
		for _, c := range ch.Counts {
			ref, err := ParseKey(c.Key)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO round_days (day, total) VALUES (?, 1) ON CONFLICT (day) DO UPDATE SET total = total + 1`, c.Day); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO round_day_prs (day, provider, unit_key, rounds) VALUES (?, 'github', ?, 1)
				ON CONFLICT (day, provider, unit_key) DO UPDATE SET rounds = rounds + 1`, c.Day, ref.String()); err != nil {
				return err
			}
			newest = max(newest, c.Day)
			tx.Touch(port.TopicRounds)
		}
		for _, n := range ch.Notes {
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO round_day_notes (day, kind, note) VALUES (?, ?, ?)`, n.Day, n.Kind, n.Text)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n > 0 {
				tx.Touch(port.TopicRounds)
			}
			newest = max(newest, n.Day)
		}
		if newest != "" {
			for _, table := range []string{"round_days", "round_day_prs", "round_day_notes"} {
				if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE day < ?`, newest); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	st.Saved()
	return nil
}

func saveFields(ctx context.Context, tx *Tx, ref pr.Ref, p round.PR, f round.Fields) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO round_state (provider, unit_key) VALUES ('github', ?) ON CONFLICT DO NOTHING`, ref.String()); err != nil {
		return err
	}
	var sets []string
	var args []any
	for _, c := range []struct {
		field  round.Fields
		column string
		value  any
	}{
		{round.FieldFingerprint, "fingerprint", p.Fingerprint},
		{round.FieldSentAt, "sent_at", formatTime(p.SentAt)},
		{round.FieldRoundAt, "round_at", formatTime(p.RoundAt)},
		{round.FieldChecksHead, "checks_head", p.ChecksHead},
		{round.FieldAgentHome, "agent_home", p.AgentHome},
		{round.FieldFirstRoundSkipped, "first_round_skipped", p.FirstRoundSkipped},
		{round.FieldConflictHead, "conflict_head", p.ConflictHead},
	} {
		if f&c.field != 0 {
			sets = append(sets, c.column+" = ?")
			args = append(args, c.value)
		}
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, ref.String())
	_, err := tx.ExecContext(ctx, `UPDATE round_state SET `+strings.Join(sets, ", ")+` WHERE provider = 'github' AND unit_key = ?`, args...)
	return err
}

// AgentHome is the agent home remembered for the pull request's rounds, empty when none is.
func (r *Rounds) AgentHome(ctx context.Context, ref pr.Ref) (string, error) {
	var home string
	err := r.db.R().QueryRowContext(ctx, `SELECT agent_home FROM round_state WHERE provider = 'github' AND unit_key = ?`, ref.String()).Scan(&home)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return home, err
}

func (r *Rounds) Pause(ctx context.Context) (round.Pause, error) {
	var p round.Pause
	var repos, at string
	err := r.db.R().QueryRowContext(ctx, `SELECT paused, repos, at, why FROM pause WHERE id = 1`).Scan(&p.On, &repos, &at, &p.Why)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(repos), &p.Repos); err != nil {
		return p, fmt.Errorf("reading the paused repositories: %w", err)
	}
	p.At = parseTime(at)
	return p, nil
}

// SetPause writes the row and a paused or resumed event in one transaction.
func (r *Rounds) SetPause(ctx context.Context, p round.Pause) error {
	_, err := r.db.Write(ctx, func(tx *Tx) error { return setPause(ctx, tx, p) })
	return err
}

func setPause(ctx context.Context, tx *Tx, p round.Pause) error {
	if p.At.IsZero() {
		p.At = time.Now()
	}
	repos, err := json.Marshal(append([]string{}, p.Repos...))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pause SET paused = ?, repos = ?, at = ?, why = ? WHERE id = 1`,
		p.On, string(repos), formatTime(p.At), p.Why); err != nil {
		return err
	}
	kind, data := KindResumed, p.Why
	switch {
	case p.On:
		kind = KindPaused
	case len(p.Repos) > 0:
		kind, data = KindPaused, strings.TrimSuffix("on "+strings.Join(p.Repos, ", ")+"; "+p.Why, "; ")
	}
	// Two changes in one instant would collide on events_unkeyed; the later
	// one moves a nanosecond on so both are kept.
	for at := p.At.UTC(); ; at = at.Add(time.Nanosecond) {
		res, err := tx.ExecContext(ctx, `INSERT INTO events (provider, unit_key, kind, key, actor, at, data) VALUES ('', '', ?, '', '', ?, ?) ON CONFLICT DO NOTHING`,
			kind, at.Format(event.TimeLayout), data)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 1 {
			tx.Touch(port.TopicPause | port.TopicLoopEvents)
			return err
		}
	}
}

// ParseKey reads the owner/repo#n key of the round state.
func ParseKey(key string) (pr.Ref, error) {
	owner, rest, ok1 := strings.Cut(key, "/")
	repo, num, ok2 := strings.Cut(rest, "#")
	n, err := strconv.Atoi(num)
	if !ok1 || !ok2 || err != nil || owner == "" || repo == "" || strings.Contains(repo, "/") || n <= 0 {
		return pr.Ref{}, fmt.Errorf("%q is not an owner/repo#number key", key)
	}
	return pr.Ref{Owner: owner, Repo: repo, Number: n}, nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeLayout)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(TimeLayout, s)
	return t
}
