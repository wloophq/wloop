package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/message"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Messages stores the Inbox messages; a write notes TopicEntries for the message's unit, since each message is
// served on its entry.
type Messages struct{ db *sqlite.DB }

var _ message.Store = (*Messages)(nil)

func NewMessages(db *sqlite.DB) *Messages { return &Messages{db: db} }

const (
	messageColumns = `id, entry_id, provider, unit_key, kind, text, options, recommend, confirm,
		origin_kind, turn_seq, session, workflow, proposal, state, answer, created_at, closed_at`
	messagesOpenSQL   = `SELECT ` + messageColumns + ` FROM messages WHERE state = 'open' ORDER BY id`
	messageByIDSQL    = `SELECT ` + messageColumns + ` FROM messages WHERE id = ?`
	messageByEntrySQL = `SELECT ` + messageColumns + ` FROM messages WHERE entry_id = ? ORDER BY id DESC LIMIT 1`
	openQuestionSQL   = `SELECT id FROM messages WHERE state = 'open' AND provider = ? AND unit_key = ?
		AND origin_kind = ? AND turn_seq = ? AND kind = ?`
	openWorkflowSQL = `SELECT id FROM messages WHERE state = 'open' AND provider = ? AND unit_key = ?
		AND origin_kind = ? AND workflow = ?`
	ledgerMessageSQL = `SELECT id FROM messages WHERE origin_kind = 'ledger' AND proposal = ?`
	insertMessageSQL = `INSERT INTO messages (entry_id, provider, unit_key, kind, text, options, recommend, confirm,
		origin_kind, turn_seq, session, workflow, proposal, state, answer, created_at, closed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	updateMessageSQL = `UPDATE messages SET entry_id = ?, text = ?, options = ?, recommend = ?, confirm = ?, session = ?
		WHERE id = ?`
	closeMessageSQL = `UPDATE messages SET state = ?1, answer = ?2, closed_at = CASE state WHEN 'open' THEN ?3 ELSE closed_at END
		WHERE id = ?4 AND (state = 'open' OR (state = 'answered' AND ?1 = 'answered' AND answer != ?2))`
)

// keyQuery is the statement and arguments that find the message m replaces when it has no ID, or "" when
// such a message is always new.
func keyQuery(m message.Message) (string, []any) {
	u := []any{string(m.Unit.Provider), m.Unit.Key}
	switch m.Origin.Kind {
	case message.OriginLedger:
		return ledgerMessageSQL, []any{m.Origin.Proposal}
	case message.OriginCore, message.OriginWorkflow:
		return openWorkflowSQL, append(u, m.Origin.Kind, m.Origin.Workflow)
	case message.OriginTurn:
		return openQuestionSQL, append(u, m.Origin.Kind, m.Origin.TurnSeq, string(m.Kind))
	}
	return "", nil
}

func (s *Messages) Upsert(ctx context.Context, m message.Message) (message.Message, error) {
	options, err := json.Marshal(nonNilOptions(m.Options))
	if err != nil {
		return message.Message{}, err
	}
	var out message.Message
	_, err = s.db.Write(ctx, func(tx *sqlite.Tx) error {
		id := m.ID
		if q, args := keyQuery(m); id == 0 && q != "" {
			switch err := tx.QueryRowContext(ctx, q, args...).Scan(&id); {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return err
			}
		}
		if id == 0 {
			if m.State == "" {
				m.State = message.Open
			}
			res, err := tx.ExecContext(ctx, insertMessageSQL, m.EntryID, string(m.Unit.Provider), m.Unit.Key, string(m.Kind), m.Text,
				string(options), m.Recommend, m.Confirm, m.Origin.Kind, m.Origin.TurnSeq, m.Origin.Session, m.Origin.Workflow,
				m.Origin.Proposal, string(m.State), m.Answer, ts(m.CreatedAt), ts(m.ClosedAt))
			if err != nil {
				return err
			}
			if id, err = res.LastInsertId(); err != nil {
				return err
			}
			tx.Touch(port.TopicEntries, m.Unit)
		} else {
			old, err := scanMessage(tx.QueryRowContext(ctx, messageByIDSQL, id))
			if err != nil {
				return err
			}
			entry := m.EntryID
			if entry == "" {
				entry = old.EntryID
			}
			res, err := tx.ExecContext(ctx, updateMessageSQL+` AND (entry_id, text, options, recommend, confirm, session) IS NOT (?, ?, ?, ?, ?, ?)`,
				entry, m.Text, string(options), m.Recommend, m.Confirm, m.Origin.Session, id,
				entry, m.Text, string(options), m.Recommend, m.Confirm, m.Origin.Session)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n > 0 {
				tx.Touch(port.TopicEntries, m.Unit)
			}
		}
		got, err := scanMessage(tx.QueryRowContext(ctx, messageByIDSQL, id))
		out = got
		return err
	})
	return out, err
}

func nonNilOptions(o []record.Option) []record.Option {
	if o == nil {
		return []record.Option{}
	}
	return o
}

func (s *Messages) Message(ctx context.Context, id int64) (message.Message, bool, error) {
	return oneMessage(scanMessage(s.db.R().QueryRowContext(ctx, messageByIDSQL, id)))
}

func (s *Messages) ByEntry(ctx context.Context, entry string) (message.Message, bool, error) {
	return oneMessage(scanMessage(s.db.R().QueryRowContext(ctx, messageByEntrySQL, entry)))
}

func oneMessage(m message.Message, err error) (message.Message, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return message.Message{}, false, nil
	}
	return m, err == nil, err
}

func (s *Messages) Open(ctx context.Context) ([]message.Message, error) {
	rows, err := s.db.R().QueryContext(ctx, messagesOpenSQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []message.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Messages) Close(ctx context.Context, id int64, st message.State, answer string, at time.Time) error {
	if st == message.Open || st == "" {
		return fmt.Errorf("closing message %d as %q", id, st)
	}
	_, err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		var u unit.Unit
		var provider string
		switch err := tx.QueryRowContext(ctx, `SELECT provider, unit_key FROM messages WHERE id = ?`, id).Scan(&provider, &u.Key); {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("no message %d", id)
		case err != nil:
			return err
		}
		u.Provider = unit.ProviderID(provider)
		res, err := tx.ExecContext(ctx, closeMessageSQL, string(st), answer, ts(at), id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n > 0 {
			tx.Touch(port.TopicEntries, u)
		}
		return nil
	})
	return err
}

type rowScanner interface{ Scan(dest ...any) error }

func scanMessage(row rowScanner) (message.Message, error) {
	var m message.Message
	var provider, kind, options, state, created, closed string
	if err := row.Scan(&m.ID, &m.EntryID, &provider, &m.Unit.Key, &kind, &m.Text, &options, &m.Recommend, &m.Confirm,
		&m.Origin.Kind, &m.Origin.TurnSeq, &m.Origin.Session, &m.Origin.Workflow, &m.Origin.Proposal, &state, &m.Answer,
		&created, &closed); err != nil {
		return message.Message{}, err
	}
	m.Unit.Provider, m.Kind, m.State = unit.ProviderID(provider), message.Kind(kind), message.State(state)
	if err := json.Unmarshal([]byte(options), &m.Options); err != nil {
		return message.Message{}, fmt.Errorf("the options of message %d: %w", m.ID, err)
	}
	if len(m.Options) == 0 {
		m.Options = nil
	}
	var err error
	if m.CreatedAt, err = parseTS(created); err != nil {
		return message.Message{}, err
	}
	if m.ClosedAt, err = parseTS(closed); err != nil {
		return message.Message{}, err
	}
	return m, nil
}
