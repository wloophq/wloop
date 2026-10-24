package sqlite

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
)

// AgentSession is what the hooks told of one agent session; a resume keeps
// the handle and starts another.
type AgentSession struct {
	ID, Kind, Handle, Home, Worktree, Transcript string
	FirstSeen, LastSeen                          time.Time
}

func eventTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(event.TimeLayout)
}

// SaveAgentSession upserts a; an empty field never clears a stored one, and
// the seen times only widen.
func (db *DB) SaveAgentSession(ctx context.Context, a AgentSession) error {
	if a.ID == "" {
		return errors.New("an agent session without an id")
	}
	first, last := cmp.Or(a.FirstSeen, a.LastSeen), cmp.Or(a.LastSeen, a.FirstSeen)
	if first.IsZero() {
		return errors.New("the agent session " + a.ID + " has no seen time")
	}
	_, err := db.Write(ctx, func(tx *Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO agent_sessions (agent_session, kind, session, home, worktree, transcript, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (agent_session) DO UPDATE SET
		kind = iif(excluded.kind = '', kind, excluded.kind),
		session = iif(excluded.session = '', session, excluded.session),
		home = iif(excluded.home = '', home, excluded.home),
		worktree = iif(excluded.worktree = '', worktree, excluded.worktree),
		transcript = iif(excluded.transcript = '', transcript, excluded.transcript),
		first_seen_at = min(first_seen_at, excluded.first_seen_at),
		last_seen_at = max(last_seen_at, excluded.last_seen_at)`,
			a.ID, a.Kind, a.Handle, a.Home, a.Worktree, a.Transcript, eventTime(first), eventTime(last))
		if err != nil {
			return err
		}
		tx.Touch(port.TopicSessions, sessionUnit(a.Handle))
		return nil
	})
	return err
}

// AgentTranscript is the transcript file and agent the hooks stored for
// the agent session, "" for each when none is stored.
func (db *DB) AgentTranscript(ctx context.Context, agentSession string) (path, kind string, err error) {
	err = db.R().QueryRowContext(ctx, `SELECT transcript, kind FROM agent_sessions WHERE agent_session = ?`, agentSession).Scan(&path, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return path, kind, err
}
