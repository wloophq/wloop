package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const sessionColumns = `s.session_id, s.manager, s.agent, coalesce(h.handle, ''), coalesce((SELECT l.handle FROM session_handles l WHERE l.session_id = s.session_id ORDER BY l.from_at DESC LIMIT 1), ''), s.pane, s.agent_session, s.workspace_id, s.worktree, s.home, s.managed,
	s.started_at, s.mcp_at, s.idle_since, s.closed_at, s.typed_at, s.typed_line, s.user_prompt_at, s.prompt_seen_at, s.provider, s.unit_key`

const sessionsFrom = ` FROM sessions s LEFT JOIN session_handles h ON h.session_id = s.session_id AND h.to_at = ''`

func scanSession(row interface{ Scan(...any) error }) (turn.Session, error) {
	var s turn.Session
	var started, mcp, idle, closed, typed, userPrompt, promptSeen string
	err := row.Scan(&s.ID, &s.Manager, &s.Agent, &s.Handle, &s.LastHandle, &s.Pane, &s.AgentSession, &s.WorkspaceID, &s.Worktree, &s.Home, &s.Managed, &started, &mcp, &idle, &closed,
		&typed, &s.TypedLine, &userPrompt, &promptSeen, &s.Unit.Provider, &s.Unit.Key)
	s.StartedAt, s.MCPAt, s.IdleSince, s.ClosedAt = parseTime(started), parseTime(mcp), parseTime(idle), parseTime(closed)
	s.TypedAt, s.UserPromptAt, s.PromptSeenAt = parseTime(typed), parseTime(userPrompt), parseTime(promptSeen)
	return s, err
}

func (s *Store) Session(ctx context.Context, id string) (turn.Session, bool, error) {
	got, err := scanSession(s.db.R().QueryRowContext(ctx, `SELECT `+sessionColumns+sessionsFrom+` WHERE s.session_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return turn.Session{}, false, nil
	}
	return got, err == nil, err
}

// SaveSession writes the session's row; its handles move only through
// BindHandle and EndHandle, so a save of a session read before its handle
// ended never opens it again.
func (s *Store) SaveSession(ctx context.Context, ses turn.Session, evs ...port.Event) error {
	return s.write(ctx, evs, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO sessions (session_id, manager, agent, pane, agent_session, workspace_id, worktree, home, managed,
			started_at, mcp_at, idle_since, closed_at, typed_at, typed_line, user_prompt_at, prompt_seen_at, provider, unit_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (session_id) DO UPDATE SET manager = iif(excluded.manager = '', manager, excluded.manager),
			agent = iif(excluded.agent = '', agent, excluded.agent), pane = excluded.pane, agent_session = excluded.agent_session,
			workspace_id = excluded.workspace_id, worktree = excluded.worktree, home = excluded.home, managed = excluded.managed,
			started_at = excluded.started_at, mcp_at = excluded.mcp_at, idle_since = excluded.idle_since, closed_at = excluded.closed_at,
			typed_at = excluded.typed_at, typed_line = excluded.typed_line, user_prompt_at = excluded.user_prompt_at, prompt_seen_at = excluded.prompt_seen_at,
			provider = excluded.provider, unit_key = excluded.unit_key`,
			ses.ID, ses.Manager, ses.Agent, ses.Pane, ses.AgentSession, ses.WorkspaceID, ses.Worktree, ses.Home, ses.Managed,
			formatTime(ses.StartedAt), formatTime(ses.MCPAt), formatTime(ses.IdleSince), formatTime(ses.ClosedAt),
			formatTime(ses.TypedAt), ses.TypedLine, formatTime(ses.UserPromptAt), formatTime(ses.PromptSeenAt),
			string(ses.Unit.Provider), ses.Unit.Key)
		if err != nil {
			return err
		}
		tx.Touch(port.TopicSessions, sessionUnit(ses.ID))
		return nil
	})
}

func (s *Store) Sessions(ctx context.Context) ([]turn.Session, error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT `+sessionColumns+sessionsFrom+` WHERE s.closed_at = '' ORDER BY s.session_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []turn.Session
	for rows.Next() {
		ses, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ses)
	}
	return out, rows.Err()
}

const (
	sessionOfSQL = `SELECT session_id FROM session_handles WHERE manager = ? AND handle = ?
		ORDER BY to_at = '' DESC, from_at DESC LIMIT 1`
	openHandleSQL = `SELECT handle, manager FROM session_handles WHERE session_id = ? AND to_at = ''`
)

// SessionOf is the session whose process the manager addressed by handle,
// the newest one when the manager gave the handle out twice.
func (s *Store) SessionOf(ctx context.Context, manager, handle string) (string, bool, error) {
	var id string
	err := s.db.R().QueryRowContext(ctx, sessionOfSQL, manager, handle).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return id, err == nil, err
}

// SessionOfHandle is SessionOf over every manager, for a caller that knows the
// handle alone; ok is false when no manager or more than one gave it out.
func (s *Store) SessionOfHandle(ctx context.Context, handle string) (id, manager string, ok bool, err error) {
	rows, err := s.db.R().QueryContext(ctx, `SELECT session_id, manager FROM session_handles WHERE handle = ?
		ORDER BY to_at = '' DESC, from_at DESC`, handle)
	if err != nil {
		return "", "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, m string
		if err := rows.Scan(&sid, &m); err != nil {
			return "", "", false, err
		}
		switch {
		case manager == "":
			id, manager = sid, m
		case m != manager:
			return "", "", false, rows.Err()
		}
	}
	return id, manager, manager != "", rows.Err()
}

// BindHandle makes handle the session's open handle from at, ending the one
// before; binding the open handle again changes nothing.
func (s *Store) BindHandle(ctx context.Context, id, manager, handle, runtime string, at time.Time) error {
	return s.write(ctx, nil, func(tx *Tx) error {
		if err := bindHandle(ctx, tx, id, manager, handle, runtime, at); err != nil {
			return err
		}
		tx.Touch(port.TopicSessions, sessionUnit(id))
		return nil
	})
}

func bindHandle(ctx context.Context, tx *Tx, id, manager, handle, runtime string, at time.Time) error {
	var open, openManager string
	err := tx.QueryRowContext(ctx, openHandleSQL, id).Scan(&open, &openManager)
	switch {
	case err == nil && open == handle && openManager == manager:
		if runtime != "" {
			_, err = tx.ExecContext(ctx, `UPDATE session_handles SET runtime = ? WHERE session_id = ? AND to_at = '' AND runtime = ''`, runtime, id)
		}
		return err
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	from := formatTime(at)
	if _, err := tx.ExecContext(ctx, `UPDATE session_handles SET to_at = ? WHERE session_id = ? AND to_at = ''`, from, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO session_handles (session_id, manager, handle, runtime, from_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (session_id, from_at) DO UPDATE SET manager = excluded.manager, handle = excluded.handle, runtime = excluded.runtime, to_at = ''`,
		id, manager, handle, runtime, from)
	return err
}

// EndHandle ends the session's open handle at at: the session is dormant.
func (s *Store) EndHandle(ctx context.Context, id string, at time.Time) error {
	return s.write(ctx, nil, func(tx *Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE session_handles SET to_at = ? WHERE session_id = ? AND to_at = ''`, formatTime(at), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			tx.Touch(port.TopicSessions, sessionUnit(id))
		}
		return nil
	})
}

const stopColumns = `session, prompt_id, agent_session, at, message, blocked, record_id`

func scanStop(row interface{ Scan(...any) error }) (turn.Stop, error) {
	var st turn.Stop
	var at string
	err := row.Scan(&st.Session, &st.PromptID, &st.AgentSession, &at, &st.Message, &st.Blocked, &st.RecordID)
	st.At = parseTime(at)
	return st, err
}

// LastStops returns the newest stop of each session that logged one.
func (s *Store) LastStops(ctx context.Context, sessions []string) (map[string]turn.Stop, error) {
	out := make(map[string]turn.Stop, len(sessions))
	for chunk := range slices.Chunk(sessions, lastStopsChunk) {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, h := range chunk {
			args[i] = h
		}
		rows, err := s.db.R().QueryContext(ctx, lastStopsSQL(marks), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			st, err := scanStop(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[st.Session] = st
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

const lastStopsChunk = 500

func lastStopsSQL(marks string) string {
	return `SELECT ` + stopColumns + ` FROM (
		SELECT ` + stopColumns + `, ROW_NUMBER() OVER (PARTITION BY session ORDER BY at DESC, rowid DESC) AS n
		FROM turn_stops WHERE session IN (` + marks + `)) WHERE n = 1`
}

func (s *Store) LogStop(ctx context.Context, st turn.Stop, evs ...port.Event) error {
	return s.write(ctx, evs, func(tx *Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO turn_stops (session, prompt_id, agent_session, at, message, blocked, record_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			st.Session, st.PromptID, st.AgentSession, st.At.UTC().Format(event.TimeLayout), st.Message, st.Blocked, st.RecordID); err != nil {
			return err
		}
		tx.Touch(port.TopicSessions, sessionUnit(st.Session))
		return nil
	})
}

// sessionUnit is the unit of a session id; no id, no unit.
func sessionUnit(id string) unit.Unit {
	if id == "" {
		return unit.Unit{}
	}
	return unit.Unit{Provider: unit.Session, Key: id}
}
