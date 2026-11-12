package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/gates"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// GateStore is the gates; now stamps OpenedAt and ClosedAt.
type GateStore struct {
	db  *DB
	now func() time.Time
}

var (
	_ gates.Store  = GateStore{}
	_ gates.Begun  = GateStore{}
	_ gates.Reader = GateStore{}
	_ gates.Sent   = GateStore{}
)

// Gates is the store of the gates.
func (db *DB) Gates(now func() time.Time) GateStore { return GateStore{db, now} }

const gateColumns = `id, task_id, provider, unit_key, kind, name, subject, status, intents, intent, answer,
	by_actor, rule, deadline, default_intent, opened_at, closed_at, session, by_session, text, approved_rev, sessions`

const (
	gateOfIDSQL       = `SELECT ` + gateColumns + ` FROM gates WHERE id = ?`
	gatesOfTaskSQL    = `SELECT ` + gateColumns + ` FROM gates WHERE task_id = ? ORDER BY id`
	gatesDueSQL       = `SELECT ` + gateColumns + ` FROM gates WHERE status = ? AND deadline != '' AND deadline <= ? ORDER BY id`
	gatesUnappliedSQL = `SELECT ` + gateColumns + ` FROM gates WHERE status NOT IN (?, ?) AND applied_at = '' ORDER BY id`
)

// gatesWhereSQL is the statement and arguments that read the gates q matches.
func gatesWhereSQL(q gates.Query) (string, []any) {
	statuses := q.Statuses
	if len(statuses) == 0 {
		statuses = []gate.Status{gate.Open}
	}
	var where []string
	var args []any
	where = append(where, `status IN (`+strings.TrimSuffix(strings.Repeat("?, ", len(statuses)), ", ")+`)`)
	for _, s := range statuses {
		args = append(args, string(s))
	}
	if !q.Unit.IsZero() {
		where = append(where, `provider = ? AND unit_key = ?`)
		args = append(args, string(q.Unit.Provider), q.Unit.Key)
	}
	if q.Task != "" {
		where = append(where, `task_id = ?`)
		args = append(args, string(q.Task))
	}
	if q.Kind != "" {
		where = append(where, `kind = ?`)
		args = append(args, string(q.Kind))
	}
	if q.Name != "" {
		where = append(where, `name = ?`)
		args = append(args, q.Name)
	}
	return `SELECT ` + gateColumns + ` FROM gates WHERE ` + strings.Join(where, ` AND `) + ` ORDER BY id`, args
}

const insertGateSQL = `INSERT INTO gates (task_id, provider, unit_key, kind, name, subject, status, intents, intent, answer,
	by_actor, rule, deadline, default_intent, opened_at, closed_at, session, by_session, text, approved_rev, sessions)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Open stores g as open with a new ID and OpenedAt.
func (s GateStore) Open(ctx context.Context, g gate.Gate) (gate.Gate, error) {
	g.Status, g.OpenedAt = gate.Open, s.now()
	g.Intent, g.By, g.BySession, g.Rule, g.Answer, g.ClosedAt = "", "", "", "", "", time.Time{}
	return s.insert(ctx, g)
}

// RecordSent stores g closed as approved, opened and closed now, its
// consequence applied: the tool posted it.
func (s GateStore) RecordSent(ctx context.Context, g gate.Gate) (gate.Gate, error) {
	at := s.now()
	g.Status, g.OpenedAt, g.ClosedAt = gate.Approved, at, at
	g, err := s.insert(ctx, g)
	if err != nil {
		return gate.Gate{}, err
	}
	return g, s.Applied(ctx, g.ID)
}

// Applied marks resolved gate id's consequence as run.
func (s GateStore) Applied(ctx context.Context, id int64) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE gates SET applied_at = ? WHERE id = ? AND status != ?`, eventTime(s.now()), id, string(gate.Open))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return cmp.Or(err, fmt.Errorf("gate %d is open or missing: no consequence ran", id))
		}
		return nil
	})
	return err
}

// Begin marks resolved gate id's consequence run, and begun until End.
func (s GateStore) Begin(ctx context.Context, id int64) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		at := eventTime(s.now())
		res, err := tx.ExecContext(ctx, `UPDATE gates SET applied_at = ?, begun_at = ? WHERE id = ? AND status != ?`, at, at, id, string(gate.Open))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return cmp.Or(err, fmt.Errorf("gate %d is open or missing: no consequence ran", id))
		}
		return nil
	})
	return err
}

// End marks gate id's begun consequence ended.
func (s GateStore) End(ctx context.Context, id int64) error {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE gates SET begun_at = '' WHERE id = ?`, id)
		return err
	})
	return err
}

// Begun are the resolved gates whose consequence began and never ended.
func (s GateStore) Begun(ctx context.Context) ([]gate.Gate, error) {
	return s.list(ctx, `SELECT `+gateColumns+` FROM gates WHERE begun_at != '' ORDER BY id`)
}

// Unapplied are the resolved gates whose consequence has not run.
func (s GateStore) Unapplied(ctx context.Context) ([]gate.Gate, error) {
	return s.list(ctx, gatesUnappliedSQL, string(gate.Open), string(gate.Superseded))
}

func (s GateStore) insert(ctx context.Context, g gate.Gate) (gate.Gate, error) {
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		id, err := insertGate(ctx, tx.Tx, g)
		if err != nil {
			return err
		}
		g.ID = id
		touchGate(tx, g)
		return nil
	})
	if err != nil {
		return gate.Gate{}, err
	}
	return g, nil
}

// insertGate writes g as it is and returns its new ID.
func insertGate(ctx context.Context, tx *sql.Tx, g gate.Gate) (int64, error) {
	intents, err := json.Marshal(nonNil(g.Intents))
	if err != nil {
		return 0, err
	}
	sessions, err := json.Marshal(nonNil(g.Sessions))
	if err != nil {
		return 0, err
	}
	var taskID any
	if g.Task != "" {
		taskID = string(g.Task)
	}
	res, err := tx.ExecContext(ctx, insertGateSQL, taskID, string(g.Unit.Provider), g.Unit.Key, string(g.Kind), g.Name, g.Subject,
		string(g.Status), string(intents), g.Intent, g.Answer, string(g.By), g.Rule, eventTime(g.Deadline), g.Default,
		eventTime(g.OpenedAt), eventTime(g.ClosedAt), g.Session, g.BySession, g.Text, g.ApprovedRev, string(sessions))
	if err != nil {
		return 0, fmt.Errorf("a %s gate on %q: %w", g.Kind, g.Subject, err)
	}
	return res.LastInsertId()
}

func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

func touchGate(tx *Tx, g gate.Gate) {
	var us []unit.Unit
	if !g.Unit.IsZero() {
		us = append(us, g.Unit)
	}
	if g.Task != "" {
		us = append(us, task.UnitOf(g.Task))
	}
	tx.Touch(port.TopicGates, us...)
}

// Resolve closes the open gate id with r.
func (s GateStore) Resolve(ctx context.Context, id int64, r gates.Resolution) (gate.Gate, error) {
	if r.Status == "" || r.Status == gate.Open {
		return gate.Gate{}, fmt.Errorf("resolving gate %d to %q is no resolution", id, r.Status)
	}
	var g gate.Gate
	_, err := s.db.Write(ctx, func(tx *Tx) error {
		var err error
		g, err = scanGate(tx.QueryRowContext(ctx, gateOfIDSQL, id))
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("%w: %d", gates.ErrNoGate, id)
		case err != nil:
			return err
		case g.Status != gate.Open:
			return fmt.Errorf("%w: %d is %s", gates.ErrResolved, id, g.Status)
		}
		g.Status, g.Intent, g.Answer, g.By, g.BySession, g.Rule, g.ClosedAt = r.Status, r.Intent, r.Answer, r.By, r.Session, r.Rule, s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE gates SET status = ?, intent = ?, answer = ?, by_actor = ?, by_session = ?, rule = ?, closed_at = ? WHERE id = ?`,
			string(g.Status), g.Intent, g.Answer, string(g.By), g.BySession, g.Rule, eventTime(g.ClosedAt), id); err != nil {
			return err
		}
		touchGate(tx, g)
		return nil
	})
	if err != nil {
		if errors.Is(err, gates.ErrResolved) {
			return g, err
		}
		return gate.Gate{}, err
	}
	return g, nil
}

// Get is gate id.
func (s GateStore) Get(ctx context.Context, id int64) (gate.Gate, error) {
	g, err := scanGate(s.db.R().QueryRowContext(ctx, gateOfIDSQL, id))
	if errors.Is(err, sql.ErrNoRows) {
		return gate.Gate{}, fmt.Errorf("%w: %d", gates.ErrNoGate, id)
	}
	return g, err
}

// OfTask are task id's gates, open and closed.
func (s GateStore) OfTask(ctx context.Context, id task.ID) ([]gate.Gate, error) {
	return s.list(ctx, gatesOfTaskSQL, string(id))
}

// Where are the gates matching q.
func (s GateStore) Where(ctx context.Context, q gates.Query) ([]gate.Gate, error) {
	query, args := gatesWhereSQL(q)
	return s.list(ctx, query, args...)
}

// Due are the open gates whose Deadline is set and not after now.
func (s GateStore) Due(ctx context.Context, now time.Time) ([]gate.Gate, error) {
	return s.list(ctx, gatesDueSQL, string(gate.Open), eventTime(now))
}

func (s GateStore) list(ctx context.Context, query string, args ...any) ([]gate.Gate, error) {
	rows, err := s.db.R().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []gate.Gate{}
	for rows.Next() {
		g, err := scanGate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func scanGate(row interface{ Scan(...any) error }) (gate.Gate, error) {
	var g gate.Gate
	var taskID sql.NullString
	var provider, kind, status, intents, by, deadline, opened, closed, sessions string
	if err := row.Scan(&g.ID, &taskID, &provider, &g.Unit.Key, &kind, &g.Name, &g.Subject, &status, &intents, &g.Intent, &g.Answer,
		&by, &g.Rule, &deadline, &g.Default, &opened, &closed, &g.Session, &g.BySession, &g.Text, &g.ApprovedRev, &sessions); err != nil {
		return gate.Gate{}, err
	}
	g.Task, g.Unit.Provider, g.Kind, g.Status, g.By = task.ID(taskID.String), unit.ProviderID(provider), gate.Kind(kind), gate.Status(status), task.Actor(by)
	if err := json.Unmarshal([]byte(intents), &g.Intents); err != nil {
		return gate.Gate{}, fmt.Errorf("the intents of gate %d: %w", g.ID, err)
	}
	if err := json.Unmarshal([]byte(sessions), &g.Sessions); err != nil {
		return gate.Gate{}, fmt.Errorf("the sessions of gate %d: %w", g.ID, err)
	}
	for _, t := range []struct {
		dst  *time.Time
		name string
		raw  string
	}{{&g.Deadline, "deadline", deadline}, {&g.OpenedAt, "opened_at", opened}, {&g.ClosedAt, "closed_at", closed}} {
		var err error
		if *t.dst, err = parseStoredTime(t.raw); err != nil {
			return gate.Gate{}, fmt.Errorf("the %s of gate %d: %w", t.name, g.ID, err)
		}
	}
	return g, nil
}

// The SQL that writes the draft gates' rows outside GateStore's methods lives here, with the gates store,
// which alone writes a gate's status: migration 13's backfill, and a rehomed draft's gates.
const (
	// draftGatesApproveTheirDraftsRevSQL gives an open or approved draft gate its draft's rev (migration 13).
	draftGatesApproveTheirDraftsRevSQL = `UPDATE gates SET approved_rev = coalesce((SELECT d.rev FROM drafts d WHERE d.id = CAST(gates.subject AS INTEGER)), 0)
	WHERE kind = 'draft' AND status IN ('open', 'approved')`
	// draftGatesStampTheirSessionSQL stamps a draft gate's session among its sessions (migration 13).
	draftGatesStampTheirSessionSQL = `UPDATE gates SET sessions = json_array(session) WHERE kind = 'draft' AND session != ''`
	// rehomeDraftGatesSQL moves the gates of the drafts a rehome moves along, so a draft's gate stays on its
	// unit; each still approves the rev it did, which the rehome's revision left behind.
	rehomeDraftGatesSQL = `UPDATE gates SET unit_key = ?1
		WHERE kind = 'draft' AND provider = ?2 AND unit_key = ?3`
)
