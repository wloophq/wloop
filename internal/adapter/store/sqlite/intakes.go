package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type intakeStore struct{ db *DB }

var _ intake.Store = intakeStore{}

func (db *DB) Intakes() intake.Store { return intakeStore{db} }

const (
	intakeColumns = `seq, provider, unit_key, intent, source, request, state, brief, note,
	accepted_at, briefing_at, starting_at, ended_at, turn_seq, turn_id, error, log`
	openStates = `('accepted', 'briefing', 'starting')`
)

type scanner interface{ Scan(dest ...any) error }

func scanIntake(row scanner) (intake.Intake, error) {
	var i intake.Intake
	var u unit.Unit
	var intent, state, request, accepted, briefing, starting, ended, log string
	if err := row.Scan(&i.Seq, &u.Provider, &u.Key, &intent, &i.Source, &request, &state, &i.Brief, &i.Note,
		&accepted, &briefing, &starting, &ended, &i.TurnSeq, &i.TurnID, &i.Error, &log); err != nil {
		return intake.Intake{}, err
	}
	i.Unit, i.Intent, i.State, i.Request = u, turn.Intent(intent), intake.State(state), []byte(request)
	for n, dst := range []*time.Time{&i.AcceptedAt, &i.BriefingAt, &i.StartingAt, &i.EndedAt} {
		raw := []string{accepted, briefing, starting, ended}[n]
		if raw == "" {
			continue
		}
		t, err := time.Parse(event.TimeLayout, raw)
		if err != nil {
			return intake.Intake{}, fmt.Errorf("a time of intake %d: %w", i.Seq, err)
		}
		*dst = t
	}
	if err := json.Unmarshal([]byte(log), &i.Log); err != nil {
		return intake.Intake{}, fmt.Errorf("the log of intake %d: %w", i.Seq, err)
	}
	return i, nil
}

func encodeLog(log []string) (string, error) {
	if log == nil {
		return "[]", nil
	}
	b, err := json.Marshal(log)
	return string(b), err
}

func queryOpen(ctx context.Context, tx *sql.Tx, i intake.Intake) (intake.Intake, bool, error) {
	got, err := scanIntake(tx.QueryRowContext(ctx, `SELECT `+intakeColumns+` FROM intakes
		WHERE provider = ? AND unit_key = ? AND intent = ? AND state IN `+openStates,
		string(i.Unit.Provider), i.Unit.Key, string(i.Intent)))
	if errors.Is(err, sql.ErrNoRows) {
		return intake.Intake{}, false, nil
	}
	return got, err == nil, err
}

var errJoined = errors.New("joined")

func (s intakeStore) Accept(ctx context.Context, i intake.Intake, evs ...port.Event) (intake.Intake, bool, error) {
	if i.AcceptedAt.IsZero() {
		return intake.Intake{}, false, errors.New("the intake has no accepted time")
	}
	log, err := encodeLog(i.Log)
	if err != nil {
		return intake.Intake{}, false, err
	}
	keyed := slices.Clone(evs)
	var open intake.Intake
	err = New(s.db).write(ctx, keyed, func(tx *Tx) error {
		got, ok, err := queryOpen(ctx, tx.Tx, i)
		if err != nil {
			return err
		}
		if ok {
			open = got
			return errJoined
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO intakes (provider, unit_key, intent, source, request, state, brief, note,
			accepted_at, briefing_at, starting_at, ended_at, turn_seq, turn_id, error, log)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			string(i.Unit.Provider), i.Unit.Key, string(i.Intent), i.Source, string(i.Request), string(i.State), i.Brief, i.Note,
			eventTime(i.AcceptedAt), eventTime(i.BriefingAt), eventTime(i.StartingAt), eventTime(i.EndedAt), i.TurnSeq, i.TurnID, i.Error, log)
		if err != nil {
			return err
		}
		if i.Seq, err = res.LastInsertId(); err != nil {
			return err
		}
		tx.Touch(port.TopicIntakes, i.Unit)
		key := strconv.FormatInt(i.Seq, 10)
		for n := range keyed {
			keyed[n].Key = key
		}
		return nil
	})
	if errors.Is(err, errJoined) {
		return open, true, nil
	}
	if err != nil {
		return intake.Intake{}, false, err
	}
	return i, false, nil
}

func (s intakeStore) Save(ctx context.Context, i intake.Intake, evs ...port.Event) error {
	log, err := encodeLog(i.Log)
	if err != nil {
		return err
	}
	return New(s.db).write(ctx, evs, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE intakes SET request = ?, state = ?, brief = ?, note = ?,
			briefing_at = ?, starting_at = ?, ended_at = ?, turn_seq = ?, turn_id = ?, error = ?, log = ?
			WHERE seq = ?`,
			string(i.Request), string(i.State), i.Brief, i.Note,
			eventTime(i.BriefingAt), eventTime(i.StartingAt), eventTime(i.EndedAt), i.TurnSeq, i.TurnID, i.Error, log, i.Seq)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("intake %d is not stored", i.Seq)
		}
		tx.Touch(port.TopicIntakes, i.Unit)
		return nil
	})
}

func (s intakeStore) Intake(ctx context.Context, seq int64) (intake.Intake, bool, error) {
	i, err := scanIntake(s.db.R().QueryRowContext(ctx, `SELECT `+intakeColumns+` FROM intakes WHERE seq = ?`, seq))
	if errors.Is(err, sql.ErrNoRows) {
		return intake.Intake{}, false, nil
	}
	return i, err == nil, err
}

func (s intakeStore) Open(ctx context.Context) ([]intake.Intake, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+intakeColumns+` FROM intakes WHERE state IN `+openStates+` ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []intake.Intake
	for rows.Next() {
		i, err := scanIntake(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}
